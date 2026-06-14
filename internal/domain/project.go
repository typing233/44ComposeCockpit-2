package domain

import "time"

type ProjectID string

type ProjectStatus string

const (
	ProjectStatusRunning ProjectStatus = "running"
	ProjectStatusPartial ProjectStatus = "partial"
	ProjectStatusStopped ProjectStatus = "stopped"
	ProjectStatusUnknown ProjectStatus = "unknown"
)

type ContainerState string

const (
	StateCreated    ContainerState = "created"
	StateRunning    ContainerState = "running"
	StatePaused     ContainerState = "paused"
	StateRestarting ContainerState = "restarting"
	StateRemoving   ContainerState = "removing"
	StateExited     ContainerState = "exited"
	StateDead       ContainerState = "dead"
)

type HealthStatus string

const (
	HealthNone      HealthStatus = "none"
	HealthStarting  HealthStatus = "starting"
	HealthHealthy   HealthStatus = "healthy"
	HealthUnhealthy HealthStatus = "unhealthy"
)

type Project struct {
	ID            ProjectID          `json:"id"`
	Name          string             `json:"name"`
	Path          string             `json:"path"`
	ComposeFiles  []string           `json:"compose_files"`
	EnvFiles      []string           `json:"env_files,omitempty"`
	Services      []Service          `json:"services"`
	Networks      map[string]Network `json:"networks,omitempty"`
	Volumes       map[string]Volume  `json:"volumes,omitempty"`
	Profiles      []string           `json:"profiles,omitempty"`
	Status        ProjectStatus      `json:"status"`
	LastOperation *OperationResult   `json:"last_operation,omitempty"`
	DiscoveredAt  time.Time          `json:"discovered_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type Service struct {
	Name        string                       `json:"name"`
	Image       string                       `json:"image"`
	Build       *BuildConfig                 `json:"build,omitempty"`
	Ports       []PortMapping                `json:"ports,omitempty"`
	Environment map[string]string            `json:"environment,omitempty"`
	Labels      map[string]string            `json:"labels,omitempty"`
	DependsOn   map[string]ServiceDependency `json:"depends_on,omitempty"`
	Profiles    []string                     `json:"profiles,omitempty"`
	Volumes     []VolumeMount                `json:"volumes,omitempty"`
	Networks    []string                     `json:"networks,omitempty"`
	Restart     string                       `json:"restart,omitempty"`
	Command     []string                     `json:"command,omitempty"`
	Entrypoint  []string                     `json:"entrypoint,omitempty"`
	ContainerID string                       `json:"container_id,omitempty"`
	State       ContainerState               `json:"state"`
	Health      HealthStatus                 `json:"health"`
}

type BuildConfig struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

type PortMapping struct {
	HostIP        string `json:"host_ip,omitempty"`
	HostPort      string `json:"host_port"`
	ContainerPort string `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type ServiceDependency struct {
	Condition string `json:"condition"`
}

type VolumeMount struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Type     string `json:"type"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type Network struct {
	Name     string            `json:"name,omitempty"`
	Driver   string            `json:"driver,omitempty"`
	External bool              `json:"external,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type Volume struct {
	Name     string            `json:"name,omitempty"`
	Driver   string            `json:"driver,omitempty"`
	External bool              `json:"external,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}
