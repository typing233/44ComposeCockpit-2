package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/pkg/apierr"
)

type Operations struct {
	client Client
	logger *slog.Logger
}

func NewOperations(client Client, logger *slog.Logger) *Operations {
	return &Operations{client: client, logger: logger}
}

func (o *Operations) Up(ctx context.Context, project *domain.Project, services []string) (*domain.OperationResult, error) {
	targetServices := o.filterServices(project, services)

	if err := o.checkPortConflicts(ctx, targetServices); err != nil {
		return nil, err
	}

	result := &domain.OperationResult{
		Status: domain.OpStatusRunning,
		Steps:  make([]domain.OpStep, 0),
	}
	now := time.Now()
	result.StartedAt = &now

	var startedContainers []string
	projectLabel := map[string]string{
		"com.composecockpit.project": string(project.ID),
		"com.composecockpit.name":    project.Name,
	}

	sorted := o.topologicalSort(targetServices)

	for _, svc := range sorted {
		if err := ctx.Err(); err != nil {
			o.rollback(context.Background(), startedContainers)
			result.Status = domain.OpStatusCancelled
			return result, nil
		}

		step := domain.OpStep{Service: svc.Name, Action: "pull"}
		stepStart := time.Now()

		exists, err := o.client.ImageExists(ctx, svc.Image)
		if err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollback(context.Background(), startedContainers)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrDockerUnavailable, Message: err.Error()}
			return result, nil
		}

		if !exists {
			reader, err := o.client.ImagePull(ctx, svc.Image, ImagePullOpts{})
			if err != nil {
				step.Status = domain.OpStatusFailed
				step.Error = err.Error()
				step.Duration = time.Since(stepStart)
				result.Steps = append(result.Steps, step)
				o.rollback(context.Background(), startedContainers)
				result.Status = domain.OpStatusRolledBack
				result.Error = &domain.OpError{Code: apierr.ErrImagePullFailed, Message: fmt.Sprintf("failed to pull %s: %v", svc.Image, err)}
				return result, nil
			}
			reader.Close()
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		step = domain.OpStep{Service: svc.Name, Action: "create"}
		stepStart = time.Now()

		labels := make(map[string]string)
		for k, v := range projectLabel {
			labels[k] = v
		}
		labels["com.composecockpit.service"] = svc.Name
		for k, v := range svc.Labels {
			labels[k] = v
		}

		containerName := fmt.Sprintf("%s-%s-1", project.Name, svc.Name)
		cfg := ContainerCreateConfig{
			Name:          containerName,
			Image:         svc.Image,
			Labels:        labels,
			Env:           envMapToSlice(svc.Environment),
			Cmd:           svc.Command,
			Entrypoint:    svc.Entrypoint,
			Ports:         convertPorts(svc.Ports),
			Volumes:       convertVolumes(svc.Volumes),
			Networks:      svc.Networks,
			RestartPolicy: svc.Restart,
		}

		containerID, err := o.client.ContainerCreate(ctx, cfg)
		if err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollback(context.Background(), startedContainers)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrContainerFailed, Message: err.Error()}
			return result, nil
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		step = domain.OpStep{Service: svc.Name, Action: "start"}
		stepStart = time.Now()

		if err := o.client.ContainerStart(ctx, containerID); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.client.ContainerRemove(context.Background(), containerID, true)
			o.rollback(context.Background(), startedContainers)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrContainerFailed, Message: err.Error()}
			return result, nil
		}

		startedContainers = append(startedContainers, containerID)
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	finished := time.Now()
	result.FinishedAt = &finished
	result.Status = domain.OpStatusSucceeded
	return result, nil
}

