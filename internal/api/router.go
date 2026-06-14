package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/composecockpit/server/internal/api/handlers"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/httputil"
)

type RouterDeps struct {
	AuthHandler      *handlers.AuthHandler
	ProjectHandler   *handlers.ProjectHandler
	OperationHandler *handlers.OperationHandler
	EventsHandler    *handlers.EventsHandler
	UserHandler      *handlers.UserHandler
	AuditHandler     *handlers.AuditHandler
	HealthHandler    *handlers.HealthHandler
	JWTManager       *auth.JWTManager
	Logger           *slog.Logger
}

func NewRouter(deps RouterDeps) *chi.Mux {
	r := chi.NewRouter()

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(httputil.RequestIDMiddleware)
	r.Use(httputil.LoggingMiddleware(deps.Logger))
	r.Use(httputil.RecoveryMiddleware(deps.Logger))

	// Public endpoints (no auth)
	r.Get("/health", deps.HealthHandler.Liveness)
	r.Get("/ready", deps.HealthHandler.Readiness)
	r.Get("/version", deps.HealthHandler.Version)

	r.Route("/api/v1", func(r chi.Router) {
		// Auth endpoints (no middleware)
		r.Post("/auth/login", deps.AuthHandler.Login)
		r.Post("/auth/refresh", deps.AuthHandler.Refresh)
		r.Post("/auth/register", deps.AuthHandler.Register)

		// Protected endpoints
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(deps.JWTManager))

			// Projects
			r.Get("/projects", deps.ProjectHandler.ListProjects)
			r.Get("/projects/{projectID}", deps.ProjectHandler.GetProjectDetail)
			r.Get("/projects/{projectID}/services", deps.ProjectHandler.ListServices)
			r.Get("/projects/{projectID}/services/{serviceName}", deps.ProjectHandler.GetServiceDetail)

			// Admin-only: trigger scan
			r.With(auth.RequireRole(domain.RoleAdmin)).Post("/projects/scan", deps.ProjectHandler.TriggerScan)

			// Operations (operator + admin)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequirePermission(auth.PermProjectOperate))

				r.Post("/projects/{projectID}/up", deps.OperationHandler.Up)
				r.Post("/projects/{projectID}/down", deps.OperationHandler.Down)
				r.Post("/projects/{projectID}/start", deps.OperationHandler.Start)
				r.Post("/projects/{projectID}/stop", deps.OperationHandler.Stop)
				r.Post("/projects/{projectID}/restart", deps.OperationHandler.Restart)

				r.Post("/projects/{projectID}/services/{serviceName}/start", deps.OperationHandler.ServiceStart)
				r.Post("/projects/{projectID}/services/{serviceName}/stop", deps.OperationHandler.ServiceStop)
				r.Post("/projects/{projectID}/services/{serviceName}/restart", deps.OperationHandler.ServiceRestart)
			})

			// Operations management
			r.Get("/operations/{opID}", deps.OperationHandler.GetOperation)
			r.With(auth.RequireRole(domain.RoleAdmin)).Delete("/operations/{opID}", deps.OperationHandler.CancelOperation)

			// SSE Streaming
			r.Get("/projects/{projectID}/events", deps.EventsHandler.ProjectEvents)
			r.Get("/projects/{projectID}/stats", deps.EventsHandler.ProjectStats)
			r.Get("/projects/{projectID}/services/{serviceName}/logs", deps.EventsHandler.ServiceLogs)
			r.With(auth.RequireRole(domain.RoleAdmin)).Get("/events", deps.EventsHandler.GlobalEvents)

			// Users (admin only)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(domain.RoleAdmin))

				r.Get("/users", deps.UserHandler.List)
				r.Post("/users", deps.UserHandler.Create)
				r.Put("/users/{userID}", deps.UserHandler.Update)
				r.Delete("/users/{userID}", deps.UserHandler.Delete)
			})

			// Audit
			r.With(auth.RequireRole(domain.RoleAdmin)).Get("/audit", deps.AuditHandler.ListAll)
			r.With(auth.RequirePermission(auth.PermAuditRead)).Get("/projects/{projectID}/audit", deps.AuditHandler.ListByProject)
		})
	})

	return r
}
