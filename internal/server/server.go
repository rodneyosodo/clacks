package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rodneyosodo/clacks/internal/record"
	"golang.org/x/crypto/bcrypt"
)

// Server exposes the clacks record-sync HTTP API.
type Server struct {
	store *Store
	mux   *http.ServeMux
}

// New returns a Server backed by store.
func New(store *Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /register", s.handleRegister)
	s.mux.HandleFunc("POST /login", s.handleLogin)
	s.mux.HandleFunc("GET /api/v0/record", s.withAuth(s.handleStatus))
	s.mux.HandleFunc("POST /api/v0/record", s.withAuth(s.handleUpload))
	s.mux.HandleFunc("GET /api/v0/record/next", s.withAuth(s.handleNext))
	s.mux.HandleFunc("GET /api/v0/me", s.withAuth(s.handleMe))
	s.mux.HandleFunc("POST /api/v0/password", s.withAuth(s.handleChangePassword))
	s.mux.HandleFunc("POST /api/v0/logout", s.withAuth(s.handleLogout))
	s.mux.HandleFunc("DELETE /api/v0/store", s.withAuth(s.handleWipe))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	return s
}

// Handler returns the HTTP handler, wrapped in request logging.
func (s *Server) Handler() http.Handler { return requestLog(s.mux) }

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// minPasswordLen is the minimum accepted password length for register, change
// and reset. It is a policy floor, not a correctness bound.
const minPasswordLen = 8

