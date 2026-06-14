package sse

import (
	"fmt"
	"log/slog"
	"net/http"
)

type Handler struct {
	broker Broker
	logger *slog.Logger
}

func NewHandler(broker Broker, logger *slog.Logger) *Handler {
	return &Handler{broker: broker, logger: logger}
}

func (h *Handler) ServeSSE(w http.ResponseWriter, r *http.Request, topics []string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := h.broker.Subscribe(r.Context(), topics)
	defer unsubscribe()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}

			data, err := event.Marshal()
			if err != nil {
				h.logger.Error("marshal SSE event", "error", err)
				continue
			}

			fmt.Fprintf(w, "id: %s\n", event.ID)
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
