#!/usr/bin/env bash
# Orion Belt — curl|bash server installer (distro-aware).
#
# Interactive:
#   curl -fsSL https://raw.githubusercontent.com/orion-belt-dev/orion-belt/master/scripts/install-server.sh | sudo bash
#
# Unattended example:
#   curl -fsSL .../install-server.sh | sudo bash -s -- \
#     --unattended \
#     --public-url https://orion.example.com \
#     --admin-name admin \
#     --admin-email admin@example.com \
#     --admin-key-file /root/admin.pub \
#     --db-url 'postgres://orionbelt:SECRET@127.0.0.1:5432/orionbelt?sslmode=disable' \
#     --jwt-secret "$(openssl rand -hex 32)"
#
# Flags:
#   --unattended              Require all needed flags; never prompt
#   --version VER|latest      Package/binary version (default: latest from package mirror)
#   --public-url URL          Advertised UI/API origin (required in unattended)
#   --public-ssh-host HOST    Agents dial this (default: host from public-url)
#   --public-ssh-port PORT    Agents dial this port (default: 2222)
#   --admin-name NAME         First admin username (default: admin)
#   --admin-email EMAIL       First admin email
#   --admin-key-file PATH     Path to admin SSH public key (.pub / YubiKey sk-*.pub)
#   --admin-key-generate DIR  Generate ed25519 keypair under DIR (default: /root)
#   --db-url DSN              Postgres connection string
#   --jwt-secret SECRET       JWT signing secret
#   --package-base URL        Package mirror (default: GitHub Pages packages)
#   --binary-only             Skip packages; install release tarball + unit files
#   --skip-start              Install + configure but do not enable/start
#   --skip-setup              Do not run `orion-belt-server setup`
#   --yes                     Assume yes for destructive prompts (non-unattended)
#   -h, --help                Show this help
set -euo pipefail

PKG_BASE_DEFAULT="https://orion-belt-dev.github.io/packages"
GITHUB_REPO="orion-belt-dev/orion-belt"
CONFIG_PATH="/etc/orion-belt/server.yaml"
BIN_PATH="/usr/bin/orion-belt-server"

UNATTENDED=0
ASSUME_YES=0
VERSION="latest"
PUBLIC_URL=""
PUBLIC_SSH_HOST=""
PUBLIC_SSH_PORT="2222"
ADMIN_NAME="admin"
ADMIN_EMAIL=""
ADMIN_KEY_FILE=""
ADMIN_KEY_GENERATE=""
DB_URL=""
JWT_SECRET=""
PKG_BASE="$PKG_BASE_DEFAULT"
BINARY_ONLY=0
SKIP_START=0
SKIP_SETUP=0

usage() { sed -n '2,36p' "$0" | sed 's/^# \{0,1\}//'; }

have() { command -v "$1" >/dev/null 2>&1; }

die() { echo "error: $*" >&2; exit 1; }

need_root() {
  [ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
}

prompt() {
  local label="$1" def="${2:-}" out
  if [ -n "$def" ]; then
    printf '%s [%s]: ' "$label" "$def" >&2
  else
    printf '%s: ' "$label" >&2
  fi
  read -r out || true
  out=$(printf '%s' "$out" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  if [ -z "$out" ]; then
    printf '%s' "$def"
  else
    printf '%s' "$out"
  fi
}

confirm() {
  local msg="$1"
  if [ "$ASSUME_YES" -eq 1 ] || [ "$UNATTENDED" -eq 1 ]; then
    return 0
  fi
  printf '%s [y/N] ' "$msg" >&2
  read -r reply || true
  case "$reply" in [Yy]|[Yy][Ee][Ss]) return 0 ;; *) return 1 ;; esac
}

while [ $# -gt 0 ]; do
  case "$1" in
    --unattended) UNATTENDED=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --version) VERSION="${2:?}"; shift ;;
    --public-url) PUBLIC_URL="${2:?}"; shift ;;
    --public-ssh-host) PUBLIC_SSH_HOST="${2:?}"; shift ;;
    --public-ssh-port) PUBLIC_SSH_PORT="${2:?}"; shift ;;
    --admin-name) ADMIN_NAME="${2:?}"; shift ;;
    --admin-email) ADMIN_EMAIL="${2:?}"; shift ;;
    --admin-key-file) ADMIN_KEY_FILE="${2:?}"; shift ;;
    --admin-key-generate)
      if [ -n "${2:-}" ] && [ "${2#-}" = "$2" ]; then
        ADMIN_KEY_GENERATE="$2"
        shift
      else
        ADMIN_KEY_GENERATE=/root
      fi
      ;;
    --db-url) DB_URL="${2:?}"; shift ;;
    --jwt-secret) JWT_SECRET="${2:?}"; shift ;;
    --package-base) PKG_BASE="${2:?}"; shift ;;
    --binary-only) BINARY_ONLY=1 ;;
    --skip-start) SKIP_START=1 ;;
    --skip-setup) SKIP_SETUP=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

