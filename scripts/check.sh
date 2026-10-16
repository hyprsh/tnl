#!/bin/sh
# Every check CI runs, in CI's order; exits non-zero on the first failure.
# Run from the repo root. Extra arguments go to `go test`, e.g. -count=1.
# -tags replaces GOFLAGS, so every command passes its tags itself.
set -eu

unformatted=$(gofmt -l cmd internal)
if [ -n "$unformatted" ]; then
  echo "::error::not gofmt-formatted (run gofmt -w):" >&2
  echo "$unformatted" >&2
  exit 1
fi

for tags in nomysql nomysql,outpost; do
  go vet -tags "$tags" ./...
  go tool staticcheck -tags "$tags" ./...
done

# Includes the Caddy suite (internal/harness/caddy_suite_test.go, about 40 s).
# Caddy is upgraded only in a PR of its own, so that suite can vouch for it.
go test -timeout 15m -tags nomysql "$@" ./...
# The Outpost build's own CLI tests (its refusals).
go test -tags nomysql,outpost "$@" ./cmd/tnl

scripts/check-outpost-deps.sh
echo "ok: every check passed"
