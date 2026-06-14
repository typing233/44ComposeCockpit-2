package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server    ServerConfig
	Database  DatabaseConfig
	Docker    DockerConfig
	Auth      AuthConfig
	Discovery DiscoveryConfig
	Telemetry TelemetryConfig
}

type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

type DatabaseConfig struct {
	URL             string
	MaxConns        int
	MinConns        int
	MaxConnLifetime time.Duration
}

type DockerConfig struct {
	Host       string
	APIVersion string
	TLSVerify  bool
	CertPath   string
}

type AuthConfig struct {
	JWTSecret         string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration
	BcryptCost        int
}

type DiscoveryConfig struct {
	RootDir      string
	ScanInterval time.Duration
	MaxDepth     int
}

type TelemetryConfig struct {
	LogLevel       string
	OTLPEndpoint   string
	ServiceName    string
	EnableTracing  bool
}

func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Host:            envOrDefault("HOST", "0.0.0.0"),
			Port:            envIntOrDefault("PORT", 8080),
			ReadTimeout:     envDurationOrDefault("READ_TIMEOUT", 30*time.Second),
			WriteTimeout:    envDurationOrDefault("WRITE_TIMEOUT", 120*time.Second),
			ShutdownTimeout: envDurationOrDefault("SHUTDOWN_TIMEOUT", 30*time.Second),
		},
		Database: DatabaseConfig{
			URL:             envOrDefault("DATABASE_URL", "postgres://cockpit:cockpit@localhost:5432/cockpit?sslmode=disable"),
			MaxConns:        envIntOrDefault("DB_MAX_CONNS", 20),
			MinConns:        envIntOrDefault("DB_MIN_CONNS", 5),
			MaxConnLifetime: envDurationOrDefault("DB_MAX_CONN_LIFETIME", 30*time.Minute),
		},
		Docker: DockerConfig{
			Host:       envOrDefault("DOCKER_HOST", "unix:///var/run/docker.sock"),
			APIVersion: envOrDefault("DOCKER_API_VERSION", "1.44"),
		},
		Auth: AuthConfig{
			JWTSecret:       os.Getenv("JWT_SECRET"),
			AccessTokenTTL:  envDurationOrDefault("ACCESS_TOKEN_TTL", 15*time.Minute),
			RefreshTokenTTL: envDurationOrDefault("REFRESH_TOKEN_TTL", 7*24*time.Hour),
			BcryptCost:      envIntOrDefault("BCRYPT_COST", 12),
		},
		Discovery: DiscoveryConfig{
			RootDir:      envOrDefault("COMPOSE_ROOT", "/data/projects"),
			ScanInterval: envDurationOrDefault("SCAN_INTERVAL", 5*time.Minute),
			MaxDepth:     envIntOrDefault("SCAN_MAX_DEPTH", 10),
		},
		Telemetry: TelemetryConfig{
			LogLevel:      envOrDefault("LOG_LEVEL", "info"),
			OTLPEndpoint:  envOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
			ServiceName:   envOrDefault("OTEL_SERVICE_NAME", "compose-cockpit"),
			EnableTracing: envBoolOrDefault("ENABLE_TRACING", false),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required")
	}
	if len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if c.Discovery.RootDir == "" {
		return fmt.Errorf("COMPOSE_ROOT is required")
	}
	return nil
}

func (c *Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func envDurationOrDefault(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBoolOrDefault(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
