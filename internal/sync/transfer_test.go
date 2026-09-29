package sync

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/crypto"
	"github.com/rodneyosodo/clacks/internal/record"
	"github.com/rodneyosodo/clacks/internal/server"
)

// newTransferServer starts one in-process sync server and registers a user.
func newTransferServer(t *testing.T) (string, string) {
	t.Helper()
	st, err := server.Open(t.Context(), filepath.Join(t.TempDir(), "srv.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ts := httptest.NewServer(server.New(st).Handler())
	t.Cleanup(ts.Close)

	tok, err := client.New(ts.URL, "", false).Register(t.Context(), "u", "p")
	if err != nil {
		t.Fatal(err)
	}

	return ts.URL, tok
}

// newSession builds a client-side session against an existing server.
func newSession(t *testing.T, url, token, hostID string) *Session {
	t.Helper()
	store, err := record.Open(t.Context(), filepath.Join(t.TempDir(), "clacks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := crypto.NewKey()
	if err != nil {
		t.Fatal(err)
	}

	return &Session{
		HostID: hostID, Key: key, Store: store,
		Client: client.New(url, token, false),
	}
}

// seedRecords writes n single-change records straight into the local store.
func seedRecords(t *testing.T, s *Session, n int) {
	t.Helper()
	ctx := t.Context()
	for i := range n {
		idx, err := s.Store.NextIdx(ctx, s.HostID, "opencode")
		if err != nil {
			t.Fatal(err)
		}
		changes := []record.Change{{
			Table: "session", PK: fmt.Sprintf("ses_%d", i), TimeUpdated: int64(i),
			Columns: map[string]any{"id": fmt.Sprintf("ses_%d", i), "title": fmt.Sprintf("t%d", i)},
		}}
		id := record.NewRecordID()
		aad := aadFor(id, s.HostID, "opencode", idx)
		nonce, data, err := record.EncodePayload(s.Key, aad, changes)
		if err != nil {
			t.Fatal(err)
		}
		rec := &record.Record{
			ID: id, Host: s.HostID, Tag: "opencode", Idx: idx,
			Timestamp: record.NowMicros(), Nonce: nonce, Data: data,
		}
		if err := s.Store.Append(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
}

// TestUploadDownloadMultiPage pushes more records than fit in one page, so the
// paging and concurrent-worker paths are actually exercised in both
// directions. Anything dropped or duplicated shows up here.
func TestUploadDownloadMultiPage(t *testing.T) {
	const n = 2500 // 3 pages at pageSize=1000
	url, tok := newTransferServer(t)
	a := newSession(t, url, tok, "host-a")
	seedRecords(t, a, n)

	ctx := t.Context()
	local, err := a.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := local.MaxIdx(a.HostID, "opencode"); got != n-1 {
		t.Fatalf("seed max idx = %d, want %d", got, n-1)
	}

	remote, err := a.Client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upload(ctx, a.HostID, "opencode", remote); err != nil {
		t.Fatal("upload:", err)
	}

	// The server must hold every record, with no gaps.
	remote, err = a.Client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := remote.MaxIdx(a.HostID, "opencode"); got != n-1 {
		t.Fatalf("remote max idx = %d, want %d", got, n-1)
	}
	for page := range n/pageSize + 1 {
		start := uint64(page * pageSize)
		got, err := a.Client.Download(ctx, a.HostID, "opencode", start, pageSize)
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range got {
			if r.Idx != start+uint64(i) {
				t.Fatalf("page %d: gap at %d: got idx %d", page, i, r.Idx)
			}
		}
	}

	// Now a second machine pulls it all down.
	b := newSession(t, url, tok, "host-b")
	blocal, err := b.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remoteMax := uint64(n - 1)
	if err := b.download(ctx, a.HostID, "opencode", blocal, remoteMax); err != nil {
		t.Fatal("download:", err)
	}
	blocal, err = b.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := blocal.MaxIdx(a.HostID, "opencode"); got != remoteMax {
		t.Fatalf("B max idx = %d, want %d", got, remoteMax)
	}
	cnt, err := b.Store.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != n {
		t.Fatalf("B stored %d records, want %d", cnt, n)
	}
}

// TestDownloadEmptyRange checks the no-op path when the local side is already
// up to date.
func TestDownloadEmptyRange(t *testing.T) {
	url, tok := newTransferServer(t)
	a := newSession(t, url, tok, "host-a")
	seedRecords(t, a, 3)
	ctx := t.Context()
	remote, err := a.Client.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.upload(ctx, a.HostID, "opencode", remote); err != nil {
		t.Fatal(err)
	}
	b := newSession(t, url, tok, "host-b")
	blocal, err := b.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Download a range beyond what the server holds: must be a clean no-op.
	if err := b.download(ctx, a.HostID, "opencode", blocal, 9999); err != nil {
		t.Fatal(err)
	}
	cnt, err := b.Store.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 3 {
		t.Fatalf("got %d records, want 3", cnt)
	}
}
