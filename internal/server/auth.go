package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

type ctxUserKey struct{}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("Authorization")
		tok = strings.TrimPrefix(tok, "Bearer ")
		tok = strings.TrimSpace(tok)
		if tok == "" {
			slog.Warn("auth rejected", slog.String("reason", "missing token"), slog.String("remote", r.RemoteAddr))
			writeErr(w, 401, "missing token")

			return
		}
		user, err := s.userByToken(r.Context(), tok)
		if err != nil {
			slog.Warn("auth rejected", slog.String("reason", "invalid token"), slog.String("remote", r.RemoteAddr))
			writeErr(w, 401, "invalid token")

			return
		}
		if p, ok := r.Context().Value(logUserKey{}).(*string); ok {
			*p = user
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, user)))
	}
}
