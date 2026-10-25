#!/bin/sh
# Orion Belt server package pre-install (deb, rpm, apk, Arch): create the
# orionbelt user before the files are unpacked, so the package's directories
# can be owned by it. apk applies directory ownership from the package after
# post-install, so a chown there alone did not last on Alpine.
set -e

if ! getent group orionbelt >/dev/null 2>&1; then
  groupadd --system orionbelt || addgroup -S orionbelt 2>/dev/null || true
fi
if ! getent passwd orionbelt >/dev/null 2>&1; then
  useradd --system --gid orionbelt --home-dir /var/lib/orion-belt --shell /usr/sbin/nologin orionbelt \
    || adduser -S -G orionbelt -h /var/lib/orion-belt -s /sbin/nologin orionbelt 2>/dev/null || true
fi
exit 0
