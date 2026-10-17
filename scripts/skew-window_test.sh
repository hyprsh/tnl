#!/bin/sh
# Tests scripts/skew-window.sh against the versions next-release.sh gives.
# Run from the repo root; scripts/check.sh calls it.
set -eu

script=scripts/skew-window.sh
fail=0

# check TAG_VERSION NEXT WANT
check() {
  got=$(sh "$script" "$1" "$2") || got="error"
  if [ "$got" != "$3" ]; then
    echo "FAIL: tag $1, next $2: got '$got', want '$3'" >&2
    fail=1
  fi
}

check 0.1.2 0.1.3 '0.2.0 1'   # 0.x patch
check 0.1.2 0.2.0 '0.2.0 0'   # 0.x breaking
check 1.4.2 1.5.0 '3.0.0 1'   # 1.x feat
check 1.4.2 1.4.3 '3.0.0 1'   # 1.x patch
check 1.4.2 2.0.0 '3.0.0 0'   # 1.x breaking

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "ok: skew-window.sh matches the version window"
