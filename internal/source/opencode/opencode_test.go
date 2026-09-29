package opencode

import (
	"context"
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
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchema(db); err != nil {
		t.Fatal(err)
	}
	return p, db
}

func seedSession(t *testing.T, db *sql.DB, title string, tu int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO project(id, worktree, time_created, time_updated) VALUES('proj1','/home/alice/work',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO session(id, project_id, directory, title, version, time_created, time_updated) VALUES('ses_1','proj1','/home/alice/work',?, 'v',1,?)`, title, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES('msg_1','ses_1',1,?, '{"role":"user"}')`, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES('prt_1','msg_1','ses_1',1,?, '{"kind":"text"}')`, tu); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO todo(session_id, content, status, priority, position, time_created, time_updated) VALUES('ses_1','write code','pending','high',0,1,?)`, tu); err != nil {
		t.Fatal(err)
	}
}

func TestMissingDBHelpfulError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "opencode.db")
	src := &Source{DBPath: missing}
	_, err := src.Scan(context.Background(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("scan should name the missing path, got: %v", err)
	}
	err = src.Apply(context.Background(), []record.Change{{Table: "session", PK: "s"}})
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("apply should name the missing path, got: %v", err)
	}
	// Empty apply is a no-op even without a DB.
	if err := src.Apply(context.Background(), nil); err != nil {
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
	if _, err := db.Exec(`CREATE TABLE junk(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	src := &Source{DBPath: p}
	_, err = src.Scan(context.Background(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), "not initialised") || !strings.Contains(err.Error(), p) {
		t.Fatalf("scan should report uninitialised DB with path, got: %v", err)
	}
	err = src.Apply(context.Background(), []record.Change{{Table: "session", PK: "s"}})
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
	if _, err := db.Exec(`CREATE TABLE junk(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// The live database on a non-latest channel install.
	sib := filepath.Join(dir, "opencode-dev.db")
	db2, err := sql.Open("sqlite", sib+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateSchema(db2); err != nil {
		t.Fatal(err)
	}
	db2.Close()
	src := &Source{DBPath: main}
	_, err = src.Scan(context.Background(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), "opencode-dev.db") || !strings.Contains(err.Error(), "opencode debug paths db") {
		t.Fatalf("scan should hint at the channel sibling, got: %v", err)
	}
}

func TestV2SchemaHelpfulError(t *testing.T) {
	// An opencode v2 database: session_v2 exists, v1 tables don't.
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", p+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session_v2(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	src := &Source{DBPath: p}
	_, err = src.Scan(context.Background(), map[string]int64{})
	if err == nil || !strings.Contains(err.Error(), "v2 schema") {
		t.Fatalf("scan should report v2 schema mismatch, got: %v", err)
	}
}

func TestScanApplyRoundTrip(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)

	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(context.Background(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	// project + session + message + part + todo = 5 changes.
	if len(changes) != 5 {
		t.Fatalf("want 5 changes, got %d: %+v", len(changes), changes)
	}

	dstPath, _ := openFixture(t)
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	var title, dir, worktree string
	if err := srcDB.QueryRow(`SELECT title FROM session WHERE id='ses_1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	_ = title
	db2, err := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.QueryRow(`SELECT title, directory FROM session WHERE id='ses_1'`).Scan(&title, &dir); err != nil {
		t.Fatal(err)
	}
	if title != "hello" {
		t.Fatalf("title=%q", title)
	}
	if err := db2.QueryRow(`SELECT worktree FROM project WHERE id='proj1'`).Scan(&worktree); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, q := range []string{
		`SELECT COUNT(*) FROM message WHERE session_id='ses_1'`,
		`SELECT COUNT(*) FROM part WHERE session_id='ses_1'`,
		`SELECT COUNT(*) FROM todo WHERE session_id='ses_1'`,
	} {
		if err := db2.QueryRow(q).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s -> %d, %v", q, n, err)
		}
	}
}

func TestScanIncremental(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(context.Background(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, ch := range changes {
		versions[record.VersionKey(ch.Table, ch.PK)] = ch.TimeUpdated
	}
	again, err := src.Scan(context.Background(), versions)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("want 0 incremental changes, got %d", len(again))
	}
	// Touch one message; only it should re-emit.
	if _, err := srcDB.Exec(`UPDATE message SET data='{"role":"assistant"}', time_updated=200 WHERE id='msg_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.Exec(`UPDATE session SET time_updated=200 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	third, err := src.Scan(context.Background(), versions)
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
	changes, err := src.Scan(context.Background(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	var title string
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	_ = db2.QueryRow(`SELECT title FROM session WHERE id='ses_1'`).Scan(&title)
	if title != "new" {
		t.Fatalf("older write won: %q", title)
	}
	// Now the newer side wins.
	if _, err := srcDB.Exec(`UPDATE session SET title='newest', time_updated=300 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	changes, _ = src.Scan(context.Background(), map[string]int64{
		record.VersionKey("session", "ses_1"): 200,
		record.VersionKey("project", "proj1"): 1,
		record.VersionKey("message", "msg_1"): 100,
		record.VersionKey("part", "prt_1"):    100,
		record.VersionKey("todo", "ses_1:0"):  100,
	})
	if err := dst.Apply(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	_ = db2.QueryRow(`SELECT title FROM session WHERE id='ses_1'`).Scan(&title)
	if title != "newest" {
		t.Fatalf("newer write lost: %q", title)
	}
}

func TestPathRewrite(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath}
	changes, _ := src.Scan(context.Background(), map[string]int64{})

	dstPath, _ := openFixture(t)
	dst := &Source{DBPath: dstPath, LocalHome: "/home/bob"}
	if err := dst.Apply(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	var dir, work string
	_ = db2.QueryRow(`SELECT directory FROM session WHERE id='ses_1'`).Scan(&dir)
	_ = db2.QueryRow(`SELECT worktree FROM project WHERE id='proj1'`).Scan(&work)
	if dir != "/home/bob/work" {
		t.Fatalf("directory not rewritten: %q", dir)
	}
	if work != "/home/bob/work" {
		t.Fatalf("worktree not rewritten: %q", work)
	}
	// Explicit path_map wins over the HOME default.
	dst2Path, _ := openFixture(t)
	dst2 := &Source{DBPath: dst2Path, PathMap: [][2]string{{"/home/alice/work", "/data/w"}}, LocalHome: "/home/bob"}
	if err := dst2.Apply(context.Background(), changes); err != nil {
		t.Fatal(err)
	}
	db3, _ := sql.Open("sqlite", "file:"+dst2Path+"?mode=ro")
	defer db3.Close()
	_ = db3.QueryRow(`SELECT directory FROM session WHERE id='ses_1'`).Scan(&dir)
	if dir != "/data/w" {
		t.Fatalf("path_map not applied: %q", dir)
	}
	// Existing local project keeps its worktree.
	db3rw, err := sql.Open("sqlite", dst2Path+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db3rw.Close()
	if _, err := db3rw.Exec(`UPDATE project SET worktree='/home/bob/other' WHERE id='proj1'`); err != nil {
		t.Fatal(err)
	}
	_ = dst2.Apply(context.Background(), changes)
	_ = db3.QueryRow(`SELECT worktree FROM project WHERE id='proj1'`).Scan(&work)
	if work != "/home/bob/other" {
		t.Fatalf("local worktree clobbered: %q", work)
	}
}

func TestSchemaDriftExtraColumn(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	if _, err := srcDB.Exec(`ALTER TABLE session ADD COLUMN workspace_id TEXT`); err != nil {
		t.Fatal(err)
	}
	if _, err := srcDB.Exec(`UPDATE session SET workspace_id='ws1', time_updated=150 WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	src := &Source{DBPath: srcPath}
	changes, err := src.Scan(context.Background(), map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	dstPath, _ := openFixture(t) // no workspace_id column
	dst := &Source{DBPath: dstPath}
	if err := dst.Apply(context.Background(), changes); err != nil {
		t.Fatalf("apply with drifted schema failed: %v", err)
	}
}

func TestPropagateDeletes(t *testing.T) {
	srcPath, srcDB := openFixture(t)
	seedSession(t, srcDB, "hello", 100)
	src := &Source{DBPath: srcPath, PropagateDeletes: true}
	changes, _ := src.Scan(context.Background(), map[string]int64{})
	versions := map[string]int64{}
	for _, ch := range changes {
		versions[record.VersionKey(ch.Table, ch.PK)] = ch.TimeUpdated
	}
	if _, err := srcDB.Exec(`DELETE FROM session WHERE id='ses_1'`); err != nil {
		t.Fatal(err)
	}
	tombs, err := src.Scan(context.Background(), versions)
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
	_ = dst.Apply(context.Background(), changes)
	if err := dst.Apply(context.Background(), tombs); err != nil {
		t.Fatal(err)
	}
	db2, _ := sql.Open("sqlite", "file:"+dstPath+"?mode=ro")
	defer db2.Close()
	var n int
	_ = db2.QueryRow(`SELECT COUNT(*) FROM session WHERE id='ses_1'`).Scan(&n)
	if n != 0 {
		t.Fatal("tombstone not applied")
	}
}
