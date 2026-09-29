package e2e

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/crypto"
	"github.com/rodneyosodo/clacks/internal/server"
	"github.com/rodneyosodo/clacks/internal/source/opencode"
)

// TestThreeMachinesV2 runs three opencode 2.x machines through one server, so
// the v2 table set is exercised over the real scan/upload/download/apply path
// rather than only in adapter unit tests.
func TestThreeMachinesV2(t *testing.T) {
	ctx := t.Context()

	st, err := server.Open(ctx, filepath.Join(t.TempDir(), "srv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(server.New(st).Handler())
	defer ts.Close()

	c := client.New(ts.URL, "", false)
	tok, err := c.Register(ctx, "alice", "secret")
	if err != nil {
		t.Fatal(err)
	}
	key, err := crypto.NewKey()
	if err != nil {
		t.Fatal(err)
	}

	a := newMachineSchema(t, ts.URL, tok, key, "a", opencode.CreateSchemaV2)
	b := newMachineSchema(t, ts.URL, tok, key, "b", opencode.CreateSchemaV2)
	cc := newMachineSchema(t, ts.URL, tok, key, "c", opencode.CreateSchemaV2)

	for _, m := range []*machine{a, b, cc} {
		p := "proj_" + m.host
		execOp(t, m.opDB, `INSERT INTO project(id, worktree, time_created, time_updated) VALUES(?,'/home/x/work',1,1)`, p)
		execOp(t, m.opDB, `INSERT INTO session_v2(id, project_id, directory, title, version, time_created, time_updated) VALUES(? ,?,'/home/x/work','from-'||?,'v',1,100)`,
			"ses_"+m.host, p, m.host)
	}

	// Two passes: the first publishes each series, the second lets them meet.
	for range 2 {
		for _, m := range []*machine{a, b, cc} {
			if err := m.session.Sync(ctx); err != nil {
				t.Fatalf("sync %s: %v", m.host, err)
			}
		}
	}

	for _, m := range []*machine{a, b, cc} {
		if got := countOp(t, m.opDB, `SELECT COUNT(*) FROM session_v2`); got != 3 {
			t.Errorf("%s: session_v2 rows = %d, want 3", m.host, got)
		}
		// v1-only tables must never be created.
		for _, table := range []string{"session", "message", "part", "todo"} {
			if got := countOp(t, m.opDB, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table); got != 0 {
				t.Errorf("%s: v1 table %q was created on a v2 machine", m.host, table)
			}
		}
	}

	// A message authored on C reaches everyone, with parts folded into data.
	execOp(t, cc.opDB, `INSERT INTO session_message(id, session_id, type, seq, time_created, time_updated, data) VALUES('m1',?,'assistant',0,1,200,'{"parts":[]}')`, "ses_"+cc.host)
	execOp(t, cc.opDB, `UPDATE session_v2 SET time_updated=200 WHERE id=?`, "ses_"+cc.host)
	for _, m := range []*machine{cc, a, b} {
		if err := m.session.Sync(ctx); err != nil {
			t.Fatalf("sync %s: %v", m.host, err)
		}
	}
	for _, m := range []*machine{a, b, cc} {
		if got := countOp(t, m.opDB, `SELECT COUNT(*) FROM session_message WHERE id='m1'`); got != 1 {
			t.Errorf("%s: session_message m1 = %d, want 1", m.host, got)
		}
	}
}
