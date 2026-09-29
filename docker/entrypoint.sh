#!/bin/sh
# Two modes, chosen by the uid we start as:
#
#   root    -> normalise ownership of the data/config dirs, then drop
#              privileges and exec clacks as the unprivileged clacks user.
#              Used by the server, whose /data is a root-owned named volume.
#   non-root-> exec clacks directly. Used when compose pins the container to
#              the host user's uid (${UID}:${GID}) so that bind-mounted host
#              files, like opencode.db, are read and written as that user and
#              never chowned underneath them.
#
# The clacks process never runs as root.
set -e

if [ "$(id -u)" = "0" ]; then
    want=$(id -u clacks)
    for dir in /data /config; do
        # Skip read-only mounts (the server mounts /config :ro): chown would
        # fail there and must not stop startup.
        if [ -d "$dir" ] && [ "$(stat -c %u "$dir")" != "$want" ]; then
            echo "entrypoint: chown $dir to clacks ($(stat -c %u "$dir") -> $want)"
            chown -R clacks:clacks "$dir" 2>/dev/null ||
                echo "entrypoint: $dir is read-only, leaving as is"
        fi
    done
    exec su-exec clacks /usr/local/bin/clacks "$@"
fi

exec /usr/local/bin/clacks "$@"
