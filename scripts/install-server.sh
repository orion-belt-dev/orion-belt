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
#   # Uninstall:
#   sudo ./install-server.sh --uninstall
#   sudo ./install-server.sh --uninstall --unattended --drop-db --drop-logs --drop-data
#
# Flags:
#   --unattended              Require all needed flags; never prompt
#   --version VER|latest      Package/binary version (default: latest from package mirror)
#   --public-url URL          Advertised UI/API origin (required in unattended)
#   --api-port PORT           Gateway HTTP listen port (default: port in public-url, else http=80 / https=443)
#   --public-ssh-host HOST    Agents dial this (default: host from public-url)
#   --public-ssh-port PORT    Agents dial this port (default: 2222)
#   --admin-name NAME         First admin username (default: admin)
#   --admin-email EMAIL       First admin email
#   --admin-key-file PATH     Path to admin SSH public key (.pub / YubiKey sk-*.pub)
#   --admin-key-generate DIR  Generate ed25519 keypair under DIR (default: /root)
#   --db-url DSN              Postgres connection string (or use --install-postgres)
#   --install-postgres        Install/start local Postgres and create orionbelt DB/user
#   --recreate-db             With --install-postgres: drop+recreate orionbelt DB if it exists
#   --jwt-secret SECRET       JWT signing secret
#   --webauthn-rp-id DOMAIN   WebAuthn rp_id (domain name — not an IP)
#   --disable-webauthn        Skip WebAuthn (SSH-key login still works)
#   --package-base URL        Package mirror (default: GitHub Pages packages)
#   --binary-only             Skip packages; install release tarball + unit files
#   --skip-start              Install + configure but do not enable/start
#   --skip-setup              Do not run `orion-belt-server setup`
#   --uninstall               Remove Orion Belt server (prompts for DB/logs/data)
#   --drop-db                 With --uninstall: drop the orionbelt Postgres DB/role
#   --keep-db                 With --uninstall: keep the orionbelt Postgres DB/role
#   --drop-logs               With --uninstall: remove /var/log/orion-belt
#   --keep-logs               With --uninstall: keep /var/log/orion-belt
#   --drop-data               With --uninstall: remove /var/lib/orion-belt (recordings)
#   --keep-data               With --uninstall: keep /var/lib/orion-belt
#   --yes                     Assume yes for destructive prompts (non-unattended)
#   -h, --help                Show this help
set -euo pipefail

PKG_BASE_DEFAULT="https://orion-belt-dev.github.io/packages"
GITHUB_REPO="orion-belt-dev/orion-belt"
CONFIG_PATH="/etc/orion-belt/server.yaml"
BIN_PATH="/usr/bin/orion-belt-server"
DB_USER="orionbelt"
DB_NAME="orionbelt"
ETC_DIR="/etc/orion-belt"
VAR_LIB="/var/lib/orion-belt"
VAR_LOG="/var/log/orion-belt"

UNATTENDED=0
ASSUME_YES=0
VERSION="latest"
PUBLIC_URL=""
API_PORT=""
PUBLIC_SSH_HOST=""
PUBLIC_SSH_PORT="2222"
ADMIN_NAME="admin"
ADMIN_EMAIL=""
ADMIN_KEY_FILE=""
ADMIN_KEY_GENERATE=""
DB_URL=""
INSTALL_POSTGRES=0
RECREATE_DB=0
JWT_SECRET=""
WEBAUTHN_RP_ID=""
WEBAUTHN_ENABLED=""   # empty = decide later; true/false once resolved
WEBAUTHN_ORIGIN=""     # browser origin written to auth.webauthn.origins
DISABLE_WEBAUTHN=0
PKG_BASE="$PKG_BASE_DEFAULT"
BINARY_ONLY=0
SKIP_START=0
SKIP_SETUP=0
DO_UNINSTALL=0
DROP_DB=""   # empty = ask; 1 = drop; 0 = keep
DROP_LOGS=""
DROP_DATA=""

usage() { sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//'; }

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
  # Read from the controlling TTY so `curl … | sudo bash` can still prompt.
  if [ -t 0 ]; then
    read -r out || true
  else
    read -r out </dev/tty || true
  fi
  out=$(printf '%s' "$out" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  if [ -z "$out" ]; then
    printf '%s' "$def"
  else
    printf '%s' "$out"
  fi
}

confirm() {
  local msg="$1" reply
  if [ "$ASSUME_YES" -eq 1 ] || [ "$UNATTENDED" -eq 1 ]; then
    return 0
  fi
  printf '%s [y/N] ' "$msg" >&2
  if [ -t 0 ]; then
    read -r reply || true
  else
    read -r reply </dev/tty || true
  fi
  case "$reply" in [Yy]|[Yy][Ee][Ss]) return 0 ;; *) return 1 ;; esac
}

# True when we can prompt the operator (real TTY, or a usable /dev/tty under a pipe).
can_prompt() {
  [ -t 0 ] && return 0
  [ -r /dev/tty ] && [ -w /dev/tty ]
}

# Default listen/public port for a URL scheme (http→80, https→443).
default_port_for_scheme() {
  case "$1" in
    https|HTTPS) printf '443' ;;
    *) printf '80' ;;
  esac
}

# Port embedded in a URL, or empty if absent / default-implied.
public_url_port() {
  local u="$1" rest hostport
  case "$u" in
    http://*|https://*) ;;
    *) u="http://$u" ;;
  esac
  rest="${u#*://}"
  hostport="${rest%%/*}"
  case "$hostport" in
    \[*\]:*) printf '%s' "${hostport##*]:}" ;; # [ipv6]:port
    \[*\]) printf '' ;;
    *:*) printf '%s' "${hostport##*:}" ;;
    *) printf '' ;;
  esac
}

# If the operator pastes http://192.168.x.x with no port, browsers hit :80.
# For bare IPs only, append the scheme default (80 / 443) — not the old :8080 lab default.
# Hostnames without a port are left alone (browsers already default to 80/443).
normalize_public_url() {
  local u="$1" scheme rest hostport host port
  case "$u" in
    http://*|https://*) ;;
    *) u="http://$u" ;;
  esac
  scheme="${u%%://*}"
  rest="${u#*://}"
  hostport="${rest%%/*}"
  case "$hostport" in
    \[*\]:*|*:*) printf '%s' "$u"; return ;; # already has a port
  esac
  host="${hostport}"
  case "$host" in
    \[*\]) host="${host#\[}"; host="${host%\]}" ;;
  esac
  if host_is_ip "$host"; then
    port=$(default_port_for_scheme "$scheme")
    printf '%s://%s:%s' "$scheme" "$hostport" "$port"
    case "$rest" in */*) printf '/%s' "${rest#*/}" ;; esac
    return
  fi
  printf '%s' "$u"
}

# True for single-label names (orion-test) — LAN/hosts/mDNS only; not public DNS.
# localhost is allowed as a special WebAuthn rp_id.
host_is_single_label() {
  local h="$1"
  case "$h" in
    ""|localhost) return 1 ;;
    *.*) return 1 ;;
    *) return 0 ;;
  esac
}

# Rebuild origin as scheme://host:port using PUBLIC_URL's scheme/port and a new host.
origin_with_host() {
  local base="$1" host="$2" scheme port
  case "$base" in
    https://*) scheme=https ;;
    *) scheme=http ;;
  esac
  port=$(public_url_port "$base")
  [ -n "$port" ] || port=$(default_port_for_scheme "$scheme")
  # Omit default ports in the origin string for cleanliness.
  case "$scheme:$port" in
    http:80|https:443) printf '%s://%s' "$scheme" "$host" ;;
    *) printf '%s://%s:%s' "$scheme" "$host" "$port" ;;
  esac
}

# WebAuthn rp_id must be a domain (or localhost), not an IP.
host_is_ip() {
  local h="$1"
  case "$h" in
    *:*) return 0 ;; # IPv6
  esac
  case "$h" in
    *[!0-9.]*|"") return 1 ;;
  esac
  case "$h" in
    *.*.*.*) return 0 ;;
    *) return 1 ;;
  esac
}

public_url_host() {
  printf '%s' "$1" | sed -E 's|^[a-zA-Z][a-zA-Z0-9+.-]*://||; s|[:/].*$||'
}

