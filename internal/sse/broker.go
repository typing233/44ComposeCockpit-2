package sse

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type EventType string

const (
	EventContainerState EventType = "container.state"
	EventContainerStats EventType = "container.stats"
	EventOperation      EventType = "operation"
	EventLog            EventType = "log"
	EventDockerEvent    EventType = "docker.event"
	EventShutdown       EventType = "shutdown"
)

type Event struct {
	ID        string      `json:"id"`
	Type      EventType   `json:"type"`
	Topic     string      `json:"topic"`
	Data      interface{} `json:"data"`
	Timestamp time.Time   `json:"timestamp"`
}

func (e Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

type subscriber struct {
	id     string
	ch     chan Event
	topics map[string]struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

type Broker interface {
	Subscribe(ctx context.Context, topics []string) (<-chan Event, func())
	Publish(topic string, event Event)
	Close()
}

type broker struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	closed      bool
}

func NewBroker() Broker {
	return &broker{
		subscribers: make(map[string]*subscriber),
	}
}

func (b *broker) Subscribe(ctx context.Context, topics []string) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subCtx, cancel := context.WithCancel(ctx)
	id := generateID()

	topicSet := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		topicSet[t] = struct{}{}
	}

	sub := &subscriber{
		id:     id,
		ch:     make(chan Event, 128),
		topics: topicSet,
		ctx:    subCtx,
		cancel: cancel,
	}

	b.subscribers[id] = sub

	go func() {
		<-subCtx.Done()
		b.mu.Lock()
		delete(b.subscribers, id)
		b.mu.Unlock()
		close(sub.ch)
	}()

	unsubscribe := func() {
		cancel()
	}

	return sub.ch, unsubscribe
}

func (b *broker) Publish(topic string, event Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	event.Topic = topic
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.ID == "" {
		event.ID = generateID()
	}

	for _, sub := range b.subscribers {
		if _, ok := sub.topics[topic]; !ok {
			if _, ok := sub.topics["*"]; !ok {
				continue
			}
		}

		select {
		case sub.ch <- event:
		default:
			// drop if subscriber is too slow
		}
	}
}

func (b *broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	shutdownEvent := Event{
		ID:        generateID(),
		Type:      EventShutdown,
		Timestamp: time.Now(),
	}

	for _, sub := range b.subscribers {
		select {
		case sub.ch <- shutdownEvent:
		default:
		}
		sub.cancel()
	}
}

var idCounter uint64
var idMu sync.Mutex

func generateID() string {
	idMu.Lock()
	idCounter++
	id := idCounter
	idMu.Unlock()
	return time.Now().Format("20060102150405") + "-" + json.Number(json.Number(string(rune('0'+id%10)))).String()
}
