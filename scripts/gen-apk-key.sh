#!/usr/bin/env bash
# Generate an Alpine abuild-compatible RSA keypair for signing APKINDEX.
#
# Usage:
#   ./scripts/gen-apk-key.sh              # create if missing, export pubkey
#   ./scripts/gen-apk-key.sh --force      # rotate (new key)
#
# Writes:
#   packaging/keys/orion-belt.rsa.pub     (safe to commit)
#   .local/apk-keys/orion-belt.rsa       (NEVER commit; gitignored via .local/)
#
# Upload the private key to GitHub as secret APK_RSA_PRIVATE_KEY:
#   gh secret set APK_RSA_PRIVATE_KEY -R orion-belt-dev/orion-belt \
#     < .local/apk-keys/orion-belt.rsa
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEYS_PUB="$ROOT/packaging/keys"
KEYS_PRIV="${ORION_APK_KEY_DIR:-$ROOT/.local/apk-keys}"
PRIV="$KEYS_PRIV/orion-belt.rsa"
PUB="$KEYS_PUB/orion-belt.rsa.pub"
FORCE=0

for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    -h|--help)
      sed -n '2,16p' "$0"
      exit 0
      ;;
    *)
      echo "unknown arg: $arg" >&2
      exit 1
      ;;
  esac
done

mkdir -p "$KEYS_PUB" "$KEYS_PRIV"
chmod 700 "$KEYS_PRIV"

if [[ -f "$PRIV" && "$FORCE" -ne 1 ]]; then
  echo "private key already exists: $PRIV (use --force to rotate)"
else
  umask 077
  openssl genrsa -out "$PRIV" 4096
  chmod 600 "$PRIV"
  echo "wrote $PRIV"
fi

openssl rsa -in "$PRIV" -pubout -out "$PUB"
chmod 644 "$PUB"
echo "wrote $PUB"

cat <<EOF

Next:
  export ORION_APK_PRIVKEY=$PRIV
  export ORION_APK_PUBKEY=$PUB
  ORION_REQUIRE_SIGN=1 make repos

CI (once per org/key):
  gh secret set APK_RSA_PRIVATE_KEY -R orion-belt-dev/orion-belt < $PRIV

Commit only the public key ($PUB). Keep $PRIV out of git.
EOF
