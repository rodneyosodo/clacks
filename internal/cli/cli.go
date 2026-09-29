package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/config"
	"github.com/rodneyosodo/clacks/internal/crypto"
	"github.com/rodneyosodo/clacks/internal/logging"
	"github.com/rodneyosodo/clacks/internal/paths"
	"github.com/rodneyosodo/clacks/internal/record"
	"github.com/rodneyosodo/clacks/internal/server"
	"github.com/rodneyosodo/clacks/internal/source"
	"github.com/rodneyosodo/clacks/internal/source/opencode"
	syncpkg "github.com/rodneyosodo/clacks/internal/sync"
	"github.com/spf13/cobra"
)

// insecureFlag is the global --insecure escape hatch for tunnels and
// self-signed servers (e.g. minimal containers without a CA bundle).
var insecureFlag bool

// logLevel is the global --log-level flag.
var logLevel string

// RootCmd builds the cobra root.
func RootCmd(bi BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:           "clacks",
		Short:         "encrypted sync for AI coding sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if logLevel == "" {
				logLevel = logging.LevelFromEnv()
			}
			logging.InitCLI(logLevel)
		},
	}
	root.PersistentFlags().BoolVar(&insecureFlag, "insecure", false, "skip TLS certificate verification (tunnels/self-signed only, never on untrusted networks)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "", "log level: debug, info, warn, error (or $CLACKS_LOG_LEVEL)")
	root.AddCommand(registerCmd(), loginCmd(), logoutCmd(), keyCmd(), syncCmd(), statusCmd(), opencodeCmd(), daemonCmd(), serverCmd(), versionCmd(bi))

	return root
}

// fail reports err on stderr and exits non-zero. The error text is the whole
// message: wrapping it in a level/attribute envelope just adds noise.
func fail(err error) {
	slog.Error(err.Error())
	os.Exit(1)
}

func mustConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fail(fmt.Errorf("config: %w", err))
	}
	// Escape hatch last-wins source: --insecure flag, then CLACKS_INSECURE
	// env, then insecure_skip_verify in config. Applies to this run only.
	if insecureFlag || insecureFromEnv() {
		if !cfg.InsecureSkipVerify {
			slog.Warn("skipping TLS certificate verification (insecure); only use on networks you trust")
		}
		cfg.InsecureSkipVerify = true
	} else if cfg.InsecureSkipVerify {
		slog.Warn("skipping TLS certificate verification (insecure_skip_verify); only use on networks you trust")
	}

	return cfg
}

func insecureFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLACKS_INSECURE"))) {
	case "1", "true", "yes":
		return true
	}

	return false
}

// loadKey resolves the sync key from CLACKS_KEY (mnemonic) or the key file,
// generating and saving one on first run.
func loadKey() [32]byte {
	if m := os.Getenv("CLACKS_KEY"); m != "" {
		k, err := crypto.KeyFromMnemonic(strings.TrimSpace(m))
		if err != nil {
			fail(fmt.Errorf("bad CLACKS_KEY: %w", err))
		}

		return k
	}
	kp := paths.KeyPath()
	if data, err := os.ReadFile(kp); err == nil {
		if k, err := crypto.KeyFromMnemonic(strings.TrimSpace(string(data))); err == nil {
			return k
		}
	}
	k, err := crypto.NewKey()
	if err != nil {
		fail(fmt.Errorf("keygen: %w", err))
	}
	m, err := crypto.ToMnemonic(k)
	if err != nil {
		fail(fmt.Errorf("mnemonic: %w", err))
	}
	if err := os.MkdirAll(paths.ClacksHome(), 0o755); err != nil {
		fail(fmt.Errorf("clacks home: %w", err))
	}
	if err := os.WriteFile(kp, []byte(m+"\n"), 0o600); err != nil {
		fail(fmt.Errorf("key save: %w", err))
	}

	return k
}

