package main

import (
	"log/slog"
	"os"

	"github.com/rodneyosodo/clacks/internal/cli"
)

// Build metadata, stamped by the Makefile with -ldflags. See internal/cli.Get
// for the fallback used when they are absent.
var (
	version    = ""
	commit     = ""
	commitTime = ""
	buildTime  = ""
)

func main() {
	// Diagnostics are configured by the root command's PersistentPreRun; this
	// default keeps anything logged before then from being dropped.
	slog.SetDefault(slog.Default())
	bi := cli.BuildInfo{
		Version:    version,
		Commit:     commit,
		CommitTime: commitTime,
		BuildTime:  buildTime,
	}
	if err := cli.RootCmd(bi).Execute(); err != nil {
		slog.Error("failed", slog.Any("error", err))
		os.Exit(1)
	}
}
