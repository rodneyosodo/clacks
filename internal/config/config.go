package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/rodneyosodo/clacks/internal/paths"
)

// PathMap rewrites absolute paths between machines.
type PathMap struct {
	From string `toml:"from"`
	To   string `toml:"to"`
}

// OpencodeSource configures the opencode adapter.
type OpencodeSource struct {
	Enabled          bool   `toml:"enabled"`
	Path             string `toml:"path"`
	Since            int64  `toml:"since"`
	PropagateDeletes bool   `toml:"propagate_deletes"`
}

// Sources groups all sync sources.
type Sources struct {
	Opencode OpencodeSource `toml:"opencode"`
}

// Config is ~/.config/clacks/config.toml.
type Config struct {
	SyncAddress        string    `toml:"sync_address"`
	SyncFrequency      string    `toml:"sync_frequency"`
	HostID             string    `toml:"host_id"`
	Token              string    `toml:"token"`
	InsecureSkipVerify bool      `toml:"insecure_skip_verify"`
	PathMap            []PathMap `toml:"path_map"`
	Sources            Sources   `toml:"sources"`
}

// Default returns a config with sane defaults.
func Default() *Config {
	return &Config{
		SyncAddress:   "http://localhost:8080",
		SyncFrequency: "5m",
		HostID:        uuid.Must(uuid.NewV7()).String(),
		Sources: Sources{
			Opencode: OpencodeSource{Enabled: true},
		},
	}
}

// SyncFrequencyDuration parses SyncFrequency.
func (c *Config) SyncFrequencyDuration() time.Duration {
	d, err := time.ParseDuration(c.SyncFrequency)
	if err != nil {
		return 5 * time.Minute
	}

	return d
}

// Path returns the config file path.
func Path() string {
	if v := os.Getenv("CLACKS_CONFIG"); v != "" {
		return v
	}

	return filepath.Join(paths.ConfigHome(), "config.toml")
}

// Load reads the config file, creating it with defaults if missing.
func Load() (*Config, error) {
	p := Path()
	cfg := Default()
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			if err := Save(cfg); err != nil {
				return nil, err
			}

			return cfg, nil
		}

		return nil, err
	}
	if _, err := toml.Decode(string(data), cfg); err != nil {
		return nil, err
	}
	if cfg.HostID == "" {
		cfg.HostID = uuid.Must(uuid.NewV7()).String()
		if err := Save(cfg); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// Save writes the config file.
func Save(cfg *Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()

	return toml.NewEncoder(f).Encode(cfg)
}
