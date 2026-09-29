package cli

import (
	"context"
	"net/http"
	"time"

	"github.com/rodneyosodo/clacks/internal/server"
)

// Server timeouts. Without these a slow or idle client can hold a connection
// open indefinitely, which is a trivially exploitable denial-of-service.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 5 * time.Minute
	writeTimeout      = 5 * time.Minute
	idleTimeout       = 2 * time.Minute
)

func listenAndServe(ctx context.Context, addr string, srv *server.Server) error {
	s := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readHeaderTimeout)
		defer cancel()
		_ = s.Shutdown(shutdownCtx)
	}()
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}