need_root
have curl || die "curl is required"
have openssl || die "openssl is required"
have ssh-keygen || die "ssh-keygen is required"

# ---------------------------------------------------------------- distro
OS_ID=unknown
OS_LIKE=""
if [ -f /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-unknown}"
  OS_LIKE="${ID_LIKE:-}"
fi

arch_raw=$(uname -m)
case "$arch_raw" in
  x86_64|amd64) ARCH=amd64; RPM_ARCH=x86_64 ;;
  aarch64|arm64) ARCH=arm64; RPM_ARCH=aarch64 ;;
  *) die "unsupported architecture: $arch_raw" ;;
esac

init_system=none
if have systemctl && [ -d /run/systemd/system ]; then
  init_system=systemd
elif have rc-service || [ -d /etc/init.d ]; then
  init_system=openrc
fi

echo "== Orion Belt server install =="
echo "   distro: $OS_ID (like: ${OS_LIKE:-n/a})"
echo "   arch:   $ARCH"
echo "   init:   $init_system"

# -------------------------------------------------------------- resolve ver
resolve_version() {
  local v="$1"
  if [ "$v" != "latest" ]; then
    printf '%s' "${v#v}"
    return
  fi
  if ver=$(curl -fsSL "${PKG_BASE%/}/VERSION" 2>/dev/null); then
    ver=$(printf '%s' "$ver" | tr -d '[:space:]')
    ver=${ver#v}
    if [ -n "$ver" ] && [ "$ver" != "dev" ] && [ "$ver" != "0.0.0" ]; then
      printf '%s' "$ver"
      return
    fi
  fi
  if have python3; then
    ver=$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
      | python3 -c 'import sys,json; print(json.load(sys.stdin).get("tag_name",""))' 2>/dev/null || true)
  else
    ver=$(curl -fsSL "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
      | sed -n 's/.*"tag_name": *"\(v[^"]*\)".*/\1/p' | head -1)
  fi
  ver=${ver#v}
  [ -n "$ver" ] || die "could not resolve latest version (set --version)"
  printf '%s' "$ver"
}

VERSION=$(resolve_version "$VERSION")
echo "   version: $VERSION"

# -------------------------------------------------------------- interactive
if [ "$UNATTENDED" -eq 1 ]; then
  [ -n "$PUBLIC_URL" ] || die "--public-url is required with --unattended"
  [ -n "$DB_URL" ] || die "--db-url is required with --unattended"
  [ -n "$JWT_SECRET" ] || die "--jwt-secret is required with --unattended"
  [ -n "$ADMIN_EMAIL" ] || ADMIN_EMAIL="admin@$(hostname -f 2>/dev/null || echo localhost)"
  if [ -z "$ADMIN_KEY_FILE" ] && [ -z "$ADMIN_KEY_GENERATE" ]; then
    die "provide --admin-key-file or --admin-key-generate with --unattended"
  fi
else
  if [ ! -t 0 ]; then
    die "stdin is not a terminal; pass --unattended with flags, or run without piping"
  fi
  echo
  echo "── Public address ──"
  echo "  Origin browsers open and (by default) the host agents dial."
  PUBLIC_URL=$(prompt "Public URL (e.g. https://orion.example.com)" "${PUBLIC_URL:-}")
  [ -n "$PUBLIC_URL" ] || die "public URL is required"
  PUBLIC_URL=${PUBLIC_URL%/}
  def_host=$(printf '%s' "$PUBLIC_URL" | sed -E 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||; s|[:/].*$||')
  PUBLIC_SSH_HOST=$(prompt "Public SSH host (agents)" "${PUBLIC_SSH_HOST:-$def_host}")
  PUBLIC_SSH_PORT=$(prompt "Public SSH port" "${PUBLIC_SSH_PORT:-2222}")

  echo
  echo "── Database ──"
  DB_URL=$(prompt "Postgres DSN" "${DB_URL:-postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable}")

  echo
  echo "── Secrets ──"
  if [ -z "$JWT_SECRET" ]; then
    if confirm "Generate a random JWT secret now?"; then
      JWT_SECRET=$(openssl rand -hex 32)
      echo "   generated jwt_secret"
    else
      JWT_SECRET=$(prompt "JWT secret (openssl rand -hex 32)" "")
    fi
  fi
  [ -n "$JWT_SECRET" ] || die "jwt secret is required"

  echo
  echo "── First admin ──"
  ADMIN_NAME=$(prompt "Admin username" "$ADMIN_NAME")
  ADMIN_EMAIL=$(prompt "Admin email" "${ADMIN_EMAIL:-admin@localhost}")
  echo "Admin SSH public key:"
  echo "  1) path to an existing .pub file (incl. YubiKey sk-*.pub)"
  echo "  2) paste one authorized_keys line"
  echo "  3) generate a new ed25519 keypair"
  choice=$(prompt "Choice" "3")
  case "$choice" in
    1)
      ADMIN_KEY_FILE=$(prompt "Path to public key" "${ADMIN_KEY_FILE:-$HOME/.ssh/id_ed25519.pub}")
      ;;
    2)
      ADMIN_KEY_LINE=$(prompt "Paste public key line" "")
      [ -n "$ADMIN_KEY_LINE" ] || die "empty public key"
      ADMIN_KEY_FILE=/etc/orion-belt/admin.pub
      mkdir -p /etc/orion-belt
      printf '%s\n' "$ADMIN_KEY_LINE" > "$ADMIN_KEY_FILE"
      chmod 644 "$ADMIN_KEY_FILE"
      ;;
    3|*)
      ADMIN_KEY_GENERATE=$(prompt "Directory for new keypair" "${ADMIN_KEY_GENERATE:-/root}")
      ;;
  esac
