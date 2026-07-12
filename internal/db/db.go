package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	SQL  *sql.DB
	Path string
}

func Open(path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	db := &DB{
		SQL:  sqlDB,
		Path: path,
	}

	if err := db.configure(); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return db, nil
}

func (d *DB) Close() error {
	if d == nil || d.SQL == nil {
		return nil
	}
	return d.SQL.Close()
}

func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return d.SQL.PingContext(ctx)
}

func (d *DB) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			raw_input TEXT NOT NULL,
			summary TEXT NOT NULL,
			action TEXT NOT NULL DEFAULT 'run_agent',
			agent TEXT,
			instruction TEXT,
			model TEXT,
			schedule_type TEXT NOT NULL,
			timezone TEXT NOT NULL,
			run_at INTEGER,
			repeat_rule TEXT,
			time_of_day TEXT,
			next_run_at INTEGER NOT NULL,
			cwd TEXT NOT NULL,
			notify_policy TEXT NOT NULL DEFAULT 'default_on',
			channel TEXT NOT NULL,
			channel_ref TEXT,
			tags TEXT,
			status TEXT NOT NULL,
			confirm_status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			last_error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_next_run_status
			ON tasks(next_run_at, status);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			status TEXT NOT NULL,
			ai_input TEXT,
			ai_output TEXT,
			exec_output TEXT,
			notify_output TEXT,
			error TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_task_id ON runs(task_id);`,
		`CREATE TABLE IF NOT EXISTS task_channels (
			task_id TEXT NOT NULL REFERENCES tasks(id),
			sort_order INTEGER NOT NULL,
			channel TEXT NOT NULL,
			channel_ref TEXT,
			PRIMARY KEY (task_id, sort_order)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_task_channels_task_id
			ON task_channels(task_id);`,
		`CREATE TABLE IF NOT EXISTS outbox_notifications (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL REFERENCES tasks(id),
			run_id TEXT REFERENCES runs(id),
			channel TEXT NOT NULL,
			channel_ref TEXT,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			priority TEXT,
			status TEXT NOT NULL,
			retry_count INTEGER NOT NULL,
			next_retry_at INTEGER NOT NULL,
			last_error TEXT,
			delivery_detail TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			sent_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_outbox_notifications_status_retry
			ON outbox_notifications(status, next_retry_at, created_at);`,
	}

	for _, stmt := range stmts {
		if _, err := d.SQL.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}

	if err := d.ensureColumn(ctx, "tasks", "tags", "TEXT"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "tasks", "action", "TEXT NOT NULL DEFAULT 'run_agent'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "tasks", "agent", "TEXT"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "tasks", "instruction", "TEXT"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "tasks", "model", "TEXT"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "tasks", "notify_policy", "TEXT NOT NULL DEFAULT 'default_on'"); err != nil {
		return err
	}
	if err := d.ensureColumn(ctx, "outbox_notifications", "delivery_detail", "TEXT"); err != nil {
		return err
	}

	return nil
}

func (d *DB) configure() error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 5000;`,
		`PRAGMA synchronous = NORMAL;`,
	}

	for _, stmt := range pragmas {
		if _, err := d.SQL.Exec(stmt); err != nil {
			return fmt.Errorf("apply sqlite pragma: %w", err)
		}
	}

	return nil
}

func (d *DB) ensureColumn(ctx context.Context, table, column, definition string) error {
	stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition)
	if _, err := d.SQL.ExecContext(ctx, stmt); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return nil
		}
		return fmt.Errorf("ensure column %s.%s: %w", table, column, err)
	}
	return nil
}
