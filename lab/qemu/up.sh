#!/usr/bin/env bash
# Boot Orion Belt QEMU lab: Ubuntu server + Alpine/SUSE/Debian agents.
set -euo pipefail

LAB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$LAB/../.." && pwd)"
RUN="$LAB/run"
IMG="$LAB/images"
PKG_PORT="${ORION_PKG_PORT:-8765}"
VERSION="${ORION_VERSION:-0.0.0-dev}"
SSH_KEY="${ORION_LAB_SSH_KEY:-$RUN/lab_id_ed25519}"

need() { command -v "$1" >/dev/null || { echo "Install $1 first (see lab/qemu/README.md)" >&2; exit 1; }; }
need qemu-system-x86_64
need qemu-img
need python3
need curl
need ssh-keygen

# cloud-localds OR genisoimage/mkisofs for seed ISOs
make_seed() {
  local ud="$1" md="$2" out="$3"
  if command -v cloud-localds >/dev/null; then
    cloud-localds "$out" "$ud" "$md"
  elif command -v genisoimage >/dev/null; then
    local tmp
    tmp="$(mktemp -d)"
    cp "$ud" "$tmp/user-data"
    cp "$md" "$tmp/meta-data"
    genisoimage -output "$out" -volid cidata -joliet -rock "$tmp/user-data" "$tmp/meta-data" >/dev/null 2>&1
    rm -rf "$tmp"
  elif command -v mkisofs >/dev/null; then
    local tmp
    tmp="$(mktemp -d)"
    cp "$ud" "$tmp/user-data"
    cp "$md" "$tmp/meta-data"
    mkisofs -output "$out" -volid cidata -joliet -rock "$tmp/user-data" "$tmp/meta-data" >/dev/null 2>&1
    rm -rf "$tmp"
  else
    echo "Need cloud-localds, genisoimage, or mkisofs to build cloud-init seed ISOs" >&2
    exit 1
  fi
}

render() {
  local src="$1" dest="$2"
  local pubkey
  pubkey="$(cat "${SSH_KEY}.pub")"
  local pkg_base="http://10.0.2.2:${PKG_PORT}"
  sed \
    -e "s|{{SSH_PUBKEY}}|${pubkey}|g" \
    -e "s|{{PKG_BASE_URL}}|${pkg_base}|g" \
    -e "s|{{VERSION}}|${VERSION}|g" \
    -e "s|{{HOSTNAME}}|${HOSTNAME:-host}|g" \
    -e "s|{{LOGIN}}|${LOGIN:-ubuntu}|g" \
    -e "s|{{DISTRO}}|${DISTRO:-unknown}|g" \
    -e "s|{{PACKAGE_FMT}}|${PACKAGE_FMT:-deb}|g" \
    "$src" > "$dest"
}

mkdir -p "$RUN" "$IMG" "$RUN/logs"
if [[ ! -f "$SSH_KEY" ]]; then
  ssh-keygen -t ed25519 -f "$SSH_KEY" -N "" -C "orion-qemu-lab"
fi

# Ensure images exist
if [[ ! -f "$IMG/ubuntu.qcow2" ]]; then
  echo "Cloud images missing — running download-images.sh"
  bash "$LAB/download-images.sh"
fi

# Build release artifacts into dist/ for the package HTTP server
echo "==> Building web UI → web/static (embedded in the server binary)"
(cd "$ROOT" && make build-ui)
echo "==> Building linux/amd64 binaries + packages into dist/"
mkdir -p "$ROOT/dist"
(
  cd "$ROOT"
  export GOTOOLCHAIN=go1.26.6
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/orion-belt-server ./cmd/server
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/orion-belt-agent ./cmd/agent
)
# Prefer goreleaser/nfpm packages when available
if command -v goreleaser >/dev/null; then
  (cd "$ROOT" && goreleaser release --snapshot --clean --skip=publish 2>/dev/null) || true
elif command -v nfpm >/dev/null && [[ -f "$ROOT/packaging/nfpm-server.yaml" ]]; then
  (cd "$ROOT" && nfpm package -p deb -f packaging/nfpm-server.yaml -t dist/) || true
  (cd "$ROOT" && nfpm package -p rpm -f packaging/nfpm-agent.yaml -t dist/) || true
fi

# Symlink friendly names for cloud-init fallbacks
ln -sfn "$ROOT/dist/orion-belt-server" "$ROOT/dist/orion-belt-server" 2>/dev/null || true
ln -sfn "$ROOT/dist/orion-belt-agent" "$ROOT/dist/orion-belt-agent" 2>/dev/null || true

