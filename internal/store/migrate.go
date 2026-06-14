package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func (db *DB) AutoMigrate(ctx context.Context) error {
	// Create migrations tracking table if not exists
	_, err := db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, filename := range files {
		// Check if already applied
		var exists bool
		err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, filename).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", filename, err)
		}
		if exists {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + filename)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", filename, err)
		}

		// Extract only the "Up" portion (between "-- +goose Up" and "-- +goose Down")
		sql := extractUpSQL(string(content))

		_, err = db.Pool.Exec(ctx, sql)
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", filename, err)
		}

		_, err = db.Pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, filename)
		if err != nil {
			return fmt.Errorf("record migration %s: %w", filename, err)
		}
	}

	return nil
}

func extractUpSQL(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inUp := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "-- +goose Up" {
			inUp = true
			continue
		}
		if trimmed == "-- +goose Down" {
			break
		}
		if inUp {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
