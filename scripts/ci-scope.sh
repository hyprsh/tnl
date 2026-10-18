#!/bin/sh
# Prints how much CI a change needs, from its changed paths on stdin, one per
# line: "docs" when every path is documentation, the release manifest or a
# Compose example (a release PR is all three), else "full". No paths is "full".
# The docs scope still runs the tests that read those files; see ci.yml.
set -eu

scope=full
while IFS= read -r path || [ -n "$path" ]; do
  [ -n "$path" ] || continue
  case "$path" in
    *.md | docs/* | examples/compose/* | .release-please-manifest.json) scope=docs ;;
    *) echo full; exit 0 ;;
  esac
done
echo "$scope"
