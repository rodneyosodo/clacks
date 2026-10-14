package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
)

// captureLogs redirects the global logger into a buffer and returns a reader
// that parses the accumulated JSON lines.
func captureLogs(t *testing.T) func() []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var out []map[string]any
		for line := range bytes.SplitSeq(buf.Bytes(), []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err == nil {
				out = append(out, m)
			}
		}

		return out
	}
}

func findLogs(logs []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range logs {
		if l["msg"] == msg {
			out = append(out, l)
		}
	}

	return out
}

func TestRequestLogging(t *testing.T) {
	srv := testServer(t)
	read := captureLogs(t)
	tok := register(t, srv)

	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok); rec.Code != http.StatusOK {
		t.Fatalf("me %d: %s", rec.Code, rec.Body.String())
	}

	var req map[string]any
	for _, l := range findLogs(read(), "request") {
		if l["path"] == "/api/v0/me" {
			req = l
		}
	}
	if req == nil {
		t.Fatal("no access log line for /api/v0/me")
	}
	if req["method"] != "GET" || req["path"] != "/api/v0/me" {
		t.Fatalf("access log method/path = %v/%v", req["method"], req["path"])
	}
	if req["status"] != float64(http.StatusOK) {
		t.Fatalf("status = %v, want 200", req["status"])
	}
	if req["user"] != "u" {
		t.Fatalf("user = %v, want u (authenticated caller)", req["user"])
	}
	if _, ok := req["duration"]; !ok {
		t.Fatal("access log missing duration")
	}
}

func TestAuthRejectedLogged(t *testing.T) {
	srv := testServer(t)
	read := captureLogs(t)

	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}

	logs := findLogs(read(), "auth rejected")
	if len(logs) != 1 {
		t.Fatalf("auth rejected logs = %d, want 1", len(logs))
	}
	if logs[0]["reason"] != "missing token" {
		t.Fatalf("reason = %v, want missing token", logs[0]["reason"])
	}
}

func TestHealthzNotLogged(t *testing.T) {
	srv := testServer(t)
	read := captureLogs(t)

	if rec := doReq(t, srv, "GET", "/healthz", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz %d", rec.Code)
	}

	for _, l := range findLogs(read(), "request") {
		if l["path"] == "/healthz" {
			t.Fatal("healthz should not produce an access log line")
		}
	}
}
