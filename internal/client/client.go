package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/rodneyosodo/clacks/internal/record"
)

// Client talks to a clacks server.
type Client struct {
	base   string
	token  string
	client *http.Client
}

// New returns a Client. When insecure is true, TLS certificate verification
// is skipped — meant for tunnels (ngrok/cloudflared) and self-signed servers
// in containers without a CA bundle. Never use it on networks you don't trust.
func New(base, token string, insecure bool) *Client {
	c := &Client{base: base, token: token, client: http.DefaultClient}
	if insecure {
		tr := http.DefaultTransport
		if t, ok := tr.(*http.Transport); ok {
			clone := t.Clone()
			clone.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
			c.client = &http.Client{Transport: clone}
		}
	}

	return c
}

// Register creates an account and returns a token.
func (c *Client) Register(ctx context.Context, username, password string) (string, error) {
	var out map[string]string
	err := c.do(ctx, "POST", "/register", map[string]string{"username": username, "password": password}, &out)

	return out["token"], err
}

// Login returns a fresh token.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	var out map[string]string
	err := c.do(ctx, "POST", "/login", map[string]string{"username": username, "password": password}, &out)

	return out["token"], err
}

// Status fetches the remote record status.
func (c *Client) Status(ctx context.Context) (record.Status, error) {
	var st record.Status
	err := c.doIdempotent(ctx, "GET", "/api/v0/record", nil, &st)
	if st == nil {
		st = record.Status{}
	}

	return st, err
}

// Upload posts a batch of records.
func (c *Client) Upload(ctx context.Context, recs []*record.Record) error {
	if len(recs) == 0 {
		return nil
	}

	return c.doIdempotent(ctx, "POST", "/api/v0/record", recs, nil)
}

// Download fetches one page of a series.
func (c *Client) Download(ctx context.Context, host, tag string, start uint64, count int) ([]*record.Record, error) {
	q := url.Values{}
	q.Set("host", host)
	q.Set("tag", tag)
	q.Set("start", strconv.FormatUint(start, 10))
	q.Set("count", strconv.Itoa(count))
	var out []*record.Record
	err := c.doIdempotent(ctx, "GET", "/api/v0/record/next?"+q.Encode(), nil, &out)

	return out, err
}

// Me returns the authenticated username.
func (c *Client) Me(ctx context.Context) (string, error) {
	var out map[string]string
	err := c.do(ctx, "GET", "/api/v0/me", nil, &out)

	return out["username"], err
}

// ChangePassword verifies the current password and sets a new one, revoking
// every other token for the account.
func (c *Client) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	return c.do(ctx, "POST", "/api/v0/password", map[string]string{"old_password": oldPassword, "new_password": newPassword}, nil)
}

// Logout revokes the current token on the server.
func (c *Client) Logout(ctx context.Context) error {
	return c.do(ctx, "POST", "/api/v0/logout", nil, nil)
}

// Wipe deletes all remote records for the user.
func (c *Client) Wipe(ctx context.Context) error {
	return c.do(ctx, "DELETE", "/api/v0/store", nil, nil)
}

// httpError is a non-2xx response from the server, carrying the status code so
// retry logic can tell a transient 5xx/429 from a permanent 4xx.
type httpError struct {
	code int
	body string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("server %d: %s", e.code, e.body)
}

// retryable reports whether an error is worth retrying: a server-side error
// (5xx or 429), or a network/transport failure — a reset, timeout or truncated
// response. Client errors (4xx) and serialisation failures are permanent and
// returned as-is.
func retryable(err error) bool {
	if he, ok := errors.AsType[*httpError](err); ok {
		return he.code == http.StatusTooManyRequests || he.code >= http.StatusInternalServerError
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if _, ok := errors.AsType[*url.Error](err); ok {
		return true
	}
	_, ok := errors.AsType[net.Error](err)

	return ok
}

const maxRetries = 4

// retryBackoffBase is the delay before the first retry; it doubles each
// attempt. A var so tests can shrink it.
var retryBackoffBase = 500 * time.Millisecond

// doIdempotent runs do, retrying transient failures with exponential backoff.
// It is only safe for operations the server treats as idempotent — record
// upload (INSERT OR IGNORE) and download/status (read-only) — where a retried
// or partially-delivered request cannot corrupt state.
func (c *Client) doIdempotent(ctx context.Context, method, path string, body, out any) error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = c.do(ctx, method, path, body, out); err == nil {
			return nil
		}
		if ctx.Err() != nil || attempt >= maxRetries || !retryable(err) {
			return err
		}
		delay := retryBackoffBase * (1 << attempt)
		slog.Warn("retrying request", slog.Int("attempt", attempt+1), slog.String("method", method),
			slog.String("path", path), slog.Duration("delay", delay), slog.Any("error", err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &httpError{code: resp.StatusCode, body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}

	return nil
}
