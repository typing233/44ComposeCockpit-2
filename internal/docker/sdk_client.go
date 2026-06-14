package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type sdkClient struct {
	cli *dockerclient.Client
}

func NewClient(host, apiVersion string) (Client, error) {
	opts := []dockerclient.Opt{
		dockerclient.WithHost(host),
	}
	if apiVersion != "" {
		opts = append(opts, dockerclient.WithVersion(apiVersion))
	}

	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &sdkClient{cli: cli}, nil
}

func (c *sdkClient) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

func (c *sdkClient) Close() error {
	return c.cli.Close()
}

func (c *sdkClient) ContainerCreate(ctx context.Context, cfg ContainerCreateConfig) (string, error) {
	containerCfg := &container.Config{
		Image:  cfg.Image,
		Labels: cfg.Labels,
		Env:    cfg.Env,
	}
	if len(cfg.Cmd) > 0 {
		containerCfg.Cmd = cfg.Cmd
	}
	if len(cfg.Entrypoint) > 0 {
		containerCfg.Entrypoint = cfg.Entrypoint
	}

	exposedPorts, portBindings := buildPortBindings(cfg.Ports)
	containerCfg.ExposedPorts = exposedPorts

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		Mounts:        buildMounts(cfg.Volumes),
		RestartPolicy: buildRestartPolicy(cfg.RestartPolicy),
	}

	networkCfg := &network.NetworkingConfig{}
	if len(cfg.Networks) > 0 {
		networkCfg.EndpointsConfig = make(map[string]*network.EndpointSettings)
		networkCfg.EndpointsConfig[cfg.Networks[0]] = &network.EndpointSettings{}
	}

	resp, err := c.cli.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, cfg.Name)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *sdkClient) ContainerStart(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, container.StartOptions{})
}

func (c *sdkClient) ContainerStop(ctx context.Context, id string, timeout *time.Duration) error {
	var opts container.StopOptions
	if timeout != nil {
		secs := int(timeout.Seconds())
		opts.Timeout = &secs
	}
	return c.cli.ContainerStop(ctx, id, opts)
}

func (c *sdkClient) ContainerRemove(ctx context.Context, id string, force bool) error {
	return c.cli.ContainerRemove(ctx, id, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
}

func (c *sdkClient) ContainerList(ctx context.Context, filterMap map[string][]string) ([]ContainerInfo, error) {
	f := filters.NewArgs()
	for k, vals := range filterMap {
		for _, v := range vals {
			f.Add(k, v)
		}
	}

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}

	result := make([]ContainerInfo, 0, len(containers))
	for _, ctr := range containers {
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		result = append(result, ContainerInfo{
			ID:      ctr.ID,
			Name:    name,
			Image:   ctr.Image,
			State:   ctr.State,
			Status:  ctr.Status,
			Labels:  ctr.Labels,
			Created: time.Unix(ctr.Created, 0),
		})
	}
	return result, nil
}

func (c *sdkClient) ContainerInspect(ctx context.Context, id string) (*ContainerDetail, error) {
	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}

	detail := &ContainerDetail{
		ContainerInfo: ContainerInfo{
			ID:      info.ID,
			Name:    strings.TrimPrefix(info.Name, "/"),
			Image:   info.Config.Image,
			State:   info.State.Status,
			Labels:  info.Config.Labels,
			Created: parseTime(info.Created),
		},
		Pid:      info.State.Pid,
		ExitCode: info.State.ExitCode,
	}

	if info.State.Health != nil {
		detail.Health = info.State.Health.Status
	}

	if info.State.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err == nil {
			detail.StartedAt = t
		}
	}

	return detail, nil
}

func (c *sdkClient) ContainerLogs(ctx context.Context, id string, opts LogOptions) (io.ReadCloser, error) {
	return c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Tail:       opts.Tail,
		Since:      opts.Since,
		Timestamps: opts.Timestamps,
	})
}

func (c *sdkClient) ContainerStats(ctx context.Context, id string, stream bool) (io.ReadCloser, error) {
	resp, err := c.cli.ContainerStats(ctx, id, stream)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *sdkClient) NetworkCreate(ctx context.Context, name string, opts NetworkCreateOpts) (string, error) {
	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:  opts.Driver,
		Labels:  opts.Labels,
		Options: opts.Options,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c *sdkClient) NetworkRemove(ctx context.Context, id string) error {
	return c.cli.NetworkRemove(ctx, id)
}

func (c *sdkClient) NetworkList(ctx context.Context) ([]NetworkInfo, error) {
	networks, err := c.cli.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := make([]NetworkInfo, 0, len(networks))
	for _, n := range networks {
		result = append(result, NetworkInfo{
			ID:     n.ID,
			Name:   n.Name,
			Driver: n.Driver,
			Labels: n.Labels,
		})
	}
	return result, nil
}

func (c *sdkClient) VolumeCreate(ctx context.Context, name string, opts VolumeCreateOpts) error {
	_, err := c.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:       name,
		Driver:     opts.Driver,
		Labels:     opts.Labels,
		DriverOpts: opts.Options,
	})
	return err
}

func (c *sdkClient) VolumeRemove(ctx context.Context, name string, force bool) error {
	return c.cli.VolumeRemove(ctx, name, force)
}