func (o *Operations) Down(ctx context.Context, project *domain.Project, services []string) (*domain.OperationResult, error) {
	result := &domain.OperationResult{Status: domain.OpStatusRunning, Steps: make([]domain.OpStep, 0)}
	now := time.Now()
	result.StartedAt = &now

	containers, err := o.getProjectContainers(ctx, project)
	if err != nil {
		return nil, err
	}

	for _, ctr := range containers {
		if len(services) > 0 && !containsService(services, ctr.Labels["com.composecockpit.service"]) {
			continue
		}

		if err := ctx.Err(); err != nil {
			result.Status = domain.OpStatusCancelled
			return result, nil
		}

		svcName := ctr.Labels["com.composecockpit.service"]

		step := domain.OpStep{Service: svcName, Action: "stop"}
		stepStart := time.Now()
		timeout := 10 * time.Second
		if err := o.client.ContainerStop(ctx, ctr.ID, &timeout); err != nil {
			o.logger.Warn("stop container failed", "id", ctr.ID, "error", err)
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		step = domain.OpStep{Service: svcName, Action: "remove"}
		stepStart = time.Now()
		if err := o.client.ContainerRemove(ctx, ctr.ID, true); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
		} else {
			step.Status = domain.OpStatusSucceeded
		}
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	finished := time.Now()
	result.FinishedAt = &finished
	result.Status = domain.OpStatusSucceeded
	return result, nil
}

func (o *Operations) Stop(ctx context.Context, project *domain.Project, services []string) (*domain.OperationResult, error) {
	result := &domain.OperationResult{Status: domain.OpStatusRunning, Steps: make([]domain.OpStep, 0)}
	now := time.Now()
	result.StartedAt = &now

	containers, err := o.getProjectContainers(ctx, project)
	if err != nil {
		return nil, err
	}

	for _, ctr := range containers {
		if len(services) > 0 && !containsService(services, ctr.Labels["com.composecockpit.service"]) {
			continue
		}
		if ctr.State != "running" {
			continue
		}

		svcName := ctr.Labels["com.composecockpit.service"]
		step := domain.OpStep{Service: svcName, Action: "stop"}
		stepStart := time.Now()
		timeout := 10 * time.Second
		if err := o.client.ContainerStop(ctx, ctr.ID, &timeout); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
		} else {
			step.Status = domain.OpStatusSucceeded
		}
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	finished := time.Now()
	result.FinishedAt = &finished
	result.Status = domain.OpStatusSucceeded
	return result, nil
}

func (o *Operations) Start(ctx context.Context, project *domain.Project, services []string) (*domain.OperationResult, error) {
	result := &domain.OperationResult{Status: domain.OpStatusRunning, Steps: make([]domain.OpStep, 0)}
	now := time.Now()
	result.StartedAt = &now

	containers, err := o.getProjectContainers(ctx, project)
	if err != nil {
		return nil, err
	}

	for _, ctr := range containers {
		if len(services) > 0 && !containsService(services, ctr.Labels["com.composecockpit.service"]) {
			continue
		}
		if ctr.State == "running" {
			continue
		}

		svcName := ctr.Labels["com.composecockpit.service"]
		step := domain.OpStep{Service: svcName, Action: "start"}
		stepStart := time.Now()
		if err := o.client.ContainerStart(ctx, ctr.ID); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
		} else {
			step.Status = domain.OpStatusSucceeded
		}
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	finished := time.Now()
	result.FinishedAt = &finished
	result.Status = domain.OpStatusSucceeded
	return result, nil
}

func (o *Operations) Restart(ctx context.Context, project *domain.Project, services []string) (*domain.OperationResult, error) {
	result := &domain.OperationResult{Status: domain.OpStatusRunning, Steps: make([]domain.OpStep, 0)}
	now := time.Now()
	result.StartedAt = &now

	containers, err := o.getProjectContainers(ctx, project)
	if err != nil {
		return nil, err
	}

	for _, ctr := range containers {
		if len(services) > 0 && !containsService(services, ctr.Labels["com.composecockpit.service"]) {
			continue
		}

		svcName := ctr.Labels["com.composecockpit.service"]

		step := domain.OpStep{Service: svcName, Action: "stop"}
		stepStart := time.Now()
		timeout := 10 * time.Second
		_ = o.client.ContainerStop(ctx, ctr.ID, &timeout)
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		step = domain.OpStep{Service: svcName, Action: "start"}
		stepStart = time.Now()
		if err := o.client.ContainerStart(ctx, ctr.ID); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
		} else {
			step.Status = domain.OpStatusSucceeded
		}
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	finished := time.Now()
	result.FinishedAt = &finished
	result.Status = domain.OpStatusSucceeded
	return result, nil
}

func (o *Operations) GetProjectStatus(ctx context.Context, project *domain.Project) (domain.ProjectStatus, map[string]ContainerInfo, error) {
	containers, err := o.getProjectContainers(ctx, project)
	if err != nil {
		return domain.ProjectStatusUnknown, nil, err
	}

	if len(containers) == 0 {
		return domain.ProjectStatusStopped, nil, nil
	}

	containerMap := make(map[string]ContainerInfo)
	runningCount := 0
	for _, c := range containers {
		svcName := c.Labels["com.composecockpit.service"]
		containerMap[svcName] = c
		if c.State == "running" {
			runningCount++
		}
	}

	if runningCount == len(containers) {
		return domain.ProjectStatusRunning, containerMap, nil
	}
	if runningCount == 0 {
		return domain.ProjectStatusStopped, containerMap, nil
	}
	return domain.ProjectStatusPartial, containerMap, nil
}

func (o *Operations) checkPortConflicts(ctx context.Context, services []domain.Service) error {
	for _, svc := range services {
		for _, port := range svc.Ports {
			if port.HostPort == "" {
				continue
			}

			portNum, err := strconv.Atoi(port.HostPort)
			if err != nil {
				continue
			}

			containers, err := o.client.ContainerList(ctx, map[string][]string{
				"status": {"running"},
			})
			if err != nil {
				continue
			}

			for _, ctr := range containers {
				for _, ctrPort := range ctr.Ports {
					if ctrPort.HostPort == port.HostPort && ctrPort.Protocol == port.Protocol {
						return &domain.AppError{
							Code:       apierr.ErrPortConflict,
							Message:    fmt.Sprintf("port %d/%s is already in use by container %s", portNum, port.Protocol, ctr.Name),
							HTTPStatus: 409,
							Details: apierr.PortConflictDetails{
								Port:              portNum,
								Protocol:          port.Protocol,
								BlockingContainer: ctr.Name,
							},
						}
					}
				}
			}

			hostIP := port.HostIP
			if hostIP == "" {
				hostIP = "0.0.0.0"
			}
			addr := net.JoinHostPort(hostIP, port.HostPort)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return &domain.AppError{
					Code:       apierr.ErrPortConflict,
					Message:    fmt.Sprintf("port %d/%s is already in use on host", portNum, port.Protocol),
					HTTPStatus: 409,
					Details: apierr.PortConflictDetails{
						Port:     portNum,
						Protocol: port.Protocol,
					},
				}
			}
			ln.Close()
		}
	}
	return nil
}

