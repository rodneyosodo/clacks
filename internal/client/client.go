package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

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
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		c.client = &http.Client{Transport: tr}
	}
	return c
}

func (c *Client) do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
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
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server %d: %s", resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Register creates an account and returns a token.
func (c *Client) Register(username, password string) (string, error) {
	var out map[string]string
	err := c.do("POST", "/register", map[string]string{"username": username, "password": password}, &out)
	return out["token"], err
}

// Login returns a fresh token.
func (c *Client) Login(username, password string) (string, error) {
	var out map[string]string
	err := c.do("POST", "/login", map[string]string{"username": username, "password": password}, &out)
	return out["token"], err
}

// Status fetches the remote record status.
func (c *Client) Status() (record.Status, error) {
	var st record.Status
	err := c.do("GET", "/api/v0/record", nil, &st)
	if st == nil {
		st = record.Status{}
	}
	return st, err
}

// Upload posts a batch of records.
func (c *Client) Upload(recs []*record.Record) error {
	if len(recs) == 0 {
		return nil
	}
	return c.do("POST", "/api/v0/record", recs, nil)
}

// Download fetches one page of a series.
func (c *Client) Download(host, tag string, start uint64, count int) ([]*record.Record, error) {
	q := url.Values{}
	q.Set("host", host)
	q.Set("tag", tag)
	q.Set("start", strconv.FormatUint(start, 10))
	q.Set("count", strconv.Itoa(count))
	var out []*record.Record
	err := c.do("GET", "/api/v0/record/next?"+q.Encode(), nil, &out)
	return out, err
}

// Me returns the authenticated username.
func (c *Client) Me() (string, error) {
	var out map[string]string
	err := c.do("GET", "/api/v0/me", nil, &out)
	return out["username"], err
}

// Wipe deletes all remote records for the user.
func (c *Client) Wipe() error { return c.do("DELETE", "/api/v0/store", nil, nil) }
