#!/usr/bin/env bash
# Install the packages through the package site, the way users do, in
# containers of each distribution family:
#
#   debian    apt repository (signed InRelease), then the agent under SysV init
#   fedora    dnf repository (signed repomd)
#   opensuse  zypper, same repository
#   alpine    apk repository (signed APKINDEX), then the agent under OpenRC
#   arch      pacman repository (signed database and packages)
#   binary    release archives under /usr/local (no package manager used)
#   void      Void Linux (no package format of ours): archives, then the agent
#             under runit
#   <family>-file  (debian, fedora, alpine, arch) the release's package files,
#             as install.sh does where the site has no repository for the CPU
#
# Needs a signed snapshot in dist/ and the key that signed it:
#
#   GPG_FINGERPRINT=<fpr> GPG_PASSWORD= NFPM_KEY_FILE=key.asc NFPM_PASSPHRASE= \
#     NFPM_APK_KEY_FILE=apk.rsa goreleaser release --snapshot --clean --skip=publish
#   ORION_GPG_KEY=<fpr> ORION_APK_PRIVKEY=apk.rsa scripts/packages-smoke.sh [scenario...]
#
# The site and dist/ are served on 127.0.0.1 and the containers use the host
# network (docker or podman, ORION_CONTAINER_RUNTIME).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ORION_DIST:-$ROOT/dist}"
SITE_PORT="${ORION_SMOKE_SITE_PORT:-18080}"
DIST_PORT="${ORION_SMOKE_DIST_PORT:-18081}"
RUNTIME="${ORION_CONTAINER_RUNTIME:-$(command -v docker >/dev/null && echo docker || echo podman)}"
SCENARIOS=("$@")
((${#SCENARIOS[@]})) || SCENARIOS=(debian fedora opensuse alpine arch binary void debian-file fedora-file alpine-file arch-file)

: "${ORION_GPG_KEY:?set ORION_GPG_KEY to the key that signed dist/}"
: "${ORION_APK_PRIVKEY:?set ORION_APK_PRIVKEY to the RSA key that signed the apk packages}"

WORK="$(mktemp -d)"
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done
  rm -rf "$WORK"
}
trap cleanup EXIT

# The release version, from the agent archive's name.
agz="$(basename "$(ls "$DIST"/orion-belt-agent_*_linux_amd64.tar.gz | head -1)")"
VER="${agz#orion-belt-agent_}"
VER="${VER%_linux_amd64.tar.gz}"
TAG="v$VER"

echo "==> Building the site for $TAG"
ORION_DIST="$DIST" ORION_PKG_TAG="$TAG" ORION_REQUIRE_SIGN=1 \
  ORION_PACKAGES_OUT="$WORK/site" ORION_PACKAGES_URL="http://127.0.0.1:$SITE_PORT" \
  ORION_CONTAINER_RUNTIME="$RUNTIME" \
  bash "$ROOT/scripts/publish-packages-pages.sh" >"$WORK/publish.log" 2>&1 \
  || { cat "$WORK/publish.log"; exit 1; }
# A release lists TAG; install.sh reads it from the site and the mirror.
printf '%s\n' "$TAG" >"$WORK/site/TAG"
cp -a "$DIST" "$WORK/release"
printf '%s\n' "$TAG" >"$WORK/release/TAG"

python3 -m http.server --bind 127.0.0.1 --directory "$WORK/site" "$SITE_PORT" >/dev/null 2>&1 &
pids+=($!)
python3 -m http.server --bind 127.0.0.1 --directory "$WORK/release" "$DIST_PORT" >/dev/null 2>&1 &
pids+=($!)
sleep 1

SITE="http://127.0.0.1:$SITE_PORT"
REL="http://127.0.0.1:$DIST_PORT"

# ORION_SMOKE_TRACE=1 prints every command a scenario runs.
TRACE=""
[[ "${ORION_SMOKE_TRACE:-}" == 1 ]] && TRACE=x
run() { # run IMAGE SCRIPT
  "$RUNTIME" run --rm --network host -v "$ROOT/scripts/install.sh:/install.sh:ro" \
    -e ORION_PKGS_URL="$SITE" -e ORION_RELEASE_URL="$REL" "$1" sh "-eu${TRACE}c" "$2"
}

# Every scenario ends by running the installed binaries.
CHECK='
orion-belt-server --help >/dev/null
orion-belt-agent --help >/dev/null
osh --help >/dev/null 2>&1 || osh --version >/dev/null
oadmin --help >/dev/null
test -x /usr/lib/orion-belt/orion-belt-agent-service || test -x /usr/local/lib/orion-belt/orion-belt-agent-service
echo "binaries ok"
'

# The agent's service lifecycle: configured with a real key and a gateway
# that refuses connections, so the agent exits at once and the init system
# must keep restarting it (counted in its log, LOG). Then teardown must leave
# no agent process behind.
LIFECYCLE='
ssh-keygen -q -t ed25519 -N "" -f /etc/orion-belt/agent_key
printf "server:\n  host: 127.0.0.1\n  port: 1\nagent:\n  name: smoke\nauth:\n  key_file: /etc/orion-belt/agent_key\n  known_hosts: /etc/orion-belt/known_hosts\n  strict_host_key_checking: \"no\"\n" >/etc/orion-belt/agent.yaml
agent=${AGENT:-/usr/bin/orion-belt-agent}
agents() { for p in /proc/[0-9]*; do tr "\0" " " <"$p/cmdline" 2>/dev/null | grep -q "^$agent " && echo "${p#/proc/}"; done; true; }
svc=${SVC:-/usr/lib/orion-belt/orion-belt-agent-service}
log=${LOG:-/var/log/orion-belt-agent.log}
"$svc" setup
sleep 12
"$svc" status || true   # a crash-looping agent may be between restarts
starts=$(grep -c "Starting Orion-Belt agent" "$log" || true)
test "$starts" -ge 2 || { echo "agent not restarted (started $starts times)"; exit 1; }
echo "agent restarted $starts times"
"$svc" teardown
sleep 1
test -z "$(agents)" || { echo "agent left running: $(agents)"; exit 1; }
test ! -e /etc/init.d/orion-belt-agent && test ! -e /etc/sv/orion-belt-agent
'

declare -A IMAGES=(
  [debian]="debian:13@sha256:4c25275446ca371766b8b093c696782eabce06263fccacb8d5d813b3dc44b308"
  [fedora]="fedora:44@sha256:43b29f65a41eb9c35e1cd5323e3bdf3b655c2357a9f4f1ff2f9c2798e5045d80"
  [opensuse]="opensuse/leap:15.6@sha256:79be7751205ea84559990fb76b1bec71e38d6fad41c70a4f6c921b803b58f421"
  [alpine]="alpine:3.24@sha256:71e9b7bae160ba167fa17414401b94cbcdaf3c32feeab9298bd794a2a0614c35"
  [arch]="archlinux:base@sha256:52dfbd5afb23a220d5ee92dafcb4c788d96c026ed04179890c23adef4c897fa0"
)
IMAGES[binary]="${IMAGES[debian]}"
IMAGES[void]="ghcr.io/void-linux/void-glibc-full@sha256:9eb02d7ed63f77b785f5b597099292767fab23bbb824aa987f93ac583d8aa44f"

scenario() {
  case $1 in
    debian) echo '
apt-get update -qq && apt-get install -y -qq curl gpg >/dev/null
sh /install.sh --method repo server agent tools
grep -q "signed-by=/usr/share/keyrings/orion-belt.gpg" /etc/apt/sources.list.d/orion-belt.list
'"$CHECK"'
# The agent under SysV init.
apt-get install -y -qq sysvinit-utils init-system-helpers >/dev/null
export ORION_INIT=sysv
'"$LIFECYCLE"'
apt-get remove -y -qq orion-belt-agent >/dev/null
echo "sysv ok"' ;;
    fedora) echo '
sh /install.sh --method repo server agent tools
'"$CHECK"'
rpm -K "$(rpm -q --qf "%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}" orion-belt-agent)" >/dev/null 2>&1 || rpm -q --qf "%{SIGPGP:pgpsig}\n" orion-belt-agent | grep -qv none' ;;
    opensuse) echo '
