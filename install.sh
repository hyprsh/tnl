#!/bin/sh
# Installs tnl from a GitHub release.
#
#   install.sh edge --address edge.example.com
#   install.sh outpost --token <token>
#
# Each release attaches its own copy of this script, which installs that
# release's version. Re-running it is the upgrade. It stays out of the firewall
# and of uninstalling; docs/guide/install.md lists every step.
#
# Test and packaging hooks (not for operators): TNL_VERSION, TNL_RELEASE_DIR,
# TNL_ROOT (prefix for /usr/local, /etc), TNL_PLATFORM (os_arch),
# TNL_MODE (system or user, instead of detecting root and systemd).
set -eu

version=${TNL_VERSION:-__VERSION__}
repo=hyprsh/tnl

die() {
  echo "install.sh: $*" >&2
  exit 1
}

usage() {
  echo "usage: install.sh edge --address <dns name>" >&2
  echo "       install.sh outpost --token <token>" >&2
  exit 2
}

case $version in
  __*) die "this copy of install.sh is not from a release; download it from a release's assets, or set TNL_VERSION" ;;
esac
version=${version#v}

[ $# -ge 1 ] || usage
role=$1
shift
address=
token=
while [ $# -gt 0 ]; do
  case $1 in
    --address) [ $# -ge 2 ] || usage; address=$2; shift 2 ;;
    --address=*) address=${1#--address=}; shift ;;
    --token) [ $# -ge 2 ] || usage; token=$2; shift 2 ;;
    --token=*) token=${1#--token=}; shift ;;
    *) usage ;;
  esac
done
case $role in
  edge) [ -n "$address" ] || die "edge needs --address, the DNS name Outposts dial" ;;
  outpost) [ -n "$token" ] || die "outpost needs --token, from 'tnl outpost add' on the Edge" ;;
  *) usage ;;
esac

# Platform.
if [ -n "${TNL_PLATFORM:-}" ]; then
  os=${TNL_PLATFORM%_*}
  arch=${TNL_PLATFORM#*_}
else
  case $(uname -s) in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported OS $(uname -s); tnl ships for Linux and macOS (Outpost only)" ;;
  esac
  case $(uname -m) in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) die "unsupported CPU $(uname -m); tnl ships for amd64 and arm64" ;;
  esac
fi
if [ "$role" = edge ] && [ "$os" != linux ]; then
  die "the Edge runs on Linux only"
fi

# System install (binary, unit, env file, service) or user install (binary only).
mode=${TNL_MODE:-}
if [ -z "$mode" ]; then
  mode=user
  if [ "$(id -u)" = 0 ] && [ "$os" = linux ] && command -v systemctl >/dev/null 2>&1; then
    mode=system
  fi
fi
root=${TNL_ROOT:-}
if [ "$mode" = system ]; then
  bindir=$root/usr/local/bin
  etcdir=$root/etc/tnl
  unitdir=$root/etc/systemd/system
else
  [ -n "${HOME:-}" ] || die "HOME is not set"
  bindir=$HOME/.local/bin
fi
bin=$bindir/tnl

# The full build serves both roles, so an installed full build is never replaced
# by the Outpost build: the Outpost role then installs the full archive.
build=outpost
prefix=tnl-outpost
if [ "$role" = edge ]; then
  build=full
else
  existing=$("$bin" version 2>/dev/null | awk '{print $3}') || existing=
  [ "$existing" = full ] && build=full