func (o *Operations) rollback(ctx context.Context, containerIDs []string) {
	for i := len(containerIDs) - 1; i >= 0; i-- {
		id := containerIDs[i]
		timeout := 5 * time.Second
		_ = o.client.ContainerStop(ctx, id, &timeout)
		_ = o.client.ContainerRemove(ctx, id, true)
	}
	o.logger.Info("rollback completed", "containers_removed", len(containerIDs))
}

func (o *Operations) getProjectContainers(ctx context.Context, project *domain.Project) ([]ContainerInfo, error) {
	return o.client.ContainerList(ctx, map[string][]string{
		"label": {fmt.Sprintf("com.composecockpit.project=%s", string(project.ID))},
	})
}

func (o *Operations) filterServices(project *domain.Project, services []string) []domain.Service {
	if len(services) == 0 {
		return project.Services
	}
	var filtered []domain.Service
	for _, svc := range project.Services {
		for _, name := range services {
			if svc.Name == name {
				filtered = append(filtered, svc)
				break
			}
		}
	}
	return filtered
}

func (o *Operations) topologicalSort(services []domain.Service) []domain.Service {
	graph := make(map[string][]string)
	svcMap := make(map[string]domain.Service)
	for _, svc := range services {
		svcMap[svc.Name] = svc
		graph[svc.Name] = nil
		for dep := range svc.DependsOn {
			graph[svc.Name] = append(graph[svc.Name], dep)
		}
	}

	visited := make(map[string]bool)
	var order []domain.Service
	var visit func(string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		for _, dep := range graph[name] {
			visit(dep)
		}
		if svc, ok := svcMap[name]; ok {
			order = append(order, svc)
		}
	}

	for name := range graph {
		visit(name)
	}
	return order
}

func envMapToSlice(env map[string]string) []string {
	if env == nil {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

func convertPorts(ports []domain.PortMapping) []PortBinding {
	result := make([]PortBinding, 0, len(ports))
	for _, p := range ports {
		result = append(result, PortBinding{
			HostIP:        p.HostIP,
			HostPort:      p.HostPort,
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
		})
	}
	return result
}

func convertVolumes(vols []domain.VolumeMount) []MountConfig {
	result := make([]MountConfig, 0, len(vols))
	for _, v := range vols {
		result = append(result, MountConfig{
			Type:     v.Type,
			Source:   v.Source,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		})
	}
	return result
}

func containsService(services []string, name string) bool {
	for _, s := range services {
		if s == name {
			return true
		}
	}
	return false
}
