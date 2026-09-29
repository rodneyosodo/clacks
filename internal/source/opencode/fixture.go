package opencode

import (
	"context"
	"database/sql"
)

// Schema mirrors the opencode tables clacks syncs (subset of columns is
// fine; adapters intersect on PRAGMA table_info).
const Schema = `
CREATE TABLE IF NOT EXISTS project (
  id TEXT PRIMARY KEY,
  worktree TEXT NOT NULL,
  vcs TEXT,
  name TEXT,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  directory TEXT NOT NULL,
  title TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS session_project_idx ON session(project_id);
CREATE TABLE IF NOT EXISTS message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS message_session_time_created_id_idx ON message(session_id, time_created, id);
CREATE TABLE IF NOT EXISTS part (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES message(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS part_session_idx ON part(session_id);
CREATE INDEX IF NOT EXISTS part_message_id_id_idx ON part(message_id, id);
CREATE TABLE IF NOT EXISTS todo (
  session_id TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  content TEXT NOT NULL,
  status TEXT NOT NULL,
  priority TEXT NOT NULL,
  position INTEGER NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  PRIMARY KEY(session_id, position)
);
CREATE INDEX IF NOT EXISTS todo_session_idx ON todo(session_id);
`

// SchemaV2 mirrors the opencode 2.x tables clacks syncs. v2 renamed `session`
// to `session_v2`, folded part rows into the message's `data` JSON, and dropped
// `todo` entirely, so the synced set is project + session_v2 + session_message.
const SchemaV2 = `
CREATE TABLE IF NOT EXISTS project (
  id TEXT PRIMARY KEY,
  worktree TEXT NOT NULL,
  vcs TEXT,
  name TEXT,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session_v2 (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES project(id) ON DELETE CASCADE,
  directory TEXT NOT NULL,
  title TEXT,
  version TEXT NOT NULL DEFAULT '',
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS session_v2_project_idx ON session_v2(project_id);
CREATE TABLE IF NOT EXISTS session_message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL REFERENCES session_v2(id) ON DELETE CASCADE,
  type TEXT NOT NULL DEFAULT 'user',
  seq INTEGER NOT NULL DEFAULT 0,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS session_message_session_idx ON session_message(session_id, time_created, id);
`

// CreateSchema builds fixture opencode tables.
func CreateSchema(ctx context.Context, db *sql.DB) error {
	return createSchema(ctx, db, Schema)
}

// CreateSchemaV2 builds fixture opencode 2.x tables.
func CreateSchemaV2(ctx context.Context, db *sql.DB) error {
	return createSchema(ctx, db, SchemaV2)
}

func createSchema(ctx context.Context, db *sql.DB, ddl string) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON;"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, ddl)

	return err
}
