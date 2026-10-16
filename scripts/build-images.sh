#!/bin/sh
# Builds both images from the archives GoReleaser wrote to dist/ (no compiling
# here, so no QEMU is needed for arm64).
#
#   build-images.sh VERSION DIST push   multi-arch, tagged X.Y.Z, X.Y, X and latest,
#                                       pushed; digests go to DIST/digest-<image>.txt
#   build-images.sh VERSION DIST load   this host's arch only, loaded as tnl:local
#                                       and tnl-outpost:local (a local smoke test)
#   build-images.sh VERSION DIST tags   prints the tags and builds nothing
#
# Only a plain X.Y.Z is accepted, so no other version can move X.Y, X or latest.
set -eu
[ $# -eq 3 ] || { echo "usage: $0 VERSION DIST push|load|tags" >&2; exit 2; }
version=${1#v}
dist=$2
mode=$3
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
  { echo "version must be a plain X.Y.Z, got $1" >&2; exit 2; }
# X.Y.Z, X.Y, X and latest (spec, Releases).
minor=${version%.*}
tags="$version $minor ${minor%.*} latest"
if [ "$mode" = tags ]; then echo "$tags"; exit 0; fi
here=$(cd "$(dirname "$0")/.." && pwd)
registry=ghcr.io/hyprsh

case $mode in
  push) platforms="linux/amd64 linux/arm64" ;;
  load)
    case $(uname -m) in
      x86_64 | amd64) platforms=linux/amd64 ;;
      *) platforms=linux/arm64 ;;
    esac
    ;;
  *) echo "mode must be push, load or tags" >&2; exit 2 ;;
esac

for image in tnl tnl-outpost; do
  if [ "$image" = tnl ]; then dockerfile=Dockerfile.edge; else dockerfile=Dockerfile.outpost; fi
  ctx=$(mktemp -d)
  plist=
  for p in $platforms; do
    mkdir -p "$ctx/$p"
    tar -xzf "$dist/${image}_${version}_${p%/*}_${p#*/}.tar.gz" -C "$ctx/$p" tnl
    plist="$plist,$p"
  done
  plist=${plist#,}
  if [ "$mode" = load ]; then
    docker buildx build --platform "$plist" -f "$here/packaging/docker/$dockerfile" \
      -t "$image:local" --load "$ctx"
  else
    targs=
    for t in $tags; do targs="$targs -t $registry/$image:$t"; done
    # shellcheck disable=SC2086
    docker buildx build --platform "$plist" -f "$here/packaging/docker/$dockerfile" \
      $targs --push --metadata-file "$ctx/meta.json" "$ctx"
    sed -n 's/.*"containerimage.digest": *"\([^"]*\)".*/\1/p' "$ctx/meta.json" > "$dist/digest-$image.txt"
  fi
  rm -rf "$ctx"
done
