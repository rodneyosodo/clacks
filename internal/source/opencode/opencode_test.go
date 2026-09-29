package opencode

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodneyosodo/clacks/internal/record"
	_ "modernc.org/sqlite"
)

func openFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", p+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	return p, db
}

func seedSession(t *testing.T, db *sql.DB, title string, tu int64) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `INSERT OR IGNORE INTO project(id, worktree, time_created, time_updated) VALUES('proj1','/home/alice/work',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO session(id, project_id, directory, title, version, time_created, time_updated) VALUES('ses_1','proj1','/home/alice/work',?, 'v',1,?)`, title, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES('msg_1','ses_1',1,?, '{"role":"user"}')`, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES('prt_1','msg_1','ses_1',1,?, '{"kind":"text"}')`, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO todo(session_id, content, status, priority, position, time_created, time_updated) VALUES('ses_1','write code','pending','high',0,1,?)`, tu); err != nil {
		t.Fatal(err)
	}
}

func TestMissingDBHelpfulError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "opencode.db")
	src := &Source{DBPath: missing}
	_, err := src.Scan(t.Context(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("scan should name the missing path, got: %v", err)
	}
	err = src.Apply(t.Context(), []record.Change{{Table: "session", PK: "s"}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("apply should name the missing path, got: %v", err)
	}
	// Empty apply is a no-op even without a DB.
	if err := src.Apply(t.Context(), nil); err != nil {
		t.Fatalf("empty apply should succeed, got: %v", err)
	}
}

func TestUninitialisedDBHelpfulError(t *testing.T) {
	// A file exists but opencode never created its tables.
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", p+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE junk(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	src := &Source{DBPath: p}
	_, err = src.Scan(t.Context(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), "not initialised") || !strings.Contains(err.Error(), p) {
		t.Fatalf("scan should report uninitialised DB with path, got: %v", err)
	}
	err = src.Apply(t.Context(), []record.Change{{Table: "session", PK: "s"}})
	if err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("apply should report uninitialised DB, got: %v", err)
	}
}

func TestChannelSiblingHint(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", main+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE junk(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// The live database on a non-latest channel install.
	sib := filepath.Join(dir, "opencode-dev.db")
	db2, err := sql.Open("sqlite", sib+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateSchema(t.Context(), db2); err != nil {
		t.Fatal(err)
	}
	db2.Close()
	src := &Source{DBPath: main}
	_, err = src.Scan(t.Context(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), "opencode-dev.db") || !strings.Contains(err.Error(), "opencode debug paths db") {
		t.Fatalf("scan should hint at the channel sibling, got: %v", err)
	}
}

func countRows(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

// openV2Fixture creates an opencode 2.x database at a temp path.
func openV2Fixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", p+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaV2(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	return p, db
}

func seedSessionV2(t *testing.T, db *sql.DB, id string, tu int64) {
	t.Helper()
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT OR IGNORE INTO project(id, worktree, time_created, time_updated) VALUES('proj1','/home/alice/work',1,1)`, nil},
		{`INSERT INTO session_v2(id, project_id, directory, title, version, time_created, time_updated) VALUES(?,'proj1','/home/alice/work','hello','v',1,?)`, []any{id, tu}},
		{`INSERT INTO session_message(id, session_id, type, seq, time_created, time_updated, data) VALUES(?,?,'user',0,1,?,'{}')`, []any{"msg_" + id, id, tu}},
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(t.Context(), s.q, s.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanV2(t *testing.T) {
	p, db := openV2Fixture(t)
	defer db.Close()
	seedSessionV2(t, db, "ses_1", 100)

	src := &Source{DBPath: p}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range changes {
		got[c.Table] = true
		if c.PK == "" && !c.Tombstone {
			t.Fatalf("change with empty pk: %+v", c)
		}
	}
	// v2 syncs project, session_v2 and session_message; there is no part or todo.
	for _, want := range []string{"project", "session_v2", "session_message"} {
		if !got[want] {
			t.Errorf("missing change for table %q; got %v", want, got)
		}
	}
	if got["session"] || got["part"] || got["todo"] {
		t.Errorf("v1-only tables scanned: %v", got)
	}
}

func TestScanApplyV2RoundTrip(t *testing.T) {
	srcPath, srcDB := openV2Fixture(t)
	defer srcDB.Close()
	seedSessionV2(t, srcDB, "ses_1", 100)

	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}

	dstPath, dstDB := openV2Fixture(t)
	defer dstDB.Close()
	dst := &Source{DBPath: dstPath, LocalHome: "/home/u"}
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, dstDB, `SELECT COUNT(*) FROM session_v2 WHERE id='ses_1'`); got != 1 {
		t.Fatalf("session_v2 rows = %d, want 1", got)
	}
	if got := countRows(t, dstDB, `SELECT COUNT(*) FROM session_message WHERE session_id='ses_1'`); got != 1 {
		t.Fatalf("session_message rows = %d, want 1", got)
	}
}

