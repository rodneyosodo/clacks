package e2e

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/crypto"
	"github.com/rodneyosodo/clacks/internal/record"
	"github.com/rodneyosodo/clacks/internal/server"
	"github.com/rodneyosodo/clacks/internal/source"
	"github.com/rodneyosodo/clacks/internal/source/opencode"
	syncpkg "github.com/rodneyosodo/clacks/internal/sync"
	_ "modernc.org/sqlite"
)

type machine struct {
	host    string
	store   *record.Store
	opDB    string
	session *syncpkg.Session
}

func newMachine(t *testing.T, srvURL, token string, key [32]byte, name string) *machine {
	t.Helper()

	return newMachineSchema(t, srvURL, token, key, name, opencode.CreateSchema)
}

func newMachineSchema(t *testing.T, srvURL, token string, key [32]byte, name string, create func(context.Context, *sql.DB) error) *machine {
	t.Helper()
	dir := t.TempDir()
	opDB := filepath.Join(dir, "opencode.db")
	db, err := sql.Open("sqlite", opDB+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}
	if err := create(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := record.Open(t.Context(), filepath.Join(dir, "clacks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	host := "host-" + name
	src := &opencode.Source{DBPath: opDB, Store: st, LocalHome: "/home/" + name}
	sess := &syncpkg.Session{
		HostID: host, Key: key, Store: st,
		Client:  client.New(srvURL, token, false),
		Sources: []source.Source{src},
	}

	return &machine{host: host, store: st, opDB: opDB, session: sess}
}

func execOp(t *testing.T, opDB, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", opDB+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(t.Context(), "PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func countOp(t *testing.T, opDB, q string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+opDB+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func queryOp(t *testing.T, opDB, q string, args ...any) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+opDB+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var s string
	if err := db.QueryRowContext(t.Context(), q, args...).Scan(&s); err != nil {
		t.Fatal(err)
	}

	return s
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	// In-process server.
	st, err := server.Open(t.Context(), filepath.Join(t.TempDir(), "srv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(st)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := client.New(ts.URL, "", false)
	tok, err := c.Register(t.Context(), "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypto.NewKey()
	if err != nil {
		t.Fatal(err)
	}

	a := newMachine(t, ts.URL, tok, key, "a")
	b := newMachine(t, ts.URL, tok, key, "b")

	// A to B: create a session on A, sync A then B.
	execOp(t, a.opDB, `INSERT INTO project(id, worktree, time_created, time_updated) VALUES('proj1','/home/a/work',1,100)`)
	execOp(t, a.opDB, `INSERT INTO session(id, project_id, directory, title, version, time_created, time_updated) VALUES('ses_1','proj1','/home/a/work','first', 'v',1,100)`)
	execOp(t, a.opDB, `INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES('msg_1','ses_1',1,100,'{}')`)
	execOp(t, a.opDB, `INSERT INTO part(id, message_id, session_id, time_created, time_updated, data) VALUES('prt_1','msg_1','ses_1',1,100,'{}')`)
	execOp(t, a.opDB, `INSERT INTO todo(session_id, content, status, priority, position, time_created, time_updated) VALUES('ses_1','task','pending','high',0,1,100)`)

	if err := a.session.Sync(ctx); err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if err := b.session.Sync(ctx); err != nil {
		t.Fatalf("sync B: %v", err)
	}
	for _, tc := range []struct {
		q    string
		want int
	}{
		{`SELECT COUNT(*) FROM session WHERE id='ses_1'`, 1},
		{`SELECT COUNT(*) FROM message WHERE session_id='ses_1'`, 1},
		{`SELECT COUNT(*) FROM part WHERE session_id='ses_1'`, 1},
		{`SELECT COUNT(*) FROM todo WHERE session_id='ses_1'`, 1},
	} {
		if got := countOp(t, b.opDB, tc.q); got != tc.want {
			t.Fatalf("B %s = %d, want %d", tc.q, got, tc.want)
		}
	}

	// B to A: add a message on B, sync B then A.
	execOp(t, b.opDB, `INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES('msg_2','ses_1',2,200,'{}')`)
	execOp(t, b.opDB, `UPDATE session SET time_updated=200 WHERE id='ses_1'`)
	na, _ := a.store.Count(ctx)
	nb, _ := b.store.Count(ctx)
	if err := b.session.Sync(ctx); err != nil {
		t.Fatalf("sync B2: %v", err)
	}
	if err := a.session.Sync(ctx); err != nil {
		t.Fatalf("sync A2: %v", err)
	}
	if got := countOp(t, a.opDB, `SELECT COUNT(*) FROM message WHERE session_id='ses_1'`); got != 2 {
		t.Fatalf("A messages = %d, want 2", got)
	}
	// Re-sync both: no duplicate records should come back.
	if err := a.session.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.session.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countOp(t, a.opDB, `SELECT COUNT(*) FROM message WHERE session_id='ses_1'`); got != 2 {
		t.Fatalf("dup messages after resync: %d", got)
	}
	na2, _ := a.store.Count(ctx)
	nb2, _ := b.store.Count(ctx)
	// Record counts may grow by at most the apply-side scan records (should be
	// stable: each side already has everything).
	if na2 < na || nb2 < nb {
		t.Fatalf("record counts shrank: %d->%d %d->%d", na, na2, nb, nb2)
	}

	// Conflicts: edit the title on both; newer time_updated wins.
	execOp(t, a.opDB, `UPDATE session SET title='from-a', time_updated=300 WHERE id='ses_1'`)
	execOp(t, b.opDB, `UPDATE session SET title='from-b', time_updated=400 WHERE id='ses_1'`)
	if err := a.session.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.session.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.session.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryOp(t, a.opDB, `SELECT title FROM session WHERE id='ses_1'`); got != "from-b" {
		t.Fatalf("conflict: A title=%q want from-b", got)
	}
	if got := queryOp(t, b.opDB, `SELECT title FROM session WHERE id='ses_1'`); got != "from-b" {
		t.Fatalf("conflict: B title=%q want from-b", got)
	}
}
