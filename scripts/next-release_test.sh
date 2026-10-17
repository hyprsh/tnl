#!/bin/sh
# Tests scripts/next-release.sh on sample commits. Each case makes a throwaway
# repo, tags a base commit, adds one commit with the given message, and checks
# the version. Run from the repo root; scripts/check.sh calls it.
set -eu

script="$(pwd)/scripts/next-release.sh"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/tnl-next.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
fail=0

# check TAG MESSAGE WANT: MESSAGE is printf %b (\n is a newline); an empty
# MESSAGE adds no commit after the tag.
check() {
  d=$(mktemp -d "$tmp/r.XXXXXX")
  git -C "$d" init -q
  git -C "$d" -c commit.gpgsign=false commit -q --allow-empty -m base
  git -C "$d" tag "$1"
  if [ -n "$2" ]; then
    printf '%b' "$2" >"$tmp/msg"
    git -C "$d" -c commit.gpgsign=false commit -q --allow-empty -F "$tmp/msg"
  fi
  got=$(cd "$d" && sh "$script" "$1") || got="error"
  if [ "$got" != "$3" ]; then
    echo "FAIL: tag $1, message '$2': got $got, want $3" >&2
    fail=1
  fi
}

# Nothing since the tag: the next patch.
check v0.1.2 '' 0.1.3

# Not breaking: the next patch.
check v0.1.2 'fix: plain fix' 0.1.3
check v0.1.2 'fix(liveness): a scoped fix' 0.1.3
check v0.1.2 'chore(main): release 0.1.3' 0.1.3
check v0.1.2 'feat!' 0.1.3
check v0.1.2 'fix: BREAKING CHANGE in the subject only' 0.1.3
check v0.1.2 'fix: x\n\nThe old format is not a BREAKING CHANGE here.' 0.1.3
check v0.1.2 'fix: x\n\n  BREAKING CHANGE: indented, not a footer' 0.1.3

# Breaking before 1.0: the next minor.
check v0.1.2 'feat(wire)!: change the Route frame' 0.2.0
check v0.1.2 'refactor!: drop the old state.json' 0.2.0
check v0.1.2 'feat!: no scope' 0.2.0
check v0.1.2 'feat(a)!: a\n\nwith a body' 0.2.0
check v0.1.2 'fix: x\n\nBREAKING CHANGE: the Outpost token format changed' 0.2.0
check v0.1.2 'fix: x\n\nBREAKING-CHANGE: state.json shape' 0.2.0
check v0.9.4 'feat!: x' 0.10.0

# Breaking from 1.0: the next major.
check v1.4.2 'fix: plain' 1.4.3
check v1.4.2 'feat!: breaking' 2.0.0
check v1.4.2 'fix: x\n\nBREAKING CHANGE: y' 2.0.0

# A feat before 1.0 is a patch (bump-patch-for-minor-pre-major).
check v0.1.2 'feat: a feature' 0.1.3
check v0.1.2 'feat(route): a scoped feature' 0.1.3

# A feat from 1.0 is the next minor; breaking still wins.
check v1.4.2 'feat: a feature' 1.5.0
check v1.4.2 'feat(route): scoped' 1.5.0
check v1.4.2 'feat: x\n\nBREAKING CHANGE: y' 2.0.0
check v1.4.2 'chore: nothing' 1.4.3
check v1.4.2 'featx: not a feat' 1.4.3

# A plain vX.Y.Z tag only.
if (cd "$tmp" && sh "$script" v0.1.2-rc1) >/dev/null 2>&1; then
  echo "FAIL: a pre-release tag was accepted" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "ok: next-release.sh gives the version release-please would"
