package record

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
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

// TestPayloadConcurrent hammers the pooled zstd encoder/decoder from many
// goroutines. The pool is shared mutable state, so this guards the invariant
// that concurrent encode/decode stays correct (run under -race).
func TestPayloadConcurrent(t *testing.T) {
	k := testKey()
	const workers = 16
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			changes := []Change{{
				Table:       "part",
				PK:          "prt_" + strconv.Itoa(w),
				TimeUpdated: int64(100 + w),
				Columns:     map[string]any{"id": "prt_" + strconv.Itoa(w), "data": strings.Repeat("x", w*10+1)},
			}}
			for range 20 {
				aad := []byte("aad-" + strconv.Itoa(w))
				nonce, data, err := EncodePayload(k, aad, changes)
				if err != nil {
					t.Error(err)

					return
				}
				back, err := DecodePayload(k, aad, nonce, data)
				if err != nil {
					t.Error(err)

					return
				}
				if len(back) != 1 || back[0].PK != changes[0].PK {
					t.Errorf("worker %d: round trip mismatch: %+v", w, back)

					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// TestDecodesStdlibJSON pins wire compatibility. Records written by an older
// client used encoding/json; the decoder must still read them after the
// switch to goccy/go-json.
func TestDecodesStdlibJSON(t *testing.T) {
	// Literal JSON in the shape encoding/json emitted: struct fields in
	// declaration order, map keys sorted.
	const stdlibJSON = `{"changes":[{"table":"session","pk":"ses_1","time_updated":100,` +
		`"columns":{"directory":"/home/u","id":"ses_1","title":"hi"}}]}`
	var p Payload
	if err := json.Unmarshal([]byte(stdlibJSON), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Changes) != 1 || p.Changes[0].PK != "ses_1" || p.Changes[0].TimeUpdated != 100 {
		t.Fatalf("bad decode: %+v", p.Changes)
	}
	cols := p.Changes[0].Columns
	if cols["title"] != "hi" || cols["directory"] != "/home/u" {
		t.Fatalf("bad columns: %+v", cols)
	}
}

// TestEncodeMatchesStdlibJSON checks our encoder's output is ordinary JSON
// that the stdlib reads back identically, so a mixed-version fleet works.
func TestEncodeMatchesStdlibJSON(t *testing.T) {
	changes := []Change{{
		Table: "session", PK: "ses_1", TimeUpdated: 100,
		Columns: map[string]any{"id": "ses_1", "title": "hi", "directory": "/home/u"},
	}}
	raw, err := json.Marshal(Payload{Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	var back Payload
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Changes) != 1 || back.Changes[0].PK != "ses_1" || back.Changes[0].Columns["title"] != "hi" {
		t.Fatalf("round trip via encoder failed: %+v", back.Changes)
	}
}

func TestBatchChanges(t *testing.T) {
	changes := make([]Change, 0, 1200)
	for i := range 1200 {
		changes = append(changes, Change{Table: "part", PK: string(rune(i))})
	}
	batches, err := BatchChanges(changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 {
		t.Fatalf("want 3 batches, got %d", len(batches))
	}
	total := 0
	for _, b := range batches {
		total += len(b.Changes)
	}
	if total != 1200 {
		t.Fatalf("lost rows: %d", total)
	}
	// Each batch must carry the marshalled bytes and they must decode back to
	// the same rows, so callers can skip a second serialisation.
	for i, b := range batches {
		var p Payload
		if err := json.Unmarshal(b.JSON, &p); err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		if len(p.Changes) != len(b.Changes) {
			t.Fatalf("batch %d: JSON has %d changes, batch has %d", i, len(p.Changes), len(b.Changes))
		}
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
