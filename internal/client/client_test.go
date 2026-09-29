package client

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rodneyosodo/clacks/internal/server"
)

// A TLS test server uses a cert signed by an unknown authority, mimicking a
// tunnel/self-signed endpoint inside a container without a CA bundle.
func TestInsecureSkipVerify(t *testing.T) {
	st, err := server.Open(t.Context(), t.TempDir()+"/srv.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewTLSServer(server.New(st).Handler())
	defer ts.Close()

	if _, err := New(ts.URL, "", false).Register(t.Context(), "u", "p"); err == nil {
		t.Fatal("secure client against untrusted cert should fail")
	} else if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("want x509 error, got: %v", err)
	}

	tok, err := New(ts.URL, "", true).Register(t.Context(), "u2", "p")
	if err != nil {
		t.Fatalf("insecure client failed: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	if _, err := New(ts.URL, tok, true).Status(t.Context()); err != nil {
		t.Fatalf("insecure status failed: %v", err)
	}
}
