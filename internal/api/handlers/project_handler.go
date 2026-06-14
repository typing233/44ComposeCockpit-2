package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/discovery"
	"github.com/composecockpit/server/internal/docker"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/store"
	"github.com/composecockpit/server/pkg/apierr"
)

type ProjectHandler struct {
	scanner    discovery.Scanner
	parser     discovery.Parser
	ops        *docker.Operations
	aclRepo    store.ACLRepository
	logger     *slog.Logger

	mu       sync.RWMutex
	projects map[domain.ProjectID]*domain.Project
	rootDir  string
}

func NewProjectHandler(scanner discovery.Scanner, parser discovery.Parser, ops *docker.Operations, aclRepo store.ACLRepository, rootDir string, logger *slog.Logger) *ProjectHandler {
	return &ProjectHandler{
		scanner:  scanner,
		parser:   parser,
		ops:      ops,
		aclRepo:  aclRepo,
		logger:   logger,
		projects: make(map[domain.ProjectID]*domain.Project),
		rootDir:  rootDir,
	}
}

func (h *ProjectHandler) GetProject(ctx context.Context, id domain.ProjectID) (*domain.Project, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.projects[id]
	if !ok {
		return nil, &domain.AppError{
			Code:       apierr.ErrProjectNotFound,
			Message:    "project not found",
			HTTPStatus: 404,
		}
	}
	return p, nil
}

func (h *ProjectHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetClaims(r.Context())
	h.mu.RLock()
	defer h.mu.RUnlock()

	var projects []*domain.Project
	for _, p := range h.projects {
		if claims.Role == domain.RoleAdmin {
			projects = append(projects, p)
			continue
		}
		role, _ := h.aclRepo.GetUserProjectRole(r.Context(), claims.UserID, string(p.ID))
		if role != "" {
			projects = append(projects, p)
		}
	}

	httputil.WriteJSON(w, r, http.StatusOK, projects)
}

func (h *ProjectHandler) GetProjectDetail(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))

	h.mu.RLock()
	project, ok := h.projects[projectID]
	h.mu.RUnlock()

	if !ok {
		httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrProjectNotFound, "project not found", nil)
		return
	}

	status, containerMap, err := h.ops.GetProjectStatus(r.Context(), project)
	if err != nil {
		h.logger.Error("get project status", "error", err)
	} else {
		project.Status = status
		for i, svc := range project.Services {
			if ctr, ok := containerMap[svc.Name]; ok {
				project.Services[i].ContainerID = ctr.ID
				project.Services[i].State = domain.ContainerState(ctr.State)
			}
		}
	}

	httputil.WriteJSON(w, r, http.StatusOK, project)
}

func (h *ProjectHandler) TriggerScan(w http.ResponseWriter, r *http.Request) {
	discovered, err := h.scanner.Scan(r.Context(), h.rootDir)
	if err != nil {
		httputil.WriteError(w, r, http.StatusInternalServerError, apierr.ErrInternal, "scan failed: "+err.Error(), nil)
		return
	}

	h.mu.Lock()
	newProjects := make(map[domain.ProjectID]*domain.Project)
	for _, disc := range discovered {
		project, err := h.parser.Parse(r.Context(), disc)
		if err != nil {
			h.logger.Warn("parse project failed", "path", disc.Path, "error", err)
			continue
		}
		newProjects[project.ID] = project
	}
	h.projects = newProjects
	h.mu.Unlock()

	httputil.WriteJSON(w, r, http.StatusOK, map[string]interface{}{
		"projects_found": len(newProjects),
	})
}

func (h *ProjectHandler) ListServices(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))

	h.mu.RLock()
	project, ok := h.projects[projectID]
	h.mu.RUnlock()

	if !ok {
		httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrProjectNotFound, "project not found", nil)
		return
	}

	httputil.WriteJSON(w, r, http.StatusOK, project.Services)
}

func (h *ProjectHandler) GetServiceDetail(w http.ResponseWriter, r *http.Request) {
	projectID := domain.ProjectID(chi.URLParam(r, "projectID"))
	svcName := chi.URLParam(r, "serviceName")

	h.mu.RLock()
	project, ok := h.projects[projectID]
	h.mu.RUnlock()

	if !ok {
		httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrProjectNotFound, "project not found", nil)
		return
	}

	for _, svc := range project.Services {
		if svc.Name == svcName {
			httputil.WriteJSON(w, r, http.StatusOK, svc)
			return
		}
	}

	httputil.WriteError(w, r, http.StatusNotFound, apierr.ErrServiceNotFound, "service not found", nil)
}

func (h *ProjectHandler) InitialScan(ctx context.Context) error {
	discovered, err := h.scanner.Scan(ctx, h.rootDir)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, disc := range discovered {
		project, err := h.parser.Parse(ctx, disc)
		if err != nil {
			h.logger.Warn("parse project failed", "path", disc.Path, "error", err)
			continue
		}
		h.projects[project.ID] = project
	}

	h.logger.Info("initial scan complete", "projects", len(h.projects))
	return nil
}
