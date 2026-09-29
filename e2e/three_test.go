package e2e

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/crypto"
	"github.com/rodneyosodo/clacks/internal/server"
)

// TestThreeMachines checks the mesh case: A, B and C all push and pull through
// one server, in an order where a machine can pull records that were only ever
// uploaded by another machine it never heard from directly.
func TestThreeMachines(t *testing.T) {
	ctx := t.Context()

	st, err := server.Open(ctx, filepath.Join(t.TempDir(), "srv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := server.New(st)
	ts := httptest.NewServer(srv.Handler())
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

	a := newMachine(t, ts.URL, tok, key, "a")
	b := newMachine(t, ts.URL, tok, key, "b")
	cc := newMachine(t, ts.URL, tok, key, "c")

	// Each machine authors a session locally, so every host series has content.
	seed(t, a, "ses_a", "from-a", 100)
	seed(t, b, "ses_b", "from-b", 200)
	seed(t, cc, "ses_c", "from-c", 300)

	// A single round-robin pass must be enough: nobody has to be synced twice,
	// so C cannot see B's session and B cannot see C's until a later pass.
	for _, m := range []*machine{a, b, cc} {
		if err := m.session.Sync(ctx); err != nil {
			t.Fatalf("sync %s: %v", m.host, err)
		}
	}

	// A second pass lets the newly published series propagate around the mesh.
	for _, m := range []*machine{a, b, cc} {
		if err := m.session.Sync(ctx); err != nil {
			t.Fatalf("resync %s: %v", m.host, err)
		}
	}

	want := map[string]string{"ses_a": "from-a", "ses_b": "from-b", "ses_c": "from-c"}
	for _, m := range []*machine{a, b, cc} {
		for id, title := range want {
			got := queryOp(t, m.opDB, `SELECT title FROM session WHERE id=?`, id)
			if got != title {
				t.Fatalf("%s: session %q title=%q, want %q", m.host, id, got, title)
			}
		}
	}

	// A message authored on C must reach A and B unchanged. Bumping the parent
	// session's time_updated is what clacks treats as "this session changed";
	// opencode does the same when it appends to a session.
	execOp(t, cc.opDB, `INSERT INTO message(id, session_id, time_created, time_updated, data) VALUES('msg_c','ses_c',4,400,'{}')`)
	execOp(t, cc.opDB, `UPDATE session SET time_updated=400 WHERE id='ses_c'`)
	for _, m := range []*machine{cc, a, b} {
		if err := m.session.Sync(ctx); err != nil {
			t.Fatalf("sync %s: %v", m.host, err)
		}
	}
	for _, m := range []*machine{a, b, cc} {
		if got := countOp(t, m.opDB, `SELECT COUNT(*) FROM message WHERE session_id='ses_c'`); got != 1 {
			t.Fatalf("%s: messages for ses_c = %d, want 1", m.host, got)
		}
	}
}

func seed(t *testing.T, m *machine, sessionID, title string, tu int64) {
	t.Helper()
	execOp(t, m.opDB, `INSERT INTO project(id, worktree, time_created, time_updated) VALUES(?,?,1,1)`,
		"proj_"+m.host, "/home/"+m.host+"/work")
	execOp(t, m.opDB, `INSERT INTO session(id, project_id, directory, title, version, time_created, time_updated) VALUES(?,?,?,?,'v',1,?)`,
		sessionID, "proj_"+m.host, "/home/"+m.host+"/work", title, tu)
}