func (c *sdkClient) VolumeList(ctx context.Context) ([]VolumeInfo, error) {
	resp, err := c.cli.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, err
	}
	result := make([]VolumeInfo, 0, len(resp.Volumes))
	for _, v := range resp.Volumes {
		result = append(result, VolumeInfo{
			Name:   v.Name,
			Driver: v.Driver,
			Labels: v.Labels,
		})
	}
	return result, nil
}

func (c *sdkClient) ImagePull(ctx context.Context, ref string, opts ImagePullOpts) (io.ReadCloser, error) {
	pullOpts := image.PullOptions{
		Platform: opts.Platform,
	}
	return c.cli.ImagePull(ctx, ref, pullOpts)
}

func (c *sdkClient) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, _, err := c.cli.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		if dockerclient.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *sdkClient) ImageBuild(ctx context.Context, opts ImageBuildOpts) error {
	buildCtx, err := createBuildContext(opts.Context, opts.Dockerfile)
	if err != nil {
		return fmt.Errorf("create build context: %w", err)
	}
	defer buildCtx.Close()

	dockerfile := opts.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	buildArgs := make(map[string]*string)
	for k, v := range opts.BuildArgs {
		v := v
		buildArgs[k] = &v
	}

	resp, err := c.cli.ImageBuild(ctx, buildCtx, types.ImageBuildOptions{
		Tags:       opts.Tags,
		Dockerfile: dockerfile,
		BuildArgs:  buildArgs,
		Target:     opts.Target,
		Remove:     true,
		ForceRemove: true,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read the build output to completion to ensure the build finishes
	decoder := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("build error: %s", msg.Error)
		}
	}
	return nil
}

func (c *sdkClient) Events(ctx context.Context, opts EventsOptions) (<-chan DockerEvent, <-chan error) {
	f := filters.NewArgs()
	for k, vals := range opts.Filters {
		for _, v := range vals {
			f.Add(k, v)
		}
	}

	msgCh, errCh := c.cli.Events(ctx, events.ListOptions{
		Since:   opts.Since,
		Until:   opts.Until,
		Filters: f,
	})

	outCh := make(chan DockerEvent, 64)
	outErrCh := make(chan error, 1)

	go func() {
		defer close(outCh)
		defer close(outErrCh)
		for {
			select {
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				outCh <- DockerEvent{
					Type:   string(msg.Type),
					Action: string(msg.Action),
					Actor: EventActor{
						ID:         msg.Actor.ID,
						Attributes: msg.Actor.Attributes,
					},
					Time: time.Unix(msg.Time, msg.TimeNano),
				}
			case err, ok := <-errCh:
				if !ok {
					return
				}
				outErrCh <- err
				return
			}
		}
	}()

	return outCh, outErrCh
}

func buildPortBindings(ports []PortBinding) (nat.PortSet, nat.PortMap) {
	exposedPorts := nat.PortSet{}
	portMap := nat.PortMap{}

	for _, p := range ports {
		natPort, _ := nat.NewPort(p.Protocol, p.ContainerPort)
		exposedPorts[natPort] = struct{}{}
		portMap[natPort] = []nat.PortBinding{
			{
				HostIP:   p.HostIP,
				HostPort: p.HostPort,
			},
		}
	}
	return exposedPorts, portMap
}

func buildMounts(volumes []MountConfig) []mount.Mount {
	var mounts []mount.Mount
	for _, v := range volumes {
		m := mount.Mount{
			Target:   v.Target,
			Source:   v.Source,
			ReadOnly: v.ReadOnly,
		}
		switch v.Type {
		case "bind":
			m.Type = mount.TypeBind
		case "tmpfs":
			m.Type = mount.TypeTmpfs
		default:
			m.Type = mount.TypeVolume
		}
		mounts = append(mounts, m)
	}
	return mounts
}

func buildRestartPolicy(policy string) container.RestartPolicy {
	switch policy {
	case "always":
		return container.RestartPolicy{Name: container.RestartPolicyAlways}
	case "unless-stopped":
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	case "on-failure":
		return container.RestartPolicy{Name: container.RestartPolicyOnFailure}
	default:
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	}
}

// ParseStats decodes a single stats JSON payload from Docker API
func ParseStats(data []byte) (*ContainerStats, error) {
	var raw types.StatsJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(raw.CPUStats.SystemUsage - raw.PreCPUStats.SystemUsage)
	cpuPercent := 0.0
	if systemDelta > 0 && cpuDelta > 0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(raw.CPUStats.OnlineCPUs) * 100.0
	}

	memPercent := 0.0
	if raw.MemoryStats.Limit > 0 {
		memPercent = float64(raw.MemoryStats.Usage) / float64(raw.MemoryStats.Limit) * 100.0
	}

	var netRx, netTx uint64
	for _, netStats := range raw.Networks {
		netRx += netStats.RxBytes
		netTx += netStats.TxBytes
	}

	return &ContainerStats{
		CPUPercent:  cpuPercent,
		MemoryUsage: raw.MemoryStats.Usage,
		MemoryLimit: raw.MemoryStats.Limit,
		MemPercent:  memPercent,
		NetworkRx:   netRx,
		NetworkTx:   netTx,
		PIDs:        raw.PidsStats.Current,
		Timestamp:   raw.Read,
	}, nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func createBuildContext(contextDir, dockerfile string) (io.ReadCloser, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.Walk(contextDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(contextDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}

	return io.NopCloser(&buf), nil
}
