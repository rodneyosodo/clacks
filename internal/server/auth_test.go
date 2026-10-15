package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

func loginTok(t *testing.T, srv *Server, password string) (string, int) {
	t.Helper()
	rec := doReq(t, srv, "POST", "/login", map[string]string{"username": "u", "password": password}, "")
	var out map[string]string
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}

	return out["token"], rec.Code
}

func TestChangePassword(t *testing.T) {
	srv := testServer(t)
	tok1 := register(t, srv) // password "password1"

	tok2, code := loginTok(t, srv, "password1")
	if code != http.StatusOK {
		t.Fatalf("second login %d", code)
	}

	rec := doReq(t, srv, "POST", "/api/v0/password",
		map[string]string{"old_password": "password1", "new_password": "newsecret1"}, tok1)
	if rec.Code != http.StatusOK {
		t.Fatalf("change password %d: %s", rec.Code, rec.Body.String())
	}

	// The current token survives; the other one is revoked.
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok1); rec.Code != http.StatusOK {
		t.Fatalf("current token after change: %d", rec.Code)
	}
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token after change: %d, want 401", rec.Code)
	}

	// Old password no longer logs in; the new one does.
	if _, code := loginTok(t, srv, "password1"); code != http.StatusUnauthorized {
		t.Fatalf("old password login = %d, want 401", code)
	}
	if _, code := loginTok(t, srv, "newsecret1"); code != http.StatusOK {
		t.Fatalf("new password login = %d, want 200", code)
	}
}

func TestChangePasswordRejectsWrongOld(t *testing.T) {
	srv := testServer(t)
	tok := register(t, srv)

	rec := doReq(t, srv, "POST", "/api/v0/password",
		map[string]string{"old_password": "wrong", "new_password": "newsecret1"}, tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	// Token must still work — a failed attempt must not log the caller out.
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok); rec.Code != http.StatusOK {
		t.Fatalf("token after failed change: %d", rec.Code)
	}
}

func TestLogoutRevokesToken(t *testing.T) {
	srv := testServer(t)
	tok := register(t, srv)

	if rec := doReq(t, srv, "POST", "/api/v0/logout", nil, tok); rec.Code != http.StatusOK {
		t.Fatalf("logout %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token after logout: %d, want 401", rec.Code)
	}
}

func TestPasswordPolicy(t *testing.T) {
	srv := testServer(t)
	if rec := doReq(t, srv, "POST", "/register", map[string]string{"username": "short", "password": "abc"}, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password register = %d, want 400", rec.Code)
	}
	if rec := doReq(t, srv, "POST", "/register", map[string]string{"username": "ok", "password": "longenough"}, ""); rec.Code != http.StatusOK {
		t.Fatalf("valid register = %d", rec.Code)
	}
}

func TestResetPassword(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir()+"/srv.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(st)
	tok := register(t, srv)

	ok, err := st.ResetPassword(t.Context(), "u", "brandnewpw")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("reset returned false for existing user")
	}
	// All tokens revoked.
	if rec := doReq(t, srv, "GET", "/api/v0/me", nil, tok); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token after reset: %d, want 401", rec.Code)
	}
	// New password logs in.
	if _, code := loginTok(t, srv, "brandnewpw"); code != http.StatusOK {
		t.Fatalf("login with reset password = %d, want 200", code)
	}
	// Missing user is reported, not an error.
	if ok, err := st.ResetPassword(t.Context(), "nobody", "whatever"); err != nil || ok {
		t.Fatalf("reset missing user = %v, %v; want false, nil", ok, err)
	}
}

// ensure no 401 leak path: logout/change must require auth.
func TestAuthEndpointsRequireToken(t *testing.T) {
	srv := testServer(t)
	rec := doReq(t, srv, "POST", "/api/v0/password", map[string]string{"old_password": "password1", "new_password": "longenough"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated password change = %d, want 401", rec.Code)
	}
	rec = doReq(t, srv, "POST", "/api/v0/logout", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated logout = %d, want 401", rec.Code)
	}
}
