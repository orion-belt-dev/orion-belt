#!/bin/sh
# Orion Belt server package post-install and post-upgrade (deb, rpm, apk,
# Arch): create the orionbelt user and its directories, generate the SSH host
# key, and install the service file for the init system in use (systemd,
# OpenRC, runit or SysV, see /usr/lib/orion-belt/orion-belt-server-service). A fresh
# install is not started: the server needs its database settings first. An
# upgrade restarts a running server.
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

# Without ssh-keygen (e.g. minimal openSUSE, where recommends are skipped) the
# server creates the key itself on first start.
if [ ! -f /etc/orion-belt/ssh_host_key ]; then
  if command -v ssh-keygen >/dev/null 2>&1; then
    ssh-keygen -q -t ed25519 -f /etc/orion-belt/ssh_host_key -N "" -C "orion-belt-host"
    chown orionbelt:orionbelt /etc/orion-belt/ssh_host_key /etc/orion-belt/ssh_host_key.pub 2>/dev/null || true
    chmod 600 /etc/orion-belt/ssh_host_key
  fi
fi

svc=/usr/lib/orion-belt/orion-belt-server-service
"$svc" install || echo "orion-belt: could not install the service file; see $svc" >&2
"$svc" restart-if-running || true

cat <<EOF

Orion Belt server installed.
────────────────────────────────────────
Next steps (setup wizard):

  1. Edit /etc/orion-belt/server.yaml
     - server.public_url          (UI/API origin agents and browsers use)
     - server.public_ssh_host/port (optional; defaults from public_url)
     - database.connection_string
     - auth.jwt_secret

  2. Enable and start the service:
$("$svc" hint 2>/dev/null || true)

  3. orion-belt-server setup
     Creates the first admin, sets the public address, prints agent/user steps.

  4. Open <public_url>/ui  → Setup guide

Docs: https://orion-belt.dev/docs

EOF
exit 0
