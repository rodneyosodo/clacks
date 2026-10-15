package client

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rodneyosodo/clacks/internal/record"
)

// shrinkBackoff speeds the retry tests up; the default backoff is half a
// second and would add seconds to the suite.
func shrinkBackoff(t *testing.T) {
	t.Helper()
	old := retryBackoffBase
	retryBackoffBase = time.Millisecond
	t.Cleanup(func() { retryBackoffBase = old })
}

func TestUploadRetriesTransient5xx(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"db error"}`))

			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "", false)
	recs := []*record.Record{{ID: "r1", Host: "h", Tag: "opencode", Idx: 0, Timestamp: 1, Nonce: []byte("n"), Data: []byte("d")}}
	if err := c.Upload(t.Context(), recs); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("handler called %d times, want 3", got)
	}
}

func TestUploadNoRetryOnClientError(t *testing.T) {
	shrinkBackoff(t)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad records"}`))
	}))
	defer ts.Close()

	c := New(ts.URL, "", false)
	err := c.Upload(t.Context(), []*record.Record{{ID: "r1", Host: "h", Tag: "opencode", Idx: 0, Timestamp: 1, Nonce: []byte("n"), Data: []byte("d")}})
	if err == nil {
		t.Fatal("want error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler called %d times, want 1 (no retry on 400)", got)
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"500", &httpError{code: http.StatusInternalServerError}, true},
		{"503", &httpError{code: http.StatusServiceUnavailable}, true},
		{"429", &httpError{code: http.StatusTooManyRequests}, true},
		{"400", &httpError{code: http.StatusBadRequest}, false},
		{"401", &httpError{code: http.StatusUnauthorized}, false},
		{"404", &httpError{code: http.StatusNotFound}, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
	}
	for _, tc := range cases {
		if got := retryable(tc.err); got != tc.want {
			t.Errorf("retryable(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
