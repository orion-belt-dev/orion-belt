#!/usr/bin/env bash
# Orion Belt — one-command quick start.
#
# Takes a fresh checkout to a signed-in web console with one connectable
# machine, so a first run ends in a working demo instead of a setup checklist.
# It generates the secrets, starts the gateway, bootstraps the first admin,
# builds the `osh` client, registers a lab agent, and prints a sign-in link.
#
# Usage:
#   ./scripts/docker-quickstart.sh              # everything (recommended)
#   ./scripts/docker-quickstart.sh --no-agent   # gateway + sign-in only
#   ./scripts/docker-quickstart.sh --down       # stop it all again
#
# Safe to re-run: every step skips itself if it is already done.
#
# This is a local lab, not a deployment. See docs/DEPLOYMENT_HARDENING.md
# before putting Orion Belt on a real network.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

API_PORT="${ORION_API_PORT:-8080}"
SSH_PORT="${ORION_SSH_PORT:-2222}"
AGENT_NAME="${ORION_AGENT_NAME:-lab-1}"
CFG=/etc/orion-belt/config.generated.yaml
GO_IMAGE="${ORION_GO_IMAGE:-golang:1.26.5-alpine}"

WITH_AGENT=1
for arg in "$@"; do
  case "$arg" in
    --no-agent|--no-demo-agent) WITH_AGENT=0 ;;
    --down|--stop)
      echo "-> stopping Orion Belt"
      docker compose -f docker-compose.server.yml --env-file .env.server \
        --profile demo down
      echo "Stopped. Data volumes were kept; add --volumes to remove them."
      exit 0
      ;;
    -h|--help) sed -n '2,17p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "Unknown option: $arg (try --help)" >&2; exit 2 ;;
  esac
done

COMPOSE=(docker compose -f docker-compose.server.yml --env-file .env.server)
TOTAL=6
[ "$WITH_AGENT" -eq 1 ] && TOTAL=7
STEP=0
step() { STEP=$((STEP + 1)); printf '\n[%d/%d] %s\n' "$STEP" "$TOTAL" "$1"; }
have() { command -v "$1" >/dev/null 2>&1; }

echo "== Orion Belt quick start =="

# ---------------------------------------------------------------- preflight
have docker || { echo "Docker is required: https://docs.docker.com/get-docker/" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || {
  echo "Docker Compose v2 is required (try: docker compose version)" >&2; exit 1; }
docker info >/dev/null 2>&1 || {
  echo "Docker is installed but not running — start Docker Desktop (or dockerd) and re-run." >&2; exit 1; }
for tool in openssl ssh-keygen curl; do
  have "$tool" || { echo "Missing required tool: $tool" >&2; exit 1; }
done

# ------------------------------------------------------------------ secrets
step "Secrets"
if [ ! -f .env.server ]; then
  {
    echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)"
    echo "ORION_JWT_SECRET=$(openssl rand -hex 32)"
  } > .env.server
  chmod 600 .env.server
  echo "   generated .env.server"
else
  echo "   .env.server already exists — reusing it"
fi

# ---------------------------------------------------------------- key pairs
# Generated before `compose up` because the agent service bind-mounts
# ./agent-key: if the file is missing, Docker would create a directory there.
step "Keys"
if [ ! -f admin-key ]; then
  ssh-keygen -t ed25519 -f admin-key -N "" -C "orion-belt-admin" -q
  echo "   generated admin-key (your sign-in identity)"
else
  echo "   admin-key already exists — reusing it"
fi
if [ "$WITH_AGENT" -eq 1 ] && [ ! -f agent-key ]; then
  ssh-keygen -t ed25519 -f agent-key -N "" -C "orion-belt-agent-$AGENT_NAME" -q
  chmod 600 agent-key
  echo "   generated agent-key (identity of machine '$AGENT_NAME')"
fi

# ------------------------------------------------------------------ gateway
step "Gateway (first run builds the image — a few minutes)"

# sanity check — the project name is used to find containers to stop and remove
PROJECT="${COMPOSE_PROJECT_NAME:-$(basename "$PWD" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-')}"
project_containers() {
  docker ps -aq --filter "label=com.docker.compose.project=$PROJECT" 2>/dev/null
}

