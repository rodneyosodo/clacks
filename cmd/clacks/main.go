package main

import (
	"log/slog"
	"os"

	"github.com/rodneyosodo/clacks/internal/cli"
)

func main() {
	// Diagnostics are set up by the root command's PersistentPreRun; this
	// default keeps anything logged before then from being dropped.
	slog.SetDefault(slog.Default())
	if err := cli.RootCmd().Execute(); err != nil {
		slog.Error("failed", slog.Any("error", err))
		os.Exit(1)
	}
}
