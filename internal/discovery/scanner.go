package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/composecockpit/server/internal/domain"
)

var composeFileNames = []string{
	"docker-compose.yml",
	"docker-compose.yaml",
	"compose.yml",
	"compose.yaml",
}

var overridePatterns = []string{
	"docker-compose.override.yml",
	"docker-compose.override.yaml",
	"compose.override.yml",
	"compose.override.yaml",
}

type DiscoveredProject struct {
	Path         string
	ComposeFiles []string
	EnvFiles     []string
}

type Scanner interface {
	Scan(ctx context.Context, rootDir string) ([]DiscoveredProject, error)
}

type fsScanner struct {
	maxDepth int
	logger   *slog.Logger
}

func NewScanner(maxDepth int, logger *slog.Logger) Scanner {
	return &fsScanner{maxDepth: maxDepth, logger: logger}
}

func (s *fsScanner) Scan(ctx context.Context, rootDir string) ([]DiscoveredProject, error) {
	rootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}

	visited := make(map[uint64]struct{})
	var projects []DiscoveredProject

	err = s.walk(ctx, rootDir, rootDir, 0, visited, &projects)
	if err != nil {
		return nil, err
	}

	s.logger.Info("scan complete", "root", rootDir, "projects_found", len(projects))
	return projects, nil
}

func (s *fsScanner) walk(ctx context.Context, root, current string, depth int, visited map[uint64]struct{}, projects *[]DiscoveredProject) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if depth > s.maxDepth {
		return nil
	}

	info, err := os.Lstat(current)
	if err != nil {
		s.logger.Warn("cannot stat", "path", current, "error", err)
		return nil
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		realPath, err := filepath.EvalSymlinks(current)
		if err != nil {
			s.logger.Warn("broken symlink", "path", current, "error", err)
			return nil
		}
		realInfo, err := os.Stat(realPath)
		if err != nil {
			return nil
		}
		if !realInfo.IsDir() {
			return nil
		}
		stat, ok := realInfo.Sys().(*syscall.Stat_t)
		if ok {
			if _, seen := visited[stat.Ino]; seen {
				s.logger.Warn("symlink loop detected", "path", current, "target", realPath)
				return nil
			}
			visited[stat.Ino] = struct{}{}
		}
		current = realPath
	} else if info.IsDir() {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if ok {
			if _, seen := visited[stat.Ino]; seen {
				return nil
			}
			visited[stat.Ino] = struct{}{}
		}
	}

	entries, err := os.ReadDir(current)
	if err != nil {
		if os.IsPermission(err) {
			s.logger.Warn("permission denied", "path", current)
			return nil
		}
		return nil
	}

	var composeFiles []string
	var envFiles []string

	for _, entry := range entries {
		name := entry.Name()
		for _, cf := range composeFileNames {
			if strings.EqualFold(name, cf) {
				composeFiles = append(composeFiles, filepath.Join(current, name))
			}
		}
		for _, of := range overridePatterns {
			if strings.EqualFold(name, of) {
				composeFiles = append(composeFiles, filepath.Join(current, name))
			}
		}
		if name == ".env" {
			envFiles = append(envFiles, filepath.Join(current, name))
		}
	}

	if len(composeFiles) > 0 {
		composeFiles = deduplicateAndOrder(composeFiles)
		*projects = append(*projects, DiscoveredProject{
			Path:         current,
			ComposeFiles: composeFiles,
			EnvFiles:     envFiles,
		})
	}

	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			continue
		}
		childPath := filepath.Join(current, name)
		if err := s.walk(ctx, root, childPath, depth+1, visited, projects); err != nil {
			return err
		}
	}

	return nil
}

func deduplicateAndOrder(files []string) []string {
	seen := make(map[string]struct{})
	var primary []string
	var overrides []string

	for _, f := range files {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		base := filepath.Base(f)
		if strings.Contains(base, "override") {
			overrides = append(overrides, f)
		} else {
			primary = append(primary, f)
		}
	}
	return append(primary, overrides...)
}

func GenerateProjectID(path string) domain.ProjectID {
	hash := sha256.Sum256([]byte(path))
	return domain.ProjectID(hex.EncodeToString(hash[:12]))
}
