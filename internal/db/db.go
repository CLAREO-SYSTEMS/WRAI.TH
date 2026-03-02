package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	conn *sql.DB
	path string
}

func New() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	dbDir := filepath.Join(home, ".agent-relay")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dbPath := filepath.Join(dbDir, "relay.db")
	conn, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	conn.SetMaxOpenConns(1)

	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{conn: conn, path: dbPath}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// Path returns the database file path.
func (d *DB) Path() string {
	return d.path
}

// DBPath returns the default database path without opening it.
func DBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agent-relay", "relay.db"), nil
}

// NewReadOnly opens the database in read-only mode for CLI queries.
// Does not run migrations or create the directory.
func NewReadOnly() (*DB, error) {
	dbPath, err := DBPath()
	if err != nil {
		return nil, fmt.Errorf("get db path: %w", err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("database not found at %s (relay never started?)", dbPath)
	}

	conn, err := sql.Open("sqlite3", dbPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db readonly: %w", err)
	}

	conn.SetMaxOpenConns(1)

	return &DB{conn: conn, path: dbPath}, nil
}

func migrate(conn *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS agents (
		id            TEXT PRIMARY KEY,
		name          TEXT NOT NULL UNIQUE,
		role          TEXT NOT NULL DEFAULT '',
		description   TEXT NOT NULL DEFAULT '',
		registered_at TEXT NOT NULL,
		last_seen     TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS messages (
		id         TEXT PRIMARY KEY,
		from_agent TEXT NOT NULL,
		to_agent   TEXT NOT NULL,
		reply_to   TEXT,
		type       TEXT NOT NULL DEFAULT 'notification',
		subject    TEXT NOT NULL DEFAULT '',
		content    TEXT NOT NULL,
		metadata   TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL,
		read_at    TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_messages_to ON messages(to_agent);
	CREATE INDEX IF NOT EXISTS idx_messages_from ON messages(from_agent);
	CREATE INDEX IF NOT EXISTS idx_messages_unread ON messages(to_agent, read_at) WHERE read_at IS NULL;
	CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(reply_to);
	`

	_, err := conn.Exec(schema)
	return err
}
