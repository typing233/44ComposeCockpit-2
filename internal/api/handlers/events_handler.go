package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/docker"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/sse"
)

type EventsHandler struct {
	sseHandler *sse.Handler
	streamer   *docker.Streamer
	projectHandler *ProjectHandler
	logger     *slog.Logger
}

func NewEventsHandler(sseHandler *sse.Handler, streamer *docker.Streamer, projectHandler *ProjectHandler, logger *slog.Logger) *EventsHandler {
	return &EventsHandler{
		sseHandler: sseHandler,
		streamer:   streamer,
		projectHandler: projectHandler,
		logger:     logger,
	}
}

func (h *EventsHandler) ProjectEvents(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")
	topics := []string{
		fmt.Sprintf("project:%s:events", projectID),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) ProjectStats(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))

	// Start streaming stats for this project when a client connects
	project, err := h.projectHandler.GetProject(r.Context(), projectID)
	if err == nil && project != nil {
		go h.streamer.StreamProjectStats(r.Context(), project)
	}

	topics := []string{
		fmt.Sprintf("project:%s:stats", string(projectID)),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) ServiceLogs(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))
	serviceName := chi.URLParam(r, "serviceName")

	// Start streaming logs when a client connects
	go h.streamer.StreamProjectLogs(r.Context(), projectID, serviceName)

	topics := []string{
		fmt.Sprintf("project:%s:service:%s:logs", string(projectID), serviceName),
	}
	h.sseHandler.ServeSSE(w, r, topics)
}

func (h *EventsHandler) GlobalEvents(w http.ResponseWriter, r *http.Request) {
	topics := []string{"global:events"}
	h.sseHandler.ServeSSE(w, r, topics)
}
