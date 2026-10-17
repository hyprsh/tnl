#!/bin/sh
# Prints the version release-please gives the current tree after a plain vX.Y.Z
# tag (spec: Build order). release-please-config.json sets bump-minor-pre-major
# and bump-patch-for-minor-pre-major, so before 1.0 a breaking commit gives the
# next minor (0.(m+1).0) and anything else the next patch. From 1.0 a breaking
# commit gives the next major, a `feat` the next minor and anything else the next
# patch. A commit is breaking when its subject is `type(scope)!:` or `type!:`, or
# a line of its body starts with `BREAKING CHANGE:` or `BREAKING-CHANGE:`. Run
# from a checkout that has the tag:
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
# 2 when a commit is breaking, 1 when one is a feat, else 0.
level=$(git log --format='%x1e%B' "$tag..HEAD" | awk '
  BEGIN { RS = "\036" }
  {
    sub(/^\n+/, "")
    if ($0 == "") next
    n = split($0, lines, "\n")
    if (lines[1] ~ /^[A-Za-z]+([(][^)]*[)])?!:/) found = 2
    else if (lines[1] ~ /^feat([(][^)]*[)])?:/ && found < 1) found = 1
    for (i = 2; i <= n; i++)
      if (lines[i] ~ /^BREAKING[ -]CHANGE:/) found = 2
  }
  END { print found + 0 }
')

if [ "$level" = 2 ]; then
  if [ "$major" = 0 ]; then
    echo "0.$((minor + 1)).0"
  else
    echo "$((major + 1)).0.0"
  fi
elif [ "$level" = 1 ] && [ "$major" != 0 ]; then
  echo "$major.$((minor + 1)).0"
else
  echo "$major.$minor.$((patch + 1))"
fi
