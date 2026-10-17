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

# The version the check against the latest release stamps on the current tree.
sh scripts/next-release_test.sh
sh scripts/skew-window_test.sh

# Real Edge and Outpost processes of other versions, and of the latest release tag.
scripts/check-skew.sh

# The shell scripts parse. (packaging/ tests install.sh and the units; the snapshot
# build is scripts/check-release.sh, which CI does not run.)
for f in install.sh scripts/*.sh; do sh -n "$f"; done
echo "ok: every check passed"