zypper -q -n install curl gpg2 >/dev/null
sh /install.sh --method repo server agent tools
'"$CHECK" ;;
    alpine) echo '
apk add -q curl gnupg
sh /install.sh --method repo server agent tools
grep -q "/apk$" /etc/apk/repositories
'"$CHECK"'
# The agent under OpenRC (not booted in a container: mark it started).
apk add -q openrc
mkdir -p /run/openrc && touch /run/openrc/softlevel
'"$LIFECYCLE"'
echo "openrc ok"' ;;
    arch) echo '
pacman -Sy --noconfirm --needed gnupg curl >/dev/null
pacman-key --init >/dev/null 2>&1
sh /install.sh --method repo server agent tools
'"$CHECK" ;;
    binary) echo '
apt-get update -qq && apt-get install -y -qq curl gpg >/dev/null
sh /install.sh --method binary server agent tools
PATH=/usr/local/bin:$PATH
'"$CHECK" ;;
    void) echo '
xbps-install -Syu xbps >/dev/null && xbps-install -y curl openssh >/dev/null
sh /install.sh agent tools
PATH=/usr/local/bin:$PATH
orion-belt-agent --help >/dev/null && oadmin --help >/dev/null
# runit is not booted in a container: run its service scanner.
runsvdir /etc/runit/runsvdir/default >/tmp/runit.log 2>&1 &
export SVC=/usr/local/lib/orion-belt/orion-belt-agent-service AGENT=/usr/local/bin/orion-belt-agent LOG=/tmp/runit.log
test "$("$SVC" detect)" = runit
'"$LIFECYCLE"'
echo "runit ok"' ;;
    *-file)
      case ${1%-file} in
        debian) echo 'apt-get update -qq && apt-get install -y -qq curl gpg >/dev/null' ;;
        alpine) echo 'apk add -q curl gnupg' ;;
        arch) echo 'pacman -Sy --noconfirm --needed gnupg curl >/dev/null' ;;
      esac
      echo '
sh /install.sh --method package server agent tools
'"$CHECK" ;;
    *) echo "unknown scenario $1" >&2; return 1 ;;
  esac
}

failed=()
for s in "${SCENARIOS[@]}"; do
  echo "==> $s"
  if run "${IMAGES[${s%-file}]}" "$(scenario "$s")" >"$WORK/$s.log" 2>&1; then
    tail -2 "$WORK/$s.log"
  else
    tail -30 "$WORK/$s.log"
    failed+=("$s")
  fi
done
((${#failed[@]} == 0)) || { echo "FAILED: ${failed[*]}" >&2; exit 1; }
echo "all scenarios passed: ${SCENARIOS[*]}"
