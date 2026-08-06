#!/bin/sh
set -e

if ! getent group orionbelt >/dev/null 2>&1; then
  groupadd --system orionbelt || addgroup -S orionbelt 2>/dev/null || true
fi
if ! getent passwd orionbelt >/dev/null 2>&1; then
  useradd --system --gid orionbelt --home-dir /var/lib/orion-belt --shell /usr/sbin/nologin orionbelt \
    || adduser -S -G orionbelt -h /var/lib/orion-belt -s /sbin/nologin orionbelt 2>/dev/null || true
fi

mkdir -p /var/lib/orion-belt/recordings /var/log/orion-belt /etc/orion-belt
# The service runs as orionbelt with ProtectSystem=strict and ReadWritePaths
# covering these three trees — ownership must match or startup/setup fail.
chown -R orionbelt:orionbelt /var/lib/orion-belt /var/log/orion-belt /etc/orion-belt 2>/dev/null || true
chmod 750 /var/lib/orion-belt /var/log/orion-belt /etc/orion-belt 2>/dev/null || true
# server.yaml may have been unpacked as root:root 644 from the package; tighten.
if [ -f /etc/orion-belt/server.yaml ]; then
  chown orionbelt:orionbelt /etc/orion-belt/server.yaml 2>/dev/null || true
  chmod 640 /etc/orion-belt/server.yaml 2>/dev/null || true
fi

if [ ! -f /etc/orion-belt/ssh_host_key ]; then
  if command -v ssh-keygen >/dev/null 2>&1; then
    ssh-keygen -t ed25519 -f /etc/orion-belt/ssh_host_key -N "" -C "orion-belt-host"
    chown orionbelt:orionbelt /etc/orion-belt/ssh_host_key /etc/orion-belt/ssh_host_key.pub 2>/dev/null || true
    chmod 600 /etc/orion-belt/ssh_host_key
  fi
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi

cat <<'EOF'

Orion Belt server installed.
────────────────────────────────────────
Next steps (setup wizard):

  1. Edit /etc/orion-belt/server.yaml
     - server.public_url          (UI/API origin agents and browsers use)
     - server.public_ssh_host/port (optional; defaults from public_url)
     - database.connection_string
     - auth.jwt_secret

  2. systemctl enable --now orion-belt-server
     # Alpine / OpenRC: rc-update add orion-belt-server default && rc-service orion-belt-server start

  3. orion-belt-server setup
     Creates the first admin, sets the public address, prints agent/user steps.

  4. Open <public_url>/ui  → Setup guide

  Or one-shot from releases:
    curl -fsSL https://raw.githubusercontent.com/orion-belt-dev/orion-belt/master/scripts/install-server.sh | sudo bash

Docs: https://github.com/orion-belt-dev/orion-belt/blob/master/docs/SETUP.md

EOF
