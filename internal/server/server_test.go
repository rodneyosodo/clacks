package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rodneyosodo/clacks/internal/record"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	st, err := Open(t.TempDir() + "/srv.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func doReq(t *testing.T, srv *Server, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func register(t *testing.T, srv *Server) string {
	t.Helper()
	rec := doReq(t, srv, "POST", "/register", map[string]string{"username": "u", "password": "p"}, "")
	if rec.Code != 200 {
		t.Fatalf("register %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out["token"]
}

func TestAuth(t *testing.T) {
	srv := testServer(t)
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	tok := register(t, srv)
	rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("me %d: %s", rec.Code, rec.Body.String())
	}
	// Duplicate register rejected.
	if rec := doReq(t, srv, "POST", "/register", map[string]string{"username": "u", "password": "p"}, ""); rec.Code != 409 {
		t.Fatalf("want 409, got %d", rec.Code)
	}
	// Bad login rejected.
	if rec := doReq(t, srv, "POST", "/login", map[string]string{"username": "u", "password": "wrong"}, ""); rec.Code != 401 {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	srv := testServer(t)
	tok := register(t, srv)
	recs := []*record.Record{
		{ID: "r1", Host: "h1", Tag: "opencode", Idx: 0, Timestamp: 1, Nonce: []byte("n"), Data: []byte("d")},
		{ID: "r2", Host: "h1", Tag: "opencode", Idx: 1, Timestamp: 2, Nonce: []byte("n"), Data: []byte("d")},
	}
	if rec := doReq(t, srv, "POST", "/api/v0/record", recs, tok); rec.Code != 200 {
		t.Fatalf("upload %d: %s", rec.Code, rec.Body.String())
	}
	rec := doReq(t, srv, "GET", "/api/v0/record", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var st record.Status
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["h1"]["opencode"] != 1 {
		t.Fatalf("bad status: %+v", st)
	}
	rec = doReq(t, srv, "GET", "/api/v0/record/next?host=h1&tag=opencode&start=1&count=10", nil, tok)
	if rec.Code != 200 {
		t.Fatalf("next %d", rec.Code)
	}
	var page []*record.Record
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page) != 1 || page[0].ID != "r2" {
		t.Fatalf("bad page: %+v", page)
	}
	// Re-upload is idempotent.
	if rec := doReq(t, srv, "POST", "/api/v0/record", recs, tok); rec.Code != 200 {
		t.Fatalf("re-upload %d", rec.Code)
	}
	if rec := doReq(t, srv, "DELETE", "/api/v0/store", nil, tok); rec.Code != 200 {
		t.Fatalf("wipe %d", rec.Code)
	}
	rec = doReq(t, srv, "GET", "/api/v0/record", nil, tok)
	var st2 record.Status
	_ = json.Unmarshal(rec.Body.Bytes(), &st2)
	if len(st2) != 0 {
		t.Fatalf("wipe failed: %+v", st2)
	}
}
