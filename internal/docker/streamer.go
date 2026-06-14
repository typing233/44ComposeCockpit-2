package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/sse"
)

// Streamer provides real-time container stats, events, and log streaming.
type Streamer struct {
	client Client
	broker sse.Broker
	logger *slog.Logger
}

func NewStreamer(client Client, broker sse.Broker, logger *slog.Logger) *Streamer {
	return &Streamer{client: client, broker: broker, logger: logger}
}

// StreamProjectStats continuously publishes CPU/memory stats for all containers
// of a project to the SSE broker.
func (s *Streamer) StreamProjectStats(ctx context.Context, project *domain.Project) {
	containers, err := s.client.ContainerList(ctx, map[string][]string{
		"label":  {fmt.Sprintf("com.composecockpit.project=%s", string(project.ID))},
		"status": {"running"},
	})
	if err != nil {
		s.logger.Error("list containers for stats", "error", err)
		return
	}

	for _, ctr := range containers {
		ctr := ctr
		go s.streamContainerStats(ctx, project.ID, ctr)
	}
}

func (s *Streamer) streamContainerStats(ctx context.Context, projectID domain.ProjectID, ctr ContainerInfo) {
	reader, err := s.client.ContainerStats(ctx, ctr.ID, true)
	if err != nil {
		s.logger.Warn("start container stats stream", "container", ctr.Name, "error", err)
		return
	}
	defer reader.Close()

	topic := fmt.Sprintf("project:%s:stats", string(projectID))
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}

		stats, err := ParseStats(scanner.Bytes())
		if err != nil {
			continue
		}
		stats.ContainerID = ctr.ID
		stats.Name = ctr.Name

		s.broker.Publish(topic, sse.Event{
			Type: sse.EventContainerStats,
			Data: stats,
		})
	}
}

// StreamProjectLogs streams logs for a specific service container.
func (s *Streamer) StreamProjectLogs(ctx context.Context, projectID domain.ProjectID, serviceName string) {
	containers, err := s.client.ContainerList(ctx, map[string][]string{
		"label": {
			fmt.Sprintf("com.composecockpit.project=%s", string(projectID)),
			fmt.Sprintf("com.composecockpit.service=%s", serviceName),
		},
	})
	if err != nil || len(containers) == 0 {
		return
	}

	ctr := containers[0]
	reader, err := s.client.ContainerLogs(ctx, ctr.ID, LogOptions{
		Follow:     true,
		Tail:       "100",
		Timestamps: true,
	})
	if err != nil {
		s.logger.Warn("start log stream", "container", ctr.Name, "error", err)
		return
	}
	defer reader.Close()

	topic := fmt.Sprintf("project:%s:service:%s:logs", string(projectID), serviceName)
	s.demuxDockerLogs(ctx, reader, topic, serviceName)
}

// demuxDockerLogs reads Docker multiplexed log stream (8-byte header per frame)
// and publishes each line to SSE.
func (s *Streamer) demuxDockerLogs(ctx context.Context, reader io.Reader, topic, serviceName string) {
	header := make([]byte, 8)
	for {
		if ctx.Err() != nil {
			return
		}

		_, err := io.ReadFull(reader, header)
		if err != nil {
			return
		}

		streamType := "stdout"
		if header[0] == 2 {
			streamType = "stderr"
		}

		size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
		if size <= 0 || size > 1024*1024 {
			continue
		}

		payload := make([]byte, size)
		_, err = io.ReadFull(reader, payload)
		if err != nil {
			return
		}

		line := strings.TrimRight(string(payload), "\n\r")
		if line == "" {
			continue
		}

		s.broker.Publish(topic, sse.Event{
			Type: sse.EventLog,
			Data: map[string]string{
				"service": serviceName,
				"stream":  streamType,
				"line":    line,
			},
		})
	}
}

// StreamDockerEvents subscribes to Docker daemon events and publishes
// container lifecycle changes to per-project and global topics.
func (s *Streamer) StreamDockerEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		eventCh, errCh := s.client.Events(ctx, EventsOptions{
			Filters: map[string][]string{
				"type": {"container", "network", "volume"},
			},
		})

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-eventCh:
				if !ok {
					goto reconnect
				}
				s.publishDockerEvent(event)
			case err, ok := <-errCh:
				if !ok {
					goto reconnect
				}
				s.logger.Warn("docker events error", "error", err)
				goto reconnect
			}
		}

	reconnect:
		s.logger.Info("reconnecting docker events stream")
		time.Sleep(5 * time.Second)
	}
}

func (s *Streamer) publishDockerEvent(event DockerEvent) {
	sseEvent := sse.Event{
		Type: sse.EventDockerEvent,
		Data: event,
	}

	projectID := event.Actor.Attributes["com.composecockpit.project"]
	if projectID != "" {
		s.broker.Publish(fmt.Sprintf("project:%s:events", projectID), sseEvent)

		// Publish container state change if it's a container event
		if event.Type == "container" {
			s.broker.Publish(fmt.Sprintf("project:%s:events", projectID), sse.Event{
				Type: sse.EventContainerState,
				Data: map[string]string{
					"container_id": event.Actor.ID,
					"service":      event.Actor.Attributes["com.composecockpit.service"],
					"action":       event.Action,
					"status":       event.Action,
				},
			})
		}
	}
	s.broker.Publish("global:events", sseEvent)
}

// StartStatsCollector runs periodic stats collection for all managed containers.
func (s *Streamer) StartStatsCollector(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.collectAllStats(ctx)
		}
	}
}

func (s *Streamer) collectAllStats(ctx context.Context) {
	containers, err := s.client.ContainerList(ctx, map[string][]string{
		"label":  {"com.composecockpit.project"},
		"status": {"running"},
	})
	if err != nil {
		return
	}

	for _, ctr := range containers {
		if ctx.Err() != nil {
			return
		}

		reader, err := s.client.ContainerStats(ctx, ctr.ID, false)
		if err != nil {
			continue
		}

		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			continue
		}

		stats, err := ParseStats(data)
		if err != nil {
			continue
		}
		stats.ContainerID = ctr.ID
		stats.Name = ctr.Name

		projectID := ctr.Labels["com.composecockpit.project"]
		if projectID != "" {
			s.broker.Publish(fmt.Sprintf("project:%s:stats", projectID), sse.Event{
				Type: sse.EventContainerStats,
				Data: stats,
			})
		}
	}
}

// ParseStatsStream is a helper to decode streaming stats JSON lines.
func ParseStatsStream(data []byte) (*ContainerStats, error) {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return ParseStats(data)
}
