#!/usr/bin/env bash
# Register QEMU agents with the running Orion Belt server via the API.
# Requires pubkeys in lab/qemu/run/<name>.pub (see collect-agent-keys.sh).
# Registration requires an admin or operator credential. Set ORION_API_KEY or
# ORION_SESSION_TOKEN; otherwise a short-lived key is minted for the lab admin
# (lab/credentials/admin_ed25519).
set -euo pipefail
# shellcheck source=lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

need curl
need python3
wait_api "${ORION_WAIT_SECS:-180}"

auth_headers=()
if [[ -n "${ORION_API_KEY:-}" ]]; then
  auth_headers=(-H "X-API-Key: ${ORION_API_KEY}")
elif [[ -n "${ORION_SESSION_TOKEN:-}" ]]; then
  auth_headers=(-H "X-Session-Token: ${ORION_SESSION_TOKEN}")
else
  lab_key="$(lab_admin_api_key)"
  auth_headers=(-H "X-API-Key: ${lab_key}")
fi

register_one() {
  local name="$1" port="$2" user="$3" distro="$4"
  local pubfile="$RUN_DIR/${name}.pub"
  if [[ ! -s "$pubfile" ]]; then
    echo "skip $name (no pubkey at $pubfile — run collect-agent-keys.sh first)"
    return 0
  fi
  local pub
  pub="$(tr -d '\n' <"$pubfile")"
  echo "==> Registering $name (distro=$distro)"
  local payload
  payload="$(
    ORION_A_NAME="$name" ORION_A_HOST="$name" ORION_A_PUB="$pub" ORION_A_DISTRO="$distro" python3 - <<'PY'
import json, os
print(json.dumps({
  "name": os.environ["ORION_A_NAME"],
  "hostname": os.environ["ORION_A_HOST"],
  "port": 22,
  "public_key": os.environ["ORION_A_PUB"],
  "tags": {
    "environment": "qemu-lab",
    "distro": os.environ["ORION_A_DISTRO"],
    "role": "agent",
  },
}))
PY
  )"
  local tmp http_code
  tmp="$(mktemp)"
  http_code="$(curl -sS -o "$tmp" -w '%{http_code}' -X POST "$API/api/v1/public/register/agent" \
    -H 'Content-Type: application/json' "${auth_headers[@]}" -d "$payload" || true)"
  case "$http_code" in
    201)
      echo "  registered OK"
      cat "$tmp"; echo
      ;;
    409)
      echo "  already registered (OK)"
      ;;
    *)
      echo "  FAILED HTTP $http_code: $(cat "$tmp")" >&2
      ;;
  esac
  rm -f "$tmp"
}

each_agent register_one
echo "Registration pass complete."