# Resolve A/AAAA for a hostname. Prints one address per line.
dns_forward_addrs() {
  local name="$1" out=""
  if have getent; then
    out=$(getent ahosts "$name" 2>/dev/null | awk '{print $1}' | sort -u)
  fi
  if [ -z "$out" ] && have dig; then
    out=$( {
      dig +short A "$name" 2>/dev/null
      dig +short AAAA "$name" 2>/dev/null
    } | sed '/^$/d' | sort -u)
  fi
  if [ -z "$out" ] && have host; then
    out=$(host "$name" 2>/dev/null | awk '/has address/{print $4} /has IPv6/{print $5}' | sort -u)
  fi
  if [ -z "$out" ] && have python3; then
    out=$(python3 - "$name" <<'PY' 2>/dev/null
import socket, sys
name = sys.argv[1]
seen = set()
for fam, _, _, _, sockaddr in socket.getaddrinfo(name, None):
    addr = sockaddr[0]
    if addr not in seen:
        seen.add(addr)
        print(addr)
PY
)
  fi
  printf '%s\n' "$out"
}

# Reverse PTR for an IP. Prints the hostname (no trailing dot) or empty.
dns_reverse_name() {
  local ip="$1" name=""
  if have dig; then
    name=$(dig +short -x "$ip" 2>/dev/null | head -1 | sed 's/\.$//')
  fi
  if [ -z "$name" ] && have host; then
    name=$(host "$ip" 2>/dev/null | awk '/domain name pointer/{print $NF}' | sed 's/\.$//' | head -1)
  fi
  if [ -z "$name" ] && have getent; then
    name=$(getent hosts "$ip" 2>/dev/null | awk '{print $2}' | head -1)
  fi
  if [ -z "$name" ] && have python3; then
    name=$(python3 - "$ip" <<'PY' 2>/dev/null
import socket, sys
try:
    print(socket.gethostbyaddr(sys.argv[1])[0])
except Exception:
    pass
PY
)
  fi
  printf '%s' "$name"
}

# Returns 0 if domain's A/AAAA includes expect_ip.
dns_forward_matches_ip() {
  local domain="$1" expect_ip="$2" addr
  while IFS= read -r addr; do
    [ -z "$addr" ] && continue
    if [ "$addr" = "$expect_ip" ]; then
      return 0
    fi
  done <<EOF
$(dns_forward_addrs "$domain")
EOF
  return 1
}

# Returns 0 if PTR for ip equals domain or is a subdomain of it (case-insensitive).
dns_reverse_matches_domain() {
  local ip="$1" domain="$2" ptr
  ptr=$(dns_reverse_name "$ip")
  [ -n "$ptr" ] || return 1
  ptr=$(printf '%s' "$ptr" | tr '[:upper:]' '[:lower:]')
  domain=$(printf '%s' "$domain" | tr '[:upper:]' '[:lower:]')
  [ "$ptr" = "$domain" ] && return 0
  case "$ptr" in
    *."$domain") return 0 ;;
  esac
  return 1
}

# Interactive / flag-driven WebAuthn rp_id. Sets WEBAUTHN_RP_ID, WEBAUTHN_ENABLED,
# and may adjust PUBLIC_URL / WEBAUTHN_ORIGIN so the browser host matches rp_id.
configure_webauthn_rp_id() {
  local pub_host="$1"  # host from public_url (may be IP)
  local def_domain="" rp_id="" want_origin=""

  WEBAUTHN_ORIGIN="${WEBAUTHN_ORIGIN:-$PUBLIC_URL}"

  if [ "$DISABLE_WEBAUTHN" -eq 1 ]; then
    WEBAUTHN_ENABLED=false
    WEBAUTHN_RP_ID="localhost"
    echo "   WebAuthn disabled (--disable-webauthn)"
    return
  fi

  if [ -n "$WEBAUTHN_RP_ID" ]; then
    rp_id="$WEBAUTHN_RP_ID"
  elif [ "$UNATTENDED" -eq 1 ]; then
    if host_is_ip "$pub_host"; then
      WEBAUTHN_ENABLED=false
      WEBAUTHN_RP_ID="localhost"
      echo "   WebAuthn disabled (public host is an IP; pass --webauthn-rp-id DOMAIN to enable)"
      return
    fi
    rp_id="$pub_host"
  else
    echo
    echo "── WebAuthn (FIDO2 / YubiKey) ──"
    echo "  WebAuthn requires a DNS domain name as rp_id — an IP address is not valid"
    echo "  and will prevent the gateway from starting (or leave WebAuthn broken)."
    echo "  The browser must open that same hostname (not a raw IP); SSH-key login works either way."
    if host_is_ip "$pub_host"; then
      echo
      echo "  Your public URL uses IP $pub_host."
      echo "  To enable WebAuthn, enter a domain that resolves here (LAN DNS, /etc/hosts, or mDNS)."
      printf 'Enable WebAuthn with a DNS domain? [y/N] ' >&2
      if [ -t 0 ]; then read -r _wa || true; else read -r _wa </dev/tty || true; fi
      case "${_wa:-}" in
        [Yy]|[Yy][Ee][Ss]) ;;
        *)
          WEBAUTHN_ENABLED=false
          WEBAUTHN_RP_ID="localhost"
          echo "   WebAuthn disabled — you can set auth.webauthn later in server.yaml"
          return
          ;;
      esac
      def_domain=""
    else
      def_domain="$pub_host"
    fi
    rp_id=$(prompt "WebAuthn domain (rp_id)" "$def_domain")
    if [ -z "$rp_id" ]; then
      WEBAUTHN_ENABLED=false
      WEBAUTHN_RP_ID="localhost"
      echo "   WebAuthn disabled (no domain given)"
      return
    fi
  fi

  rp_id=$(printf '%s' "$rp_id" | tr '[:upper:]' '[:lower:]' | sed 's/\.$//')
  case "$rp_id" in
    http://*|https://*|*/*|*:*)
      die "WebAuthn rp_id must be a bare domain (e.g. orion.example.com), not a URL"
      ;;
  esac
  if host_is_ip "$rp_id"; then
    die "WebAuthn rp_id cannot be an IP ($rp_id) — use a DNS name"
  fi

  # Single-label names (orion-test) are LAN-only and rejected by go-webauthn
  # ("domain component must actually be a domain"). Require a dotted name.
  if host_is_single_label "$rp_id"; then
    echo "   ! '$rp_id' is a single-label local hostname (LAN /etc/hosts or mDNS only)." >&2
    echo "     Public DNS will not resolve it — only machines that know this name can open it." >&2
    echo "     WebAuthn also requires a domain with a dot (e.g. ${rp_id}.local or ${rp_id}.lan)." >&2
    if [ "$UNATTENDED" -eq 1 ]; then
      die "WebAuthn rp_id '$rp_id' is not a valid domain; use a dotted name or --disable-webauthn"
    fi
    rp_id=$(prompt "WebAuthn domain with a dot (or blank to disable)" "${rp_id}.local")
    if [ -z "$rp_id" ]; then
      WEBAUTHN_ENABLED=false
      WEBAUTHN_RP_ID="localhost"
      echo "   WebAuthn disabled"
      return
    fi
    rp_id=$(printf '%s' "$rp_id" | tr '[:upper:]' '[:lower:]' | sed 's/\.$//')
    if host_is_single_label "$rp_id" || host_is_ip "$rp_id"; then
      WEBAUTHN_ENABLED=false
      WEBAUTHN_RP_ID="localhost"
      echo "   WebAuthn disabled ('$rp_id' is still not a dotted domain)"
      return
    fi
    echo "   note: '$rp_id' is still LAN-only unless you publish it in real DNS."
  fi

  # DNS checks against the public URL host when that host is an IP.
  if host_is_ip "$pub_host"; then
    echo "   checking DNS: $rp_id → should include $pub_host"
    if dns_forward_matches_ip "$rp_id" "$pub_host"; then
      echo "   ✓ forward lookup: $rp_id resolves to $pub_host"
    else
      echo "   ! forward lookup: $rp_id does not resolve to $pub_host" >&2
      dns_forward_addrs "$rp_id" | sed 's/^/     got: /' >&2 || echo "     got: (no addresses)" >&2
      echo "     If this name is only in LAN DNS or /etc/hosts, that is fine for lab use —" >&2
      echo "     public Internet DNS will not resolve it." >&2
      if [ "$UNATTENDED" -eq 1 ]; then
        die "fix DNS A/AAAA for $rp_id, or pass --disable-webauthn"
      fi
      if ! confirm "Continue with LAN-only name? WebAuthn needs browsers to open http(s)://$rp_id/…"; then
        WEBAUTHN_ENABLED=false
        WEBAUTHN_RP_ID="localhost"
        echo "   WebAuthn disabled"
        return
      fi
    fi

    echo "   checking reverse DNS: $pub_host → should be $rp_id"
    if dns_reverse_matches_domain "$pub_host" "$rp_id"; then
      echo "   ✓ reverse lookup: $(dns_reverse_name "$pub_host") matches $rp_id"
    else
      ptr=$(dns_reverse_name "$pub_host")
      if [ -n "$ptr" ]; then
        echo "   ! reverse lookup: $pub_host → $ptr (expected $rp_id)" >&2
      else
        echo "   ! reverse lookup: no PTR for $pub_host" >&2
      fi
      echo "     PTR is optional on a LAN; forward name→IP (or /etc/hosts) matters more." >&2
      if [ "$UNATTENDED" -eq 0 ]; then
        confirm "Continue without a matching PTR?" || true
      fi
    fi

    # Browser origin must use the rp_id hostname, not the raw IP.
    want_origin=$(origin_with_host "$PUBLIC_URL" "$rp_id")
    echo "   WebAuthn browsers must open: ${want_origin}/ui  (not the raw IP)"
    if [ "$UNATTENDED" -eq 0 ]; then
      if confirm "Set public URL to $want_origin so the UI origin matches rp_id"; then
        PUBLIC_URL="$want_origin"
        echo "   public URL → $PUBLIC_URL"
      fi
    else
      PUBLIC_URL="$want_origin"
      echo "   public URL → $PUBLIC_URL (aligned with WebAuthn rp_id)"
    fi
    WEBAUTHN_ORIGIN="$PUBLIC_URL"
  else
    # Public URL already uses a hostname — rp_id should match (or be a parent registrable domain).
    if [ "$rp_id" != "$pub_host" ]; then
      case "$pub_host" in
        *."$rp_id") ;;
        *)
          echo "   ! rp_id $rp_id differs from public URL host $pub_host" >&2
          echo "     WebAuthn only works when the browser hostname matches rp_id (or is a subdomain)." >&2
          if [ "$UNATTENDED" -eq 0 ] && ! confirm "Continue with rp_id=$rp_id"; then
            WEBAUTHN_ENABLED=false
            WEBAUTHN_RP_ID="localhost"
            echo "   WebAuthn disabled"
            return
          fi
          ;;
      esac
    fi
    if host_is_single_label "$pub_host"; then
      echo "   note: public host '$pub_host' is LAN-only — public DNS will not resolve it."
    fi
    WEBAUTHN_ORIGIN="$PUBLIC_URL"
  fi

  WEBAUTHN_RP_ID="$rp_id"
  WEBAUTHN_ENABLED=true
  echo "   WebAuthn enabled (rp_id=$WEBAUTHN_RP_ID)"
}


