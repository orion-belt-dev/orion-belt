#!/bin/sh
set -eu

# Generate a unique SSH host key on first run and persist it in the mounted
# volume. Never bake this into the image — a host key shared across every
# container built from the same image defeats host-key verification entirely.
HOST_KEY="${ORION_SSH_HOST_KEY:-/etc/orion-belt/ssh_host_key}"
if [ ! -f "$HOST_KEY" ]; then
  echo "[init] generating SSH host key at $HOST_KEY"
  mkdir -p "$(dirname "$HOST_KEY")"
  ssh-keygen -t ed25519 -f "$HOST_KEY" -N "" -q
fi

# Advanced use: mount your own full config.yaml and point ORION_CONFIG_FILE at
# it to skip templating entirely.
if [ -n "${ORION_CONFIG_FILE:-}" ]; then
  exec /app/orion-belt-server -c "$ORION_CONFIG_FILE"
fi

: "${ORION_JWT_SECRET:?ORION_JWT_SECRET is required — generate one with: openssl rand -hex 32}"
: "${POSTGRES_PASSWORD:?POSTGRES_PASSWORD is required}"

DB_HOST="${POSTGRES_HOST:-postgres}"
DB_USER="${POSTGRES_USER:-orionbelt}"
DB_NAME="${POSTGRES_DB:-orionbelt}"
CFG=/etc/orion-belt/config.generated.yaml

# Advertised addresses: what browsers and agents use to reach this gateway.
# Defaults keep local-lab behaviour; production compose requires them set.
PUBLIC_URL="${ORION_PUBLIC_URL:-${ORION_PUBLIC_ORIGIN:-http://localhost:8080}}"
PUBLIC_HOST="${ORION_PUBLIC_HOST:-localhost}"
# If only PUBLIC_URL is set, derive the host for WebAuthn / SSH advertise.
case "$PUBLIC_HOST" in
  localhost|"")
    _host=$(printf '%s' "$PUBLIC_URL" | sed -E 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||; s|[:/].*$||')
    [ -n "$_host" ] && PUBLIC_HOST="$_host"
    ;;
esac
PUBLIC_SSH_HOST="${ORION_PUBLIC_SSH_HOST:-$PUBLIC_HOST}"
PUBLIC_SSH_PORT="${ORION_PUBLIC_SSH_PORT:-2222}"

cat > "$CFG" <<EOF
server:
  host: "0.0.0.0"
  port: 2222
  api_port: 8080
  ssh_host_key: "${HOST_KEY}"
  public_url: "${PUBLIC_URL}"
  public_ssh_host: "${PUBLIC_SSH_HOST}"
  public_ssh_port: ${PUBLIC_SSH_PORT}
  metrics_enabled: true

database:
  driver: "postgres"
  connection_string: "postgres://${DB_USER}:${POSTGRES_PASSWORD}@${DB_HOST}:5432/${DB_NAME}?sslmode=disable"

auth:
  rebac_enabled: true
  allow_temp_access: true
  jwt_secret: "${ORION_JWT_SECRET}"
  jwt_expiry_hours: ${ORION_JWT_EXPIRY_HOURS:-24}
  mfa_required: ${ORION_MFA_REQUIRED:-false}
  rate_limit_per_minute: ${ORION_RATE_LIMIT_PER_MINUTE:-600}
  webauthn:
    enabled: ${ORION_WEBAUTHN_ENABLED:-true}
    rp_display_name: "${ORION_WEBAUTHN_RP_NAME:-Orion Belt}"
    rp_id: "${PUBLIC_HOST}"
    origins:
      - "${PUBLIC_URL}"

recording:
  enabled: true
  storage_path: "/var/lib/orion-belt/recordings"
  retention_days: ${ORION_RECORDING_RETENTION_DAYS:-90}
  encryption_key: "${ORION_RECORDING_ENCRYPTION_KEY:-}"

plugins: {}
EOF

exec /app/orion-belt-server -c "$CFG"
