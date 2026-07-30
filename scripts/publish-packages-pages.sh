#!/usr/bin/env bash
# Build a flat GitHub Pages tree for Add-agent package downloads.
#
# Layout matches install-script filenames:
#   orion-belt-agent_${VERSION}_amd64.deb
#   orion-belt-agent-${VERSION}-1.x86_64.rpm
#   orion-belt-agent_${VERSION}_x86_64.apk
#   orion-belt-agent   (linux/amd64 binary fallback)
# plus GoReleaser originals (*_linux_amd64.*) when present.
#
# Sources (first match wins):
#   1. ORION_DIST / dist/  (after make packages / goreleaser)
#   2. GitHub Release assets for ORION_PKG_TAG (default: latest tag)
#
# Usage:
#   make packages && ./scripts/publish-packages-pages.sh
#   ORION_PKG_TAG=v1.0.0 ./scripts/publish-packages-pages.sh --from-release
#   ./scripts/publish-packages-pages.sh --push   # commit+push to orion-belt-dev/packages gh-pages
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ORION_DIST:-$ROOT/dist}"
OUT="${ORION_PACKAGES_OUT:-$ROOT/packages-site}"
REPO_SLUG="${ORION_PACKAGES_REPO:-orion-belt-dev/packages}"
PAGES_URL="${ORION_PACKAGES_URL:-https://orion-belt-dev.github.io/packages}"
FROM_RELEASE=0
PUSH=0

for arg in "$@"; do
  case "$arg" in
    --from-release) FROM_RELEASE=1 ;;
    --push) PUSH=1 ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
  esac
done

