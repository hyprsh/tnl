#!/bin/sh
# Tests scripts/ci-scope.sh. Run from the repo root; scripts/check.sh calls it.
set -eu

fail=0

# check WANT PATHS: PATHS is printf %b (\n separates paths).
check() {
  got=$(printf '%b' "$2" | sh scripts/ci-scope.sh)
  if [ "$got" != "$1" ]; then
    echo "FAIL: paths '$2': got $got, want $1" >&2
    fail=1
  fi
}

# A release PR's paths.
check docs '.release-please-manifest.json\nCHANGELOG.md\nREADME.md\ndocs/guide/outposts.md\nexamples/compose/edge.yaml\nexamples/compose/outpost.yaml\n'
check docs 'docs/adr/0015-x.md'
check docs 'GLOSSARY.md\n'

# Anything else needs every check.
check full ''
check full 'CHANGELOG.md\ninternal/edge/web.go'
check full 'scripts/check.sh'
check full '.github/workflows/ci.yml'
check full 'release-please-config.json'
check full 'install.sh'
check full 'go.mod\nREADME.md'

[ "$fail" -eq 0 ] || exit 1
echo "ok: ci-scope.sh tells a docs-only change from the rest"