fi

PUBLIC_URL=${PUBLIC_URL%/}
if [ -z "$PUBLIC_SSH_HOST" ]; then
  PUBLIC_SSH_HOST=$(printf '%s' "$PUBLIC_URL" | sed -E 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||; s|[:/].*$||')
fi
[ -n "$ADMIN_EMAIL" ] || ADMIN_EMAIL="admin@localhost"

# -------------------------------------------------------------- admin key
if [ -n "$ADMIN_KEY_GENERATE" ]; then
  mkdir -p "$ADMIN_KEY_GENERATE"
  key_priv="${ADMIN_KEY_GENERATE%/}/orion-belt-admin"
  if [ -f "$key_priv" ]; then
    echo "   reusing existing $key_priv"
  else
    ssh-keygen -t ed25519 -f "$key_priv" -N "" -C "orion-belt-admin" -q
    chmod 600 "$key_priv"
    echo "   generated $key_priv (+ .pub) — keep the private key safe"
  fi
  ADMIN_KEY_FILE="${key_priv}.pub"
fi
[ -n "$ADMIN_KEY_FILE" ] || die "admin public key is required"
[ -f "$ADMIN_KEY_FILE" ] || die "admin key file not found: $ADMIN_KEY_FILE"

# ---------------------------------------------------------------- install
install_via_package() {
  local ver="$1" url tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN

  case "$OS_ID" in
    debian|ubuntu|raspbian)
      for cand in \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${ARCH}.deb" \
        "${PKG_BASE%/}/orion-belt_${ver}_${ARCH}.deb"
      do
        if curl -fsSL -o "$tmp/orion-belt.deb" "$cand"; then
          echo "   installing $cand"
          dpkg -i "$tmp/orion-belt.deb" || apt-get install -f -y
          return 0
        fi
      done
      return 1
      ;;
    rhel|centos|rocky|almalinux|fedora|ol)
      for cand in \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${RPM_ARCH}.rpm" \
        "${PKG_BASE%/}/orion-belt_${ver}_${RPM_ARCH}.rpm" \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${ARCH}.rpm"
      do
        if curl -fsSL -o "$tmp/orion-belt.rpm" "$cand"; then
          echo "   installing $cand"
          if have dnf; then dnf -y install "$tmp/orion-belt.rpm"
          elif have yum; then yum -y install "$tmp/orion-belt.rpm"
          else rpm -Uvh "$tmp/orion-belt.rpm"
          fi
          return 0
        fi
      done
      return 1
      ;;
    opensuse*|sles)
      for cand in \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${RPM_ARCH}.rpm" \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${ARCH}.rpm"
      do
        if curl -fsSL -o "$tmp/orion-belt.rpm" "$cand"; then
          echo "   installing $cand"
          zypper -n install "$tmp/orion-belt.rpm" || rpm -Uvh "$tmp/orion-belt.rpm"
          return 0
        fi
      done
      return 1
      ;;
    alpine)
      for cand in \
        "${PKG_BASE%/}/orion-belt_${ver}_x86_64.apk" \
        "${PKG_BASE%/}/orion-belt_${ver}_linux_${ARCH}.apk"
      do
        if curl -fsSL -o "$tmp/orion-belt.apk" "$cand"; then
          echo "   installing $cand"
          apk add --allow-untrusted "$tmp/orion-belt.apk"
          return 0
        fi
      done
      return 1
      ;;
    *)
      return 1
      ;;
  esac
}

