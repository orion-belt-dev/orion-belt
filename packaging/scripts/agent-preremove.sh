#!/bin/sh
# Orion Belt agent package pre-remove: stop, disable and remove the service
# on removal. An upgrade keeps it (post-install restarts it). The agent's
# configuration and key are left in place.
#   deb: $1 = remove | upgrade | failed-upgrade | deconfigure
#   rpm: $1 = number of versions left (0 = erase, 1 = upgrade)
#   apk, Arch: run on removal only
case "${1:-remove}" in
upgrade | failed-upgrade | 1) exit 0 ;;
esac
/usr/lib/orion-belt/orion-belt-agent-service teardown >/dev/null 2>&1 || true
exit 0
