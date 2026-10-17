#!/bin/sh
# The version window as scripts/check-skew.sh needs it (internal/version's
# EdgeAccepts is the rule): given a release tag's version and the version the
# current tree will have, prints "OUT INSIDE".
#   OUT    the first version outside the tag's window: the next 0.x minor
#          before 1.0, two majors on from 1.0.
#   INSIDE 1 when NEXT is in the tag's window (the same minor before 1.0, the
#          same major from 1.0), else 0. A breaking tree is stamped outside, so
#          it cannot join the tag.
#   sh scripts/skew-window.sh 1.4.2 1.5.0
set -eu

v=${1:?usage: skew-window.sh TAG_VERSION NEXT_VERSION}
next=${2:?usage: skew-window.sh TAG_VERSION NEXT_VERSION}
major=${v%%.*}
rest=${v#*.}
minor=${rest%%.*}
nmajor=${next%%.*}
nrest=${next#*.}
nminor=${nrest%%.*}

if [ "$major" = 0 ]; then
  out=0.$((minor + 1)).0
  if [ "$nmajor" = 0 ] && [ "$nminor" = "$minor" ]; then inside=1; else inside=0; fi
else
  out=$((major + 2)).0.0
  if [ "$nmajor" = "$major" ]; then inside=1; else inside=0; fi
fi
echo "$out $inside"
