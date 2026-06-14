package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/composecockpit/server/internal/api/handlers"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/domain"
	"github.com/composecockpit/server/internal/httputil"
	"github.com/composecockpit/server/internal/store"
)

type RouterDeps struct {
	AuthHandler      *handlers.AuthHandler
	ProjectHandler   *handlers.ProjectHandler
	OperationHandler *handlers.OperationHandler
	EventsHandler    *handlers.EventsHandler
	UserHandler      *handlers.UserHandler
	AuditHandler     *handlers.AuditHandler
	ACLHandler       *handlers.ACLHandler
	HealthHandler    *handlers.HealthHandler
	JWTManager       *auth.JWTManager
	ACLRepo          store.ACLRepository
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

			// Projects list (ACL filtering inside handler)
			r.Get("/projects", deps.ProjectHandler.ListProjects)

			// Admin-only: trigger scan
			r.With(auth.RequireRole(domain.RoleAdmin)).Post("/projects/scan", deps.ProjectHandler.TriggerScan)

			// Project-scoped read endpoints (require project ACL read)
			r.Route("/projects/{projectID}", func(r chi.Router) {
				r.Use(handlers.ProjectACLMiddleware(deps.ACLRepo, auth.PermProjectRead))

				r.Get("/", deps.ProjectHandler.GetProjectDetail)
				r.Get("/services", deps.ProjectHandler.ListServices)
				r.Get("/services/{serviceName}", deps.ProjectHandler.GetServiceDetail)

				// SSE Streaming (read permission)
				r.Get("/events", deps.EventsHandler.ProjectEvents)
				r.Get("/stats", deps.EventsHandler.ProjectStats)
				r.Get("/services/{serviceName}/logs", deps.EventsHandler.ServiceLogs)

				// Audit (read permission)
				r.Get("/audit", deps.AuditHandler.ListByProject)

				// ACL management (admin only)
				r.Route("/acl", func(r chi.Router) {
					r.Use(auth.RequireRole(domain.RoleAdmin))
					r.Get("/", deps.ACLHandler.ListByProject)
					r.Post("/", deps.ACLHandler.Grant)
					r.Delete("/{userID}", deps.ACLHandler.Revoke)
				})

				// Operations (require project ACL operate)
				r.Group(func(r chi.Router) {
					r.Use(handlers.ProjectACLMiddleware(deps.ACLRepo, auth.PermProjectOperate))
					r.Use(auth.RequirePermission(auth.PermProjectOperate))

					r.Post("/up", deps.OperationHandler.Up)
					r.Post("/down", deps.OperationHandler.Down)
					r.Post("/start", deps.OperationHandler.Start)
					r.Post("/stop", deps.OperationHandler.Stop)
					r.Post("/restart", deps.OperationHandler.Restart)

					r.Post("/services/{serviceName}/start", deps.OperationHandler.ServiceStart)
					r.Post("/services/{serviceName}/stop", deps.OperationHandler.ServiceStop)
					r.Post("/services/{serviceName}/restart", deps.OperationHandler.ServiceRestart)

					// Async submit (queued)
					r.Post("/async/{action}", deps.OperationHandler.SubmitAsync)
				})
			})

			// Operations management
			r.Get("/operations", deps.OperationHandler.ListRunning)
			r.Get("/operations/{opID}", deps.OperationHandler.GetOperation)
			r.With(auth.RequireRole(domain.RoleAdmin)).Delete("/operations/{opID}", deps.OperationHandler.CancelOperation)

			// Global events (admin)
			r.With(auth.RequireRole(domain.RoleAdmin)).Get("/events", deps.EventsHandler.GlobalEvents)

			// Users (admin only)
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireRole(domain.RoleAdmin))
				r.Get("/users", deps.UserHandler.List)
				r.Post("/users", deps.UserHandler.Create)
				r.Put("/users/{userID}", deps.UserHandler.Update)
				r.Delete("/users/{userID}", deps.UserHandler.Delete)
			})

			// Global audit (admin only)
			r.With(auth.RequireRole(domain.RoleAdmin)).Get("/audit", deps.AuditHandler.ListAll)
		})
	})

	return r
}
