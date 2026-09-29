package cli

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// BuildInfo is stamped at link time by the Makefile:
//
//	-ldflags "-X main.version=$(VERSION) ..."
//
// When it is absent (a plain `go build`, or `go run`), it falls back to the
// VCS revision Go embeds in the binary, so `clacks version` is never blank.
type BuildInfo struct {
	Version    string
	Commit     string
	CommitTime string
	BuildTime  string
}

// Get resolves build info, preferring the link-time values.
func Get(bi BuildInfo) BuildInfo {
	if bi.Version != "" {
		return bi
	}
	bi.Version = "dev"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return bi
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		bi.Version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			bi.Commit = s.Value
		case "vcs.time":
			bi.CommitTime = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				bi.Version += "-dirty"
			}
		}
	}
	if len(bi.Commit) > 12 {
		bi.Commit = bi.Commit[:12]
	}

	return bi
}

func versionCmd(bi BuildInfo) *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build information",
		Run: func(cmd *cobra.Command, args []string) {
			got := Get(bi)
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), got.Version)

				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "clacks %s", got.Version)
			if got.Commit != "" {
				fmt.Fprintf(&b, " (%s", got.Commit)
				if got.CommitTime != "" {
					fmt.Fprintf(&b, ", %s", got.CommitTime)
				}
				b.WriteString(")")
			}
			fmt.Fprintf(&b, "\n  %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
			if got.BuildTime != "" {
				fmt.Fprintf(&b, "\n  built: %s", got.BuildTime)
			}
			b.WriteByte('\n')
			fmt.Fprint(cmd.OutOrStdout(), b.String())
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print just the version number")

	return cmd
}
