package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/config"
	"github.com/rodneyosodo/clacks/internal/crypto"
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

// RootCmd builds the cobra root.
func RootCmd() *cobra.Command {
	root := &cobra.Command{Use: "clacks", Short: "encrypted sync for AI coding sessions"}
	root.PersistentFlags().BoolVar(&insecureFlag, "insecure", false, "skip TLS certificate verification (tunnels/self-signed only, never on untrusted networks)")
	root.AddCommand(registerCmd(), loginCmd(), logoutCmd(), keyCmd(), syncCmd(), statusCmd(), opencodeCmd(), daemonCmd(), serverCmd())
	return root
}

func mustConfig() *config.Config {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config: "+err.Error())
		os.Exit(1)
	}
	// Escape hatch last-wins source: --insecure flag, then CLACKS_INSECURE
	// env, then insecure_skip_verify in config. Applies to this run only.
	if insecureFlag || insecureFromEnv() {
		if !cfg.InsecureSkipVerify {
			fmt.Fprintln(os.Stderr, "warning: skipping TLS certificate verification (insecure). Use only on networks you trust.")
		}
		cfg.InsecureSkipVerify = true
	} else if cfg.InsecureSkipVerify {
		fmt.Fprintln(os.Stderr, "warning: skipping TLS certificate verification (insecure_skip_verify). Use only on networks you trust.")
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
			fmt.Fprintln(os.Stderr, "bad CLACKS_KEY: "+err.Error())
			os.Exit(1)
		}
		return k
	}
	kp := paths.KeyPath()
	if data, err := os.ReadFile(kp); err == nil {
		k, err := crypto.KeyFromMnemonic(strings.TrimSpace(string(data)))
		if err == nil {
			return k
		}
	}
	k, err := crypto.NewKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "keygen: "+err.Error())
		os.Exit(1)
	}
	m, err := crypto.ToMnemonic(k)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mnemonic: "+err.Error())
		os.Exit(1)
	}
	_ = os.MkdirAll(paths.ClacksHome(), 0o755)
	if err := os.WriteFile(kp, []byte(m+"\n"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "key save: "+err.Error())
		os.Exit(1)
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

func registerCmd() *cobra.Command {
	var username, password string
	cmd := &cobra.Command{Use: "register", Short: "Register on the sync server",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if username == "" || password == "" {
				fmt.Fprintln(os.Stderr, "register --username U --password P")
				os.Exit(1)
			}
			tok, err := client.New(cfg.SyncAddress, "", cfg.InsecureSkipVerify).Register(username, password)
			if err != nil {
				fmt.Fprintln(os.Stderr, "register: "+err.Error())
				os.Exit(1)
			}
			cfg.Token = tok
			_ = config.Save(cfg)
			fmt.Println("registered")
		}}
	cmd.Flags().StringVar(&username, "username", "", "username")
	cmd.Flags().StringVar(&password, "password", "", "password")
	return cmd
}

func loginCmd() *cobra.Command {
	var username, password string
	cmd := &cobra.Command{Use: "login", Short: "Log in to the sync server",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if username == "" || password == "" {
				fmt.Fprintln(os.Stderr, "login --username U --password P")
				os.Exit(1)
			}
			tok, err := client.New(cfg.SyncAddress, "", cfg.InsecureSkipVerify).Login(username, password)
			if err != nil {
				fmt.Fprintln(os.Stderr, "login: "+err.Error())
				os.Exit(1)
			}
			cfg.Token = tok
			_ = config.Save(cfg)
			fmt.Println("logged in")
		}}
	cmd.Flags().StringVar(&username, "username", "", "username")
	cmd.Flags().StringVar(&password, "password", "", "password")
	return cmd
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{Use: "logout", Short: "Clear the stored token",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			cfg.Token = ""
			_ = config.Save(cfg)
			fmt.Println("logged out")
		}}
}

