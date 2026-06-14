package docker

import (
	"context"
	"io"
	"time"
)

type ContainerCreateConfig struct {
	Name          string
	Image         string
	Labels        map[string]string
	Env           []string
	Cmd           []string
	Entrypoint    []string
	Ports         []PortBinding
	Volumes       []MountConfig
	Networks      []string
	RestartPolicy string
}

type PortBinding struct {
	HostIP        string
	HostPort      string
	ContainerPort string
	Protocol      string
}

type MountConfig struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	State   string
	Status  string
	Labels  map[string]string
	Ports   []PortBinding
	Created time.Time
}

type ContainerDetail struct {
	ContainerInfo
	Health    string
	Pid       int
	StartedAt time.Time
	ExitCode  int
	Config    ContainerCreateConfig
}

type NetworkInfo struct {
	ID     string
	Name   string
	Driver string
	Labels map[string]string
}

type NetworkCreateOpts struct {
	Driver  string
	Labels  map[string]string
	Options map[string]string
}

type VolumeCreateOpts struct {
	Driver  string
	Labels  map[string]string
	Options map[string]string
}

type VolumeInfo struct {
	Name   string
	Driver string
	Labels map[string]string
}

type LogOptions struct {
	Follow     bool
	Tail       string
	Since      string
	Timestamps bool
}

type ImagePullOpts struct {
	Platform string
	AuthConfig string
}

type ImageBuildOpts struct {
	Context    string
	Dockerfile string
	Tags       []string
	BuildArgs  map[string]string
	Target     string
}

type EventsOptions struct {
	Since   string
	Until   string
	Filters map[string][]string
}

type DockerEvent struct {
	Type   string
	Action string
	Actor  EventActor
	Time   time.Time
}

type EventActor struct {
	ID         string
	Attributes map[string]string
}

type ContainerStats struct {
	ContainerID string
	Name        string
	CPUPercent  float64
	MemoryUsage uint64
	MemoryLimit uint64
	MemPercent  float64
	NetworkRx   uint64
	NetworkTx   uint64
	BlockRead   uint64
	BlockWrite  uint64
	PIDs        uint64
	Timestamp   time.Time
}

type Client interface {
	ContainerCreate(ctx context.Context, cfg ContainerCreateConfig) (string, error)
	ContainerStart(ctx context.Context, id string) error
	ContainerStop(ctx context.Context, id string, timeout *time.Duration) error
	ContainerRemove(ctx context.Context, id string, force bool) error
	ContainerList(ctx context.Context, filters map[string][]string) ([]ContainerInfo, error)
	ContainerInspect(ctx context.Context, id string) (*ContainerDetail, error)
	ContainerLogs(ctx context.Context, id string, opts LogOptions) (io.ReadCloser, error)
	ContainerStats(ctx context.Context, id string, stream bool) (io.ReadCloser, error)

	NetworkCreate(ctx context.Context, name string, opts NetworkCreateOpts) (string, error)
	NetworkRemove(ctx context.Context, id string) error
	NetworkList(ctx context.Context) ([]NetworkInfo, error)

	VolumeCreate(ctx context.Context, name string, opts VolumeCreateOpts) error
	VolumeRemove(ctx context.Context, name string, force bool) error
	VolumeList(ctx context.Context) ([]VolumeInfo, error)

	ImagePull(ctx context.Context, ref string, opts ImagePullOpts) (io.ReadCloser, error)
	ImageBuild(ctx context.Context, opts ImageBuildOpts) error
	ImageExists(ctx context.Context, ref string) (bool, error)

	Events(ctx context.Context, opts EventsOptions) (<-chan DockerEvent, <-chan error)
	Ping(ctx context.Context) error
	Close() error
}