while [ $# -gt 0 ]; do
  case "$1" in
    --unattended) UNATTENDED=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --version) VERSION="${2:?}"; shift ;;
    --public-url) PUBLIC_URL="${2:?}"; shift ;;
    --api-port) API_PORT="${2:?}"; shift ;;
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
        # Prefer the sudo caller's home so osh login works without root.
        ADMIN_KEY_GENERATE=/root
        if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
          _h=$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6)
          [ -n "$_h" ] && ADMIN_KEY_GENERATE="${_h}/.orion-belt"
        fi
      fi
      ;;
    --db-url) DB_URL="${2:?}"; shift ;;
    --install-postgres|--with-postgres) INSTALL_POSTGRES=1 ;;
    --recreate-db) RECREATE_DB=1 ;;
    --jwt-secret) JWT_SECRET="${2:?}"; shift ;;
    --webauthn-rp-id) WEBAUTHN_RP_ID="${2:?}"; shift ;;
    --disable-webauthn) DISABLE_WEBAUTHN=1 ;;
    --package-base) PKG_BASE="${2:?}"; shift ;;
    --binary-only) BINARY_ONLY=1 ;;
    --skip-start) SKIP_START=1 ;;
    --skip-setup) SKIP_SETUP=1 ;;
    --uninstall) DO_UNINSTALL=1 ;;
    --drop-db) DROP_DB=1 ;;
    --keep-db) DROP_DB=0 ;;
    --drop-logs) DROP_LOGS=1 ;;
    --keep-logs) DROP_LOGS=0 ;;
    --drop-data) DROP_DATA=1 ;;
    --keep-data) DROP_DATA=0 ;;
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
# Read os-release in a subshell so its VERSION/NAME vars cannot clobber ours
# (Ubuntu sets VERSION="24.04 LTS …", which used to become the package version).
OS_ID=unknown
OS_LIKE=""
if [ -f /etc/os-release ]; then
  OS_ID=$(. /etc/os-release; printf '%s' "${ID:-unknown}")
  OS_LIKE=$(. /etc/os-release; printf '%s' "${ID_LIKE:-}")
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

# ---------------------------------------------------------------- uninstall
ask_keep_or_drop() {
  # Prints 1 to drop, 0 to keep. Default is keep.
  local label="$1" current="$2" reply
  if [ -n "$current" ]; then
    printf '%s' "$current"
    return
  fi
  if [ "$UNATTENDED" -eq 1 ]; then
    printf '0'
    return
  fi
  printf 'Keep %s? [Y/n] ' "$label" >&2
  if [ -t 0 ]; then
    read -r reply || true
  else
    read -r reply </dev/tty || true
  fi
  case "$reply" in
    [Nn]|[Nn][Oo]) printf '1' ;;
    *) printf '0' ;;
  esac
}

uninstall_orion() {
  echo "== Orion Belt server uninstall =="
  echo "   distro: $OS_ID  init: $init_system"

  if [ "$UNATTENDED" -eq 0 ] && ! can_prompt; then
    die "no TTY for prompts; pass --unattended with --keep-*/--drop-* flags"
  fi

  DROP_DB=$(ask_keep_or_drop "Postgres database/role '${DB_NAME}'" "$DROP_DB")
  DROP_LOGS=$(ask_keep_or_drop "logs in ${VAR_LOG}" "$DROP_LOGS")
  DROP_DATA=$(ask_keep_or_drop "recordings/data in ${VAR_LIB}" "$DROP_DATA")

  echo
  echo "── Stopping service ──"
  case "$init_system" in
    systemd)
      systemctl disable --now orion-belt-server 2>/dev/null || true
      ;;
    openrc)
      rc-service orion-belt-server stop 2>/dev/null || true
      rc-update del orion-belt-server default 2>/dev/null || true
      ;;
  esac

  echo "── Removing package / binary ──"
  if dpkg -l orion-belt 2>/dev/null | grep -q '^ii'; then
    apt-get remove -y orion-belt || dpkg -r orion-belt || true
  elif rpm -q orion-belt >/dev/null 2>&1; then
    if have dnf; then dnf -y remove orion-belt
    elif have yum; then yum -y remove orion-belt
    else rpm -e orion-belt
    fi
  elif have apk && apk info -e orion-belt >/dev/null 2>&1; then
    apk del orion-belt || true
  fi
  rm -f "$BIN_PATH"
  rm -f /lib/systemd/system/orion-belt-server.service \
        /usr/lib/systemd/system/orion-belt-server.service \
        /etc/init.d/orion-belt-server
  if [ "$init_system" = "systemd" ]; then
    systemctl daemon-reload 2>/dev/null || true
  fi

  echo "── Removing config (${ETC_DIR}) ──"
  rm -rf "$ETC_DIR"

  if [ "$DROP_LOGS" = "1" ]; then
    echo "── Removing logs (${VAR_LOG}) ──"
    rm -rf "$VAR_LOG"
  else
    echo "── Keeping logs (${VAR_LOG}) ──"
  fi

  if [ "$DROP_DATA" = "1" ]; then
    echo "── Removing data (${VAR_LIB}) ──"
    rm -rf "$VAR_LIB"
  else
    echo "── Keeping data (${VAR_LIB}) ──"
  fi

  if [ "$DROP_DB" = "1" ]; then
    echo "── Dropping Postgres database/role ${DB_NAME}/${DB_USER} ──"
    if have psql && (have runuser || have su); then
      if have runuser; then
        runuser -u postgres -- psql -v ON_ERROR_STOP=0 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${DB_NAME}' AND pid <> pg_backend_pid();" >/dev/null 2>&1 || true
        runuser -u postgres -- psql -v ON_ERROR_STOP=0 -c "DROP DATABASE IF EXISTS ${DB_NAME};" || true
        runuser -u postgres -- psql -v ON_ERROR_STOP=0 -c "DROP ROLE IF EXISTS ${DB_USER};" || true
      else
        su -s /bin/sh postgres -c "psql -c \"DROP DATABASE IF EXISTS ${DB_NAME};\"" || true
        su -s /bin/sh postgres -c "psql -c \"DROP ROLE IF EXISTS ${DB_USER};\"" || true
      fi
      echo "   dropped ${DB_NAME} / ${DB_USER}"
    else
      echo "   ! could not drop DB (psql/postgres user missing) — do it manually"
    fi
  else
    echo "── Keeping Postgres database/role ${DB_NAME}/${DB_USER} ──"
  fi

  # Leave the OS user if data/logs remain (ownership); remove only when both gone.
  if [ "$DROP_DATA" = "1" ] && [ "$DROP_LOGS" = "1" ]; then
    if getent passwd orionbelt >/dev/null 2>&1; then
      userdel orionbelt 2>/dev/null || deluser orionbelt 2>/dev/null || true
    fi
    if getent group orionbelt >/dev/null 2>&1; then
      groupdel orionbelt 2>/dev/null || delgroup orionbelt 2>/dev/null || true
    fi
  fi

  echo
  echo "Orion Belt server removed."
  echo "Note: PostgreSQL packages were left installed. To remove them too:"
  case "$OS_ID" in
    debian|ubuntu|raspbian) echo "  sudo apt-get remove --purge postgresql postgresql-*" ;;
    *) echo "  use your package manager to remove postgresql" ;;
  esac
  exit 0
}