existing=$(project_containers || true)
if [ -n "$existing" ]; then
  echo "   an Orion Belt lab already exists here:"
  docker ps -a --filter "label=com.docker.compose.project=$PROJECT" \
    --format '{{.Names}}  ({{.Status}})' 2>/dev/null | sed 's/^/     /' || true
  recreate=1
  if [ -t 0 ]; then
    printf '\n   Stop and recreate its containers? Your data is kept. [Y/n] '
    read -r reply
    case "$reply" in [Nn]*) recreate=0 ;; esac
  else
    echo "   (not a terminal — recreating automatically)"
  fi
  if [ "$recreate" -eq 1 ]; then
    "${COMPOSE[@]}" --profile demo down --remove-orphans >/dev/null 2>&1 || true
    leftover=$(project_containers || true)
    if [ -n "$leftover" ]; then
      # What `down` leaves behind: a container renamed mid-recreate still holds
      # the name compose wants, so remove it by ID.
      # shellcheck disable=SC2086
      docker rm -f $leftover >/dev/null 2>&1 || true
    fi
    echo "   removed — starting fresh containers"
  else
    echo "   leaving it as it is (a name clash may still stop the rebuild)"
  fi
fi

# Check the ports before compose does, so a clash produces advice instead of a
# raw "port is already allocated". Skipped once our own gateway is up, since
# then the ports are supposed to be in use — by us.
port_busy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
if [ -z "$("${COMPOSE[@]}" ps -q server 2>/dev/null)" ]; then
  clash=""
  port_busy "$API_PORT" && clash="$clash $API_PORT"
  port_busy "$SSH_PORT" && clash="$clash $SSH_PORT"
  if [ -n "$clash" ]; then
    echo "   ! something else is already listening on:$clash" >&2
    echo "     Re-run on free ports, for example:" >&2
    echo "       ORION_API_PORT=18080 ORION_SSH_PORT=12222 $0" >&2
    exit 1
  fi
fi

"${COMPOSE[@]}" up -d --build

printf '   waiting for the gateway to answer'
for _ in $(seq 1 60); do
  if curl -sf "http://localhost:${API_PORT}/health" >/dev/null 2>&1; then
    printf ' ok\n'; break
  fi
  printf '.'; sleep 2
done
if ! curl -sf "http://localhost:${API_PORT}/health" >/dev/null 2>&1; then
  printf '\n'
  echo "The gateway did not come up. Logs:" >&2
  echo "  ${COMPOSE[*]} logs server" >&2
  echo "If port ${API_PORT} or ${SSH_PORT} is already in use, re-run with" >&2
  echo "  ORION_API_PORT=18080 ORION_SSH_PORT=12222 $0" >&2
  exit 1
fi

# --------------------------------------------------------------- admin user
# `setup` detects an existing admin and no-ops, so this is safe every run.
step "Admin user"
"${COMPOSE[@]}" exec -T \
  -e ORION_SETUP_ADMIN_NAME=admin \
  -e ORION_SETUP_ADMIN_EMAIL=admin@localhost \
  -e ORION_SETUP_ADMIN_KEY="$(cat admin-key.pub)" \
  server /app/orion-belt-server -c "$CFG" setup < /dev/null >/dev/null
echo "   admin ready (authenticates with admin-key)"

# ------------------------------------------------------------- osh client
# Signing in to the console goes through osh: it proves possession of
# admin-key and hands the browser a one-time code. A browser cannot sign a
# challenge with a key file, which is why there is no key field in the UI.
step "Sign-in client (bin/osh)"
if [ -x bin/osh ]; then
  echo "   bin/osh already built — reusing it"
elif have go; then
  make build-client >/dev/null
  echo "   built bin/osh with your Go toolchain"
else
  # No Go on the host: cross-build for this OS/arch inside the same Go image
  # the gateway was built with. Pure Go, so this needs no C toolchain.
  case "$(uname -s)" in
    Linux) goos=linux ;;
    Darwin) goos=darwin ;;
    *) goos="" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) goarch=amd64 ;;
    arm64 | aarch64) goarch=arm64 ;;
    *) goarch="" ;;
  esac
  if [ -z "$goos" ] || [ -z "$goarch" ]; then
    echo "   ! cannot auto-build osh for $(uname -s)/$(uname -m)" >&2
    echo "     Install Go and run: make build-client" >&2
  else
    echo "   no Go toolchain — building osh in Docker ($goos/$goarch)"
    mkdir -p bin
    docker run --rm \
      -v "$PWD":/src -w /src \
      --user "$(id -u):$(id -g)" \
      -e HOME=/tmp -e GOPATH=/tmp/gopath \
      -e GOCACHE=/tmp/gocache -e GOMODCACHE=/tmp/gomod \
      -e GOOS="$goos" -e GOARCH="$goarch" -e CGO_ENABLED=0 \
      "$GO_IMAGE" go build -o bin/osh ./cmd/osh
    echo "   built bin/osh"
  fi
