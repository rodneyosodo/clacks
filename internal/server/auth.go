package server

import (
	"context"
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
			writeErr(w, 401, "missing token")
			return
		}
		user, err := s.userByToken(tok)
		if err != nil {
			writeErr(w, 401, "invalid token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, user)))
	}
}
