#!/usr/bin/env bash
# Build the docs site (branch orion-belt-docs) and publish it to
#   orion-belt-dev/orion-belt  →  branch gh-pages  →  https://orion-belt.dev
# Fallback for when the Deploy site workflow cannot run.
#
# Usage:
#   ./scripts/deploy-gh-pages.sh
#
# Requirements:
#   - Node >= 20
#   - git write access to orion-belt-dev/orion-belt (gh-pages)
set -euo pipefail
cd "$(dirname "$0")/.."

ORG_REPO="${ORION_PAGES_REPO:-orion-belt-dev/orion-belt}"
PAGES_BRANCH="${ORION_PAGES_BRANCH:-gh-pages}"
BUILD_DIR="${ORION_BUILD_DIR:-build}"

if ! command -v node >/dev/null; then
  echo "node required" >&2
  exit 1
fi

NODE_MAJOR="$(node -p 'process.versions.node.split(".")[0]')"
if [ "$NODE_MAJOR" -lt 20 ]; then
  echo "Node >= 20 required (current: $(node -v)). Try: nvm use 20" >&2
  exit 1
fi

echo "==> npm ci / install"
if [ -f package-lock.json ]; then
  npm ci
else
  npm install
fi

echo "==> building site"
npm run build

echo "==> publishing $BUILD_DIR → git@$ORG_REPO:$PAGES_BRANCH"
# USE_SSH=true uses SSH; GIT_USER is for HTTPS token auth via Docusaurus deploy
export USE_SSH="${USE_SSH:-true}"
# Point docusaurus deploy at the public product repo (already set in docusaurus.config.js)
npx docusaurus deploy

echo "==> done. Site: https://orion-belt.dev"