func keyCmd() *cobra.Command {
	return &cobra.Command{Use: "key", Short: "Print the sync mnemonic to copy to another machine",
		Run: func(cmd *cobra.Command, args []string) {
			k := loadKey()
			m, err := crypto.ToMnemonic(k)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			fmt.Println(m)
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
	fmt.Fprintln(os.Stderr, "bad --since, want RFC3339 or YYYY-MM-DD")
	os.Exit(1)
	return 0
}

func syncCmd() *cobra.Command {
	var force bool
	var since string
	cmd := &cobra.Command{Use: "sync", Short: "Push local changes and pull remote ones",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if cfg.Token == "" {
				fmt.Fprintln(os.Stderr, "not logged in: run clacks register/login")
				os.Exit(1)
			}
			if since != "" {
				cfg.Sources.Opencode.Since = parseSince(since)
			}
			key := loadKey()
			store, err := record.Open(paths.DBPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, "store: "+err.Error())
				os.Exit(1)
			}
			defer store.Close()
			fmt.Fprintf(os.Stderr, "opencode db: %s\n", opencodeSource(cfg).DBPath)
			sess := buildSession(cfg, key, store)
			sess.Force = force
			if err := sess.Sync(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "sync: "+err.Error())
				os.Exit(1)
			}
			fmt.Println("sync ok")
		}}
	cmd.Flags().BoolVar(&force, "force", false, "re-emit all local rows, ignoring sync cursors")
	cmd.Flags().StringVar(&since, "since", "", "limit first upload (RFC3339 or YYYY-MM-DD)")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show local and remote status per host/tag",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			store, err := record.Open(paths.DBPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, "store: "+err.Error())
				os.Exit(1)
			}
			defer store.Close()
			local, _ := store.Status()
			fmt.Println("local:")
			printStatus(local)
			if cfg.Token != "" {
				remote, err := client.New(cfg.SyncAddress, cfg.Token, cfg.InsecureSkipVerify).Status()
				if err != nil {
					fmt.Fprintln(os.Stderr, "remote: "+err.Error())
					os.Exit(1)
				}
				fmt.Println("remote:")
				printStatus(remote)
			}
		}}
}

func printStatus(st record.Status) {
	for _, s := range st.SortedSeries() {
		v, _ := st.MaxIdx(s.Host, s.Tag)
		fmt.Printf("  %s %s %d\n", s.Host, s.Tag, v)
	}
}

func opencodeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "opencode", Short: "opencode helpers"}
	cmd.AddCommand(&cobra.Command{Use: "sessions", Short: "List sessions and sync state",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			store, err := record.Open(paths.DBPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, "store: "+err.Error())
				os.Exit(1)
			}
			defer store.Close()
			versions, _ := store.RowVersions()
			src := opencodeSource(cfg)
			infos, err := src.ListSessions(versions)
			if err != nil {
				fmt.Fprintln(os.Stderr, "sessions: "+err.Error())
				os.Exit(1)
			}
			for _, in := range infos {
				mark := "pending"
				if in.Synced {
					mark = "synced"
				}
				fmt.Printf("%s %s [%s]\n", in.ID, in.Title, mark)
			}
		}})
	return cmd
}

func daemonCmd() *cobra.Command {
	return &cobra.Command{Use: "daemon", Short: "Sync every sync_frequency",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := mustConfig()
			if cfg.Token == "" {
				fmt.Fprintln(os.Stderr, "not logged in")
				os.Exit(1)
			}
			key := loadKey()
			store, err := record.Open(paths.DBPath())
			if err != nil {
				fmt.Fprintln(os.Stderr, "store: "+err.Error())
				os.Exit(1)
			}
			defer store.Close()
			interval := cfg.SyncFrequencyDuration()
			fmt.Printf("daemon every %s\n", interval)
			for {
				sess := buildSession(cfg, key, store)
				if err := sess.Sync(context.Background()); err != nil {
					fmt.Fprintln(os.Stderr, "sync: "+err.Error())
				} else {
					fmt.Println("sync ok", time.Now().Format(time.RFC3339))
				}
				time.Sleep(interval)
			}
		}}
}

func serverCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "server", Short: "Run the sync server"}
	start := &cobra.Command{Use: "start", Short: "Start the server",
		Run: func(cmd *cobra.Command, args []string) {
			listen, _ := cmd.Flags().GetString("listen")
			dbPath, _ := cmd.Flags().GetString("db")
			if dbPath == "" {
				dbPath = paths.ClacksHome() + "/server.db"
			}
			st, err := server.Open(dbPath)
			if err != nil {
				fmt.Fprintln(os.Stderr, "server store: "+err.Error())
				os.Exit(1)
			}
			defer st.Close()
			srv := server.New(st)
			fmt.Printf("listening on %s\n", listen)
			if err := listenAndServe(listen, srv); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}}
	start.Flags().String("listen", ":8080", "listen address")
	start.Flags().String("db", "", "server db path")
	cmd.AddCommand(start)
	return cmd
}