func opencodeSource(cfg *config.Config) *opencode.Source {
	dbPath := cfg.Sources.Opencode.Path
	if dbPath == "" {
		dbPath = paths.OpencodeDBPath()
	}
	rules := make([][2]string, 0, len(cfg.PathMap))
	for _, r := range cfg.PathMap {
		rules = append(rules, [2]string{r.From, r.To})
	}

	return &opencode.Source{
		DBPath:           dbPath,
		Since:            cfg.Sources.Opencode.Since,
		PropagateDeletes: cfg.Sources.Opencode.PropagateDeletes,
		PathMap:          rules,
	}
}

func buildSession(cfg *config.Config, key [32]byte, store *record.Store) *syncpkg.Session {
	var sources []source.Source
	if cfg.Sources.Opencode.Enabled {
		src := opencodeSource(cfg)
		src.Store = store
		sources = append(sources, src)
	}

	return &syncpkg.Session{
		HostID: cfg.HostID, Key: key, Store: store,
		Client: client.New(cfg.SyncAddress, cfg.Token, cfg.InsecureSkipVerify), Sources: sources,
	}
}

// authCmd builds register/login, which differ only in the client call.
func authCmd(use, short, done string, call func(context.Context, *client.Client, string, string) (string, error)) *cobra.Command {
	var username, password string
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Run: func(cmd *cobra.Command, args []string) {
			if username == "" || password == "" {
				fail(fmt.Errorf("%s --username U --password P", use))
			}
			cfg := mustConfig()
			c := client.New(cfg.SyncAddress, "", cfg.InsecureSkipVerify)
			tok, err := call(cmd.Context(), c, username, password)
			if err != nil {
				fail(fmt.Errorf("%s: %w", use, err))
			}
			cfg.Token = tok
			if err := config.Save(cfg); err != nil {
				fail(fmt.Errorf("save config: %w", err))
			}
			fmt.Fprintln(cmd.OutOrStdout(), done)
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "username")
	cmd.Flags().StringVar(&password, "password", "", "password")

	return cmd
}

func registerCmd() *cobra.Command {
	return authCmd("register", "Register on the sync server", "registered",
		func(ctx context.Context, c *client.Client, u, p string) (string, error) {
			return c.Register(ctx, u, p)
		})
}

func loginCmd() *cobra.Command {
	return authCmd("login", "Log in to the sync server", "logged in",
		func(ctx context.Context, c *client.Client, u, p string) (string, error) {
			return c.Login(ctx, u, p)
		})
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Clear the stored token",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			cfg.Token = ""
			if err := config.Save(cfg); err != nil {
				fail(fmt.Errorf("save config: %w", err))
			}
			fmt.Fprintln(cmd.OutOrStdout(), "logged out")
		}}
}

func keyCmd() *cobra.Command {
	return &cobra.Command{Use: "key", Short: "Print the sync mnemonic to copy to another machine",
		Run: func(cmd *cobra.Command, args []string) {
			m, err := crypto.ToMnemonic(loadKey())
			if err != nil {
				fail(err)
			}
			// The mnemonic is the product of this command: stdout on purpose.
			fmt.Fprintln(cmd.OutOrStdout(), m)
		}}
}

