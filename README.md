# clacks

Encrypted, self-hosted sync for AI coding sessions — opencode first.

Sessions live in opencode's SQLite database (`~/.local/share/opencode/opencode.db`),
which is stuck on the machine where it was created. clacks is a single Go binary
with a self-hostable `clacks server` that stores only encrypted records, plus a
client that pushes local changes and pulls remote ones. A session started on
machine A can be opened and continued in opencode on machine B. The design leaves
room for Claude Code and other tools later via the `Source` interface.

## How it works

- **Records**: `id` (UUIDv7), `host` (UUIDv7 per machine),
  `tag` (e.g. `opencode`), `idx` (sequence per host+tag), `version`, `timestamp`,
  `data`. Payloads are batches of row changes (`{table, pk, time_updated, columns}`)
  cut at ~1 MB / 500 rows, zstd-compressed, then encrypted with XChaCha20-Poly1305
  (AAD = `id|host|tag|idx`). The server never sees plaintext.
- **Sync loop** (`clacks sync`): scan local changes into new records under this
  host, diff local vs remote status per `(host, tag)` into an
  `Upload` / `Download` / `Noop` decision, upload/download the gap, then apply
  records from other hosts past the `applied_idx` cursor through the source.
- **opencode adapter**: change detection without new indexes (dirty sessions via
  `session.time_updated`, children through existing `message_session_*`,
  `part_session_idx`, `todo_session_idx` indexes), newer-`time_updated`-wins
  upserts in FK order, column intersection via `PRAGMA table_info` for schema
  drift, `path_map` rewriting of `session.directory` / `project.worktree`
  (remote `$HOME` → local `$HOME` by default), opt-in delete propagation.

## Quickstart

```sh
# 1. Build / install
go build -o bin/clacks ./cmd/clacks

# 2. Start a server (self-hosted; default :8080, SQLite store)
clacks server start --listen :8080 --db ~/.local/share/clacks/server.db

# 3. On machine A: point at the server, register, sync (limit history!)
clacks register --username alice --password '...'
clacks sync --since 2026-01-01

# 4. Copy the key to machine B
clacks key   # BIP39 mnemonic — keep it secret

# 5. On machine B: same server address in ~/.config/clacks/config.toml,
#    set CLACKS_KEY to the mnemonic (or paste into the key file), then:
clacks login --username alice --password '...'
clacks sync --since 2026-01-01
clacks status
```

Config lives at `~/.config/clacks/config.toml`:

```toml
sync_address = "http://myserver:8080"
sync_frequency = "5m"
# Skip TLS verification (ngrok/cloudflared tunnels, self-signed certs,
# minimal containers without a CA bundle). Also: `clacks --insecure ...`
# or `CLACKS_INSECURE=1`. Only on networks you trust.
insecure_skip_verify = false

[sources.opencode]
enabled = true
propagate_deletes = false   # off by default

[[path_map]]
from = "/home/alice"
to = "/home/bob"
```

Useful env overrides: `CLACKS_HOME` (data dir: holds `clacks.db` and `key`),
`CLACKS_CONFIG_HOME` (config dir: holds `config.toml`), `CLACKS_CONFIG` (exact
config file), `XDG_DATA_HOME` / `XDG_CONFIG_HOME` (machine-B isolation:
`XDG_DATA_HOME=/tmp/b clacks sync`), `OPENCODE_DB`, `CLACKS_KEY` (mnemonic),
`CLACKS_DB`, `CLACKS_KEY_FILE`.
`CLACKS_INSECURE=1` / `clacks --insecure` skips TLS verification for one run
without editing the config.

## Logging

The client speaks to a person, the server to a log pipeline, so they log
differently. Both go through `log/slog`.

- **CLI**: one plain line per record on stderr — no timestamps, no level
  prefixes, no quoted error blobs. Command results stay on stdout, so
  `clacks status | grep ses_` and `clacks key > key.txt` keep working.
- **Server**: JSON on stdout, ready for collection.

Set the level with `--log-level debug|info|warn|error` or `CLACKS_LOG_LEVEL`:

```sh
$ clacks status
local:
  01a0cec6-... opencode 3097
remote status: dial tcp [::1]:8080: connect: connection refused

$ clacks --log-level debug sync
syncing opencode_db=/root/.local/share/opencode/opencode.db force=false
scanned local changes tag=opencode changes=2
uploading host=01a0cccc-... tag=opencode
sync complete

$ clacks server start
{"time":"...","level":"INFO","msg":"server listening","addr":":8080","db":"..."}
```

## TLS errors (`x509: certificate signed by unknown authority`)

Both ngrok and Cloudflare tunnels use publicly-trusted CAs, so this error on
both means the *client* machine can't verify anything — typically a minimal
container without a CA bundle. In order:

1. **Fix the CA store (proper fix):** `apt-get update && apt-get install -y
   ca-certificates`, then retry. Confirm the bundle exists:
   `ls -la /etc/ssl/certs/ca-certificates.crt` (a dangling symlink is the
   classic symptom).
2. **Skip verification (escape hatch):** `clacks --insecure login ...`, or set
   `insecure_skip_verify = true` in config, or `CLACKS_INSECURE=1`. A warning
   is printed every run. Only on networks you trust.

## Sync errors

- `database not found at ...` / `not initialised (missing table "session")`:
  clacks is looking at the wrong file. opencode uses **per-channel**
  filenames — non-`latest`/`beta` installs write to
  `opencode-<channel>.db`, and `OPENCODE_DB` relocates it entirely. Find the
  live one with `opencode debug paths db` (or `ls ~/.local/share/opencode/`),
  then set it as `[sources.opencode] path`. clacks also names any
  `opencode*.db` siblings in the error itself.

## CLI

- `clacks register | login | logout` — account + token management
- `clacks key` — print the sync mnemonic for the second machine
- `clacks sync [--force] [--since DATE]` — push + pull (`--since` as RFC3339
  or `YYYY-MM-DD`; `--force` re-emits all local rows)
- `clacks status` — local and remote status per host/tag
- `clacks opencode sessions` — sessions with sync state
- `clacks daemon` — sync every `sync_frequency` (systemd unit in `contrib/`)
- `clacks server start` — run the sync server

## Verification

```sh
go test ./...   # unit + e2e (in-process server, two temp machines)
```

End-to-end covers A→B session/message/part/todo sync, B→A message sync with
no duplicates on re-sync, and newer-`time_updated`-wins conflicts (see `e2e/`).
Manual check against a real `opencode.db`: sync read-only with `--since` recent
from machine A, sync into an empty `XDG_DATA_HOME` on B, then diff
`opencode export <id>` output between the two.

## Limitations (v1)

- `snapshot/` git snapshots and `tool-output/` are not synced.
- A running opencode TUI may need a restart before synced sessions show up.
- Deletes are off by default (`sources.opencode.propagate_deletes`).
- No CGO: storage uses `modernc.org/sqlite`.
