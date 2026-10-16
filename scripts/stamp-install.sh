#!/bin/sh
# Writes install.sh with its version baked in: stamp-install.sh VERSION OUTFILE.
# A leading "v" is dropped, so v1.2.3 and 1.2.3 both stamp 1.2.3.
set -eu
[ $# -eq 2 ] || { echo "usage: $0 VERSION OUTFILE" >&2; exit 2; }
version=${1#v}
mkdir -p "$(dirname "$2")"
sed "s/__VERSION__/$version/" "$(dirname "$0")/../install.sh" > "$2"
chmod 755 "$2"
