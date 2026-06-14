package handlers

import (
	"net/http"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/docker"
	"github.com/composecockpit/server/internal/store"
)

var version = "dev"

type HealthHandler struct {
	db           *store.DB
	dockerClient docker.Client
}

func NewHealthHandler(db *store.DB, dockerClient docker.Client) *HealthHandler {
	return &HealthHandler{db: db, dockerClient: dockerClient}
}

func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{}

	if err := h.db.Health(r.Context()); err != nil {
		checks["database"] = "unhealthy: " + err.Error()
	} else {
		checks["database"] = "healthy"
	}

	if err := h.dockerClient.Ping(r.Context()); err != nil {
		checks["docker"] = "unhealthy: " + err.Error()
	} else {
		checks["docker"] = "healthy"
	}

	status := http.StatusOK
	for _, v := range checks {
		if v != "healthy" {
			status = http.StatusServiceUnavailable
			break
		}
	}

	httputil.WriteJSON(w, r, status, map[string]interface{}{
		"status": checks,
	})
}

func (h *HealthHandler) Version(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, r, http.StatusOK, map[string]string{
		"version": version,
	})
}

func SetVersion(v string) {
	version = v
}
