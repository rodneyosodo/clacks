package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// logUserKey carries a *string the access log reads after the handler runs.
// withAuth fills it in with the authenticated username, so the access log can
// name the caller even though the middleware sits outside the auth wrapper.
type logUserKey struct{}

// requestLog emits one structured line per request: method, path, status,
// duration, bytes written, remote address and (when authenticated) the
// username. Requests for /healthz are skipped so liveness probes don't flood
// the log.
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)

			return
		}

		start := time.Now()
		user := new(string)
		r = r.WithContext(context.WithValue(r.Context(), logUserKey{}, user))
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		attrs := []any{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)),
			slog.Int("bytes", rec.bytes),
			slog.String("remote", remoteIP(r)),
			slog.String("user", *user),
		}
		switch {
		case rec.status >= 500:
			slog.Error("request", attrs...)
		case rec.status >= 400:
			slog.Warn("request", attrs...)
		default:
			slog.Info("request", attrs...)
		}
	})
}

// remoteIP returns the client address, honouring X-Forwarded-For only as a
// secondary field set by the proxy, so the primary RemoteAddr is never spoofed
// by a client-supplied header.
func remoteIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return r.RemoteAddr + " (forwarded " + fwd + ")"
	}

	return r.RemoteAddr
}

// statusRecorder captures the status code and byte count written by a handler
// so the access log can report them once the handler returns. A handler that
// never calls WriteHeader implicitly wrote 200 on its first Write.
type statusRecorder struct {
	http.ResponseWriter

	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n

	return n, err
}

// Unwrap lets any http.ResponseController (and the standard library's
// http.Flusher/Hijacker helpers) reach the underlying writer through the
// recorder.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
