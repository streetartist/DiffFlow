package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// DB owns the persistent state for the first formal DiffFlow server model.
type DB struct {
	db *sql.DB
	mu sync.Mutex
}

func NewDB(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// WAL mode for better concurrent performance
	db.Exec("PRAGMA journal_mode=WAL")

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER DEFAULT (strftime('%s','now'))
		);
		CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			created_at INTEGER DEFAULT (strftime('%s','now'))
		);
		CREATE TABLE IF NOT EXISTS project_members (
			user_id INTEGER NOT NULL,
			project_id INTEGER NOT NULL,
			created_at INTEGER DEFAULT (strftime('%s','now')),
			PRIMARY KEY (user_id, project_id)
		);
		CREATE TABLE IF NOT EXISTS invite_keys (
			key TEXT PRIMARY KEY,
			max_uses INTEGER NOT NULL DEFAULT 1,
			uses INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER DEFAULT (strftime('%s','now'))
		);
		CREATE TABLE IF NOT EXISTS invite_projects (
			key TEXT NOT NULL,
			project_id INTEGER NOT NULL,
			PRIMARY KEY (key, project_id)
		);
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS file_snapshots (
			project_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			updated_by INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER DEFAULT (strftime('%s','now')),
			deleted INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (project_id, path)
		);
		CREATE TABLE IF NOT EXISTS file_objects (
			sha256 TEXT PRIMARY KEY,
			size INTEGER NOT NULL,
			created_at INTEGER DEFAULT (strftime('%s','now'))
		);
		CREATE INDEX IF NOT EXISTS idx_project_members_project ON project_members(project_id);
		CREATE INDEX IF NOT EXISTS idx_file_snapshots_project ON file_snapshots(project_id);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}