die() { echo "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "missing: $1"; }

need curl
need python3

resolve_tag() {
  if [[ -n "${ORION_PKG_TAG:-}" ]]; then
    echo "${ORION_PKG_TAG}"
    return
  fi
  local tag
  tag="$(git -C "$ROOT" describe --tags --abbrev=0 2>/dev/null || true)"
  if [[ -n "$tag" ]]; then
    echo "$tag"
    return
  fi
  curl -fsSL "https://api.github.com/repos/orion-belt-dev/orion-belt/releases/latest" \
    | python3 -c 'import sys,json; print(json.load(sys.stdin)["tag_name"])'
}

TAG="$(resolve_tag)"
VER="${TAG#v}"
RELEASE_BASE="https://github.com/orion-belt-dev/orion-belt/releases/download/${TAG}"

echo "==> Building packages Pages tree → $OUT (version $VER)"
rm -rf "$OUT"
mkdir -p "$OUT"

download() {
  local url="$1" dest="$2"
  echo "  fetch $url"
  curl -fsSL -o "$dest" "$url"
}

# Prefer binaries extracted from arch-correct .deb over release tarballs
# (tarball naming has been wrong in at least one release).
extract_agent_from_deb() {
  local deb="$1" dest="$2"
  local tmp
  tmp="$(mktemp -d)"
  (cd "$tmp" && ar x "$deb" && tar xf data.tar.*)
  local bin
  bin="$(find "$tmp" -type f -path '*/usr/bin/orion-belt-agent' | head -1)"
  [[ -n "$bin" ]] || die "no orion-belt-agent inside $deb"
  cp -f "$bin" "$dest"
  chmod 755 "$dest"
  rm -rf "$tmp"
}

copy_or_fetch() {
  local name="$1" url="$2"
  if [[ -f "$DIST/$name" ]]; then
    cp -f "$DIST/$name" "$OUT/$name"
    return 0
  fi
  if [[ "$FROM_RELEASE" == "1" ]] || [[ ! -d "$DIST" ]]; then
    download "$url" "$OUT/$name" || return 1
    return 0
  fi
  return 1
}

# Collect agent packages from dist/ or the release.
shopt -s nullglob
have_local=0
if [[ -d "$DIST" ]]; then
  for f in "$DIST"/orion-belt-agent*.{deb,rpm,apk}; do
    cp -f "$f" "$OUT/$(basename "$f")"
    have_local=1
  done
  if [[ -f "$DIST/orion-belt-agent" ]]; then
    cp -f "$DIST/orion-belt-agent" "$OUT/orion-belt-agent"
    chmod 755 "$OUT/orion-belt-agent"
    have_local=1
  fi
fi

if [[ "$have_local" -eq 0 || "$FROM_RELEASE" == "1" ]]; then
  echo "==> Fetching agent packages from $RELEASE_BASE"
  for arch in amd64 arm64; do
    copy_or_fetch "orion-belt-agent_${VER}_linux_${arch}.deb" \
      "$RELEASE_BASE/orion-belt-agent_${VER}_linux_${arch}.deb" || true
    copy_or_fetch "orion-belt-agent_${VER}_linux_${arch}.rpm" \
      "$RELEASE_BASE/orion-belt-agent_${VER}_linux_${arch}.rpm" || true
    copy_or_fetch "orion-belt-agent_${VER}_linux_${arch}.apk" \
      "$RELEASE_BASE/orion-belt-agent_${VER}_linux_${arch}.apk" || true
  done
fi

# Install-script aliases (nfpm / UI / QEMU cloud-init names).
alias_one() {
  local src="$1" dst="$2"
  [[ -f "$OUT/$src" ]] || return 0
  cp -f "$OUT/$src" "$OUT/$dst"
  echo "  alias $dst ← $src"
}

# deb: drop _linux_
alias_one "orion-belt-agent_${VER}_linux_amd64.deb" "orion-belt-agent_${VER}_amd64.deb"
alias_one "orion-belt-agent_${VER}_linux_arm64.deb" "orion-belt-agent_${VER}_arm64.deb"
# Also accept plain nfpm names already in dist/
if [[ -f "$OUT/orion-belt-agent_${VER}_amd64.deb" && ! -f "$OUT/orion-belt-agent_${VER}_linux_amd64.deb" ]]; then
  : # local nfpm layout — already correct for install script
fi

# apk: goreleaser uses linux_amd64; install script wants x86_64 / aarch64
alias_one "orion-belt-agent_${VER}_linux_amd64.apk" "orion-belt-agent_${VER}_x86_64.apk"
alias_one "orion-belt-agent_${VER}_linux_arm64.apk" "orion-belt-agent_${VER}_aarch64.apk"

# rpm: install script wants classic NEVRA; goreleaser uses _linux_amd64
alias_one "orion-belt-agent_${VER}_linux_amd64.rpm" "orion-belt-agent-${VER}-1.x86_64.rpm"
alias_one "orion-belt-agent_${VER}_linux_arm64.rpm" "orion-belt-agent-${VER}-1.aarch64.rpm"

# Prefer arch-correct binaries from .deb packages
if [[ -f "$OUT/orion-belt-agent_${VER}_amd64.deb" ]]; then
  extract_agent_from_deb "$OUT/orion-belt-agent_${VER}_amd64.deb" "$OUT/orion-belt-agent"
  cp -f "$OUT/orion-belt-agent" "$OUT/orion-belt-agent_linux_amd64"
fi
if [[ -f "$OUT/orion-belt-agent_${VER}_arm64.deb" ]]; then
  extract_agent_from_deb "$OUT/orion-belt-agent_${VER}_arm64.deb" "$OUT/orion-belt-agent_linux_arm64"
elif [[ -f "$OUT/orion-belt-agent_${VER}_linux_arm64.deb" ]]; then
  extract_agent_from_deb "$OUT/orion-belt-agent_${VER}_linux_arm64.deb" "$OUT/orion-belt-agent_linux_arm64"
fi

# If only plain nfpm deb names exist (no _linux_), still extract binary
if [[ ! -f "$OUT/orion-belt-agent" && -f "$OUT/orion-belt-agent_${VER}_amd64.deb" ]]; then
  extract_agent_from_deb "$OUT/orion-belt-agent_${VER}_amd64.deb" "$OUT/orion-belt-agent"
fi

[[ -f "$OUT/orion-belt-agent_${VER}_amd64.deb" || -f "$OUT/orion-belt-agent" ]] \
  || die "No agent packages found — run make packages or pass --from-release"

printf '%s\n' "$VER" >"$OUT/VERSION"
printf '%s\n' "$TAG" >"$OUT/TAG"
touch "$OUT/.nojekyll"

cat >"$OUT/index.html" <<EOF
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>Orion Belt packages</title>
  <style>
    body { font-family: ui-sans-serif, system-ui, sans-serif; max-width: 52rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; color: #111; }
    code { background: #f4f4f5; padding: 0.1em 0.35em; border-radius: 4px; }
    a { color: #0b57d0; }
  </style>
</head>
<body>
  <h1>Orion Belt packages</h1>
  <p>Static package mirror for <strong>Add agent</strong> install scripts.</p>
  <p>Package base URL: <code>${PAGES_URL}</code></p>
  <p>Current release: <a href="VERSION">VERSION</a> / <a href="TAG">TAG</a> (${VER}).</p>
  <p>Filenames match the install script
     (<code>orion-belt-agent_\${VERSION}_amd64.deb</code>, etc.)
     plus GoReleaser originals (<code>*_linux_amd64.*</code>).</p>
  <p>Source: <a href="https://github.com/orion-belt-dev/orion-belt/releases">GitHub Releases</a>.
     Repo: <a href="https://github.com/${REPO_SLUG}">${REPO_SLUG}</a>.
     Local mirror: <code>make serve-packages</code>.</p>
</body>
</html>
EOF

echo "==> Wrote $(find "$OUT" -type f | wc -l | tr -d ' ') files to $OUT"

if [[ "$PUSH" != "1" ]]; then
  echo "Done. To publish: $0 --push"
  echo "Or: git -C <clone of ${REPO_SLUG}> checkout gh-pages && rsync -a --delete $OUT/ . && git commit && git push"
  exit 0
fi

need git
WORKDIR="$(mktemp -d)"
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

echo "==> Pushing to ${REPO_SLUG} (gh-pages)"
git clone --branch gh-pages --single-branch "git@github.com:${REPO_SLUG}.git" "$WORKDIR/repo" \
  || git clone "git@github.com:${REPO_SLUG}.git" "$WORKDIR/repo"

cd "$WORKDIR/repo"
if ! git rev-parse --verify gh-pages >/dev/null 2>&1; then
  git checkout --orphan gh-pages
  git rm -rf . >/dev/null 2>&1 || true
else
  git checkout gh-pages
fi

# Replace tree but keep .git
find . -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
cp -a "$OUT"/. .
git add -A
if git diff --cached --quiet; then
  echo "No changes to publish."
  exit 0
fi
git -c user.name="${GIT_AUTHOR_NAME:-orion-belt-bot}" \
    -c user.email="${GIT_AUTHOR_EMAIL:-maintainers@orion-belt.dev}" \
    commit -m "Publish agent packages ${TAG}"
git push -u origin gh-pages
echo "Published → ${PAGES_URL}/"