func parseSince(s string) int64 {
	if s == "" {
		return 0
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	fail(errors.New("bad --since, want RFC3339 or YYYY-MM-DD"))

	return 0
}

func syncCmd() *cobra.Command {
	var force bool
	var since string
	cmd := &cobra.Command{Use: "sync", Short: "Push local changes and pull remote ones",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if cfg.Token == "" {
				fail(errors.New("not logged in: run clacks register/login"))
			}
			if since != "" {
				cfg.Sources.Opencode.Since = parseSince(since)
			}
			ctx := cmd.Context()
			key := loadKey()
			store, err := record.Open(ctx, paths.DBPath())
			if err != nil {
				fail(fmt.Errorf("store: %w", err))
			}
			defer func() { _ = store.Close() }()
			slog.Debug("syncing", slog.String("opencode_db", opencodeSource(cfg).DBPath), slog.Bool("force", force))
			sess := buildSession(cfg, key, store)
			sess.Force = force
			if err := sess.Sync(ctx); err != nil {
				fail(fmt.Errorf("sync: %w", err))
			}
			slog.Info("sync complete")
		}}
	cmd.Flags().BoolVar(&force, "force", false, "re-emit all local rows, ignoring sync cursors")
	cmd.Flags().StringVar(&since, "since", "", "limit first upload (RFC3339 or YYYY-MM-DD)")

	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show local and remote status per host/tag",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			ctx := cmd.Context()
			store, err := record.Open(ctx, paths.DBPath())
			if err != nil {
				fail(fmt.Errorf("store: %w", err))
			}
			defer func() { _ = store.Close() }()
			out := cmd.OutOrStdout()
			local, err := store.Status(ctx)
			if err != nil {
				fail(fmt.Errorf("local status: %w", err))
			}
			fmt.Fprintln(out, "local:")
			printStatus(out, local)
			if cfg.Token != "" {
				remote, err := client.New(cfg.SyncAddress, cfg.Token, cfg.InsecureSkipVerify).Status(ctx)
				if err != nil {
					fail(fmt.Errorf("remote status: %w", err))
				}
				fmt.Fprintln(out, "remote:")
				printStatus(out, remote)
			}
		}}
}

func printStatus(w io.Writer, st record.Status) {
	for _, s := range st.SortedSeries() {
		v, _ := st.MaxIdx(s.Host, s.Tag)
		fmt.Fprintf(w, "  %s %s %d\n", s.Host, s.Tag, v)
	}
}

func opencodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "opencode", Short: "opencode helpers"}
	cmd.AddCommand(&cobra.Command{Use: "sessions", Short: "List sessions and sync state",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			ctx := cmd.Context()
			store, err := record.Open(ctx, paths.DBPath())
			if err != nil {
				fail(fmt.Errorf("store: %w", err))
			}
			defer func() { _ = store.Close() }()
			versions, err := store.RowVersions(ctx)
			if err != nil {
				fail(fmt.Errorf("row versions: %w", err))
			}
			infos, err := opencodeSource(cfg).ListSessions(ctx, versions)
			if err != nil {
				fail(fmt.Errorf("sessions: %w", err))
			}
			for _, in := range infos {
				mark := "pending"
				if in.Synced {
					mark = "synced"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s [%s]\n", in.ID, in.Title, mark)
			}
		}})

	return cmd
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{Use: "daemon", Short: "Sync every sync_frequency",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if cfg.Token == "" {
				fail(errors.New("not logged in"))
			}
			ctx := cmd.Context()
			key := loadKey()
			store, err := record.Open(ctx, paths.DBPath())
			if err != nil {
				fail(fmt.Errorf("store: %w", err))
			}
			defer func() { _ = store.Close() }()
			interval := cfg.SyncFrequencyDuration()
			slog.Info("daemon started", slog.Duration("interval", interval))
			for {
				sess := buildSession(cfg, key, store)
				start := time.Now()
				if err := sess.Sync(ctx); err != nil {
					slog.Error("sync failed", slog.Any("error", err))
				} else {
					slog.Info("sync complete", slog.Duration("took", time.Since(start)))
				}
				time.Sleep(interval)
			}
		}}
}

func serverCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "server", Short: "Run the sync server"}
	start := &cobra.Command{Use: "start", Short: "Start the server",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logging.InitServer(logLevel)
		},
		Run: func(cmd *cobra.Command, args []string) {
			listen, _ := cmd.Flags().GetString("listen")
			dbPath, _ := cmd.Flags().GetString("db")
			if dbPath == "" {
				dbPath = paths.ClacksHome() + "/server.db"
			}
			st, err := server.Open(cmd.Context(), dbPath)
			if err != nil {
				fail(fmt.Errorf("server store: %w", err))
			}
			defer func() { _ = st.Close() }()
			slog.Info("server listening", slog.String("addr", listen), slog.String("db", dbPath))
			if err := listenAndServe(cmd.Context(), listen, server.New(st)); err != nil {
				fail(err)
			}
		}}
	start.Flags().String("listen", ":8080", "listen address")
	start.Flags().String("db", "", "server db path")
	cmd.AddCommand(start)

	return cmd
}
