# Running clacks in Docker

## Build and publish an image

```sh
make docker      # ghcr.io/rodneyosodo/clacks:<version>, :<commit> and :latest
```

Images are named `$(DOCKER_IMAGE_NAME_PREFIX)/$(BINARY):$(VERSION)`, with the prefix defaulting to `ghcr.io/rodneyosodo`. Point it elsewhere with:

```sh
DOCKER_IMAGE_NAME_PREFIX=my-registry.io/team make docker
```

Every build carries two tags: the release version and `latest`. To publish,
log in to the registry first, then:

```sh
docker login ghcr.io     # or: echo "$GHCR_TOKEN" | docker login ghcr.io -u USER --password-stdin
make docker-push         # builds, then pushes all three tags
```

The push stops at the first failure, so `latest` is never published if the version push failed. To push something already built, use `docker push <image>:<tag>` directly.

For a faster inner loop, build the binary on the host and bake it in:

```sh
make build && make docker-dev   # -> ghcr.io/rodneyosodo/clacks:dev
```

## Compose

Compose runs **prebuilt images** — there is no `build:` section, so build or pull an image first.

```sh
export CLACKS_UID=$(id -u) CLACKS_GID=$(id -g)
cp docker/config.example.toml docker/config/config.toml

make compose-up                  # server only, on :8080
make compose-up --profile client # server + a syncing client
```

```sh
docker compose -f docker/compose.yaml up -d
```

Then register once, which stores the token in the mounted config:

```sh
docker compose -f docker/compose.yaml run --rm clacks-client register \
    --username you --password 'your-password'
```

## How the two services differ

**Server** keeps its record store in a named volume. The entrypoint starts as root for one reason — to hand that root-owned volume to the unprivileged `clacks` user — and then drops privileges with `su-exec`. The server process never runs as root.

**Client** is pinned to your uid/gid (`CLACKS_UID`/`CLACKS_GID`, not `UID`, which bash makes read-only). This means the bind-mounted `opencode.db` is read and written as you, and clacks state stays visible on the host in `.clacks-data/`, so you can inspect or back it up. Relocate it with `CLACKS_CLIENT_HOME`.

## Settings

| Variable                    | Default                                   | Purpose                                         |
| --------------------------- | ----------------------------------------- | ----------------------------------------------- |
| `OPENCODE_DB`               | `$HOME/.local/share/opencode/opencode.db` | Which opencode database to mount                |
| `CLACKS_CLIENT_HOME`        | `../.clacks-data`                         | Where the client keeps its record store and key |
| `CLACKS_UID` / `CLACKS_GID` | `1000`                                    | uid/gid the client runs as                      |
| `CLACKS_KEY`                | empty                                     | The mnemonic, instead of the key file           |
| `CLACKS_SERVER_PORT`        | `8080`                                    | Published port for the server                   |
| `CLACKS_LOG_LEVEL`          | `info`                                    | Log level for both services                     |