# Start static package server on host (reachable as 10.0.2.2 from guests)
if [[ -f "$RUN/pkg-server.pid" ]] && kill -0 "$(cat "$RUN/pkg-server.pid")" 2>/dev/null; then
  echo "Package server already running (pid $(cat "$RUN/pkg-server.pid"))"
else
  python3 -m http.server "$PKG_PORT" --directory "$ROOT/dist" >"$RUN/logs/pkg-server.log" 2>&1 &
  echo $! > "$RUN/pkg-server.pid"
  echo "Package HTTP server on :$PKG_PORT (pid $(cat "$RUN/pkg-server.pid"))"
fi

boot_vm() {
  local name="$1" image="$2" seed="$3" mem="$4" cpus="$5" hostfwd="$6"
  local disk="$RUN/${name}.qcow2"
  local pidfile="$RUN/${name}.pid"
  local log="$RUN/logs/${name}.log"

  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "VM $name already running"
    return
  fi

  # Reuse existing overlay ("as is"); only create on first boot
  if [[ ! -f "$disk" ]]; then
    qemu-img create -f qcow2 -F qcow2 -b "$image" "$disk" 8G >/dev/null
  else
    echo "Reusing existing disk $disk"
  fi

  # shellcheck disable=SC2086
  qemu-system-x86_64 \
    -name "$name" \
    -machine pc,accel=kvm:tcg \
    -cpu max \
    -m "$mem" \
    -smp "$cpus" \
    -drive file="$disk",if=virtio,format=qcow2 \
    -drive file="$seed",if=virtio,format=raw \
    -netdev user,id=net0,${hostfwd} \
    -device virtio-net-pci,netdev=net0 \
    -display none \
    -serial file:"$log" \
    -daemonize \
    -pidfile "$pidfile"

  echo "Started $name (pid $(cat "$pidfile")) — serial log: $log"
}

# ── Server (Ubuntu) ──────────────────────────────────────────
HOSTNAME=orion-server LOGIN=ubuntu DISTRO=ubuntu PACKAGE_FMT=deb \
  render "$LAB/cloud-init/server/user-data.tmpl" "$RUN/server-user-data"
cp "$LAB/cloud-init/server/meta-data" "$RUN/server-meta-data"
make_seed "$RUN/server-user-data" "$RUN/server-meta-data" "$RUN/server-seed.iso"

boot_vm "server" "$IMG/ubuntu.qcow2" "$RUN/server-seed.iso" 1536 2 \
  "hostfwd=tcp::2222-:2222,hostfwd=tcp::8080-:8080,hostfwd=tcp::2200-:22"

# ── Agents ───────────────────────────────────────────────────
agent_idx=1
for distro in alpine opensuse debian rocky; do
  [[ -f "$IMG/${distro}.qcow2" ]] || { echo "skip $distro (no image)"; continue; }
  case "$distro" in
    alpine) login=alpine; fmt=apk ;;
    opensuse) login=opensuse; fmt=rpm ;;
    debian) login=debian; fmt=deb ;;
    rocky) login=rocky; fmt=rpm ;;
  esac
  host="agent-${distro}"
  mgmt=$((2200 + agent_idx))
  HOSTNAME="$host" LOGIN="$login" DISTRO="$distro" PACKAGE_FMT="$fmt" \
    render "$LAB/cloud-init/agent/user-data.tmpl" "$RUN/${host}-user-data"
  HOSTNAME="$host" render "$LAB/cloud-init/agent/meta-data.tmpl" "$RUN/${host}-meta-data"
  make_seed "$RUN/${host}-user-data" "$RUN/${host}-meta-data" "$RUN/${host}-seed.iso"
  boot_vm "$host" "$IMG/${distro}.qcow2" "$RUN/${host}-seed.iso" 768 1 \
    "hostfwd=tcp::${mgmt}-:22"
  agent_idx=$((agent_idx + 1))
done

cat <<EOF

QEMU lab is starting (cloud-init needs 1–3 minutes).

  Server SSH (mgmt):  ssh -i $SSH_KEY -p 2200 ubuntu@127.0.0.1
  Gateway SSH:        localhost:2222
  API:                http://127.0.0.1:8080
  Package mirror:     http://127.0.0.1:${PKG_PORT}/

  Agent SSH:
    Alpine     ssh -i $SSH_KEY -p 2201 alpine@127.0.0.1
    openSUSE   ssh -i $SSH_KEY -p 2202 opensuse@127.0.0.1
    Debian     ssh -i $SSH_KEY -p 2203 debian@127.0.0.1
    Rocky      ssh -i $SSH_KEY -p 2204 rocky@127.0.0.1

  Next: bash $LAB/test-e2e.sh
  Stop: bash $LAB/down.sh
EOF
