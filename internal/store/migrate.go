package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies every embedded migration that has not run yet. Each file
// runs in its own transaction and is recorded in schema_migrations, so a
// partially applied upgrade never leaves a half-migrated schema behind.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := d.appliedMigrations(ctx)
	if err != nil {
		return err
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		err = d.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name)
			return err
		})
		if err != nil {
			return err
		}
		d.log.Info("migration applied", "name", name)
	}
	return nil
}

func (d *DB) appliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := d.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
