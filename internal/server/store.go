package server

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
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
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		_ = db.Close()

		return nil, err
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()

		return nil, err
	}

	return &Store{db: db, path: path}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// ResetPassword sets a fresh bcrypt hash for username and revokes every token,
// returning whether the user existed. It is the operator path for a forgotten
// password (`clacks server reset-password`): out-of-band shell access stands
// in for an email recovery flow on a self-hosted server.
func (s *Store) ResetPassword(ctx context.Context, username, password string) (bool, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE username=?`, hash, username)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tokens WHERE username=?`, username); err != nil {
		return false, err
	}

	return true, nil
}
