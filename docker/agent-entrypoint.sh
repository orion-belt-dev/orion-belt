#!/bin/sh
set -eu

: "${ORION_SERVER_HOST:?ORION_SERVER_HOST is required (hostname/IP of the orion-belt server)}"
: "${ORION_AGENT_NAME:?ORION_AGENT_NAME is required and must match the machine name registered on the server}"
ORION_SERVER_PORT="${ORION_SERVER_PORT:-2222}"
KEY_FILE="${ORION_KEY_FILE:-/etc/orion-belt/agent_key}"

if [ ! -f "$KEY_FILE" ]; then
  cat >&2 <<EOF
[init] no agent private key found at $KEY_FILE

This agent must be registered on the server before it can connect:
  1. In the web console, go to "Add agent" (or POST /api/v1/admin/agents/install-script)
     and register an agent named "${ORION_AGENT_NAME}".
  2. Save the returned private key to ./agent-key on the host running this
     container (it's bind-mounted to $KEY_FILE — see docker-compose.agent.yml).
  3. Restart this container.
EOF
  exit 1
fi
chmod 600 "$KEY_FILE" 2>/dev/null || true

# ORION_KUBERNETES=true in a pod makes this the cluster's in-cluster agent,
# using the pod's service account (see deploy/kubernetes).
KUBERNETES_BLOCK=""
if [ "${ORION_KUBERNETES:-false}" = "true" ]; then
  KUBERNETES_BLOCK="  kubernetes:
    enabled: true"
fi

CFG=/etc/orion-belt/agent.yaml
cat > "$CFG" <<EOF
server:
  host: "${ORION_SERVER_HOST}"
  port: ${ORION_SERVER_PORT}

agent:
  name: "${ORION_AGENT_NAME}"
  tags:
    environment: "${ORION_AGENT_ENV:-production}"
${KUBERNETES_BLOCK}

auth:
  key_file: "${KEY_FILE}"
  known_hosts: "/etc/orion-belt/known_hosts"
  strict_host_key_checking: "${ORION_STRICT_HOST_KEY_CHECKING:-ask}"
EOF

exec /app/orion-belt-agent -c "$CFG"
