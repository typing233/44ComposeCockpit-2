package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/sse"
)

type EventsHandler struct {
	sseHandler *sse.Handler
	logger     *slog.Logger
}

func NewEventsHandler(sseHandler *sse.Handler, logger *slog.Logger) *EventsHandler {
	return &EventsHandler{sseHandler: sseHandler, logger: logger}
}

func (h *EventsHandler) ProjectEvents(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	topics := []string{
		fmt.Sprintf("project:%s:events", projectID),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) ProjectStats(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	topics := []string{
		fmt.Sprintf("project:%s:stats", projectID),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) ServiceLogs(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	serviceName := chi.URLParam(r, "serviceName")
	topics := []string{
		fmt.Sprintf("project:%s:service:%s:logs", projectID, serviceName),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) GlobalEvents(w http.ResponseWriter, r *http.Request) {
	topics := []string{"global:events"}
	h.sseHandler.ServeSSE(w, r, topics)
}

type LogsHandler struct {
	projectHandler *ProjectHandler
	dockerClient   interface{ ContainerLogs(ctx interface{}, id string, opts interface{}) (interface{}, error) }
	broker         sse.Broker
	logger         *slog.Logger
}

func NewLogsHandler(projectHandler *ProjectHandler, broker sse.Broker, logger *slog.Logger) *LogsHandler {
	return &LogsHandler{
		projectHandler: projectHandler,
		broker:         broker,
		logger:         logger,
	}
}

func (h *LogsHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))
	serviceName := chi.URLParam(r, "serviceName")

	topic := fmt.Sprintf("project:%s:service:%s:logs", string(projectID), serviceName)
	sseH := sse.NewHandler(h.broker, h.logger)
	sseH.ServeSSE(w, r, []string{topic})
}