// errBadPassword is returned when a password fails the policy.
const errBadPassword = "password too short (min 8 characters)"

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The status is already committed, so a late encode failure can't be
	// reported to the client; log it and move on.
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", slog.Any("error", err))
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	sum := sha256.Sum256(b[:])

	return hex.EncodeToString(sum[:])
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Username == "" || c.Password == "" {
		writeErr(w, 400, "username and password required")

		return
	}
	if len(c.Password) < minPasswordLen {
		writeErr(w, 400, errBadPassword)

		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")

		return
	}
	_, err = s.store.db.ExecContext(r.Context(), `INSERT INTO users(username, password_hash) VALUES(?,?)`, c.Username, hash)
	if err != nil {
		slog.Warn("register failed", slog.String("reason", "username taken"), slog.String("user", c.Username))
		writeErr(w, 409, "username taken")

		return
	}
	tok := newToken()
	if _, err := s.store.db.ExecContext(r.Context(), `INSERT INTO tokens(token, username) VALUES(?,?)`, tok, c.Username); err != nil {
		writeErr(w, 500, "token failed")

		return
	}
	slog.Info("register", slog.String("user", c.Username))
	writeJSON(w, 200, map[string]string{"token": tok})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.Username == "" || c.Password == "" {
		writeErr(w, 400, "username and password required")

		return
	}
	var hash []byte
	err := s.store.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE username=?`, c.Username).Scan(&hash)
	if err != nil {
		slog.Warn("login failed", slog.String("reason", "unknown user"), slog.String("user", c.Username))
		writeErr(w, 401, "invalid credentials")

		return
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(c.Password)); err != nil {
		slog.Warn("login failed", slog.String("reason", "bad password"), slog.String("user", c.Username))
		writeErr(w, 401, "invalid credentials")

		return
	}
	tok := newToken()
	if _, err := s.store.db.ExecContext(r.Context(), `INSERT INTO tokens(token, username) VALUES(?,?)`, tok, c.Username); err != nil {
		writeErr(w, 500, "token failed")

		return
	}
	slog.Info("login", slog.String("user", c.Username))
	writeJSON(w, 200, map[string]string{"token": tok})
}

func (s *Server) username(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(ctxUserKey{}).(string)

	return v, ok
}

// token returns the bearer token that authenticated the request.
func (s *Server) token(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(ctxTokenKey{}).(string)

	return v, ok
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	st := record.Status{}
	rows, err := s.store.db.QueryContext(r.Context(), `SELECT host, tag, MAX(idx) FROM records WHERE owner=? GROUP BY host, tag`, user)
	if err != nil {
		writeErr(w, 500, "db error")

		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h, t string
		var m int64
		if err := rows.Scan(&h, &t, &m); err != nil {
			writeErr(w, 500, "db error")

			return
		}
		if st[h] == nil {
			st[h] = map[string]uint64{}
		}
		if m < 0 {
			writeErr(w, 500, "corrupt record index")

			return
		}
		st[h][t] = uint64(m)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	var recs []*record.Record
	if err := json.NewDecoder(r.Body).Decode(&recs); err != nil {
		writeErr(w, 400, "bad records")

		return
	}
	tx, err := s.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, 500, "db error")

		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, rec := range recs {
		if rec.ID == "" || rec.Host == "" || rec.Tag == "" {
			writeErr(w, 400, "record missing id/host/tag")

			return
		}
		_, err := tx.ExecContext(r.Context(),
			`INSERT OR IGNORE INTO records(id, owner, host, tag, idx, version, timestamp, nonce, data) VALUES(?,?,?,?,?,?,?,?,?)`,
			rec.ID, user, rec.Host, rec.Tag, rec.Idx, rec.Version, rec.Timestamp, rec.Nonce, rec.Data,
		)
		if err != nil {
			writeErr(w, 500, "db error")

			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	if len(recs) > 0 {
		slog.Info("upload", slog.String("user", user), slog.String("host", recs[0].Host), slog.String("tag", recs[0].Tag), slog.Int("count", len(recs)))
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	q := r.URL.Query()
	host, tag := q.Get("host"), q.Get("tag")
	if host == "" || tag == "" {
		writeErr(w, 400, "host and tag required")

		return
	}
	start := uint64(0)
	if v := q.Get("start"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeErr(w, 400, "bad start")

			return
		}
		start = n
	}
	count := 100
	if v := q.Get("count"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 1000 {
			writeErr(w, 400, "bad count")

			return
		}
		count = n
	}
	rows, err := s.store.db.QueryContext(r.Context(),
		`SELECT id, host, tag, idx, version, timestamp, nonce, data FROM records WHERE owner=? AND host=? AND tag=? AND idx>=? ORDER BY idx ASC LIMIT ?`,
		user, host, tag, start, count,
	)
	if err != nil {
		writeErr(w, 500, "db error")

		return
	}
	defer func() { _ = rows.Close() }()
	out := []*record.Record{}
	for rows.Next() {
		rec := &record.Record{}
		if err := rows.Scan(&rec.ID, &rec.Host, &rec.Tag, &rec.Idx, &rec.Version, &rec.Timestamp, &rec.Nonce, &rec.Data); err != nil {
			writeErr(w, 500, "db error")

			return
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	writeJSON(w, 200, map[string]string{"username": user})
}

// handleChangePassword resets an authenticated user's password after verifying
// the current one. Every other token for the user is revoked, so a leaked
// credential can be cut off in one step; the token making the request stays
// valid.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	tok, _ := s.token(r)
	var c struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil || c.OldPassword == "" || c.NewPassword == "" {
		writeErr(w, 400, "old_password and new_password required")

		return
	}
	if len(c.NewPassword) < minPasswordLen {
		writeErr(w, 400, errBadPassword)

		return
	}
	var hash []byte
	if err := s.store.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE username=?`, user).Scan(&hash); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(c.OldPassword)) != nil {
		slog.Warn("password change failed", slog.String("reason", "bad current password"), slog.String("user", user))
		writeErr(w, 401, "invalid current password")

		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(c.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "hash failed")

		return
	}
	if _, err := s.store.db.ExecContext(r.Context(), `UPDATE users SET password_hash=? WHERE username=?`, newHash, user); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	if _, err := s.store.db.ExecContext(r.Context(), `DELETE FROM tokens WHERE username=? AND token<>?`, user, tok); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	slog.Info("password changed", slog.String("user", user))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// handleLogout revokes the token that authenticated the request, so a logout
// actually invalidates the credential on the server rather than only on the
// client that happened to clear it.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	tok, _ := s.token(r)
	if _, err := s.store.db.ExecContext(r.Context(), `DELETE FROM tokens WHERE token=? AND username=?`, tok, user); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	slog.Info("logout", slog.String("user", user))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleWipe(w http.ResponseWriter, r *http.Request) {
	user, _ := s.username(r)
	if _, err := s.store.db.ExecContext(r.Context(), `DELETE FROM records WHERE owner=?`, user); err != nil {
		writeErr(w, 500, "db error")

		return
	}
	slog.Warn("wipe", slog.String("user", user))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// userByToken resolves a token to a username.
func (s *Server) userByToken(ctx context.Context, tok string) (string, error) {
	var u string
	err := s.store.db.QueryRowContext(ctx, `SELECT username FROM tokens WHERE token=?`, tok).Scan(&u)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}

	return u, err
}
