package server

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  password_hash BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
  token TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS records (
  id TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  host TEXT NOT NULL,
  tag TEXT NOT NULL,
  idx INTEGER NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  timestamp INTEGER NOT NULL,
  nonce BLOB NOT NULL,
  data BLOB NOT NULL,
  UNIQUE(owner, host, tag, idx)
);
CREATE INDEX IF NOT EXISTS records_owner_host_tag_idx ON records(owner, host, tag, idx);
`

// Store is the server-side record store.
type Store struct {
	db   *sql.DB
	path string
}

// Open creates/opens the server SQLite store.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }
