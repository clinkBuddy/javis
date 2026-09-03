// Package store owns the SQLite database: connection setup and migrations.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, keeps the build CGO-free
)

type DB struct {
	*sql.DB
	log  *slog.Logger
	path string
}

// Open connects to the database at path, creating it if necessary.
//
// MaxOpenConns is deliberately 1. The pure-Go driver still reports
// "database is locked" under concurrent writers even with a busy timeout, and
// serialising access costs nothing at this scale: an admin UI plus a metric
// sampler every few seconds is not a throughput problem.
func Open(ctx context.Context, path string, log *slog.Logger) (*DB, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?" + url.Values{
		"_pragma": []string{
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"foreign_keys(1)",
			"synchronous(NORMAL)",
		},
	}.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}

	return &DB{DB: sqlDB, log: log, path: path}, nil
}

func (d *DB) Path() string { return d.path }

// Close checkpoints the write-ahead log before closing so a cleanly stopped
// JARVIS leaves a self-contained jarvis.db behind. That matters for backups:
// copying the .db file alone would otherwise miss recent writes.
func (d *DB) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := d.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		d.log.Warn("wal checkpoint failed", "err", err)
	}
	return d.DB.Close()
}

// InTx runs fn inside a transaction, rolling back on error.
func (d *DB) InTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
