#!/bin/sh
# Fails if the Outpost-only build links Caddy. Run from the repo root.
# -tags replaces GOFLAGS, so both tags go on the command line.
set -eu

deps=$(CGO_ENABLED=0 go list -deps -tags nomysql,outpost ./cmd/tnl)
if echo "$deps" | grep -E '^github\.com/caddyserver/' >&2; then
  echo "::error::the Outpost-only build links Caddy" >&2
  exit 1
fi
CGO_ENABLED=0 go build -tags nomysql,outpost -o /dev/null ./cmd/tnl
echo "ok: the Outpost-only build links no Caddy"
