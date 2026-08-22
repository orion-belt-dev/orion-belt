#!/usr/bin/env bash
# Build deb/rpm/apk packages into dist/ using nfpm (preferred) or goreleaser snapshot.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)}"
# nFPM expects a semver-ish version (no leading v)
VERSION="${VERSION#v}"
VERSION="$(printf '%s' "$VERSION" | tr -c 'A-Za-z0-9._-' '-')"
export VERSION
mkdir -p dist
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.6}"

echo "==> Building linux/amd64 binaries"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o dist/orion-belt-server ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o dist/orion-belt-agent ./cmd/agent
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/osh ./cmd/osh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/ocp ./cmd/ocp
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w" -o dist/oadmin ./cmd/oadmin

if command -v goreleaser >/dev/null; then
  echo "==> GoReleaser snapshot"
  goreleaser release --snapshot --clean --skip=publish
  echo "Artifacts in dist/"
  exit 0
fi

if ! command -v nfpm >/dev/null; then
  echo "Installing nfpm..."
  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
  export PATH="$(go env GOPATH)/bin:$PATH"
fi

# nfpm expands env in version field when using ${VERSION}
for fmt in deb rpm apk; do
  echo "==> Packaging $fmt"
  VERSION="$VERSION" nfpm package -f packaging/nfpm-server.yaml -p "$fmt" -t dist/
  VERSION="$VERSION" nfpm package -f packaging/nfpm-agent.yaml -p "$fmt" -t dist/
  VERSION="$VERSION" nfpm package -f packaging/nfpm-tools.yaml -p "$fmt" -t dist/
done

echo "Packages written to dist/:"
ls -la dist/*.{deb,rpm,apk} 2>/dev/null || ls -la dist/
