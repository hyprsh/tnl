#!/bin/sh
# Builds a GoReleaser snapshot and checks it: every archive in the spec's table,
# each holding the files the spec lists, and `tnl version` naming the build.
# Not part of check.sh or CI: it fetches GoReleaser and takes a few minutes.
# Run it before touching .goreleaser.yaml or the packaging files.
set -eu
cd "$(dirname "$0")/.."

go run github.com/goreleaser/goreleaser/v2@v2.18.3 release --snapshot --clean --skip=publish

cd dist
test -s SHA256SUMS
test -s ../build/install.sh
n=0
for a in tnl_*_linux_amd64.tar.gz tnl_*_linux_arm64.tar.gz \
  tnl-outpost_*_linux_amd64.tar.gz tnl-outpost_*_linux_arm64.tar.gz \
  tnl-outpost_*_darwin_amd64.tar.gz tnl-outpost_*_darwin_arm64.tar.gz; do
  [ -f "$a" ] || { echo "missing archive $a" >&2; exit 1; }
  grep -q "  $a\$" SHA256SUMS || { echo "$a is not in SHA256SUMS" >&2; exit 1; }
  files=$(tar -tzf "$a" | sort | tr '\n' ' ')
  want="LICENSE NOTICE README.md systemd/ systemd/tnl-edge.service systemd/tnl-outpost.service tnl "
  [ "$files" = "$want" ] || [ "$files" = "LICENSE NOTICE README.md systemd/tnl-edge.service systemd/tnl-outpost.service tnl " ] ||
    { echo "$a holds: $files" >&2; exit 1; }
  n=$((n + 1))
done
[ "$n" = 6 ] || { echo "expected 6 archives" >&2; exit 1; }

# The archive for this host runs, and names its version and build.
case $(uname -s)_$(uname -m) in
  Linux_x86_64) os=linux arch=amd64 ;;
  Linux_aarch64) os=linux arch=arm64 ;;
  Darwin_arm64) os=darwin arch=arm64 ;;
  Darwin_x86_64) os=darwin arch=amd64 ;;
  *) echo "ok: archives checked; can't run this host's binary"; exit 0 ;;
esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tar -xzf "tnl-outpost_"*"_${os}_${arch}.tar.gz" -C "$tmp"
"$tmp/tnl" version | grep -q ' outpost$' || { echo "Outpost archive is not the Outpost build" >&2; exit 1; }
if [ "$os" = linux ]; then
  tar -xzf "tnl_"*"_${os}_${arch}.tar.gz" -C "$tmp"
  "$tmp/tnl" version | grep -q ' full$' || { echo "full archive is not the full build" >&2; exit 1; }
fi
echo "ok: snapshot release checked"
