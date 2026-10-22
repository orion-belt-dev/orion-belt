#!/bin/sh
# Orion Belt agent package post-install and post-upgrade (deb, rpm, apk,
# Arch): install the service file for the init system in use (systemd,
# OpenRC, runit or SysV, see /usr/lib/orion-belt/orion-belt-agent-service). A fresh
# install is not started: the agent needs its gateway settings and key first
# (the console's install script writes them and runs "setup"). An upgrade
# restarts a running agent.
set -e
mkdir -p /etc/orion-belt
chmod 750 /etc/orion-belt

svc=/usr/lib/orion-belt/orion-belt-agent-service
"$svc" install || echo "orion-belt: could not install the service file; see $svc" >&2
"$svc" restart-if-running || true

cat <<EOF

Orion Belt agent installed.
────────────────────────────────────────
  1. Edit /etc/orion-belt/agent.yaml (gateway host, port 2222), or use the
     install script from the console (Agents > Add agent), which does it all.
  2. Enable and start the service:
$("$svc" hint 2>/dev/null || true)

EOF
exit 0