func TestMixedVersionApplySkipsForeignTables(t *testing.T) {
	// v1 records arriving at a v2 machine (and vice versa) must be skipped
	// rather than erroring or half-writing.
	v1Path, v1DB := openFixture(t)
	defer v1DB.Close()
	seedSession(t, v1DB, "hello", 100)
	v1Changes, err := (&Source{DBPath: v1Path}).Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}

	v2Path, v2DB := openV2Fixture(t)
	defer v2DB.Close()
	v2Src := &Source{DBPath: v2Path}
	if err := v2Src.Apply(t.Context(), v1Changes); err != nil {
		t.Fatalf("v1 -> v2 apply should skip, got: %v", err)
	}
	if len(v2Src.Warns) == 0 {
		t.Error("expected a warning naming the skipped tables")
	}
	if got := countRows(t, v2DB, `SELECT COUNT(*) FROM session_v2`); got != 0 {
		t.Errorf("v2 machine gained %d v2 rows from v1 records", got)
	}

	// And the reverse direction. The v1 database already holds its own seeded
	// session, so assert it is unchanged rather than empty.
	before := countRows(t, v1DB, `SELECT COUNT(*) FROM session`)
	v2Src2 := &Source{DBPath: v2Path}
	seedSessionV2(t, v2DB, "ses_9", 100)
	v2Changes, err := v2Src2.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	v1Dst := &Source{DBPath: v1Path}
	if err := v1Dst.Apply(t.Context(), v2Changes); err != nil {
		t.Fatalf("v2 -> v1 apply should skip, got: %v", err)
	}
	if after := countRows(t, v1DB, `SELECT COUNT(*) FROM session`); after != before {
		t.Errorf("v1 session rows changed %d -> %d applying v2 records", before, after)
	}
}

func TestScanApplyRoundTrip(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)

	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	// project + session + message + part + todo = 5 changes.
	if len(changes) != 5 {
		t.Fatalf("want 5 changes, got %d: %+v", len(changes), changes)
	}

	dstPath, _ := openFixture(t)
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	var title, dir, worktree string
	if err := srcDB.QueryRowContext(t.Context(), `SELECT title FROM session WHERE id='ses_1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	_ = title
	db2, err := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRowContext(t.Context(), `SELECT title, directory FROM session WHERE id='ses_1'`).Scan(&title, &dir); err != nil {
		t.Fatal(err)
	}
	if title != "hello" {
		t.Fatalf("title=%q", title)
	}
	if err := db2.QueryRowContext(t.Context(), `SELECT worktree FROM project WHERE id='proj1'`).Scan(&worktree); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, q := range []string{
		`SELECT COUNT(*) FROM message WHERE session_id='ses_1'`,
		`SELECT COUNT(*) FROM part WHERE session_id='ses_1'`,
		`SELECT COUNT(*) FROM todo WHERE session_id='ses_1'`,
	} {
		if err := db2.QueryRowContext(t.Context(), q).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s -> %d, %v", q, n, err)
		}
	}
}

func TestScanIncremental(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, ch := range changes {
		versions[record.VersionKey(ch.Table, ch.PK)] = ch.TimeUpdated
	}
	again, err := src.Scan(t.Context(), versions)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("want 0 incremental changes, got %d", len(again))
	}
	// Touch one message; only it should re-emit.
	if _, err := srcDB.ExecContext(t.Context(), `UPDATE message SET data='{"role":"assistant"}', time_updated=200 WHERE id='msg_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.ExecContext(t.Context(), `UPDATE session SET time_updated=200 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	third, err := src.Scan(t.Context(), versions)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 2 { // session + message
		t.Fatalf("want 2 changes, got %d: %+v", len(third), third)
	}
}

