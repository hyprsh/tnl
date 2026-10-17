#!/bin/sh
# Prints the version release-please gives the current tree after a plain vX.Y.Z
# tag (spec: Build order; release-please-config.json bumps the minor pre-major).
# If any commit since the tag is breaking, the next minor (pre-1.0: 0.(m+1).0;
# from 1.0: the next major). Otherwise the next patch. A commit is breaking when
# its subject is `type(scope)!:` or `type!:`, or a line of its body starts with
# `BREAKING CHANGE:` or `BREAKING-CHANGE:`. Run from a checkout that has the tag:
#   sh scripts/next-release.sh vX.Y.Z
set -eu

tag=${1:?usage: next-release.sh vX.Y.Z}
if ! printf '%s\n' "$tag" | grep -Eq '^v[0-9]+[.][0-9]+[.][0-9]+$'; then
  echo "next-release.sh: not a plain vX.Y.Z tag: $tag" >&2
  exit 1
fi
if ! git rev-parse -q --verify "$tag^{commit}" >/dev/null; then
  echo "next-release.sh: no such tag: $tag" >&2
  exit 1
fi

v=${tag#v}
major=${v%%.*}
rest=${v#*.}
minor=${rest%%.*}
patch=${rest#*.}

# Each commit message is one record, starting with the 0x1e separator.
breaking=$(git log --format='%x1e%B' "$tag..HEAD" | awk '
  BEGIN { RS = "\036" }
  {
    sub(/^\n+/, "")
    if ($0 == "") next
    n = split($0, lines, "\n")
    if (lines[1] ~ /^[A-Za-z]+([(][^)]*[)])?!:/) found = 1
    for (i = 2; i <= n; i++)
      if (lines[i] ~ /^BREAKING[ -]CHANGE:/) found = 1
  }
  END { print found + 0 }
')

if [ "$breaking" = 1 ]; then
  if [ "$major" = 0 ]; then
    echo "0.$((minor + 1)).0"
  else
    echo "$((major + 1)).0.0"
  fi
else
  echo "$major.$minor.$((patch + 1))"
fi
