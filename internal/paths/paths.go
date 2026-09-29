package paths

import (
	"os"
	"path/filepath"
)

// ClacksHome returns the clacks data home, honoring CLACKS_HOME then
// XDG_DATA_HOME then ~/.local/share.
func ClacksHome() string {
	if v := os.Getenv("CLACKS_HOME"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "clacks")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "clacks")
}

// ConfigHome returns the clacks config dir, honoring CLACKS_CONFIG_HOME then
// XDG_CONFIG_HOME then ~/.config/clacks.
func ConfigHome() string {
	if v := os.Getenv("CLACKS_CONFIG_HOME"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "clacks")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clacks")
}

// OpencodeDBPath returns the opencode SQLite path, honoring OPENCODE_DB then
// XDG_DATA_HOME then ~/.local/share/opencode/opencode.db.
func OpencodeDBPath() string {
	if v := os.Getenv("OPENCODE_DB"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "opencode", "opencode.db")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// KeyPath returns where the sync encryption key lives.
func KeyPath() string {
	if v := os.Getenv("CLACKS_KEY_FILE"); v != "" {
		return v
	}
	return filepath.Join(ClacksHome(), "key")
}

// DBPath returns the local clacks record store path.
func DBPath() string {
	if v := os.Getenv("CLACKS_DB"); v != "" {
		return v
	}
	return filepath.Join(ClacksHome(), "clacks.db")
}
