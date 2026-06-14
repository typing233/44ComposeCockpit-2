package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/composecockpit/server/internal/api"
	"github.com/composecockpit/server/internal/api/handlers"
	"github.com/composecockpit/server/internal/auth"
	"github.com/composecockpit/server/internal/config"
	"github.com/composecockpit/server/internal/discovery"
	"github.com/composecockpit/server/internal/docker"
	"github.com/composecockpit/server/internal/orchestrator"
	"github.com/composecockpit/server/internal/sse"
	"github.com/composecockpit/server/internal/store"
)

var (
	buildVersion = "dev"
	buildTime    = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	setLogLevel(logger, cfg.Telemetry.LogLevel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Database
	db, err := store.NewDB(ctx, cfg.Database.URL, cfg.Database.MaxConns, cfg.Database.MinConns, cfg.Database.MaxConnLifetime)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	logger.Info("database connected")

	// Docker client
	dockerClient, err := docker.NewClient(cfg.Docker.Host, cfg.Docker.APIVersion)
	if err != nil {
		logger.Error("create docker client", "error", err)
		os.Exit(1)
	}
	defer dockerClient.Close()

	if err := dockerClient.Ping(ctx); err != nil {
		logger.Error("docker ping failed", "error", err)
		os.Exit(1)
	}
	logger.Info("docker connected")

	// Repositories
	userRepo := store.NewUserRepository(db, cfg.Auth.BcryptCost)
	auditRepo := store.NewAuditRepository(db)
	aclRepo := store.NewACLRepository(db)

	// Auth
	jwtManager := auth.NewJWTManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenTTL, cfg.Auth.RefreshTokenTTL)

	// Discovery
	scanner := discovery.NewScanner(cfg.Discovery.MaxDepth, logger)
	parser := discovery.NewParser(logger)

	// Docker operations
	ops := docker.NewOperations(dockerClient, logger)

	// SSE broker
	broker := sse.NewBroker()
	defer broker.Close()
	sseHandler := sse.NewHandler(broker, logger)

	// Project handler (also serves as project resolver)
	projectHandler := handlers.NewProjectHandler(scanner, parser, ops, aclRepo, cfg.Discovery.RootDir, logger)

	// Orchestrator
	locker := orchestrator.NewInMemoryLocker()
	executor := orchestrator.NewExecutor(ops, locker, projectHandler, auditRepo, logger)

	// Run initial project scan
	if err := projectHandler.InitialScan(ctx); err != nil {
		logger.Warn("initial scan failed", "error", err)
	}

	// Start Docker event streaming
	go streamDockerEvents(ctx, dockerClient, broker, logger)

	// Handlers
	authHandler := handlers.NewAuthHandler(userRepo, jwtManager)
	opHandler := handlers.NewOperationHandler(executor)
	eventsHandler := handlers.NewEventsHandler(sseHandler, logger)
	userHandler := handlers.NewUserHandler(userRepo)
	auditHandler := handlers.NewAuditHandler(auditRepo)
	healthHandler := handlers.NewHealthHandler(db, dockerClient)

	handlers.SetVersion(buildVersion)

	// Router
	router := api.NewRouter(api.RouterDeps{
		AuthHandler:      authHandler,
		ProjectHandler:   projectHandler,
		OperationHandler: opHandler,
		EventsHandler:    eventsHandler,
		UserHandler:      userHandler,
		AuditHandler:     auditHandler,
		HealthHandler:    healthHandler,
		JWTManager:       jwtManager,
		Logger:           logger,
	})

	// HTTP Server
	srv := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("server starting", "addr", cfg.ListenAddr(), "version", buildVersion)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-shutdownCh
	logger.Info("shutdown signal received", "signal", sig.String())

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	broker.Close()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}

	cancel()
	logger.Info("server stopped")
}

func streamDockerEvents(ctx context.Context, client docker.Client, broker sse.Broker, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		eventCh, errCh := client.Events(ctx, docker.EventsOptions{
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
				sseEvent := sse.Event{
					Type: sse.EventDockerEvent,
					Data: event,
				}

				projectID := event.Actor.Attributes["com.composecockpit.project"]
				if projectID != "" {
					broker.Publish(fmt.Sprintf("project:%s:events", projectID), sseEvent)
				}
				broker.Publish("global:events", sseEvent)

			case err, ok := <-errCh:
				if !ok {
					goto reconnect
				}
				logger.Warn("docker events error", "error", err)
				goto reconnect
			}
		}

	reconnect:
		logger.Info("reconnecting docker events stream")
		time.Sleep(5 * time.Second)
	}
}

func setLogLevel(logger *slog.Logger, level string) {
	switch level {
	case "debug":
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	case "warn":
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn})))
	case "error":
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})))
	}
}