if [ "$DO_UNINSTALL" -eq 1 ]; then
  uninstall_orion
fi

echo "== Orion Belt server install =="
echo "   distro: $OS_ID (like: ${OS_LIKE:-n/a})"
echo "   arch:   $ARCH"
echo "   init:   $init_system"

# -------------------------------------------------------------- resolve ver
resolve_version() {
  local v="$1" ver=""
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

# -------------------------------------------------------------- postgres
postgres_installed() {
  have psql || have postgres || dpkg -l postgresql 2>/dev/null | grep -q '^ii' ||
    rpm -q postgresql-server >/dev/null 2>&1 || rpm -q postgresql15-server >/dev/null 2>&1 ||
    [ -x /usr/lib/postgresql/*/bin/psql ] 2>/dev/null
}

postgres_running() {
  if have pg_isready; then
    pg_isready -h 127.0.0.1 -q 2>/dev/null && return 0
    pg_isready -q 2>/dev/null && return 0
  fi
  if [ "$init_system" = "systemd" ]; then
    systemctl is-active --quiet postgresql 2>/dev/null && return 0
    systemctl is-active --quiet postgresql@* 2>/dev/null && return 0
  fi
  if [ "$init_system" = "openrc" ]; then
    rc-service postgresql status >/dev/null 2>&1 && return 0
  fi
  return 1
}

run_as_postgres() {
  # Run a command as the OS postgres superuser (peer auth).
  if have runuser; then
    runuser -u postgres -- "$@"
  else
    # Flatten for su -c; prefer runuser when available.
    su -s /bin/sh postgres -c "$*"
  fi
}

ensure_postgres_packages() {
  echo "   installing PostgreSQL packages for $OS_ID"
  case "$OS_ID" in
    debian|ubuntu|raspbian)
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq
      apt-get install -y postgresql
      apt-get install -y postgresql-contrib 2>/dev/null || true
      ;;
    rhel|centos|rocky|almalinux|fedora|ol)
      if have dnf; then
        dnf -y install postgresql-server postgresql
      else
        yum -y install postgresql-server postgresql
      fi
      if [ ! -d /var/lib/pgsql/data ] && [ ! -f /var/lib/pgsql/data/PG_VERSION ]; then
        if have postgresql-setup; then
          postgresql-setup --initdb 2>/dev/null || postgresql-setup initdb 2>/dev/null || true
        elif [ -x /usr/bin/postgresql-setup ]; then
          /usr/bin/postgresql-setup --initdb || true
        fi
      fi
      ;;
    opensuse*|sles)
      zypper -n install postgresql-server postgresql
      ;;
    alpine)
      apk add --no-cache postgresql postgresql-contrib
      if [ ! -d /var/lib/postgresql/data ] || [ -z "$(ls -A /var/lib/postgresql/data 2>/dev/null)" ]; then
        mkdir -p /var/lib/postgresql/data
        chown postgres:postgres /var/lib/postgresql/data
        run_as_postgres initdb -D /var/lib/postgresql/data
      fi
      ;;
    *)
      die "automatic Postgres install is not supported on $OS_ID — install Postgres yourself and pass --db-url"
      ;;
  esac
}

start_postgres_service() {
  case "$init_system" in
    systemd)
      # Debian/Ubuntu use "postgresql"; RHEL often "postgresql" after setup.
      if systemctl list-unit-files | grep -q '^postgresql\.service'; then
        systemctl enable --now postgresql
      elif systemctl list-unit-files | grep -qE '^postgresql@[0-9]'; then
        # Prefer the highest installed cluster unit if present.
        unit=$(systemctl list-unit-files 'postgresql@*.service' --no-legend 2>/dev/null | awk '{print $1}' | sort -V | tail -1)
        [ -n "$unit" ] || unit="postgresql"
        systemctl enable --now "$unit" 2>/dev/null || systemctl enable --now postgresql
      else
        systemctl enable --now postgresql 2>/dev/null || \
          systemctl enable --now postgresql-14 2>/dev/null || \
          systemctl enable --now postgresql-15 2>/dev/null || \
          systemctl enable --now postgresql-16 2>/dev/null || \
          die "could not start a postgresql systemd unit"
      fi
      ;;
    openrc)
      rc-update add postgresql default 2>/dev/null || true
      rc-service postgresql start || /etc/init.d/postgresql start
      ;;
    *)
      die "no systemd/OpenRC — start Postgres manually, then re-run with --db-url"
      ;;
  esac

  printf '   waiting for Postgres'
  for _ in $(seq 1 45); do
    if postgres_running; then
      printf ' ok\n'
      return 0
    fi
    printf '.'
    sleep 1
  done
  printf '\n'
  die "Postgres did not become ready — check: journalctl -u postgresql -e"
}

# Run psql as the OS postgres superuser. Extra args are passed to psql.
psql_as_postgres() {
  if have runuser; then
    runuser -u postgres -- psql "$@"
  else
    # Join remaining args carefully for su -c
    su -s /bin/sh postgres -c "psql $(printf '%q ' "$@")"
  fi
}

orionbelt_db_exists() {
  postgres_running || return 1
  psql_as_postgres -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" 2>/dev/null | grep -q 1
}

saved_local_dsn() {
  local pw
  if [ -f /etc/orion-belt/db.password ]; then
    pw=$(tr -d '\n' </etc/orion-belt/db.password)
    [ -n "$pw" ] || return 1
    printf 'postgres://%s:%s@127.0.0.1:5432/%s?sslmode=disable' "$DB_USER" "$pw" "$DB_NAME"
    return 0
  fi
  return 1
}

# Drop and recreate the orionbelt role + database (destructive).
recreate_orionbelt_db() {
  local pw="$1"
  echo "   dropping existing ${DB_NAME} / ${DB_USER} (if present)"
  psql_as_postgres -v ON_ERROR_STOP=0 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${DB_NAME}' AND pid <> pg_backend_pid();" >/dev/null 2>&1 || true
  psql_as_postgres -v ON_ERROR_STOP=0 -c "DROP DATABASE IF EXISTS ${DB_NAME};" || true
  psql_as_postgres -v ON_ERROR_STOP=0 -c "DROP ROLE IF EXISTS ${DB_USER};" || true
  psql_as_postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE ${DB_USER} LOGIN PASSWORD '${pw}';"
  psql_as_postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};"
}

# Install (if needed), start, and create role+database. Sets DB_URL.
# RECREATE_DB=1 drops an existing orionbelt DB/role first.
provision_local_postgres() {
  local pw
  if ! postgres_installed; then
    ensure_postgres_packages
  else
    echo "   PostgreSQL packages already present"
  fi
  if ! postgres_running; then
    start_postgres_service
  else
    echo "   PostgreSQL already running"
  fi

  pw=$(openssl rand -hex 24)

  if [ "$RECREATE_DB" -eq 1 ] && orionbelt_db_exists; then
    recreate_orionbelt_db "$pw"
    echo "   recreated role/database ${DB_USER}/${DB_NAME}"
  else
    # Idempotent: create role/db if missing; always set password to the one in the DSN.
    if have runuser; then
      runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "DO \$\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${DB_USER}') THEN
    CREATE ROLE ${DB_USER} LOGIN PASSWORD '${pw}';
  ELSE
    ALTER ROLE ${DB_USER} WITH LOGIN PASSWORD '${pw}';
  END IF;
END
\$\$;"
      if ! runuser -u postgres -- psql -tAc "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'" | grep -q 1; then
        runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};"
      else
        runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "ALTER DATABASE ${DB_NAME} OWNER TO ${DB_USER};" || true
      fi
    else
      su -s /bin/sh postgres -c "psql -v ON_ERROR_STOP=1 -c \"DO \\\$\\\$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '${DB_USER}') THEN
    CREATE ROLE ${DB_USER} LOGIN PASSWORD '${pw}';
  ELSE
    ALTER ROLE ${DB_USER} WITH LOGIN PASSWORD '${pw}';
  END IF;
END
\\\$\\\$;\""
      if ! su -s /bin/sh postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'\"" | grep -q 1; then
        su -s /bin/sh postgres -c "psql -v ON_ERROR_STOP=1 -c \"CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};\""
      fi
    fi
    echo "   ensured role/database ${DB_USER}/${DB_NAME}"
  fi

  DB_URL="postgres://${DB_USER}:${pw}@127.0.0.1:5432/${DB_NAME}?sslmode=disable"
  mkdir -p /etc/orion-belt
  umask 077
  printf '%s\n' "$pw" > /etc/orion-belt/db.password
  chmod 600 /etc/orion-belt/db.password
  echo "   password saved in /etc/orion-belt/db.password (also embedded in server.yaml)"
}

# Interactive database selection. Sets INSTALL_POSTGRES and/or DB_URL (and maybe RECREATE_DB).
choose_database() {
  local saved="" have_pg=0 have_db=0 choice

  if postgres_installed; then
    have_pg=1
    postgres_running || start_postgres_service 2>/dev/null || true
  fi
  if [ "$have_pg" -eq 1 ] && orionbelt_db_exists; then
    have_db=1
  fi
  saved=$(saved_local_dsn 2>/dev/null || true)

  if [ "$INSTALL_POSTGRES" -eq 1 ] && [ -n "$DB_URL" ]; then
    die "pass either --install-postgres or --db-url, not both"
  fi

  # Flag already chose a path.
  if [ -n "$DB_URL" ]; then
    DB_URL=$(prompt "Postgres DSN" "$DB_URL")
    return
  fi
  if [ "$INSTALL_POSTGRES" -eq 1 ]; then
    if [ "$have_db" -eq 1 ] && [ "$RECREATE_DB" -ne 1 ]; then
      if [ "$UNATTENDED" -eq 1 ]; then
        die "local database '${DB_NAME}' already exists; pass --recreate-db or --db-url"
      fi
      echo "   PostgreSQL is already installed and database '${DB_NAME}' exists."
      echo "  1) Reuse it (paste DSN${saved:+, or keep saved password})"
      echo "  2) Recreate '${DB_NAME}' (DESTROYS existing data in that database)"
      echo "  3) Use a different Postgres (paste a DSN)"
      choice=$(prompt "Choice" "1")
      case "$choice" in
        2) RECREATE_DB=1; INSTALL_POSTGRES=1 ;;
        3)
          INSTALL_POSTGRES=0
          DB_URL=$(prompt "Postgres DSN" "postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable")
          ;;
        1|*)
          INSTALL_POSTGRES=0
          if [ -n "$saved" ] && confirm "Reuse saved DSN from /etc/orion-belt/db.password"; then
            DB_URL="$saved"
            echo "   reusing saved local DSN"
          else
            DB_URL=$(prompt "Postgres DSN" "${saved:-postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable}")
          fi
          ;;
      esac
    else
      echo "   will provision local Postgres (--install-postgres)"
      INSTALL_POSTGRES=1
    fi
    return
  fi

  # No flags — interactive menu shaped by what we detect.
  if [ "$have_pg" -eq 1 ] && [ "$have_db" -eq 1 ]; then
    echo "  PostgreSQL is already installed; database '${DB_NAME}' already exists."
    echo "  1) Reuse it (paste DSN${saved:+, or keep saved password})"
    echo "  2) Recreate '${DB_NAME}' on this host (DESTROYS existing data in that database)"
    echo "  3) Use a different Postgres (paste a DSN)"
    choice=$(prompt "Choice" "1")
    case "$choice" in
      2) INSTALL_POSTGRES=1; RECREATE_DB=1 ;;
      3)
        DB_URL=$(prompt "Postgres DSN" "postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable")
        ;;
      1|*)
        if [ -n "$saved" ] && confirm "Reuse saved DSN from /etc/orion-belt/db.password"; then
          DB_URL="$saved"
          echo "   reusing saved local DSN"
        else
          DB_URL=$(prompt "Postgres DSN" "${saved:-postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable}")
        fi
        ;;
    esac
  elif [ "$have_pg" -eq 1 ]; then
    echo "  PostgreSQL is already installed (no '${DB_NAME}' database yet)."
    echo "  1) Create the '${DB_NAME}' role + database on this instance"
    echo "  2) Use a different Postgres (paste a DSN)"
    choice=$(prompt "Choice" "1")
    case "$choice" in
      2) DB_URL=$(prompt "Postgres DSN" "postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable") ;;
      1|*) INSTALL_POSTGRES=1 ;;
    esac
  else
    echo "  1) Install PostgreSQL on this host and create the '${DB_NAME}' DB (recommended)"
    echo "  2) Use an existing Postgres (paste a DSN)"
    choice=$(prompt "Choice" "1")
    case "$choice" in
      2) DB_URL=$(prompt "Postgres DSN" "postgres://orionbelt:CHANGE_ME@127.0.0.1:5432/orionbelt?sslmode=disable") ;;
      1|*) INSTALL_POSTGRES=1 ;;
    esac
  fi
}

# -------------------------------------------------------------- interactive
if [ "$UNATTENDED" -eq 1 ]; then
  [ -n "$PUBLIC_URL" ] || die "--public-url is required with --unattended"
  if [ "$INSTALL_POSTGRES" -eq 1 ] && [ -n "$DB_URL" ]; then
    die "pass either --install-postgres or --db-url, not both"
  fi
  if [ "$INSTALL_POSTGRES" -eq 0 ] && [ -z "$DB_URL" ]; then
    die "pass --db-url or --install-postgres with --unattended"
  fi
  if [ "$INSTALL_POSTGRES" -eq 1 ] && postgres_installed && postgres_running && orionbelt_db_exists && [ "$RECREATE_DB" -ne 1 ]; then
    die "local database '${DB_NAME}' already exists; pass --recreate-db or --db-url"
  fi
  [ -n "$JWT_SECRET" ] || die "--jwt-secret is required with --unattended"
  [ -n "$ADMIN_EMAIL" ] || ADMIN_EMAIL="admin@$(hostname -f 2>/dev/null || echo localhost)"
  if [ -z "$ADMIN_KEY_FILE" ] && [ -z "$ADMIN_KEY_GENERATE" ]; then
    die "provide --admin-key-file or --admin-key-generate with --unattended"
  fi
else
  if ! can_prompt; then
    die "no TTY for prompts; pass --unattended with flags, or run: bash <(curl -fsSL …/install-server.sh)"
  fi
  echo
  echo "── Public address ──"
  echo "  Origin browsers open and (by default) the host agents dial."
  echo "  Bare IP with no port → http uses :80, https uses :443 (gateway listen port matches)."
  PUBLIC_URL=$(prompt "Public URL (e.g. https://orion.example.com or http://192.168.0.10)" "${PUBLIC_URL:-}")
  [ -n "$PUBLIC_URL" ] || die "public URL is required"
  PUBLIC_URL=${PUBLIC_URL%/}
  PUBLIC_URL=$(normalize_public_url "$PUBLIC_URL")
  echo "   using $PUBLIC_URL"
  scheme="${PUBLIC_URL%%://*}"
  _url_port=$(public_url_port "$PUBLIC_URL")
  if [ -z "$API_PORT" ]; then
    if [ -n "$_url_port" ]; then
      API_PORT="$_url_port"
    else
      API_PORT=$(default_port_for_scheme "$scheme")
    fi
  fi
  API_PORT=$(prompt "API listen port (gateway binds here; should match the public URL)" "$API_PORT")
  [ -n "$API_PORT" ] || die "API port is required"
  # If the public URL already had an explicit port and the operator changed the listen port, realign.
  if [ -n "$_url_port" ] && [ "$API_PORT" != "$_url_port" ]; then
    _host=$(public_url_host "$PUBLIC_URL")
    case "$scheme:$API_PORT" in
      http:80|https:443) PUBLIC_URL="${scheme}://${_host}" ;;
      *) PUBLIC_URL="${scheme}://${_host}:${API_PORT}" ;;
    esac
    echo "   public URL → $PUBLIC_URL (port aligned with API listen)"
  fi
  def_host=$(public_url_host "$PUBLIC_URL")
  PUBLIC_SSH_HOST=$(prompt "Public SSH host (agents)" "${PUBLIC_SSH_HOST:-$def_host}")
  PUBLIC_SSH_PORT=$(prompt "Public SSH port" "${PUBLIC_SSH_PORT:-2222}")

  configure_webauthn_rp_id "$(public_url_host "$PUBLIC_URL")"
  # WebAuthn may rewrite PUBLIC_URL to a hostname; keep API_PORT.
  echo
  echo "── Database ──"
  choose_database

  echo
  echo "── Secrets ──"
  if [ -z "$JWT_SECRET" ]; then
    printf 'Generate a random JWT secret now? [Y/n] ' >&2
    if [ -t 0 ]; then read -r _jw || true; else read -r _jw </dev/tty || true; fi
    case "${_jw:-Y}" in
      [Nn]|[Nn][Oo])
        JWT_SECRET=$(prompt "JWT secret (openssl rand -hex 32)" "")
        ;;
      *)
        JWT_SECRET=$(openssl rand -hex 32)
        echo "   generated jwt_secret"
        ;;
    esac
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
      _key_def=/root
      if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
        _sudo_home=$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6)
        if [ -n "$_sudo_home" ] && [ -d "$_sudo_home" ]; then
          _key_def="${_sudo_home}/.orion-belt"
        fi
      elif [ -n "${HOME:-}" ] && [ "$HOME" != "/root" ]; then
        _key_def="${HOME}/.orion-belt"
      fi
      ADMIN_KEY_GENERATE=$(prompt "Directory for new keypair" "${ADMIN_KEY_GENERATE:-$_key_def}")
      ;;
  esac
fi

PUBLIC_URL=${PUBLIC_URL%/}
PUBLIC_URL=$(normalize_public_url "$PUBLIC_URL")
if [ -z "$PUBLIC_SSH_HOST" ]; then
  PUBLIC_SSH_HOST=$(public_url_host "$PUBLIC_URL")
fi
[ -n "$ADMIN_EMAIL" ] || ADMIN_EMAIL="admin@localhost"

# Resolve API listen port (http→80, https→443 when URL has no explicit port).
_scheme="${PUBLIC_URL%%://*}"
_url_port=$(public_url_port "$PUBLIC_URL")
if [ -z "$API_PORT" ]; then
  if [ -n "$_url_port" ]; then
    API_PORT="$_url_port"
  else
    API_PORT=$(default_port_for_scheme "$_scheme")
  fi
fi
case "$API_PORT" in
  ''|*[!0-9]*) die "invalid API port: $API_PORT" ;;
esac

# Unattended path (and any run that skipped the interactive WebAuthn block).
if [ -z "$WEBAUTHN_ENABLED" ]; then
  configure_webauthn_rp_id "$(public_url_host "$PUBLIC_URL")"
fi
# WebAuthn may rewrite PUBLIC_URL; keep WEBAUTHN_ORIGIN in sync for the yaml.
[ -n "${WEBAUTHN_ORIGIN:-}" ] || WEBAUTHN_ORIGIN="$PUBLIC_URL"

if [ "$INSTALL_POSTGRES" -eq 1 ]; then
  echo
  echo "── Provisioning local PostgreSQL ──"
  provision_local_postgres
fi
[ -n "$DB_URL" ] || die "database URL is empty"

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
  # If we generated under a sudo user's home, give them ownership.
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
    _sudo_home=$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6)
    case "$key_priv" in
      "${_sudo_home}"/*)
        chown "$SUDO_USER:$SUDO_USER" "$key_priv" "${key_priv}.pub" 2>/dev/null || chown "$SUDO_USER" "$key_priv" "${key_priv}.pub" 2>/dev/null || true
        ;;
    esac
  fi
  ADMIN_KEY_FILE="${key_priv}.pub"
fi
[ -n "$ADMIN_KEY_FILE" ] || die "admin public key is required"
[ -f "$ADMIN_KEY_FILE" ] || die "admin key file not found: $ADMIN_KEY_FILE"

# ---------------------------------------------------------------- install
install_via_package() {
  local ver="$1" tmp cand
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  local gh="https://github.com/${GITHUB_REPO}/releases/download/v${ver}"
  local base="${PKG_BASE%/}"

  try_deb() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt.deb" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        # Avoid interactive conffile prompts when a previous run left server.yaml.
        DEBIAN_FRONTEND=noninteractive dpkg \
          --force-confdef --force-confold \
          -i "$tmp/orion-belt.deb" \
          || DEBIAN_FRONTEND=noninteractive apt-get install -f -y -o Dpkg::Options::=--force-confold
        return 0
      fi
    done
    return 1
  }
  try_rpm() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt.rpm" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        if have dnf; then dnf -y install "$tmp/orion-belt.rpm"
        elif have yum; then yum -y install "$tmp/orion-belt.rpm"
        else rpm -Uvh "$tmp/orion-belt.rpm"
        fi
        return 0
      fi
    done
    return 1
  }
  try_apk() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt.apk" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        apk add --allow-untrusted "$tmp/orion-belt.apk"
        return 0
      fi
    done
    return 1
  }

  case "$OS_ID" in
    debian|ubuntu|raspbian)
      try_deb \
        "${gh}/orion-belt_${ver}_linux_${ARCH}.deb" \
        "${base}/orion-belt_${ver}_linux_${ARCH}.deb" \
        "${base}/orion-belt_${ver}_${ARCH}.deb"
      return $?
      ;;
    rhel|centos|rocky|almalinux|fedora|ol|opensuse*|sles)
      try_rpm \
        "${gh}/orion-belt_${ver}_linux_${ARCH}.rpm" \
        "${gh}/orion-belt_${ver}_linux_${RPM_ARCH}.rpm" \
        "${base}/orion-belt_${ver}_linux_${ARCH}.rpm" \
        "${base}/orion-belt_${ver}_linux_${RPM_ARCH}.rpm" \
        "${base}/orion-belt_${ver}_${RPM_ARCH}.rpm"
      return $?
      ;;
    alpine)
      try_apk \
        "${gh}/orion-belt_${ver}_linux_${ARCH}.apk" \
        "${base}/orion-belt_${ver}_linux_${ARCH}.apk" \
        "${base}/orion-belt_${ver}_x86_64.apk"
      return $?
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
  # orionbelt must own /etc/orion-belt (mode 750) or it cannot read server.yaml.
  chown -R orionbelt:orionbelt /var/lib/orion-belt /var/log/orion-belt /etc/orion-belt 2>/dev/null || true
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

# Best-effort CLI tools (osh / ocp / oadmin) so client.yaml is immediately usable.
install_tools_package() {
  local ver="$1" tmp cand
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' RETURN
  local gh="https://github.com/${GITHUB_REPO}/releases/download/v${ver}"
  local base="${PKG_BASE%/}"

  try_tools_deb() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt-tools.deb" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        DEBIAN_FRONTEND=noninteractive dpkg \
          --force-confdef --force-confold \
          -i "$tmp/orion-belt-tools.deb" \
          || DEBIAN_FRONTEND=noninteractive apt-get install -f -y -o Dpkg::Options::=--force-confold
        return 0
      fi
    done
    return 1
  }
  try_tools_rpm() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt-tools.rpm" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        if have dnf; then dnf -y install "$tmp/orion-belt-tools.rpm"
        elif have yum; then yum -y install "$tmp/orion-belt-tools.rpm"
        else rpm -Uvh "$tmp/orion-belt-tools.rpm"
        fi
        return 0
      fi
    done
    return 1
  }
  try_tools_apk() {
    for cand in "$@"; do
      if curl -fsSL -o "$tmp/orion-belt-tools.apk" "$cand" 2>/dev/null; then
        echo "   installing $cand"
        apk add --allow-untrusted "$tmp/orion-belt-tools.apk"
        return 0
      fi
    done
    return 1
  }

  case "$OS_ID" in
    debian|ubuntu|raspbian)
      try_tools_deb \
        "${gh}/orion-belt-tools_${ver}_linux_${ARCH}.deb" \
        "${gh}/orion-belt-tools_${ver}_${ARCH}.deb" \
        "${gh}/orion-belt_tools_${ver}_linux_${ARCH}.deb" \
        "${base}/orion-belt-tools_${ver}_linux_${ARCH}.deb" \
        "${base}/orion-belt-tools_${ver}_${ARCH}.deb"
      return $?
      ;;
    rhel|centos|rocky|almalinux|fedora|ol|opensuse*|sles)
      try_tools_rpm \
        "${gh}/orion-belt-tools_${ver}_linux_${ARCH}.rpm" \
        "${gh}/orion-belt-tools_${ver}_linux_${RPM_ARCH}.rpm" \
        "${gh}/orion-belt-tools_${ver}_${ARCH}.rpm" \
        "${base}/orion-belt-tools_${ver}_linux_${ARCH}.rpm" \
        "${base}/orion-belt-tools_${ver}_${RPM_ARCH}.rpm"
      return $?
      ;;
    alpine)
      try_tools_apk \
        "${gh}/orion-belt-tools_${ver}_linux_${ARCH}.apk" \
        "${base}/orion-belt-tools_${ver}_linux_${ARCH}.apk"
      return $?
      ;;
    *) return 1 ;;
  esac
}

if [ "$BINARY_ONLY" -eq 0 ]; then
  echo
  echo "── Installing orion-belt-tools (osh / ocp / oadmin) ──"
  if install_tools_package "$VERSION"; then
    echo "   osh installed"
  else
    echo "   ! tools package not found for $VERSION — install orion-belt-tools later, or use a release tarball"
  fi
fi
have orion-belt-server || [ -x "$BIN_PATH" ] || die "orion-belt-server not installed"

# Ensure the service account can traverse /etc/orion-belt (created earlier as root
# during Postgres provisioning with mode 750).
ensure_orion_perms() {
  if ! getent passwd orionbelt >/dev/null 2>&1; then
    groupadd --system orionbelt 2>/dev/null || addgroup -S orionbelt 2>/dev/null || true
    useradd --system --gid orionbelt --home-dir /var/lib/orion-belt --shell /usr/sbin/nologin orionbelt \
      2>/dev/null || adduser -S -G orionbelt -h /var/lib/orion-belt -s /sbin/nologin orionbelt 2>/dev/null || true
  fi
  mkdir -p "$ETC_DIR" "$VAR_LIB/recordings" "$VAR_LOG"
  chown -R orionbelt:orionbelt "$ETC_DIR" "$VAR_LIB" "$VAR_LOG" 2>/dev/null || true
  chmod 750 "$ETC_DIR" "$VAR_LIB" "$VAR_LOG"
  if [ -f "$CONFIG_PATH" ]; then
    chown orionbelt:orionbelt "$CONFIG_PATH"
    chmod 640 "$CONFIG_PATH"
  fi
  if [ -f "$ETC_DIR/db.password" ]; then
    chown root:orionbelt "$ETC_DIR/db.password" 2>/dev/null || chown orionbelt:orionbelt "$ETC_DIR/db.password"
    chmod 640 "$ETC_DIR/db.password"
  fi
  if [ -f "$ETC_DIR/ssh_host_key" ]; then
    chown orionbelt:orionbelt "$ETC_DIR/ssh_host_key" "$ETC_DIR/ssh_host_key.pub" 2>/dev/null || true
    chmod 600 "$ETC_DIR/ssh_host_key"
  fi
}
ensure_orion_perms

# ---------------------------------------------------------------- config
[ -n "$WEBAUTHN_ENABLED" ] || WEBAUTHN_ENABLED=false
[ -n "$WEBAUTHN_RP_ID" ] || WEBAUTHN_RP_ID="localhost"
[ -n "${WEBAUTHN_ORIGIN:-}" ] || WEBAUTHN_ORIGIN="$PUBLIC_URL"
echo
echo "── Writing $CONFIG_PATH ──"
# Installer always writes the generated config — no conffile tug-of-war prompt.
cat > "$CONFIG_PATH" <<EOF
server:
  host: "0.0.0.0"
  port: 2222
  api_port: ${API_PORT}
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
    enabled: ${WEBAUTHN_ENABLED}
    rp_display_name: "Orion Belt"
    rp_id: "${WEBAUTHN_RP_ID}"
    origins:
      - "${WEBAUTHN_ORIGIN}"

recording:
  enabled: true
  storage_path: "/var/lib/orion-belt/recordings"
  retention_days: 90
  compression: gzip
  encryption_key: ""

plugins: {}
EOF
ensure_orion_perms
echo "   wrote $CONFIG_PATH"

# Stage admin pubkey where the orionbelt user can always read it (home dirs often cannot).
SETUP_KEY_FILE="$ETC_DIR/admin.pub"
cp -f "$ADMIN_KEY_FILE" "$SETUP_KEY_FILE"
chown orionbelt:orionbelt "$SETUP_KEY_FILE"
chmod 644 "$SETUP_KEY_FILE"

# ---------------------------------------------------------------- service
if [ "$SKIP_START" -eq 0 ]; then
  echo
  echo "── Starting service ($init_system) ──"
  case "$init_system" in
    systemd)
      systemctl enable orion-belt-server
      systemctl restart orion-belt-server
      ;;
    openrc)
      rc-update add orion-belt-server default 2>/dev/null || true
      rc-service orion-belt-server restart 2>/dev/null || rc-service orion-belt-server start || /etc/init.d/orion-belt-server start
      ;;
    *)
      echo "   ! no systemd/OpenRC detected — start manually:"
      echo "       $BIN_PATH -c $CONFIG_PATH"
      ;;
  esac

  printf '   waiting for health'
  ok=0
  for _ in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:${API_PORT}/health" >/dev/null 2>&1; then
      # Prefer a live unit over a stale process still bound to the port.
      if [ "$init_system" = "systemd" ]; then
        if systemctl is-active --quiet orion-belt-server; then
          ok=1
          printf ' ok\n'
          break
        fi
      else
        ok=1
        printf ' ok\n'
        break
      fi
    fi
    printf '.'
    sleep 1
  done
  if [ "$ok" -ne 1 ]; then
    printf '\n'
    echo "error: gateway did not become healthy on :${API_PORT}" >&2
    if [ "$init_system" = "systemd" ]; then
      systemctl --no-pager --full status orion-belt-server >&2 || true
      journalctl -u orion-belt-server -n 40 --no-pager >&2 || true
      # Common lab footgun: bad WebAuthn rp_id bricks v1.1.0 before soft-fail existed.
      if [ "${WEBAUTHN_ENABLED}" = "true" ]; then
        echo "   retrying once with WebAuthn disabled (SSH-key login still works)…" >&2
        WEBAUTHN_ENABLED=false
        WEBAUTHN_RP_ID="localhost"
        WEBAUTHN_ORIGIN="$PUBLIC_URL"
        cat > "$CONFIG_PATH" <<EOF
server:
  host: "0.0.0.0"
  port: 2222
  api_port: ${API_PORT}
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
    enabled: false
    rp_display_name: "Orion Belt"
    rp_id: "localhost"
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
        ensure_orion_perms
        systemctl restart orion-belt-server || true
        for _ in $(seq 1 20); do
          if systemctl is-active --quiet orion-belt-server \
            && curl -sf "http://127.0.0.1:${API_PORT}/health" >/dev/null 2>&1; then
            ok=1
            echo "   health ok (WebAuthn left disabled — set a dotted rp_id later if needed)"
            break
          fi
          sleep 1
        done
      fi
    fi
    if [ "$ok" -ne 1 ]; then
      die "fix permissions / config, then: systemctl restart orion-belt-server"
    fi
  fi
fi

# ---------------------------------------------------------------- setup
if [ "$SKIP_SETUP" -eq 0 ]; then
  echo
  echo "── First admin (setup wizard) ──"
  export ORION_SETUP_ADMIN_NAME="$ADMIN_NAME"
  export ORION_SETUP_ADMIN_EMAIL="$ADMIN_EMAIL"
  export ORION_SETUP_ADMIN_KEY_FILE="$SETUP_KEY_FILE"
  export ORION_SETUP_PUBLIC_URL="$PUBLIC_URL"
  export ORION_SETUP_PUBLIC_SSH_HOST="$PUBLIC_SSH_HOST"
  export ORION_SETUP_PUBLIC_SSH_PORT="$PUBLIC_SSH_PORT"
  # Run as root: setup only needs DB access; Avoids home-dir permission traps.
  orion-belt-server -c "$CONFIG_PATH" setup </dev/null
fi

# ---------------------------------------------------------------- osh client.yaml
# Resolve the private key that matches the admin pubkey we registered.
admin_private_key() {
  local pub="$ADMIN_KEY_FILE" priv
  if [ -n "${ADMIN_KEY_GENERATE:-}" ]; then
    priv="${ADMIN_KEY_GENERATE%/}/orion-belt-admin"
    [ -f "$priv" ] && { printf '%s' "$priv"; return; }
  fi
  case "$pub" in
    *.pub)
      priv="${pub%.pub}"
      [ -f "$priv" ] && { printf '%s' "$priv"; return; }
      ;;
  esac
  if [ -n "${ADMIN_KEY_GENERATE:-}" ]; then
    printf '%s' "${ADMIN_KEY_GENERATE%/}/orion-belt-admin"
  else
    printf '%s' "${pub%.pub}"
  fi
}

# Copy the admin private key into dest_dir as orion-belt-admin (600, owned by owner).
# Prints the absolute path to the private key. No-op copy if already there.
install_admin_key_for_user() {
  local src_priv="$1" dest_dir="$2" owner="$3"
  local dest_priv="${dest_dir%/}/orion-belt-admin"
  mkdir -p "$dest_dir"
  if [ ! -f "$src_priv" ]; then
    printf '%s' "$src_priv"
    return 1
  fi
  if [ "$src_priv" != "$dest_priv" ]; then
    cp -f "$src_priv" "$dest_priv"
    if [ -f "${src_priv}.pub" ]; then
      cp -f "${src_priv}.pub" "${dest_priv}.pub"
    fi
  fi
  chmod 600 "$dest_priv"
  [ -f "${dest_priv}.pub" ] && chmod 644 "${dest_priv}.pub" || true
  if [ -n "$owner" ] && id "$owner" >/dev/null 2>&1; then
    chown "$owner:$owner" "$dest_dir" "$dest_priv" 2>/dev/null || chown "$owner" "$dest_dir" "$dest_priv" 2>/dev/null || true
    [ -f "${dest_priv}.pub" ] && { chown "$owner:$owner" "${dest_priv}.pub" 2>/dev/null || chown "$owner" "${dest_priv}.pub" 2>/dev/null || true; }
  fi
  printf '%s' "$dest_priv"
}

# True if path is readable by owner (not just by root running the installer).
user_can_read() {
  local path="$1" owner="$2"
  [ -f "$path" ] || return 1
  if [ -z "$owner" ] || [ "$owner" = "root" ]; then
    [ -r "$path" ]
    return
  fi
  # Run the readability check as the target user — root can always read /root/*.
  if have runuser; then
    runuser -u "$owner" -- test -r "$path" 2>/dev/null
  elif have sudo; then
    sudo -u "$owner" test -r "$path" 2>/dev/null
  else
    # Fallback: reject paths under other users' homes / /root.
    case "$path" in
      /root/*) return 1 ;;
      *) [ -r "$path" ] ;;
    esac
  fi
}

write_osh_client_yaml() {
  local dest="$1" key_path="$2" owner="${3:-}"
  mkdir -p "$(dirname "$dest")"
  cat > "$dest" <<EOF
# Orion Belt client config (generated by install-server.sh).
# Use: osh -c $dest login   (or just: osh login  if this is ~/.orion-belt/client.yaml)
server:
  host: "${PUBLIC_SSH_HOST}"
  port: ${PUBLIC_SSH_PORT}
  api_endpoint: "${PUBLIC_URL}"
auth:
  user: "${ADMIN_NAME}"
  key_file: "${key_path}"
  known_hosts: "~/.ssh/orion_known_hosts"
  strict_host_key_checking: "ask"
EOF
  chmod 600 "$dest"
  if [ -n "$owner" ] && id "$owner" >/dev/null 2>&1; then
    chown "$owner:$owner" "$(dirname "$dest")" "$dest" 2>/dev/null || chown "$owner" "$(dirname "$dest")" "$dest" 2>/dev/null || true
  fi
  echo "   wrote $dest (key_file=$key_path)"
}

echo
echo "── Writing osh client.yaml ──"
CLIENT_KEY=$(admin_private_key)
if [ ! -f "$CLIENT_KEY" ]; then
  echo "   ! private key not found at $CLIENT_KEY — osh will need -i /path/to/private_key" >&2
fi

# Canonical copy under /etc for operators who sudo; root keeps a home copy too.
_etc_key=$(install_admin_key_for_user "$CLIENT_KEY" "/etc/orion-belt" "root" 2>/dev/null || printf '%s' "$CLIENT_KEY")
write_osh_client_yaml "/etc/orion-belt/client.yaml" "/etc/orion-belt/orion-belt-admin" "root"
write_osh_client_yaml "/root/.orion-belt/client.yaml" "${_etc_key}" "root"

CLIENT_YAML_HINT="/root/.orion-belt/client.yaml"
# Invoking sudo user — they must get a key they can actually read (not /root/…).
if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != "root" ]; then
  _sudo_home=$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f6)
  if [ -n "$_sudo_home" ] && [ -d "$_sudo_home" ]; then
    _user_key="$CLIENT_KEY"
    if ! user_can_read "$_user_key" "$SUDO_USER"; then
      _user_key=$(install_admin_key_for_user "$CLIENT_KEY" "${_sudo_home}/.orion-belt" "$SUDO_USER")
      echo "   installed admin private key for $SUDO_USER → ${_sudo_home}/.orion-belt/orion-belt-admin"
    fi
    # Prefer ~ form so the yaml stays valid if their home moves; osh expands it.
    write_osh_client_yaml "${_sudo_home}/.orion-belt/client.yaml" "~/.orion-belt/orion-belt-admin" "$SUDO_USER"
    CLIENT_YAML_HINT="${_sudo_home}/.orion-belt/client.yaml"
  fi
elif [ -n "${HOME:-}" ] && [ "$HOME" != "/root" ] && [ "$(id -u)" -eq 0 ]; then
  : # installed as root without sudo — root config is enough
fi

cat <<EOF

╔══════════════════════════════════════════════════════════════════╗
║  Orion Belt server installed                                     ║
╚══════════════════════════════════════════════════════════════════╝

  UI:          ${PUBLIC_URL}/ui
  Agents dial: ${PUBLIC_SSH_HOST}:${PUBLIC_SSH_PORT}
  Config:      ${CONFIG_PATH}
  Admin pubkey:${ADMIN_KEY_FILE}
  Admin privkey: ${CLIENT_KEY}
  osh config:  ${CLIENT_YAML_HINT}

  Sign in as the installing user (key must be readable by that user):
    osh login
    # or: osh -c ${CLIENT_YAML_HINT} login --code

  Add agents from the console (**Add agent**) — gateway host defaults to
  the public SSH address you just set.

  Docs: https://github.com/orion-belt-dev/orion-belt/blob/master/docs/SETUP.md

EOF
