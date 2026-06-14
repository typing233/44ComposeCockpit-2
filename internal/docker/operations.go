package docker

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
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

	result := &domain.OperationResult{
		Status: domain.OpStatusRunning,
		Steps:  make([]domain.OpStep, 0),
	}
	now := time.Now()
	result.StartedAt = &now

	// Phase 1: Check port conflicts before doing anything
	if err := o.checkPortConflicts(ctx, targetServices); err != nil {
		return nil, err
	}

	// Phase 2: Create networks (skip external ones)
	createdNetworks := make([]string, 0)
	for netName, netDef := range project.Networks {
		if netDef.External {
			continue
		}
		step := domain.OpStep{Service: "", Action: "create_network:" + netName}
		stepStart := time.Now()

		actualName := netDef.Name
		if actualName == "" {
			actualName = project.Name + "_" + netName
		}

		// Check for conflicts
		if err := o.checkNetworkConflict(ctx, actualName); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrNetworkConflict, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}

		driver := netDef.Driver
		if driver == "" {
			driver = "bridge"
		}
		id, err := o.client.NetworkCreate(ctx, actualName, NetworkCreateOpts{
			Driver: driver,
			Labels: map[string]string{
				"com.composecockpit.project": string(project.ID),
				"com.composecockpit.network": netName,
			},
		})
		if err != nil {
			// Network may already exist (idempotent)
			if !strings.Contains(err.Error(), "already exists") {
				step.Status = domain.OpStatusFailed
				step.Error = err.Error()
				step.Duration = time.Since(stepStart)
				result.Steps = append(result.Steps, step)
				o.rollbackNetworks(context.Background(), createdNetworks)
				result.Status = domain.OpStatusRolledBack
				result.Error = &domain.OpError{Code: apierr.ErrNetworkConflict, Message: err.Error()}
				fin := time.Now()
				result.FinishedAt = &fin
				return result, nil
			}
		} else {
			createdNetworks = append(createdNetworks, id)
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	// Phase 3: Create volumes (skip external ones)
	createdVolumes := make([]string, 0)
	for volName, volDef := range project.Volumes {
		if volDef.External {
			continue
		}
		step := domain.OpStep{Service: "", Action: "create_volume:" + volName}
		stepStart := time.Now()

		actualName := volDef.Name
		if actualName == "" {
			actualName = project.Name + "_" + volName
		}

		// Check for conflicts
		if err := o.checkVolumeConflict(ctx, actualName, project.ID); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollbackVolumes(context.Background(), createdVolumes)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrVolumeConflict, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}

		err := o.client.VolumeCreate(ctx, actualName, VolumeCreateOpts{
			Driver: volDef.Driver,
			Labels: map[string]string{
				"com.composecockpit.project": string(project.ID),
				"com.composecockpit.volume":  volName,
			},
		})
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollbackVolumes(context.Background(), createdVolumes)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrVolumeConflict, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}
		createdVolumes = append(createdVolumes, actualName)
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)
	}

	// Phase 4: Pull images and create/start containers in dependency order
	sorted := o.topologicalSort(targetServices)
	var startedContainers []string
	projectLabels := map[string]string{
		"com.composecockpit.project": string(project.ID),
		"com.composecockpit.name":    project.Name,
	}

	for _, svc := range sorted {
		if err := ctx.Err(); err != nil {
			o.rollbackContainers(context.Background(), startedContainers)
			result.Status = domain.OpStatusCancelled
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}

		// Skip build-only services (have build but no image and no ports/volumes)
		if svc.Image == "" && svc.Build != nil {
			step := domain.OpStep{Service: svc.Name, Action: "skip_build_only"}
			step.Status = domain.OpStatusSucceeded
			result.Steps = append(result.Steps, step)
			continue
		}

		// If service only has build config but no image tag, generate one
		imageName := svc.Image
		if imageName == "" && svc.Build != nil {
			imageName = project.Name + "-" + svc.Name + ":latest"
		}
		if imageName == "" {
			step := domain.OpStep{Service: svc.Name, Action: "skip_no_image"}
			step.Status = domain.OpStatusSucceeded
			step.Error = "no image or build defined"
			result.Steps = append(result.Steps, step)
			continue
		}

		// Pull image
		step := domain.OpStep{Service: svc.Name, Action: "pull"}
		stepStart := time.Now()

		exists, err := o.client.ImageExists(ctx, imageName)
		if err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollbackContainers(context.Background(), startedContainers)
			o.rollbackVolumes(context.Background(), createdVolumes)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrDockerUnavailable, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}

		if !exists {
			reader, err := o.client.ImagePull(ctx, imageName, ImagePullOpts{})
			if err != nil {
				step.Status = domain.OpStatusFailed
				step.Error = err.Error()
				step.Duration = time.Since(stepStart)
				result.Steps = append(result.Steps, step)
				o.rollbackContainers(context.Background(), startedContainers)
				o.rollbackVolumes(context.Background(), createdVolumes)
				o.rollbackNetworks(context.Background(), createdNetworks)
				result.Status = domain.OpStatusRolledBack
				result.Error = &domain.OpError{Code: apierr.ErrImagePullFailed, Message: fmt.Sprintf("failed to pull %s: %v", imageName, err)}
				fin := time.Now()
				result.FinishedAt = &fin
				return result, nil
			}
			io.Copy(io.Discard, reader)
			reader.Close()
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		// Create container
		step = domain.OpStep{Service: svc.Name, Action: "create"}
		stepStart = time.Now()

		labels := make(map[string]string)
		for k, v := range projectLabels {
			labels[k] = v
		}
		labels["com.composecockpit.service"] = svc.Name
		for k, v := range svc.Labels {
			labels[k] = v
		}

		containerName := fmt.Sprintf("%s-%s-1", project.Name, svc.Name)

		// Resolve volume sources to actual names
		volumes := o.resolveVolumeMounts(project, svc.Volumes)

		// Resolve networks to actual names
		networks := o.resolveNetworks(project, svc.Networks)

		cfg := ContainerCreateConfig{
			Name:          containerName,
			Image:         imageName,
			Labels:        labels,
			Env:           envMapToSlice(svc.Environment),
			Cmd:           svc.Command,
			Entrypoint:    svc.Entrypoint,
			Ports:         convertPorts(svc.Ports),
			Volumes:       volumes,
			Networks:      networks,
			RestartPolicy: svc.Restart,
		}

		containerID, err := o.client.ContainerCreate(ctx, cfg)
		if err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.rollbackContainers(context.Background(), startedContainers)
			o.rollbackVolumes(context.Background(), createdVolumes)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrContainerFailed, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}
		step.Status = domain.OpStatusSucceeded
		step.Duration = time.Since(stepStart)
		result.Steps = append(result.Steps, step)

		// Start container
		step = domain.OpStep{Service: svc.Name, Action: "start"}
		stepStart = time.Now()

		if err := o.client.ContainerStart(ctx, containerID); err != nil {
			step.Status = domain.OpStatusFailed
			step.Error = err.Error()
			step.Duration = time.Since(stepStart)
			result.Steps = append(result.Steps, step)
			o.client.ContainerRemove(context.Background(), containerID, true)
			o.rollbackContainers(context.Background(), startedContainers)
			o.rollbackVolumes(context.Background(), createdVolumes)
			o.rollbackNetworks(context.Background(), createdNetworks)
			result.Status = domain.OpStatusRolledBack
			result.Error = &domain.OpError{Code: apierr.ErrContainerFailed, Message: err.Error()}
			fin := time.Now()
			result.FinishedAt = &fin
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

	// Stop and remove containers
	for _, ctr := range containers {
		if len(services) > 0 && !containsService(services, ctr.Labels["com.composecockpit.service"]) {
			continue
		}
		if err := ctx.Err(); err != nil {
			result.Status = domain.OpStatusCancelled
			fin := time.Now()
			result.FinishedAt = &fin
			return result, nil
		}

		svcName := ctr.Labels["com.composecockpit.service"]

		step := domain.OpStep{Service: svcName, Action: "stop"}
		stepStart := time.Now()
		timeout := 10 * time.Second
		_ = o.client.ContainerStop(ctx, ctr.ID, &timeout)
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

	// If removing all services (not partial), also remove networks
	if len(services) == 0 {
		networks, _ := o.client.NetworkList(ctx)
		for _, net := range networks {
			if net.Labels["com.composecockpit.project"] == string(project.ID) {
				step := domain.OpStep{Action: "remove_network:" + net.Name}
				_ = o.client.NetworkRemove(ctx, net.ID)
				step.Status = domain.OpStatusSucceeded
				result.Steps = append(result.Steps, step)
			}
		}
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

// --- Conflict detection ---

func (o *Operations) checkPortConflicts(ctx context.Context, services []domain.Service) error {
	// Collect all ports we want to bind
	wantedPorts := make(map[string]string) // "proto:hostport" -> service name
	for _, svc := range services {
		for _, port := range svc.Ports {
			if port.HostPort == "" {
				continue
			}
			key := port.Protocol + ":" + port.HostPort
			wantedPorts[key] = svc.Name
		}
	}
	if len(wantedPorts) == 0 {
		return nil
	}

	// Check running containers
	containers, err := o.client.ContainerList(ctx, map[string][]string{"status": {"running"}})
	if err != nil {
		return nil
	}
	for _, ctr := range containers {
		for _, ctrPort := range ctr.Ports {
			key := ctrPort.Protocol + ":" + ctrPort.HostPort
			if _, conflicts := wantedPorts[key]; conflicts {
				portNum, _ := strconv.Atoi(ctrPort.HostPort)
				return &domain.AppError{
					Code:       apierr.ErrPortConflict,
					Message:    fmt.Sprintf("port %s/%s already in use by container %s", ctrPort.HostPort, ctrPort.Protocol, ctr.Name),
					HTTPStatus: 409,
					Details: apierr.PortConflictDetails{
						Port:              portNum,
						Protocol:          ctrPort.Protocol,
						BlockingContainer: ctr.Name,
					},
				}
			}
		}
	}

	// Check host port availability
	for _, svc := range services {
		for _, port := range svc.Ports {
			if port.HostPort == "" {
				continue
			}
			hostIP := port.HostIP
			if hostIP == "" {
				hostIP = "0.0.0.0"
			}
			addr := net.JoinHostPort(hostIP, port.HostPort)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				portNum, _ := strconv.Atoi(port.HostPort)
				return &domain.AppError{
					Code:       apierr.ErrPortConflict,
					Message:    fmt.Sprintf("port %s/%s is occupied on host", port.HostPort, port.Protocol),
					HTTPStatus: 409,
					Details:    apierr.PortConflictDetails{Port: portNum, Protocol: port.Protocol},
				}
			}
			ln.Close()
		}
	}

	return nil
}

func (o *Operations) checkNetworkConflict(ctx context.Context, name string) error {
	networks, err := o.client.NetworkList(ctx)
	if err != nil {
		return nil
	}
	for _, net := range networks {
		if net.Name == name {
			// If owned by us, no conflict
			if net.Labels["com.composecockpit.project"] != "" {
				return nil
			}
			return fmt.Errorf("network %q already exists and is not managed by ComposeCockpit", name)
		}
	}
	return nil
}

func (o *Operations) checkVolumeConflict(ctx context.Context, name string, projectID domain.ProjectID) error {
	volumes, err := o.client.VolumeList(ctx)
	if err != nil {
		return nil
	}
	for _, vol := range volumes {
		if vol.Name == name {
			if vol.Labels["com.composecockpit.project"] == string(projectID) {
				return nil
			}
			if vol.Labels["com.composecockpit.project"] != "" {
				return fmt.Errorf("volume %q belongs to another project", name)
			}
			// Existing unmanaged volume - allow reuse
			return nil
		}
	}
	return nil
}

// --- Rollback helpers ---

func (o *Operations) rollbackContainers(ctx context.Context, containerIDs []string) {
	for i := len(containerIDs) - 1; i >= 0; i-- {
		id := containerIDs[i]
		timeout := 5 * time.Second
		_ = o.client.ContainerStop(ctx, id, &timeout)
		_ = o.client.ContainerRemove(ctx, id, true)
	}
	if len(containerIDs) > 0 {
		o.logger.Info("rollback: removed containers", "count", len(containerIDs))
	}
}

func (o *Operations) rollbackNetworks(ctx context.Context, networkIDs []string) {
	for _, id := range networkIDs {
		_ = o.client.NetworkRemove(ctx, id)
	}
}

func (o *Operations) rollbackVolumes(ctx context.Context, volumeNames []string) {
	for _, name := range volumeNames {
		_ = o.client.VolumeRemove(ctx, name, true)
	}
}

// --- Resolution helpers ---

func (o *Operations) resolveVolumeMounts(project *domain.Project, mounts []domain.VolumeMount) []MountConfig {
	result := make([]MountConfig, 0, len(mounts))
	for _, m := range mounts {
		mc := MountConfig{
			Type:     m.Type,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		}
		// For named volumes, prefix with project name
		if m.Type == "volume" && m.Source != "" && !strings.HasPrefix(m.Source, "/") {
			if _, exists := project.Volumes[m.Source]; exists {
				vol := project.Volumes[m.Source]
				if vol.Name != "" {
					mc.Source = vol.Name
				} else {
					mc.Source = project.Name + "_" + m.Source
				}
			}
		}
		result = append(result, mc)
	}
	return result
}

func (o *Operations) resolveNetworks(project *domain.Project, serviceNetworks []string) []string {
	if len(serviceNetworks) == 0 {
		// Use default project network
		return []string{project.Name + "_default"}
	}
	resolved := make([]string, 0, len(serviceNetworks))
	for _, netName := range serviceNetworks {
		if netDef, exists := project.Networks[netName]; exists {
			if netDef.Name != "" {
				resolved = append(resolved, netDef.Name)
			} else if netDef.External {
				resolved = append(resolved, netName)
			} else {
				resolved = append(resolved, project.Name+"_"+netName)
			}
		} else {
			resolved = append(resolved, project.Name+"_"+netName)
		}
	}
	return resolved
}

// --- Utility helpers ---

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

func containsService(services []string, name string) bool {
	for _, s := range services {
		if s == name {
			return true
		}
	}
	return false
}
