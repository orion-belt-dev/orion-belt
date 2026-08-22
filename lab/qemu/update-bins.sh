#!/usr/bin/env bash
# Rebuild linux/amd64 binaries, push them into the running QEMU lab, reload services.
#
# Usage:
#   ./lab/qemu/update-bins.sh              # server + all agents
#   ./lab/qemu/update-bins.sh server        # server only
#   ./lab/qemu/update-bins.sh alpine debian # named agents only
#   make lab-qemu-update
#   make lab-qemu-update AGENTS="alpine rocky"
#
# Env:
#   SKIP_BUILD=1     reuse existing dist/ binaries
#   GOTOOLCHAIN      default go1.26.6
set -euo pipefail
# shellcheck source=lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

need ssh
need scp
need go

filter_targets=("$@")
want() {
  local name="$1"
  local short="${name#agent-}"
  if [[ "${#filter_targets[@]}" -eq 0 ]]; then
    return 0
  fi
  local t
  for t in "${filter_targets[@]}"; do
    case "$t" in
      "$name"|"$short"|server|ubuntu) return 0 ;;
    esac
    # allow "alpine" to match agent-alpine
    if [[ "$name" == "agent-$t" || "$short" == "$t" ]]; then
      return 0
    fi
  done
  return 1
}

want_server() {
  if [[ "${#filter_targets[@]}" -eq 0 ]]; then
    return 0
  fi
  local t
  for t in "${filter_targets[@]}"; do
    case "$t" in
      server|ubuntu) return 0 ;;
    esac
  done
  return 1
}

guest_root() {
  # stdin → remote sh that escalates with sudo/doas when needed
  agent_ssh "$1" "$2" sh
}

install_as_root_script() {
  local src_name="$1" dest_path="$2"
  cat <<REMOTE
set -e
if [ "\$(id -u)" -eq 0 ]; then
  ASROOT=""
elif command -v sudo >/dev/null 2>&1; then
  ASROOT="sudo"
elif command -v doas >/dev/null 2>&1; then
  ASROOT="doas"
else
  echo "need root, sudo, or doas" >&2
  exit 1
fi
run_root() {
  if [ -z "\$ASROOT" ]; then "\$@"; else \$ASROOT "\$@"; fi
}
trap 'run_root rm -f /tmp/${src_name}' EXIT
run_root install -m 0755 /tmp/${src_name} ${dest_path}
echo "installed ${dest_path}"
REMOTE
}

restart_server_script() {
  cat <<'REMOTE'
set -e
if [ "$(id -u)" -eq 0 ]; then
  ASROOT=""
elif command -v sudo >/dev/null 2>&1; then
  ASROOT="sudo"
elif command -v doas >/dev/null 2>&1; then
  ASROOT="doas"
else
  echo "need root, sudo, or doas" >&2
  exit 1
fi
run_root() {
  if [ -z "$ASROOT" ]; then "$@"; else $ASROOT "$@"; fi
}

run_root mkdir -p /var/log/orion-belt /var/lib/orion-belt/recordings /etc/orion-belt
if [ ! -f /etc/orion-belt/ssh_host_key ]; then
  run_root ssh-keygen -t ed25519 -f /etc/orion-belt/ssh_host_key -N ""
fi
if [ ! -x /usr/bin/orion-belt-server ]; then
  echo "orion-belt-server missing" >&2
  exit 1
fi
if [ ! -f /etc/orion-belt/server.yaml ]; then
  echo "missing /etc/orion-belt/server.yaml" >&2
  exit 1
fi

# Prefer systemd when present
if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files orion-belt-server.service >/dev/null 2>&1; then
  run_root systemctl daemon-reload || true
  run_root systemctl restart orion-belt-server
  sleep 2
  run_root systemctl is-active orion-belt-server && exit 0
fi

run_root pkill -f orion-belt-server 2>/dev/null || true
sleep 1
if [ -z "$ASROOT" ]; then
  nohup /usr/bin/orion-belt-server -c /etc/orion-belt/server.yaml >>/var/log/orion-belt/server.log 2>&1 &
else
  $ASROOT sh -c 'nohup /usr/bin/orion-belt-server -c /etc/orion-belt/server.yaml >>/var/log/orion-belt/server.log 2>&1 &'
fi
sleep 2
if run_root pgrep -f orion-belt-server >/dev/null 2>&1; then
  echo "server process running"
  run_root tail -n 8 /var/log/orion-belt/server.log 2>/dev/null || true
  exit 0
fi
echo "server failed to start; last log:" >&2
run_root tail -n 40 /var/log/orion-belt/server.log 2>/dev/null || true
exit 1
REMOTE
}

scp_to() {
  local port="$1" user="$2" local_path="$3" remote_path="$4"
  # Prefer ControlPath-free, short banner wait so a hung guest fails fast
  scp "${ssh_opts[@]}" -o ConnectTimeout=8 -i "$SSH_KEY" -P "$port" \
    "$local_path" "${user}@127.0.0.1:${remote_path}"
}

ssh_ready() {
  local port="$1" user="$2"
  port_open "$port" || return 1
  agent_ssh "$port" "$user" -o ConnectTimeout=5 true >/dev/null 2>&1
}

