package discovery_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/composecockpit/server/internal/discovery"
)

func TestScanner_Scan(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scanner := discovery.NewScanner(10, logger)

	testdataDir, _ := filepath.Abs("../../tests/e2e/testdata")
	if _, err := os.Stat(testdataDir); os.IsNotExist(err) {
		t.Skip("testdata directory not found")
	}

	projects, err := scanner.Scan(context.Background(), testdataDir)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if len(projects) < 3 {
		t.Fatalf("expected at least 3 projects, got %d", len(projects))
	}

	foundSimple := false
	foundMulti := false
	for _, p := range projects {
		base := filepath.Base(p.Path)
		switch base {
		case "simple-project":
			foundSimple = true
			if len(p.ComposeFiles) != 1 {
				t.Errorf("simple-project: expected 1 compose file, got %d", len(p.ComposeFiles))
			}
		case "multi-file-project":
			foundMulti = true
			if len(p.ComposeFiles) != 2 {
				t.Errorf("multi-file-project: expected 2 compose files, got %d", len(p.ComposeFiles))
			}
		}
	}

	if !foundSimple {
		t.Error("simple-project not discovered")
	}
	if !foundMulti {
		t.Error("multi-file-project not discovered")
	}
}

func TestScanner_SymlinkLoop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	scanner := discovery.NewScanner(10, logger)

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0o755)

	composeContent := []byte("services:\n  app:\n    image: alpine\n")
	os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), composeContent, 0o644)

	loopLink := filepath.Join(projectDir, "loop")
	os.Symlink(tmpDir, loopLink)

	projects, err := scanner.Scan(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("scan should not fail on symlink loop: %v", err)
	}

	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
}

func TestParser_Parse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	parser := discovery.NewParser(logger)

	testdataDir, _ := filepath.Abs("../../tests/e2e/testdata/simple-project")
	if _, err := os.Stat(testdataDir); os.IsNotExist(err) {
		t.Skip("testdata directory not found")
	}

	disc := discovery.DiscoveredProject{
		Path:         testdataDir,
		ComposeFiles: []string{filepath.Join(testdataDir, "docker-compose.yml")},
	}

	project, err := parser.Parse(context.Background(), disc)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if project.Name != "simple-project" {
		t.Errorf("expected name 'simple-project', got %q", project.Name)
	}

	if len(project.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(project.Services))
	}

	var foundWeb, foundRedis bool
	for _, svc := range project.Services {
		switch svc.Name {
		case "web":
			foundWeb = true
			if svc.Image != "nginx:alpine" {
				t.Errorf("web image: expected nginx:alpine, got %q", svc.Image)
			}
			if len(svc.Ports) != 1 {
				t.Errorf("web ports: expected 1, got %d", len(svc.Ports))
			} else {
				if svc.Ports[0].HostPort != "8081" {
					t.Errorf("web host port: expected 8081, got %q", svc.Ports[0].HostPort)
				}
			}
		case "redis":
			foundRedis = true
			if svc.Image != "redis:7-alpine" {
				t.Errorf("redis image: expected redis:7-alpine, got %q", svc.Image)
			}
		}
	}

	if !foundWeb {
		t.Error("web service not found")
	}
	if !foundRedis {
		t.Error("redis service not found")
	}
}

func TestParser_MultiFileOverride(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	parser := discovery.NewParser(logger)

	testdataDir, _ := filepath.Abs("../../tests/e2e/testdata/multi-file-project")
	if _, err := os.Stat(testdataDir); os.IsNotExist(err) {
		t.Skip("testdata directory not found")
	}

	disc := discovery.DiscoveredProject{
		Path: testdataDir,
		ComposeFiles: []string{
			filepath.Join(testdataDir, "docker-compose.yml"),
			filepath.Join(testdataDir, "docker-compose.override.yml"),
		},
	}

	project, err := parser.Parse(context.Background(), disc)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	var appSvc *struct{ env map[string]string; ports int }
	for _, svc := range project.Services {
		if svc.Name == "app" {
			appSvc = &struct{ env map[string]string; ports int }{env: svc.Environment, ports: len(svc.Ports)}
			break
		}
	}

	if appSvc == nil {
		t.Fatal("app service not found")
	}

	if appSvc.env["APP_ENV"] != "production" {
		t.Errorf("expected APP_ENV=production (from override), got %q", appSvc.env["APP_ENV"])
	}
	if appSvc.env["DEBUG"] != "true" {
		t.Errorf("expected DEBUG=true (from override), got %q", appSvc.env["DEBUG"])
	}
	if appSvc.ports != 1 {
		t.Errorf("expected 1 port from override, got %d", appSvc.ports)
	}
}

func TestParser_EnvVarExpansion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	parser := discovery.NewParser(logger)

	tmpDir := t.TempDir()
	composeContent := []byte(`services:
  app:
    image: ${APP_IMAGE:-myapp:latest}
    environment:
      - DB_HOST=${DB_HOST:-localhost}
`)
	os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), composeContent, 0o644)
	os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("APP_IMAGE=custom:v2\n"), 0o644)

	disc := discovery.DiscoveredProject{
		Path:         tmpDir,
		ComposeFiles: []string{filepath.Join(tmpDir, "docker-compose.yml")},
		EnvFiles:     []string{filepath.Join(tmpDir, ".env")},
	}

	project, err := parser.Parse(context.Background(), disc)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(project.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(project.Services))
	}

	svc := project.Services[0]
	if svc.Image != "custom:v2" {
		t.Errorf("expected image 'custom:v2' (from .env), got %q", svc.Image)
	}
	if svc.Environment["DB_HOST"] != "localhost" {
		t.Errorf("expected DB_HOST=localhost (default), got %q", svc.Environment["DB_HOST"])
	}
}