fi
[ "$build" = full ] && prefix=tnl
archive=${prefix}_${version}_${os}_${arch}.tar.gz

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { # fetch NAME: puts release file NAME in $tmp
  if [ -n "${TNL_RELEASE_DIR:-}" ]; then
    cp "$TNL_RELEASE_DIR/$1" "$tmp/$1" || die "no $1 in $TNL_RELEASE_DIR"
  else
    curl -fsSL -o "$tmp/$1" "https://github.com/$repo/releases/download/v$version/$1" ||
      die "could not download $1 for v$version"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

echo "Downloading tnl $version ($build build, $os/$arch)"
fetch "$archive"
fetch SHA256SUMS

# 1. Check the archive against the release's SHA256SUMS. (This catches a
# corrupted download; the attestation is the proof of origin.)
want=$(awk -v f="$archive" '$2 == f || $2 == "*" f {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "checksum: $archive is not in SHA256SUMS"
got=$(sha256 "$tmp/$archive")
[ "$got" = "$want" ] || die "checksum mismatch for $archive (wanted $want, got $got); nothing was installed"

mkdir "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x"

# Units that run now, so the upgrade restarts them.
active=
if [ "$mode" = system ]; then
  for u in tnl-edge tnl-outpost; do
    if systemctl is-active --quiet "$u"; then active="$active $u"; fi
  done
fi

# 2. Swap the binary in atomically.
mkdir -p "$bindir"
cp "$tmp/x/tnl" "$bin.new.$$"
chmod 755 "$bin.new.$$"
mv -f "$bin.new.$$" "$bin"
echo "Installed $bin"

if [ "$mode" = user ]; then
  echo
  echo "Installed the binary only: this host has no root or no systemd, so there is no unit."
  case ":$PATH:" in
    *":$bindir:"*) ;;
    *) echo "Add $bindir to your PATH." ;;
  esac
  if [ "$role" = edge ]; then
    echo "Run the Edge with: tnl edge --address $address"
  else
    echo "Connect the Outpost with: tnl connect --token $token"
  fi
  exit 0
fi

# 3. Install the unit, overwriting any older copy.
if [ "$role" = edge ]; then unit=tnl-edge; envname=edge; else unit=tnl-outpost; envname=outpost; fi
mkdir -p "$unitdir"
cp "$tmp/x/systemd/$unit.service" "$unitdir/$unit.service"
chmod 644 "$unitdir/$unit.service"
echo "Installed $unitdir/$unit.service"

# 4. Write the env file, but never overwrite one.
envfile=$etcdir/$envname.env
mkdir -p "$etcdir"
if [ -e "$envfile" ]; then
  echo "Kept the existing $envfile"
else
  umask 077
  if [ "$role" = edge ]; then
    echo "TNL_ADDRESS=$address" > "$envfile"
  else
    echo "TNL_TOKEN=$token" > "$envfile"
  fi
  echo "Wrote $envfile"
fi

# 4b. Raise the socket buffer limits. quic-go (the Edge's HTTP/3) and wireguard-go
# (both roles) ask for 7 MiB buffers, which Linux caps at net.core.rmem_max and
# wmem_max, about 208 KiB by default. An Outpost only needs this when the limits
# are low. Never overwrite the file; a failure to apply it is a warning.
sysctlfile=$root/etc/sysctl.d/60-tnl.conf
want=7500000
low=yes
if [ "$role" = outpost ]; then
  low=no
  for k in rmem_max wmem_max; do
    cur=$(sysctl -n "net.core.$k" 2>/dev/null) || cur=
    case $cur in
      '' | *[!0-9]*) low=yes ;;
      *) [ "$cur" -ge "$want" ] || low=yes ;;
    esac
  done
fi
if [ -e "$sysctlfile" ]; then
  echo "Kept the existing $sysctlfile"
elif [ "$low" = yes ]; then
  mkdir -p "$(dirname "$sysctlfile")"
  printf 'net.core.rmem_max = %s\nnet.core.wmem_max = %s\n' "$want" "$want" > "$sysctlfile"
  chmod 644 "$sysctlfile"
  echo "Wrote $sysctlfile: net.core.rmem_max and net.core.wmem_max set to $want, for the UDP socket buffers"
  sysctl -p "$sysctlfile" >/dev/null 2>&1 ||
    echo "Warning: could not apply $sysctlfile; run 'sysctl -p $sysctlfile' as root, or reboot" >&2
fi

# 5. Start the unit, and restart whichever tnl units were already running.
systemctl daemon-reload
systemctl enable --now "$unit"
for u in $active; do
  systemctl restart "$u"
  echo "Restarted $u"
done
sleep 1
if ! systemctl is-active --quiet "$unit"; then
  journalctl -u "$unit" -n 20 --no-pager || true
  die "$unit did not start; its last log lines are above"
fi
echo "$unit is running"
