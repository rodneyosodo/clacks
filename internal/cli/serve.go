package cli

import (
	"net/http"

	"github.com/rodneyosodo/clacks/internal/server"
)

func listenAndServe(addr string, srv *server.Server) error {
	return http.ListenAndServe(addr, srv.Handler())
}