failures=0
fail() {
  echo "  warn: $*" >&2
  failures=$((failures + 1))
}

# ── Build ────────────────────────────────────────────────────
mkdir -p "$ROOT/dist" "$RUN_DIR/logs"
if [[ "${SKIP_BUILD:-0}" == "1" ]]; then
  echo "==> SKIP_BUILD=1 — using existing dist/ binaries"
else
  echo "==> Building web UI → web/static (embedded in the server binary)"
  (cd "$ROOT" && make build-ui)
  echo "==> Building linux/amd64 server + agent → dist/"
  (
    cd "$ROOT"
    export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.6}"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/orion-belt-server ./cmd/server
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/orion-belt-agent ./cmd/agent
  )
fi

[[ -x "$ROOT/dist/orion-belt-server" ]] || { echo "missing dist/orion-belt-server" >&2; exit 1; }
[[ -x "$ROOT/dist/orion-belt-agent" ]] || { echo "missing dist/orion-belt-agent" >&2; exit 1; }

# Keep package HTTP mirror fresh for future cloud-init boots
PKG_PORT="${ORION_PKG_PORT:-8765}"
if [[ -f "$RUN_DIR/pkg-server.pid" ]] && kill -0 "$(cat "$RUN_DIR/pkg-server.pid")" 2>/dev/null; then
  echo "Package HTTP server already running (:$PKG_PORT)"
else
  python3 -m http.server "$PKG_PORT" --directory "$ROOT/dist" >"$RUN_DIR/logs/pkg-server.log" 2>&1 &
  echo $! > "$RUN_DIR/pkg-server.pid"
  echo "Package HTTP server on :$PKG_PORT"
fi

# ── Server VM ────────────────────────────────────────────────
if want_server; then
  if ! ssh_ready 2200 ubuntu; then
    fail "server SSH not ready (:2200) — try: make lab-qemu-restart VMS=server"
  else
    echo "==> Updating server binary on orion-server"
    if ! scp_to 2200 ubuntu "$ROOT/dist/orion-belt-server" /tmp/orion-belt-server; then
      fail "scp server binary failed"
    elif ! install_as_root_script orion-belt-server /usr/bin/orion-belt-server | guest_root 2200 ubuntu; then
      fail "install server binary failed"
    else
      echo "==> Reloading orion-belt-server"
      if ! restart_server_script | guest_root 2200 ubuntu; then
        fail "reload server failed"
      else
        wait_api 120 || fail "API not ready after server reload"
      fi
    fi
  fi
fi

# ── Agents ───────────────────────────────────────────────────
updated_agents=()
update_agent() {
  local name="$1" port="$2" user="$3" distro="$4"
  if ! want "$name"; then
    return 0
  fi
  if ! ssh_ready "$port" "$user"; then
    fail "$name SSH not ready (:$port) — try: make lab-qemu-restart VMS=${name#agent-}"
    return 0
  fi
  echo "==> Updating agent binary on $name"
  if ! scp_to "$port" "$user" "$ROOT/dist/orion-belt-agent" /tmp/orion-belt-agent; then
    fail "scp to $name failed"
    return 0
  fi
  if ! install_as_root_script orion-belt-agent /usr/bin/orion-belt-agent | guest_root "$port" "$user"; then
    fail "install on $name failed"
    return 0
  fi
  updated_agents+=("$name")
}

each_agent update_agent

# Restart agents for guests we successfully updated (skip when only server was requested)
only_server=0
if [[ "${#filter_targets[@]}" -eq 1 ]]; then
  case "${filter_targets[0]}" in
    server|ubuntu) only_server=1 ;;
  esac
fi

if [[ "$only_server" -eq 0 && "${#updated_agents[@]}" -gt 0 ]]; then
  echo "==> Reloading agents (${updated_agents[*]})"
  tmp_conf="$(mktemp)"
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" =~ ^# ]] && continue
    IFS='|' read -r name _port _user _distro <<<"$line"
    for u in "${updated_agents[@]}"; do
      if [[ "$name" == "$u" ]]; then
        printf '%s\n' "$line" >> "$tmp_conf"
        break
      fi
    done
  done < "$AGENTS_CONF"
  if [[ -s "$tmp_conf" ]]; then
    ORION_AGENTS_CONF="$tmp_conf" bash "$LAB_QEMU/restart-agents.sh" || fail "agent restart pass had errors"
  fi
  rm -f "$tmp_conf"
elif [[ "$only_server" -eq 0 && "${#filter_targets[@]}" -eq 0 ]]; then
  # Full update but nothing succeeded — still try restart if any agent SSH works later
  :
fi

echo
if [[ "$failures" -gt 0 ]]; then
  echo "QEMU lab update finished with $failures warning(s)."
  echo "  Dead/hung guests: make lab-qemu-restart VMS=\"rocky\"   # then: make lab-qemu-update AGENTS=rocky"
  echo "  Server/API OK guests were updated; only failed targets were skipped."
  exit 1
fi

echo "QEMU lab binaries updated."
echo "  Server:  ./lab/qemu/ssh.sh server -- 'pgrep -af orion-belt-server'"
echo "  Agent:   ./lab/qemu/ssh.sh alpine -- 'pgrep -af orion-belt-agent'"
echo "  API:     $API"
