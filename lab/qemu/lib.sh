#!/usr/bin/env bash
# Shared helpers for QEMU lab agent scripts.
# shellcheck disable=SC2034

LAB_QEMU="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LAB_ROOT="$(cd "$LAB_QEMU/.." && pwd)"
ROOT="$(cd "$LAB_ROOT/.." && pwd)"

API="${ORION_API:-http://127.0.0.1:8080}"
SSH_KEY="${ORION_LAB_SSH_KEY:-$LAB_QEMU/run/lab_id_ed25519}"
AGENTS_CONF="${ORION_AGENTS_CONF:-$LAB_QEMU/agents.conf}"
RUN_DIR="${ORION_LAB_RUN:-$LAB_QEMU/run}"

ssh_opts=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=20 -o LogLevel=ERROR -o BatchMode=yes)

need() { command -v "$1" >/dev/null || { echo "missing dependency: $1" >&2; exit 1; }; }

wait_api() {
  local secs="${1:-180}"
  local deadline=$((SECONDS + secs))
  echo "==> Waiting for API at $API"
  while (( SECONDS < deadline )); do
    if curl -fsS "$API/metrics" >/dev/null 2>&1; then
      echo "API is up"
      return 0
    fi
    sleep 3
  done
  echo "API not reachable at $API" >&2
  return 1
}

# Iterate agents.conf → name port user distro
each_agent() {
  local line name port user distro
  [[ -f "$AGENTS_CONF" ]] || { echo "missing $AGENTS_CONF" >&2; return 1; }
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" =~ ^# ]] && continue
    IFS='|' read -r name port user distro <<<"$line"
    "$@" "$name" "$port" "$user" "$distro"
  done < "$AGENTS_CONF"
}

port_open() {
  local port="$1"
  (echo >/dev/tcp/127.0.0.1/"$port") >/dev/null 2>&1
}

agent_ssh() {
  local port="$1" user="$2"
  shift 2
  ssh "${ssh_opts[@]}" -i "$SSH_KEY" -p "$port" "${user}@127.0.0.1" "$@"
}

# lab_admin_api_key prints a one-day API key for the lab admin. Login needs a
# signature over a server-issued challenge, which curl cannot produce, so this
# goes through osh with the admin's SSH key (bin/osh is built if missing).
lab_admin_api_key() {
  local user="${ORION_ADMIN_USER:-admin}"
  local key="${ORION_ADMIN_KEY_PATH:-${ORION_ADMIN_KEY_DIR:-$LAB_ROOT/credentials}/admin_ed25519}"
  local osh="$ROOT/bin/osh"
  local cfg out

  [[ -f "$key" ]] || { echo "missing admin key $key; run lab/bootstrap-admin.sh first" >&2; return 1; }
  if [[ ! -x "$osh" ]]; then
    echo "==> Building bin/osh" >&2
    (cd "$ROOT" && go build -o bin/osh ./cmd/osh) >&2 || return 1
  fi

  cfg="$(mktemp)"
  cat >"$cfg" <<CFG
server:
  api_endpoint: "$API"
auth:
  user: "$user"
  key_file: "$key"
CFG
  if ! out="$("$osh" -c "$cfg" --json api-keys create "lab-$(date +%Y%m%d%H%M%S)" --expires-in-days 1)"; then
    rm -f "$cfg"
    echo "could not create an API key for $user" >&2
    return 1
  fi
  rm -f "$cfg"
  python3 -c 'import json, sys; print(json.load(sys.stdin)["api_key"])' <<<"$out"
}
