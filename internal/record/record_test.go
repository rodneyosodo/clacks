package record

import (
	"testing"
)

func testKey() [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = byte(i + 1)
	}

	return k
}

func TestPayloadRoundTrip(t *testing.T) {
	k := testKey()
	changes := []Change{
		{Table: "session", PK: "ses_1", TimeUpdated: 100, Columns: map[string]any{"title": "hi"}},
		{Table: "message", PK: "msg_1", TimeUpdated: 101, Columns: map[string]any{"data": "{}"}},
	}
	aad := []byte("a|h|opencode|0")
	nonce, data, err := EncodePayload(k, aad, changes)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodePayload(k, aad, nonce, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 || back[0].PK != "ses_1" || back[1].PK != "msg_1" {
		t.Fatalf("bad round trip: %+v", back)
	}
}

func TestPayloadWrongKey(t *testing.T) {
	k := testKey()
	var k2 [32]byte
	aad := []byte("a|h|opencode|0")
	nonce, data, err := EncodePayload(k, aad, []Change{{Table: "session", PK: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePayload(k2, aad, nonce, data); err == nil {
		t.Fatal("expected wrong-key decode to fail")
	}
}

func TestBatchChanges(t *testing.T) {
	changes := make([]Change, 0, 1200)
	for i := range 1200 {
		changes = append(changes, Change{Table: "part", PK: string(rune(i))})
	}
	batches := BatchChanges(changes)
	if len(batches) != 3 {
		t.Fatalf("want 3 batches, got %d", len(batches))
	}
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 1200 {
		t.Fatalf("lost rows: %d", total)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := t.Context()
	s, err := Open(t.Context(), t.TempDir()+"/c.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &Record{ID: "r1", Host: "h1", Tag: "opencode", Idx: 0, Timestamp: 1, Nonce: []byte("n"), Data: []byte("d")}
	if err := s.Append(ctx, rec); err != nil {
		t.Fatal(err)
	}
	// Duplicate append is ignored.
	if err := s.Append(ctx, rec); err != nil {
		t.Fatal(err)
	}
	next, err := s.NextIdx(ctx, "h1", "opencode")
	if err != nil || next != 1 {
		t.Fatalf("next=%d err=%v", next, err)
	}
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st["h1"]["opencode"] != 0 {
		t.Fatalf("bad status: %+v", st)
	}
	if err := s.SetRowVersion(ctx, "session", "s1", 42); err != nil {
		t.Fatal(err)
	}
	v, err := s.RowVersion(ctx, "session", "s1")
	if err != nil || v != 42 {
		t.Fatalf("row version=%d err=%v", v, err)
	}
	// Max-wins.
	_ = s.SetRowVersion(ctx, "session", "s1", 10)
	if v, _ := s.RowVersion(ctx, "session", "s1"); v != 42 {
		t.Fatalf("max-wins broken: %d", v)
	}
	cur, err := s.AppliedCursor(ctx, "other", "opencode")
	if err != nil || cur != -1 {
		t.Fatalf("cursor=%d err=%v", cur, err)
	}
	_ = s.SetAppliedCursor(ctx, "other", "opencode", 5)
	if cur, _ := s.AppliedCursor(ctx, "other", "opencode"); cur != 5 {
		t.Fatalf("cursor=%d", cur)
	}
}