fi

# ------------------------------------------------------------- client config
# Written here so osh never has to run its interactive setup wizard.
if [ ! -f client.yaml ]; then
  cat > client.yaml <<EOF
# Orion Belt client config for this lab (generated by docker-quickstart.sh).
# Use it with: ./bin/osh -c client.yaml ...
server:
  host: "localhost"
  port: ${SSH_PORT}
  api_endpoint: "http://localhost:${API_PORT}"
auth:
  user: "admin"
  key_file: "${PWD}/admin-key"
  known_hosts: "${PWD}/.orion-known-hosts"
  strict_host_key_checking: "ask"
EOF
  chmod 600 client.yaml
fi

# ------------------------------------------------------------- demo machine
if [ "$WITH_AGENT" -eq 1 ]; then
  step "Demo machine '$AGENT_NAME'"
  if "${COMPOSE[@]}" exec -T server /app/orion-belt-server -c "$CFG" \
        agent list 2>/dev/null | grep -qw "$AGENT_NAME"; then
    echo "   '$AGENT_NAME' is already registered"
  else
    "${COMPOSE[@]}" exec -T server /app/orion-belt-server -c "$CFG" \
      agent register --name "$AGENT_NAME" --key "$(cat agent-key.pub)" \
      >/dev/null
    "${COMPOSE[@]}" exec -T server /app/orion-belt-server -c "$CFG" \
      permission grant --user admin --machine "$AGENT_NAME" \
      --type both --remote-users root >/dev/null
    echo "   registered '$AGENT_NAME' and granted admin access to it"
  fi
  "${COMPOSE[@]}" --profile demo up -d --build agent
  echo "   agent container started (dials out to the gateway)"
fi

# ------------------------------------------------------------- sign-in link
step "Sign-in link"
LINK=""
if [ -x bin/osh ]; then
  # --code prints the one-time code and URL instead of opening a browser,
  # so the link can be shown in this summary either way.
  if OUT=$(./bin/osh -c client.yaml login --code 2>&1); then
    LINK=$(printf '%s\n' "$OUT" | awk '/^URL:/ {print $2}')
  fi
  if [ -z "$LINK" ]; then
    echo "   ! could not get a sign-in link automatically:" >&2
    printf '%s\n' "$OUT" | sed 's/^/     /' >&2
  fi
fi

cat <<EOF

╔══════════════════════════════════════════════════════════════════╗
║  Orion Belt is ready                                             ║
╚══════════════════════════════════════════════════════════════════╝
EOF

if [ -n "$LINK" ]; then
  cat <<EOF

  1. Sign in — open this link (single use, expires in 5 minutes):

       $LINK

     You will be asked to create a password and scan a TOTP QR code.
     That becomes your normal sign-in from then on.
EOF
  # Best effort: most people would rather it just opened.
  if have xdg-open; then (xdg-open "$LINK" >/dev/null 2>&1 &) || true
  elif have open; then (open "$LINK" >/dev/null 2>&1 &) || true
  fi
else
  cat <<EOF

  1. Sign in — get a fresh link any time with:

       ./bin/osh -c client.yaml login          # opens your browser
       ./bin/osh -c client.yaml login --code   # prints a link instead
EOF
fi

if [ "$WITH_AGENT" -eq 1 ]; then
  cat <<EOF

  2. Machine "$AGENT_NAME" is already connected. Open **Machines**, click its
     web terminal, and run a few commands.

  3. Open **Sessions** and press Playback to replay what you just did.
EOF
else
  cat <<EOF

  2. Add a machine: **Add agent** in the console, then see
     docker-compose.agent.yml to run the agent on it.
EOF
fi

cat <<EOF

  Console:  http://localhost:${API_PORT}/ui
  Stop:     $0 --down
  Guide:    docs/TRY_IN_10_MINUTES.md

  admin-key is your private key. Keep it — the CLI and API authenticate
  with it too. It is git-ignored, along with agent-key and client.yaml.

EOF
