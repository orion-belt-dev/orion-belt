#!/usr/bin/env bash
# Serve dist/ over HTTP for local Add-agent / QEMU installs.
#
# Usage:
#   make packages && make serve-packages
#   ORION_PKG_PORT=9000 make serve-packages
#
# Then set Package base URL to http://localhost:8765 (or your host IP).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ORION_DIST:-$ROOT/dist}"
PORT="${ORION_PKG_PORT:-8765}"
BUILD="${ORION_PKG_BUILD:-0}"

if [[ ! -d "$DIST" ]] || [[ -z "$(ls -A "$DIST" 2>/dev/null || true)" ]]; then
  if [[ "$BUILD" == "1" ]]; then
    echo "==> dist/ empty — running make packages"
    (cd "$ROOT" && make packages)
  else
    echo "No packages in $DIST — run: make packages" >&2
    echo "Or: ORION_PKG_BUILD=1 make serve-packages" >&2
    exit 1
  fi
fi

if ! command -v python3 >/dev/null; then
  echo "python3 required to serve packages" >&2
  exit 1
fi

if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "Port $PORT already in use — stop the other server or set ORION_PKG_PORT" >&2
  lsof -nP -iTCP:"$PORT" -sTCP:LISTEN || true
  exit 1
fi

BIND="${ORION_PKG_BIND:-0.0.0.0}"
echo "Serving $DIST at http://${BIND}:$PORT/"
echo "Add agent → Package base URL: http://127.0.0.1:$PORT  (or http://<host-ip>:$PORT)"
echo "Ctrl-C to stop."
cd "$DIST"
exec python3 -m http.server "$PORT" --bind "$BIND"
