# clacks

Encrypted, self-hosted sync for AI coding sessions. Start a session in [opencode](https://opencode.ai) on your laptop, walk into the office, and pick up the same session on the desktop.

opencode keeps sessions in a local SQLite database, so they never leave the machine they were created on. clacks watches that database, encrypts the changes, and syncs them through a server you control. The server stores only ciphertext — it never sees your conversations.

## Requirements

- **opencode 1.18.x or 2.x.** clacks detects which schema your opencode uses and syncs the right tables. Sessions do not move between the two, so **machines you sync together must run the same major version** — a 1.x machine and a 2.x machine will sync cleanly but each keeps only its own half, since the underlying tables differ.
- A Go toolchain (1.27+) to build, or a release binary.
- A machine to host the server. It can be a VPS, a NAS, or a laptop.

## Install

Download a release archive for your platform, or build from source — it is a single static binary with no CGO and no runtime dependencies.

| Platform              | Archive                      |
| --------------------- | ---------------------------- |
| macOS (Apple silicon) | `clacks-darwin-arm64.tar.gz` |
| macOS (Intel)         | `clacks-darwin-amd64.tar.gz` |
| Linux (x86-64)        | `clacks-linux-amd64.tar.gz`  |
| Linux (ARM64)         | `clacks-linux-arm64.tar.gz`  |
| Windows (x86-64)      | `clacks-windows-amd64.zip`   |

```sh
go build -o bin/clacks ./cmd/clacks   # or: make build
```

Paths follow the XDG layout everywhere (`$XDG_DATA_HOME/clacks`, `$XDG_CONFIG_HOME/clacks`). On Windows, set `CLACKS_HOME` and `CLACKS_CONFIG_HOME` to override the defaults.

## Quick start

### 1. Start a server

Anywhere reachable, on the default port:

```sh
clacks server start --listen :8080 --db ~/.local/share/clacks/server.db
```

Put it behind TLS (a tunnel works) and set `sync_address` in the config to that URL. If you use a self-signed cert or a minimal container.

### 2. Connect machine A

```sh
clacks register --username you --password '...'
clacks sync --since 2026-01-01
```

`--since` limits how far back the first upload reaches. Without it, clacks tries to push your entire history — on a real install that is a lot. Start with a recent date and widen it if you need more.

### 3. Get the key

```sh
clacks key
```

This prints a 24-word mnemonic that decrypts everything on the server. Anyone with it can read your sessions. On machine B, save it as `~/.local/share/clacks/key` (or export `CLACKS_KEY`).

### 4. Connect machine B

Point `sync_address` at the same server, save the key, then:

```sh
clacks login --username you --password '...'
clacks sync
clacks opencode sessions
```

Sessions should now appear in opencode on machine B. If opencode is already running there, restart it. Repeat steps 3 and 4 for a third machine, and as many as you like.

The server holds one stream per machine, so sync is a mesh rather than a set of pairs: a session written on any machine reaches every other machine as records propagate. Propagation is store-and-forward, so a change made on A is not visible on C until C has synced once *and* A has synced again — expect roughly one sync round per hop, and run `clacks sync` on each machine when you want everything caught up.

### 5. Keep it in sync

```sh
clacks daemon
```

Or run it on a timer — see [`contrib/clacks.service`](contrib/clacks.service) for a systemd user unit.

## Commands

| Command                                     | What it does                                |
| ------------------------------------------- | ------------------------------------------- |
| `clacks register --username U --password P` | Create an account on the server             |
| `clacks login --username U --password P`    | Get a token for an existing account         |
| `clacks logout`                             | Forget the stored token                     |
| `clacks key`                                | Print the sync mnemonic for another machine |
| `clacks sync [--since DATE] [--force]`      | Push local changes, pull remote ones        |
| `clacks status`                             | Show what is where, per machine             |
| `clacks opencode sessions`                  | List sessions and whether each has synced   |
| `clacks daemon`                             | Sync on a timer                             |
| `clacks server start`                       | Run the sync server                         |
| `clacks version`                            | Show the build                              |

Global flags: `--log-level debug\|info\|warn\|error`, `--insecure` (skip TLS verification).

## Configuration

`~/.config/clacks/config.toml`:

```toml
sync_address = "https://sync.example.com"
sync_frequency = "5m"          # used by `clacks daemon`
insecure_skip_verify = false   # only for self-signed certs

[sources.opencode]
enabled = true
path = ""                      # defaults to opencode's own location
propagate_deletes = false      # off by default

# Rewrite absolute paths from one machine's home to another. The remote $HOME
# is mapped to the local $HOME automatically; use this for anything else.
[[path_map]]
from = "/Users/alice"
to = "/home/alice"
```

Useful environment variables:

| Variable             | Purpose                                   |
| -------------------- | ----------------------------------------- |
| `CLACKS_KEY`         | The mnemonic, instead of the key file     |
| `OPENCODE_DB`        | Path to `opencode.db`, if not the default |
| `CLACKS_HOME`        | Data directory (record store + key)       |
| `CLACKS_CONFIG_HOME` | Config directory                          |
| `XDG_DATA_HOME`      | Also relocates opencode's database        |
| `CLACKS_INSECURE`    | `1` to skip TLS verification for one run  |

`CLACKS_HOME` and `XDG_DATA_HOME` are how you test machine B without touching your real install: `XDG_DATA_HOME=/tmp/b clacks sync`.

## Docker

```sh
make docker                                    # ghcr.io/rodneyosodo/clacks:latest
export CLACKS_UID=$(id -u) CLACKS_GID=$(id -g) # so the client owns your files
cp docker/config.example.toml docker/config/config.toml
make compose-up                                # server only
make compose-up --profile client               # server + a syncing client
```

`make docker` builds for the current platform and tags it with the version and `latest`; `make docker-push` publishes both. CI is stricter: it pushes `latest` on every build, and adds the version tag (`v1.2.3`) only for tagged releases, so a branch build never overwrites a published version.

Compose runs prebuilt images, so build or pull one first. The client is pinned to your uid so it can read and write your `opencode.db`; its state stays visible on the host in `.clacks-data/`. Full details in [docker/README.md](docker/README.md).

## Development

```sh
make check   # fmt, vet, lint, test
go test ./... # unit + end-to-end (in-process server, two and three machines, v1 and v2 schemas)
```