install_via_binary() {
  local ver="$1" url tmp
  tmp=$(mktemp -d)
  # GitHub release asset: orion-belt_<ver>_linux_<arch>.tar.gz
  url="https://github.com/${GITHUB_REPO}/releases/download/v${ver}/orion-belt_${ver}_linux_${ARCH}.tar.gz"
  echo "   downloading $url"
  curl -fsSL -o "$tmp/orion.tgz" "$url" || die "failed to download release archive"
  tar -xzf "$tmp/orion.tgz" -C "$tmp"
  install -m 0755 "$tmp/orion-belt-server" "$BIN_PATH"

  # system user + dirs (mirrors packaging/scripts/server-postinstall.sh)
  if ! getent group orionbelt >/dev/null 2>&1; then
    groupadd --system orionbelt 2>/dev/null || addgroup -S orionbelt 2>/dev/null || true
  fi
  if ! getent passwd orionbelt >/dev/null 2>&1; then
    useradd --system --gid orionbelt --home-dir /var/lib/orion-belt --shell /usr/sbin/nologin orionbelt \
      2>/dev/null || adduser -S -G orionbelt -h /var/lib/orion-belt -s /sbin/nologin orionbelt 2>/dev/null || true
  fi
  mkdir -p /var/lib/orion-belt/recordings /var/log/orion-belt /etc/orion-belt
  chown -R orionbelt:orionbelt /var/lib/orion-belt /var/log/orion-belt 2>/dev/null || true
  chmod 750 /var/lib/orion-belt /var/log/orion-belt /etc/orion-belt

  if [ ! -f /etc/orion-belt/ssh_host_key ]; then
    ssh-keygen -t ed25519 -f /etc/orion-belt/ssh_host_key -N "" -C "orion-belt-host" -q
    chown orionbelt:orionbelt /etc/orion-belt/ssh_host_key /etc/orion-belt/ssh_host_key.pub 2>/dev/null || true
    chmod 600 /etc/orion-belt/ssh_host_key
  fi

  if [ "$init_system" = "systemd" ]; then
    cat > /lib/systemd/system/orion-belt-server.service <<'UNIT'
[Unit]
Description=Orion Belt PAM gateway server
Documentation=https://github.com/orion-belt-dev/orion-belt
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=orionbelt
Group=orionbelt
ExecStart=/usr/bin/orion-belt-server -c /etc/orion-belt/server.yaml
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/orion-belt /var/log/orion-belt /etc/orion-belt
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
  elif [ "$init_system" = "openrc" ]; then
    cat > /etc/init.d/orion-belt-server <<'INIT'
#!/sbin/openrc-run
name="orion-belt-server"
description="Orion Belt PAM gateway server"
command="/usr/bin/orion-belt-server"
command_args="-c /etc/orion-belt/server.yaml"
command_user="orionbelt:orionbelt"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"
depend() { need net; use dns logger; after firewall postgresql; }
start_pre() {
  checkpath -d -o orionbelt:orionbelt -m 0750 /var/lib/orion-belt
  checkpath -d -o orionbelt:orionbelt -m 0750 /var/log/orion-belt
  checkpath -d -o orionbelt:orionbelt -m 0750 /etc/orion-belt
}
INIT
    chmod 755 /etc/init.d/orion-belt-server
  fi
  rm -rf "$tmp"
}

echo
echo "── Installing orion-belt-server $VERSION ──"
if [ "$BINARY_ONLY" -eq 1 ]; then
  install_via_binary "$VERSION"
else
  if ! install_via_package "$VERSION"; then
    echo "   package install unavailable for this distro — falling back to release binary"
    install_via_binary "$VERSION"
  fi
fi
have orion-belt-server || [ -x "$BIN_PATH" ] || die "orion-belt-server not installed"

