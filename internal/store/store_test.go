package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sjkim/jarvis/internal/logging"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), logging.Discard())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateCreatesSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	want := []string{
		"schema_migrations", "settings", "users", "sessions", "jdks", "apps",
		"artifacts", "jvm_profiles", "instances", "ports", "events",
		"metric_samples", "host_samples", "alert_rules", "alerts", "audit_logs",
	}
	for _, table := range want {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Compared against the embedded set rather than a hardcoded number, so
	// adding a migration does not break this test.
	names, err := migrationNames()
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if n != len(names) {
		t.Errorf("schema_migrations rows = %d, want %d", n, len(names))
	}
}

// Every app column the launcher reads must exist after migration; a missing
// one only shows up as a scan error at the moment someone tries to start an
// app.
func TestMigrateAddsActiveArtifactColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO apps (name) VALUES ('order-api')`); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	var id, activeArtifact any
	if err := db.QueryRowContext(ctx,
		`SELECT id, active_artifact_id FROM apps WHERE name = 'order-api'`).
		Scan(&id, &activeArtifact); err != nil {
		t.Fatalf("select active_artifact_id: %v", err)
	}
	if activeArtifact != nil {
		t.Errorf("active_artifact_id = %v on a new app, want NULL", activeArtifact)
	}
}

// The port registry is what stops two apps from being pointed at the same
// listener on a single host, so the constraint is worth pinning down.
func TestPortRegistryRejectsDuplicates(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, name := range []string{"order-api", "billing-api"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO apps (name) VALUES (?)`, name); err != nil {
			t.Fatalf("insert app %s: %v", name, err)
		}
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO ports (app_id, port) VALUES ((SELECT id FROM apps WHERE name = 'order-api'), 8080)`); err != nil {
		t.Fatalf("first port insert: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ports (app_id, port) VALUES ((SELECT id FROM apps WHERE name = 'billing-api'), 8080)`); err == nil {
		t.Error("second app claimed port 8080, want a uniqueness error")
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO artifacts (app_id, version, file_name, rel_path, size_bytes, sha256)
		 VALUES (999, '1.0.0', 'app.jar', 'x/app.jar', 1, 'deadbeef')`); err == nil {
		t.Error("artifact referencing a missing app was accepted, want a foreign key error")
	}
}
