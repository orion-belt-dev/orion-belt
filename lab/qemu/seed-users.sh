#!/usr/bin/env bash
# Seed demo users with different roles / machine grants for the QEMU lab.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LAB_QEMU="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$LAB_QEMU/lib.sh"

API="${ORION_API:-http://127.0.0.1:8080}"
CRED_DIR="${ORION_ADMIN_KEY_DIR:-$ROOT/lab/credentials}"
ADMIN_USER="${ORION_ADMIN_USER:-admin}"
ADMIN_KEY="${ORION_ADMIN_KEY_PATH:-$CRED_DIR/admin_ed25519}"

need curl
need ssh-keygen
need python3
mkdir -p "$CRED_DIR"
wait_api 180

http_json() {
  # http_json METHOD URL DATA [TOKEN]
  local method="$1" url="$2" data="$3" token="${4:-}"
  local tmp code
  tmp="$(mktemp)"
  if [[ -n "$token" ]]; then
    code="$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" "$url" \
      -H 'Content-Type: application/json' \
      -H "X-API-Key: $token" \
      -d "$data" || true)"
  else
    code="$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" "$url" \
      -H 'Content-Type: application/json' -d "$data" || true)"
  fi
  HTTP_CODE="$code"
  HTTP_BODY_FILE="$tmp"
}

ensure_key() {
  local path="$1" comment="$2"
  if [[ ! -f "$path" ]]; then
    ssh-keygen -t ed25519 -f "$path" -N "" -C "$comment" >/dev/null
  fi
}

register_user() {
  local user="$1" email="$2" keypath="$3" is_admin="$4"
  ensure_key "$keypath" "${user}@orion-lab"
  local pub payload
  pub="$(tr -d '\n' <"${keypath}.pub")"
  payload="$(ORION_U="$user" ORION_E="$email" ORION_P="$pub" ORION_A="$is_admin" python3 - <<'PY'
import json, os
print(json.dumps({
  "username": os.environ["ORION_U"],
  "email": os.environ["ORION_E"],
  "public_key": os.environ["ORION_P"],
  "is_admin": os.environ["ORION_A"].lower() in ("1", "true", "yes"),
}))
PY
)"
  http_json POST "$API/api/v1/public/register/client" "$payload" "$TOKEN"
  case "$HTTP_CODE" in
    201) echo "  registered $user" ;;
    409) echo "  $user already exists" ;;
    *) echo "  FAIL register $user HTTP $HTTP_CODE: $(cat "$HTTP_BODY_FILE")" >&2
       rm -f "$HTTP_BODY_FILE"; return 1 ;;
  esac
  rm -f "$HTTP_BODY_FILE"
}

admin_login() {
  TOKEN="$(lab_admin_api_key)" || exit 1
  export TOKEN
}

set_role() {
  local user_id="$1" role="$2"
  local payload
  payload="$(python3 -c "import json; print(json.dumps({'role': '$role'}))")"
  http_json PUT "$API/api/v1/admin/users/$user_id" "$payload" "$TOKEN"
  if [[ "$HTTP_CODE" != "200" ]]; then
    echo "  warn: set role=$role HTTP $HTTP_CODE: $(cat "$HTTP_BODY_FILE")" >&2
  else
    echo "  role -> $role"
  fi
  rm -f "$HTTP_BODY_FILE"
}

user_id_by_name() {
  local name="$1"
  curl -sS "$API/api/v1/users" \
    -H "X-API-Key: $TOKEN" \
    | ORION_N="$name" python3 -c 'import json,os,sys
name=os.environ["ORION_N"]
for u in json.load(sys.stdin) or []:
  if u.get("username")==name:
    print(u["id"]); break'
}

machine_id_by_name() {
  local name="$1"
  curl -sS "$API/api/v1/machines" \
    -H "X-API-Key: $TOKEN" \
    | ORION_N="$name" python3 -c 'import json,os,sys
name=os.environ["ORION_N"]
for m in json.load(sys.stdin) or []:
  if m.get("name")==name:
    print(m["id"]); break'
}

grant() {
  local user_id="$1" machine_id="$2" access="$3"
  [[ -n "$user_id" && -n "$machine_id" ]] || { echo "  skip grant (missing ids)"; return 0; }
  local payload
  payload="$(python3 -c "import json; print(json.dumps({'user_id':'$user_id','machine_id':'$machine_id','access_type':'$access','remote_users':['root']}))")"
  http_json POST "$API/api/v1/admin/permissions" "$payload" "$TOKEN"
  case "$HTTP_CODE" in
    201) echo "  granted $access → $machine_id" ;;
    *) echo "  warn: grant HTTP $HTTP_CODE: $(cat "$HTTP_BODY_FILE")" >&2 ;;
  esac
  rm -f "$HTTP_BODY_FILE"
}

echo "==> Seeding demo users (roles + grants)"
admin_login
register_user operator "operator@lab.local" "$CRED_DIR/operator_ed25519" false
register_user auditor  "auditor@lab.local"  "$CRED_DIR/auditor_ed25519"  false
register_user alice    "alice@lab.local"    "$CRED_DIR/alice_ed25519"    false
register_user bob      "bob@lab.local"      "$CRED_DIR/bob_ed25519"      false

OP_ID="$(user_id_by_name operator || true)"
AUD_ID="$(user_id_by_name auditor || true)"
ALICE_ID="$(user_id_by_name alice || true)"
BOB_ID="$(user_id_by_name bob || true)"

[[ -n "${OP_ID:-}" ]] && set_role "$OP_ID" operator
[[ -n "${AUD_ID:-}" ]] && set_role "$AUD_ID" auditor
[[ -n "${ALICE_ID:-}" ]] && set_role "$ALICE_ID" user
[[ -n "${BOB_ID:-}" ]] && set_role "$BOB_ID" user

ALPINE_ID="$(machine_id_by_name agent-alpine || true)"
DEBIAN_ID="$(machine_id_by_name agent-debian || true)"
SUSE_ID="$(machine_id_by_name agent-opensuse || true)"
ROCKY_ID="$(machine_id_by_name agent-rocky || true)"

for mid in "${ALPINE_ID:-}" "${DEBIAN_ID:-}" "${SUSE_ID:-}" "${ROCKY_ID:-}"; do
  [[ -n "$mid" ]] && grant "$OP_ID" "$mid" both
done
[[ -n "${ALPINE_ID:-}" ]] && grant "$ALICE_ID" "$ALPINE_ID" both
[[ -n "${DEBIAN_ID:-}" ]] && grant "$BOB_ID" "$DEBIAN_ID" ssh

SUMMARY="$CRED_DIR/USERS.txt"
cat > "$SUMMARY" <<EOF
Orion Belt lab — demo users
===========================

| User     | Role     | Key                         | Machine access          |
|----------|----------|-----------------------------|-------------------------|
| admin    | admin    | $CRED_DIR/admin_ed25519     | all (admin bypass)      |
| operator | operator | $CRED_DIR/operator_ed25519  | all registered agents   |
| auditor  | auditor  | $CRED_DIR/auditor_ed25519   | none (audit role)       |
| alice    | user     | $CRED_DIR/alice_ed25519     | agent-alpine only       |
| bob      | user     | $CRED_DIR/bob_ed25519       | agent-debian (ssh) only |

UI: http://127.0.0.1:8080/ui  (username + paste matching *.pub)
EOF

echo
cat "$SUMMARY"