# ---------------------------------------------------------------- config
WEBAUTHN_HOST=$(printf '%s' "$PUBLIC_URL" | sed -E 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||; s|[:/].*$||')
echo
echo "── Writing $CONFIG_PATH ──"
WRITE_CONFIG=1
if [ -f "$CONFIG_PATH" ] && ! grep -qE 'change-me-to-a-long-random-secret|CHANGE_ME|password@localhost|orion\.example\.com' "$CONFIG_PATH" 2>/dev/null; then
  if ! confirm "$CONFIG_PATH already looks customized — overwrite?"; then
    echo "   leaving existing config in place"
    WRITE_CONFIG=0
  fi
fi

if [ "$WRITE_CONFIG" -eq 1 ]; then
  cat > "$CONFIG_PATH" <<EOF
server:
  host: "0.0.0.0"
  port: 2222
  api_port: 8080
  ssh_host_key: "/etc/orion-belt/ssh_host_key"
  public_url: "${PUBLIC_URL}"
  public_ssh_host: "${PUBLIC_SSH_HOST}"
  public_ssh_port: ${PUBLIC_SSH_PORT}
  metrics_enabled: true

database:
  driver: "postgres"
  connection_string: "${DB_URL}"

auth:
  rebac_enabled: true
  allow_temp_access: true
  jwt_secret: "${JWT_SECRET}"
  jwt_expiry_hours: 24
  mfa_required: false
  rate_limit_per_minute: 600
  webauthn:
    enabled: true
    rp_display_name: "Orion Belt"
    rp_id: "${WEBAUTHN_HOST}"
    origins:
      - "${PUBLIC_URL}"

recording:
  enabled: true
  storage_path: "/var/lib/orion-belt/recordings"
  retention_days: 90
  compression: gzip
  encryption_key: ""

plugins: {}
EOF
  chown orionbelt:orionbelt "$CONFIG_PATH" 2>/dev/null || true
  chmod 640 "$CONFIG_PATH"
  echo "   wrote $CONFIG_PATH"
fi

# ---------------------------------------------------------------- service
if [ "$SKIP_START" -eq 0 ]; then
  echo
  echo "── Starting service ($init_system) ──"
  case "$init_system" in
    systemd)
      systemctl enable --now orion-belt-server
      systemctl --no-pager --full status orion-belt-server || true
      ;;
    openrc)
      rc-update add orion-belt-server default 2>/dev/null || true
      rc-service orion-belt-server start || /etc/init.d/orion-belt-server start
      ;;
    *)
      echo "   ! no systemd/OpenRC detected — start manually:"
      echo "       $BIN_PATH -c $CONFIG_PATH"
      ;;
  esac
fi

# ---------------------------------------------------------------- setup
if [ "$SKIP_SETUP" -eq 0 ]; then
  echo
  echo "── First admin (setup wizard) ──"
  # Wait briefly for API/DB readiness when we just started the service.
  if [ "$SKIP_START" -eq 0 ]; then
    for _ in $(seq 1 30); do
      if curl -sf "http://127.0.0.1:8080/health" >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
  fi
  export ORION_SETUP_ADMIN_NAME="$ADMIN_NAME"
  export ORION_SETUP_ADMIN_EMAIL="$ADMIN_EMAIL"
  export ORION_SETUP_ADMIN_KEY_FILE="$ADMIN_KEY_FILE"
  export ORION_SETUP_PUBLIC_URL="$PUBLIC_URL"
  export ORION_SETUP_PUBLIC_SSH_HOST="$PUBLIC_SSH_HOST"
  export ORION_SETUP_PUBLIC_SSH_PORT="$PUBLIC_SSH_PORT"
  if have runuser; then
    runuser -u orionbelt -- orion-belt-server -c "$CONFIG_PATH" setup </dev/null
  elif have su; then
    su -s /bin/sh orionbelt -c "orion-belt-server -c $CONFIG_PATH setup" </dev/null
  else
    orion-belt-server -c "$CONFIG_PATH" setup </dev/null
  fi
fi

cat <<EOF

╔══════════════════════════════════════════════════════════════════╗
║  Orion Belt server installed                                     ║
╚══════════════════════════════════════════════════════════════════╝

  UI:          ${PUBLIC_URL}/ui
  Agents dial: ${PUBLIC_SSH_HOST}:${PUBLIC_SSH_PORT}
  Config:      ${CONFIG_PATH}
  Admin key:   ${ADMIN_KEY_FILE}

  Sign in (from a machine with the matching private key):
    osh login --api-endpoint ${PUBLIC_URL}

  Add agents from the console (**Add agent**) — gateway host defaults to
  the public SSH address you just set.

  Docs: https://github.com/orion-belt-dev/orion-belt/blob/master/docs/SETUP.md

EOF
