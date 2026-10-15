#!/bin/sh
# Fails if the Outpost-only build links Caddy, or if the full build does not
# (which would make the first check pass vacuously). Run from the repo root.
# -tags replaces GOFLAGS, so both tags go on the command line.
set -eu

full=$(CGO_ENABLED=0 go list -deps -tags nomysql ./cmd/tnl)
if ! echo "$full" | grep -qE '^github\.com/caddyserver/caddy/v2$'; then
  echo "::error::the full build does not link Caddy, so the Outpost-only check proves nothing" >&2
  exit 1
fi

deps=$(CGO_ENABLED=0 go list -deps -tags nomysql,outpost ./cmd/tnl)
if echo "$deps" | grep -E '^github\.com/caddyserver/' >&2; then
  echo "::error::the Outpost-only build links Caddy" >&2
  exit 1
fi
CGO_ENABLED=0 go build -tags nomysql,outpost -o /dev/null ./cmd/tnl
echo "ok: the Outpost-only build links no Caddy"
