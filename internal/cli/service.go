package cli

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// clacksServiceTmpl is the systemd user unit installed by `clacks daemon
// install`. The {{BIN}} placeholder is replaced with the running binary's
// absolute path, so the installed unit always points at the binary that did
// the installing. contrib/clacks.service is the same unit written for a
// `go install` layout (binary at ~/go/bin/clacks).
//
//go:embed clacks.service
var clacksServiceTmpl string

// renderService returns the unit content for the given clacks binary path.
func renderService(exe string) string {
	return strings.ReplaceAll(clacksServiceTmpl, "{{BIN}}", systemdEscape(exe))
}

// systemdEscape quotes an ExecStart path when it contains whitespace. systemd
// honours C-style double-quote escaping, so a path with spaces survives.
func systemdEscape(s string) string {
	if strings.ContainsAny(s, " \t\n\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}

	return s
}

// resolveBinary returns the absolute path of the running clacks binary.
func resolveBinary() (string, error) {
	return os.Executable()
}

// systemdUserDir returns the directory systemd reads user units from. On
// systemd it is $XDG_CONFIG_HOME/systemd/user (default ~/.config/systemd/user).
func systemdUserDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(cfg, "systemd", "user"), nil
}

// unitPath returns where the installed unit lives.
func unitPath() (string, error) {
	dir, err := systemdUserDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "clacks.service"), nil
}

// writeUnit writes the rendered unit file, creating the directory as needed.
func writeUnit(content string) (string, error) {
	p, err := unitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}

	//nolint:gosec // systemd unit files are not secrets; 0644 matches systemd's own units.
	return p, os.WriteFile(p, []byte(content), 0o644)
}

// systemctl runs `systemctl --user args...`. The caller decides whether a
// failure is fatal: enabling is explicit, daemon-reload is best-effort.
func systemctl(ctx context.Context, args ...string) error {
	full := append([]string{"--user"}, args...)
	out, err := exec.CommandContext(ctx, "systemctl", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return nil
}

// daemonInstallCmd writes the systemd user unit for `clacks daemon`.
func daemonInstallCmd() *cobra.Command {
	var enable bool
	cmd := &cobra.Command{Use: "install", Short: "Install a systemd user unit that runs `clacks daemon`",
		Run: func(cmd *cobra.Command, args []string) {
			exe, err := resolveBinary()
			if err != nil {
				fail(fmt.Errorf("resolve binary: %w", err))
			}
			p, err := writeUnit(renderService(exe))
			if err != nil {
				fail(fmt.Errorf("install unit: %w", err))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", p)

			if enable {
				if err := systemctl(cmd.Context(), "enable", "--now", "clacks"); err != nil {
					fail(err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "enabled and started")

				return
			}
			// Without --enable, reload so a previously-written unit is picked
			// up, but don't fail when systemctl is absent or systemd is not the
			// running init.
			if err := systemctl(cmd.Context(), "daemon-reload"); err != nil {
				slog.Warn("systemctl unavailable, skipping daemon-reload", slog.Any("error", err))
			}
		}}
	cmd.Flags().BoolVar(&enable, "enable", false, "also enable and start the unit (systemctl --user enable --now clacks)")

	return cmd
}

// daemonUninstallCmd stops, disables and removes the systemd user unit.
func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{Use: "uninstall", Short: "Remove the systemd user unit",
		Run: func(cmd *cobra.Command, args []string) {
			if err := systemctl(cmd.Context(), "disable", "--now", "clacks"); err != nil {
				slog.Warn("systemctl disable failed", slog.Any("error", err))
			}
			p, err := unitPath()
			if err != nil {
				fail(fmt.Errorf("unit path: %w", err))
			}
			if err := os.Remove(p); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					fmt.Fprintln(cmd.OutOrStdout(), "not installed")

					return
				}
				fail(fmt.Errorf("remove unit: %w", err))
			}
			_ = systemctl(cmd.Context(), "daemon-reload")
			fmt.Fprintf(cmd.OutOrStdout(), "uninstalled %s\n", p)
		}}
}
