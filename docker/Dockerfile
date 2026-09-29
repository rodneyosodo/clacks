# Build clacks from source in a builder stage, then ship only the static
# binary. CGO is off, so the result runs on any Linux host and needs no libc.
FROM golang:1.26-alpine AS builder

ARG VERSION
ARG COMMIT
ARG COMMIT_TIME
ARG TIME
ARG CGO_ENABLED=0

WORKDIR /src

# Dependencies first so the module cache survives source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=${CGO_ENABLED} go build \
    -ldflags "-s -w \
    -X 'main.version=${VERSION}' \
    -X 'main.commit=${COMMIT}' \
    -X 'main.commitTime=${COMMIT_TIME}' \
    -X 'main.buildTime=${TIME}'" \
    -o /clacks ./cmd/clacks

FROM alpine:3.24

RUN apk add --no-cache ca-certificates tzdata wget su-exec \
    && adduser -D -u 10001 clacks

COPY docker/entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

COPY --from=builder /clacks /usr/local/bin/clacks

# No USER directive here on purpose: the entrypoint starts as root so it can
# hand root-owned volumes to the clacks user, then execs clacks unprivileged
# via su-exec. The process itself never runs as root.

# Data (record store, key) and config live under these paths; mount them to
# keep sync history across container restarts.
ENV CLACKS_HOME=/data \
    CLACKS_CONFIG_HOME=/config

VOLUME ["/data", "/config"]

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["server", "start", "--listen", ":8080", "--db", "/data/server.db"]
