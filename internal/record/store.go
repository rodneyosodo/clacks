package record

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS records (
  id TEXT PRIMARY KEY,
  host TEXT NOT NULL,
  tag TEXT NOT NULL,
  idx INTEGER NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  timestamp INTEGER NOT NULL,
  nonce BLOB NOT NULL,
  data BLOB NOT NULL,
  UNIQUE(host, tag, idx)
);
CREATE INDEX IF NOT EXISTS records_host_tag_idx ON records(host, tag, idx);
CREATE TABLE IF NOT EXISTS row_versions (
  table_name TEXT NOT NULL,
  pk TEXT NOT NULL,
  time_updated INTEGER NOT NULL,
  PRIMARY KEY(table_name, pk)
);
CREATE TABLE IF NOT EXISTS applied_idx (
  host TEXT NOT NULL,
  tag TEXT NOT NULL,
  idx INTEGER NOT NULL,
  PRIMARY KEY(host, tag)
);
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// Store is the local encrypted record store (clacks.db).
type Store struct {
	db   *sql.DB
	path string
}

// Open creates/opens the SQLite store.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
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

// toIdx converts a SQLite INTEGER back into the uint64 sequence type. A
// negative value means the row is corrupt, so it is reported rather than
// silently wrapping to a huge index.
func toIdx(v int64) (uint64, error) {
	if v < 0 {
		return 0, fmt.Errorf("invalid record index %d", v)
	}

	return uint64(v), nil
}

// DB exposes the handle for adapters that share row_versions/meta.
func (s *Store) DB() *sql.DB { return s.db }

// NextIdx returns max(idx)+1 for a (host, tag) series.
func (s *Store) NextIdx(ctx context.Context, host, tag string) (uint64, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(idx) FROM records WHERE host=? AND tag=?`, host, tag).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}

	idx, err := toIdx(v.Int64)
	if err != nil {
		return 0, err
	}

	return idx + 1, nil
}

// Append inserts a record; duplicates by id are ignored.
func (s *Store) Append(ctx context.Context, r *Record) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO records(id, host, tag, idx, version, timestamp, nonce, data) VALUES(?,?,?,?,?,?,?,?)`,
		r.ID, r.Host, r.Tag, r.Idx, r.Version, r.Timestamp, r.Nonce, r.Data,
	)

	return err
}

// Has reports whether a record id is already stored.
func (s *Store) Has(ctx context.Context, id string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE id=?`, id).Scan(&n)

	return n > 0, err
}

// List returns up to count records for a series starting at start idx.
func (s *Store) List(ctx context.Context, host, tag string, start, count uint64) ([]*Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, host, tag, idx, version, timestamp, nonce, data FROM records WHERE host=? AND tag=? AND idx>=? ORDER BY idx ASC LIMIT ?`,
		host, tag, start, count,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Record
	for rows.Next() {
		r := &Record{}
		if err := rows.Scan(&r.ID, &r.Host, &r.Tag, &r.Idx, &r.Version, &r.Timestamp, &r.Nonce, &r.Data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}

	return out, rows.Err()
}

// Status returns {host: {tag: maxIdx}}.
func (s *Store) Status(ctx context.Context) (Status, error) {
	st := Status{}
	rows, err := s.db.QueryContext(ctx, `SELECT host, tag, MAX(idx) FROM records GROUP BY host, tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h, t string
		var m int64
		if err := rows.Scan(&h, &t, &m); err != nil {
			return nil, err
		}
		if st[h] == nil {
			st[h] = map[string]uint64{}
		}
		idx, err := toIdx(m)
		if err != nil {
			return nil, err
		}
		st[h][t] = idx
	}

	return st, rows.Err()
}

// RowVersion returns the last synced time_updated for a row.
func (s *Store) RowVersion(ctx context.Context, table, pk string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT time_updated FROM row_versions WHERE table_name=? AND pk=?`, table, pk).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, nil
	}

	return v, err
}

// SetRowVersion records the newest time_updated seen for a row (max-wins).
func (s *Store) SetRowVersion(ctx context.Context, table, pk string, timeUpdated int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO row_versions(table_name, pk, time_updated) VALUES(?,?,?)
		 ON CONFLICT(table_name, pk) DO UPDATE SET time_updated=MAX(time_updated, excluded.time_updated)`,
		table, pk, timeUpdated,
	)

	return err
}

// RowVersions returns all tracked versions (for scan state).
func (s *Store) RowVersions(ctx context.Context) (map[string]int64, error) {
	out := map[string]int64{}
	rows, err := s.db.QueryContext(ctx, `SELECT table_name, pk, time_updated FROM row_versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, pk string
		var v int64
		if err := rows.Scan(&t, &pk, &v); err != nil {
			return nil, err
		}
		out[t+"\x00"+pk] = v
	}

	return out, rows.Err()
}

// AppliedCursor returns the highest applied idx for a series from another host.
func (s *Store) AppliedCursor(ctx context.Context, host, tag string) (int64, error) {
	var v int64
	err := s.db.QueryRowContext(ctx, `SELECT idx FROM applied_idx WHERE host=? AND tag=?`, host, tag).Scan(&v)
	if err == sql.ErrNoRows {
		return -1, nil
	}

	return v, err
}

// SetAppliedCursor advances the applied cursor (max-wins).
func (s *Store) SetAppliedCursor(ctx context.Context, host, tag string, idx uint64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO applied_idx(host, tag, idx) VALUES(?,?,?)
		 ON CONFLICT(host, tag) DO UPDATE SET idx=MAX(idx, excluded.idx)`,
		host, tag, idx,
	)

	return err
}

// MetaGet reads a meta value.
func (s *Store) MetaGet(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}

	return v, err
}

// MetaSet writes a meta value.
func (s *Store) MetaSet(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)

	return err
}

// VersionKey builds a row_versions lookup key.
func VersionKey(table, pk string) string { return table + "\x00" + pk }

// Count returns the number of stored records.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&n)

	return n, err
}
