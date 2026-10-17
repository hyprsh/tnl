#!/bin/sh
# Real Edge and Outpost processes of different versions, run against each other
# (spec: Testing). Run from the repo root; scripts/check.sh calls it.
#
# 1. The current tree built three times with other versions stamped into
#    main.version: 0.1.0, 0.1.9 (same minor) and 0.2.0 (another minor). A same
#    minor must serve a Route and another minor must make the Outpost
#    Incompatible, with the Edge on each side.
# 2. The latest plain vX.Y.Z tag, built from its own source (git archive, so no
#    published archive is needed), against the current tree stamped as the tag's
#    next patch, in both directions, and stamped outside the tag's window (the
#    next minor before 1.0, two majors on after) for the Incompatible case, with
#    either side upgraded to it. With no tag this leg is skipped with a
#    notice, and it switches on by itself after the first release.
#
# The Edge cannot bind 80 and 443 unprivileged, and has no public setting for
# other ports. These builds are linked with -X main.testWebFile=PATH, which makes
# an Edge read its listen ports and its test CA from that file (cmd/tnl/
# edge_testweb.go); a release build never sets it. The tests are
# internal/harness/skew_test.go, which skip unless the TNL_SKEW_* variables below
# are set.
#
# -tags replaces GOFLAGS, so every command passes its tags itself; CGO is off as
# in the release.
set -eu

tmp=$(mktemp -d "${TMPDIR:-/tmp}/tnl-skew.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM
export CGO_ENABLED=0
web="$tmp/web.json"

# build VERSION OUTPUT [SOURCE_DIR]: a full build stamped as VERSION.
build() {
  (
    cd "${3:-.}"
    go build -tags nomysql -trimpath \
      -ldflags "-X main.version=$1 -X main.testWebFile=$web" -o "$2" ./cmd/tnl
  )
}

build 0.1.0 "$tmp/tnl-a"
build 0.1.9 "$tmp/tnl-b"
build 0.2.0 "$tmp/tnl-c"
TNL_SKEW_WEB="$web" TNL_SKEW_DIR="$tmp" \
  go test -count=1 -timeout 10m -tags nomysql -run 'TestSkewStampedBuilds|TestEdgeUnderTheUnitsEnvironment' ./internal/harness

# Only a plain vX.Y.Z counts: a pre-release tag releases nothing.
tag=$(git tag -l 'v[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
if [ -z "$tag" ]; then
  echo "notice: no v* release tag in this checkout, so the check against the latest release is skipped (CI needs fetch-depth: 0 to see tags)"
else
  mkdir "$tmp/src"
  git archive "$tag" | tar -x -C "$tmp/src"
  v=${tag#v}
  next=${v%.*}.$((${v##*.} + 1))
  # Outside the window: a new 0.x minor, or from 1.0 two majors on.
  major=${v%%.*}
  rest=${v#*.}
  minor=${rest%%.*}
  if [ "$major" = 0 ]; then out=0.$((minor + 1)).0; else out=$((major + 2)).0.0; fi
  build "$v" "$tmp/tnl-tag" "$tmp/src"
  build "$next" "$tmp/tnl-cur"
  build "$out" "$tmp/tnl-cur-out"
  edge_ok=0
  if grep -rqs testWebFile "$tmp/src/cmd/tnl"; then
    edge_ok=1
  else
    echo "notice: $tag predates the check's high-port hook, so its Edge is not run; only its Outpost is"
  fi
  TNL_SKEW_WEB="$web" TNL_SKEW_TAG_BIN="$tmp/tnl-tag" TNL_SKEW_CUR_BIN="$tmp/tnl-cur" TNL_SKEW_CUR_OUT_BIN="$tmp/tnl-cur-out" TNL_SKEW_TAG_EDGE_OK="$edge_ok" \
    go test -count=1 -timeout 10m -tags nomysql -run 'TestSkewAgainstTheLatestTag' ./internal/harness
fi
echo "ok: real Edge and Outpost processes of different versions work together as the spec says"