func TestConflictNewerWins(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "old", 100)
	dstPath, dstDB := openFixture(t)
	seedSession(t, dstDB, "new", 200)

	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	var title string
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	_ = db2.QueryRowContext(t.Context(), `SELECT title FROM session WHERE id='ses_1'`).Scan(&title)
	if title != "new" {
		t.Fatalf("older write won: %q", title)
	}
	// Now the newer side wins.
	if _, err := srcDB.ExecContext(t.Context(), `UPDATE session SET title='newest', time_updated=300 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	changes, _ = src.Scan(t.Context(), map[string]int64{
		record.VersionKey("session", "ses_1"): 200,
		record.VersionKey("project", "proj1"): 1,
		record.VersionKey("message", "msg_1"): 100,
		record.VersionKey("part", "prt_1"):    100,
		record.VersionKey("todo", "ses_1:0"):  100,
	})
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	_ = db2.QueryRowContext(t.Context(), `SELECT title FROM session WHERE id='ses_1'`).Scan(&title)
	if title != "newest" {
		t.Fatalf("newer write lost: %q", title)
	}
}

func TestPathRewrite(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath}
	changes, _ := src.Scan(t.Context(), map[string]int64{})

	dstPath, _ := openFixture(t)
	dst := &Source{DBPath: dstPath, LocalHome: "/home/bob"}
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	var dir, work string
	_ = db2.QueryRowContext(t.Context(), `SELECT directory FROM session WHERE id='ses_1'`).Scan(&dir)
	_ = db2.QueryRowContext(t.Context(), `SELECT worktree FROM project WHERE id='proj1'`).Scan(&work)
	if dir != "/home/bob/work" {
		t.Fatalf("directory not rewritten: %q", dir)
	}
	if work != "/home/bob/work" {
		t.Fatalf("worktree not rewritten: %q", work)
	}
	// Explicit path_map wins over the HOME default.
	dst2Path, _ := openFixture(t)
	dst2 := &Source{DBPath: dst2Path, PathMap: [][2]string{{"/home/alice/work", "/data/w"}}, LocalHome: "/home/bob"}
	if err := dst2.Apply(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	db3, _ := sql.Open("sqlite", "file:"+dst2Path+"?mode=ro")
	defer db3.Close()
	_ = db3.QueryRowContext(t.Context(), `SELECT directory FROM session WHERE id='ses_1'`).Scan(&dir)
	if dir != "/data/w" {
		t.Fatalf("path_map not applied: %q", dir)
	}
	// Existing local project keeps its worktree.
	db3rw, err := sql.Open("sqlite", dst2Path+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db3rw.Close()
	if _, err := db3rw.ExecContext(t.Context(), `UPDATE project SET worktree='/home/bob/other' WHERE id='proj1'`); err != nil {
		t.Fatal(err)
	}
	_ = dst2.Apply(t.Context(), changes)
	_ = db3.QueryRowContext(t.Context(), `SELECT worktree FROM project WHERE id='proj1'`).Scan(&work)
	if work != "/home/bob/other" {
		t.Fatalf("local worktree clobbered: %q", work)
	}
}

func TestSchemaDriftExtraColumn(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	if _, err := srcDB.ExecContext(t.Context(), `ALTER TABLE session ADD COLUMN workspace_id TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.ExecContext(t.Context(), `UPDATE session SET workspace_id='ws1', time_updated=150 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(t.Context(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	dstPath, _ := openFixture(t) // no workspace_id column
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(t.Context(), changes); err != nil {
		t.Fatalf("apply with drifted schema failed: %v", err)
	}
}

func TestPropagateDeletes(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath, PropagateDeletes: true}
	changes, _ := src.Scan(t.Context(), map[string]int64{})
	versions := map[string]int64{}
	for _, ch := range changes {
		versions[record.VersionKey(ch.Table, ch.PK)] = ch.TimeUpdated
	}
	if _, err := srcDB.ExecContext(t.Context(), `DELETE FROM session WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	tombs, err := src.Scan(t.Context(), versions)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ch := range tombs {
		if ch.Tombstone && ch.Table == "session" && ch.PK == "ses_1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tombstone emitted: %+v", tombs)
	}
	dstPath, _ := openFixture(t)
	dst := &Source{DBPath: dstPath}
	_ = dst.Apply(t.Context(), changes)
	if err := dst.Apply(t.Context(), tombs); err != nil {
		t.Fatal(err)
	}
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	var n int
	_ = db2.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM session WHERE id='ses_1'`).Scan(&n)
	if n != 0 {
		t.Fatal("tombstone not applied")
	}
}
