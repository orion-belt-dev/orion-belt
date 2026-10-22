#!/bin/sh
# Orion Belt installer for Linux, macOS, the BSDs, illumos and Solaris.
#
#   curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh                # clients (osh, ocp, oadmin)
#   curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- agent    # the agent (Linux)
#   curl -fsSL https://pkgs.orion-belt.dev/install.sh | sh -s -- server   # the server (Linux)
#
# Components: tools (default), agent, server; several may be given.
#
# Linux: adds the signed Orion Belt package repository for the system's
# package manager (apt, dnf/yum, zypper, apk, pacman) and installs from it, so
# upgrades come with the system's. Without one, it installs the release
# package, or the release archive under --prefix. The server and agent
# services are installed for the init system in use (systemd, OpenRC, runit,
# SysV) but not started: they need their configuration first.
# Other systems: the clients from the release archive, under --prefix.
#
# Options:
#   --version vX.Y.Z   release to install (default: the latest)
#   --method M         auto (default) | repo | package | binary
#   --prefix DIR       binary installs (default /usr/local)
#   --uninstall        remove the given components (configuration is kept)
#   -y, --yes          do not announce sudo
#
# Environment:
#   ORION_PKGS_URL     package repository (default https://pkgs.orion-belt.dev)
#   ORION_RELEASE_URL  where release files are downloaded from (a mirror, or
#                      file:///path for tests); defaults to GitHub Releases
set -eu

REPO=orion-belt-dev/orion-belt
PKGS=${ORION_PKGS_URL:-https://pkgs.orion-belt.dev}
PKGS=${PKGS%/}
VERSION=""
METHOD=auto
PREFIX=""
UNINSTALL=0
YES=0
COMPONENTS=""

say() { printf '%s\n' "orion-belt-install: $*" >&2; }
die() {
	say "error: $*"
	exit 1
}
have() { command -v "$1" >/dev/null 2>&1; }

while [ $# -gt 0 ]; do
	case $1 in
	tools | agent | server) COMPONENTS="$COMPONENTS $1" ;;
	--version) VERSION=${2:?--version needs a value}; shift ;;
	--version=*) VERSION=${1#*=} ;;
	--method) METHOD=${2:?--method needs a value}; shift ;;
	--method=*) METHOD=${1#*=} ;;
	--prefix) PREFIX=${2:?--prefix needs a value}; shift ;;
	--prefix=*) PREFIX=${1#*=} ;;
	--uninstall) UNINSTALL=1 ;;
	-y | --yes) YES=1 ;;
	-h | --help)
		sed -n '2,31p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*) die "unknown argument $1 (see --help)" ;;
	esac
	shift
done
[ -n "$COMPONENTS" ] || COMPONENTS=tools
case $METHOD in auto | repo | package | binary) ;; *) die "--method must be auto, repo, package or binary" ;; esac
case $VERSION in "" | v*) ;; *) VERSION=v$VERSION ;; esac

OS=$(uname -s)
case $(uname -m) in
x86_64 | amd64 | i86pc) ARCH=amd64 ;;
aarch64 | arm64 | evbarm) ARCH=arm64 ;;
armv7* | armv8l) ARCH=armv7 ;;
armv6* | arm) ARCH=armv6 ;;
i386 | i486 | i586 | i686 | x86) ARCH=386 ;;
ppc64le) ARCH=ppc64le ;;
s390x) ARCH=s390x ;;
riscv64) ARCH=riscv64 ;;
loongarch64 | loong64) ARCH=loong64 ;;
*) die "unsupported CPU $(uname -m)" ;;
esac
case $OS in
Linux) GOOS=linux ;;
Darwin) GOOS=darwin ;;
FreeBSD) GOOS=freebsd ;;
OpenBSD) GOOS=openbsd ;;
NetBSD) GOOS=netbsd ;;
DragonFly) GOOS=dragonfly ;;
SunOS) if uname -o 2>/dev/null | grep -qi illumos; then GOOS=illumos; else GOOS=solaris; fi ;;
*) die "unsupported OS $OS: on Windows use install.ps1" ;;
esac
if [ "$GOOS" != linux ]; then
	for c in $COMPONENTS; do
		[ "$c" = tools ] || die "the $c runs on Linux only; on $OS install the clients (tools)"
	done
fi

# as_root CMD...: run as root, through sudo or doas when needed.
as_root() {
	if [ "$(id -u)" = 0 ]; then
		"$@"
	elif have sudo; then
		if [ "$YES" = 0 ] && [ -z "${SUDO_ASKED:-}" ]; then
			say "root is needed for this; using sudo"
			SUDO_ASKED=1
		fi
		sudo "$@"
	elif have doas; then
		doas "$@"
	else
		die "run as root, or install sudo"
	fi
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT INT TERM

fetch() { # fetch URL DEST
	case $1 in
	file://*) cp "${1#file://}" "$2" ;;
	*)
		if have curl; then
			curl -fsSL --retry 3 -o "$2" "$1"
		elif have wget; then
			wget -q -O "$2" "$1"
		elif have fetch; then
			fetch -q -o "$2" "$1"
		elif have ftp; then
			ftp -V -o "$2" "$1"
		else
			die "curl or wget is needed"
		fi
		;;
	esac
}

resolve_version() {
	[ -n "$VERSION" ] && return
	src=$PKGS
	[ -n "${ORION_RELEASE_URL:-}" ] && src=${ORION_RELEASE_URL%/}
	if fetch "$src/TAG" "$TMP/TAG" 2>/dev/null; then VERSION=$(tr -d '[:space:]' <"$TMP/TAG"); fi
	case $VERSION in v*) ;; *) die "could not find the latest release (use --version)" ;; esac
}

base_url() {
	if [ -n "${ORION_RELEASE_URL:-}" ]; then
		echo "${ORION_RELEASE_URL%/}"
	else
		echo "https://github.com/$REPO/releases/download/$VERSION"
	fi
}

sha256() {
	if have sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif have shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif have sha256; then
		sha256 -q "$1"
	else
		digest -a sha256 "$1"
	fi
}

# download NAME: fetch a release file into $TMP and check it against the
# release's checksums.txt, whose GPG signature is checked when gpg is here.
download() {
	if [ ! -f "$TMP/checksums.txt" ]; then
		fetch "$(base_url)/checksums.txt" "$TMP/checksums.txt"
		if have gpg && fetch "$(base_url)/checksums.txt.sig" "$TMP/checksums.txt.sig" 2>/dev/null &&
			fetch "$PKGS/orion-belt.asc" "$TMP/key.asc" 2>/dev/null; then
			GNUPGHOME="$TMP/gnupg"
			export GNUPGHOME
			mkdir -m 700 "$GNUPGHOME"
			gpg --batch --quiet --import "$TMP/key.asc" 2>/dev/null || die "cannot import the release key"
			gpg --batch --quiet --verify "$TMP/checksums.txt.sig" "$TMP/checksums.txt" 2>/dev/null ||
				die "checksums.txt signature does not verify"
			say "release signature ok"
		fi
	fi
	say "downloading $1 ($VERSION)"
	fetch "$(base_url)/$1" "$TMP/$1" || die "download failed: $(base_url)/$1"
	want=$(awk -v f="$1" '$2 == f || $2 == "*"f {print $1}' "$TMP/checksums.txt")
	[ -n "$want" ] || die "$1 is not listed in checksums.txt"
	[ "$(sha256 "$TMP/$1")" = "$want" ] || die "checksum mismatch for $1"
}

# pkg_manager prints the package manager this system installs with.
pkg_manager() {
	if have apt-get && have dpkg; then echo apt
	elif have dnf; then echo dnf
	elif have zypper; then echo zypper
	elif have yum; then echo yum
	elif have apk; then echo apk
	elif have pacman; then echo pacman
	else echo none
	fi
}

package_name() { # package_name COMPONENT
	case $1 in
	server) echo orion-belt ;;
	agent) echo orion-belt-agent ;;
	tools) echo orion-belt-tools ;;
	esac
}

# Distribution architecture names, as in the package file names.
deb_arch() { case $ARCH in 386) echo i386 ;; armv7) echo armhf ;; armv6) echo armel ;; ppc64le) echo ppc64el ;; *) echo "$ARCH" ;; esac; }
rpm_arch() { case $ARCH in amd64) echo x86_64 ;; arm64) echo aarch64 ;; armv7) echo armv7hl ;; armv6) echo armv6hl ;; 386) echo i386 ;; loong64) echo loongarch64 ;; *) echo "$ARCH" ;; esac; }
apk_arch() { case $ARCH in amd64) echo x86_64 ;; arm64) echo aarch64 ;; armv7) echo armv7 ;; armv6) echo armhf ;; 386) echo x86 ;; loong64) echo loongarch64 ;; *) echo "$ARCH" ;; esac; }
pacman_arch() { case $ARCH in amd64) echo x86_64 ;; arm64) echo aarch64 ;; armv7) echo armv7h ;; armv6) echo armv6h ;; 386) echo i686 ;; *) echo "$ARCH" ;; esac; }

# add_repo PM: configure the signed Orion Belt repository; fails when the
# repository has nothing for this system, so the caller falls back.
add_repo() {
	case $1 in
	apt)
		fetch "$PKGS/dists/stable/main/binary-$(deb_arch)/Packages" "$TMP/Packages" 2>/dev/null || return 1
		fetch "$PKGS/orion-belt.gpg" "$TMP/orion-belt.gpg" || return 1
		as_root install -d -m 0755 /usr/share/keyrings
		as_root install -m 0644 "$TMP/orion-belt.gpg" /usr/share/keyrings/orion-belt.gpg
		echo "deb [signed-by=/usr/share/keyrings/orion-belt.gpg] $PKGS stable main" >"$TMP/orion-belt.list"
		as_root install -m 0644 "$TMP/orion-belt.list" /etc/apt/sources.list.d/orion-belt.list
		as_root apt-get update -o Dir::Etc::sourcelist=sources.list.d/orion-belt.list -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0
		;;
	dnf | yum | zypper)
		# The repository holds the common RPM architectures only.
		fetch "$PKGS/rpm-archs" "$TMP/rpm-archs" 2>/dev/null || return 1
		grep -qx "$(rpm_arch)" "$TMP/rpm-archs" || return 1
		fetch "$PKGS/orion-belt.repo" "$TMP/orion-belt.repo" || return 1
		if [ "$1" = zypper ]; then
			as_root install -m 0644 "$TMP/orion-belt.repo" /etc/zypp/repos.d/orion-belt.repo
			as_root rpm --import "$PKGS/orion-belt.asc"
			as_root zypper --non-interactive --gpg-auto-import-keys refresh orion-belt
		else
			as_root install -m 0644 "$TMP/orion-belt.repo" /etc/yum.repos.d/orion-belt.repo
		fi
		;;
	apk)
		a=$(apk_arch)
		fetch "$PKGS/apk/$a/APKINDEX.tar.gz" "$TMP/APKINDEX.tar.gz" 2>/dev/null || return 1
		# The index is trusted only when the repository publishes its RSA key.
		fetch "$PKGS/apk/orion-belt.rsa.pub" "$TMP/orion-belt.rsa.pub" 2>/dev/null || return 1
		as_root install -m 0644 "$TMP/orion-belt.rsa.pub" /etc/apk/keys/orion-belt.rsa.pub
		grep -qx "$PKGS/apk" /etc/apk/repositories 2>/dev/null ||
			echo "$PKGS/apk" | as_root tee -a /etc/apk/repositories >/dev/null
		as_root apk update >/dev/null
		;;
	pacman)
		fetch "$PKGS/archlinux/$(pacman_arch)/orion-belt.db" "$TMP/orion-belt.db" 2>/dev/null || return 1
		fetch "$PKGS/orion-belt.asc" "$TMP/key.asc" || return 1
		fpr=$(fetch "$PKGS/orion-belt.fingerprint" /dev/stdout | tr -d '[:space:]')
		as_root pacman-key --add "$TMP/key.asc" >/dev/null
		as_root pacman-key --lsign-key "$fpr" >/dev/null
		if ! grep -q '^\[orion-belt\]' /etc/pacman.conf; then
			printf '\n[orion-belt]\nSigLevel = Required\nServer = %s/archlinux/$arch\n' "$PKGS" | as_root tee -a /etc/pacman.conf >/dev/null
		fi
		as_root pacman -Sy --noconfirm >/dev/null
		;;
	*) return 1 ;;
	esac
}

install_from_repo() { # install_from_repo PM PACKAGES...
	pm=$1
	shift
	case $pm in
	apt) as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y "$@" ;;
	dnf) as_root dnf install -y "$@" ;;
	yum) as_root yum install -y "$@" ;;
	zypper) as_root zypper --non-interactive install "$@" ;;
	apk) as_root apk add "$@" ;;
	pacman) as_root pacman -S --noconfirm "$@" ;;
	esac
}

# package_file PM PACKAGE: the release's file name. Each format spells a
# pre-release differently (1.4.0-rc.1: deb 1.4.0~rc.1, rpm 1.4.0~rc.1 with
# later dashes as _, apk 1.4.0_rc.1, pacman 1.4.0rc.1).
package_file() {
	v=${VERSION#v}
	case $1 in
	apt) echo "${2}_$(echo "$v" | sed 's/-/~/')_$(deb_arch).deb" ;;
	dnf | yum | zypper) echo "${2}-$(echo "$v" | sed 's/-/~/; s/-/_/g')-1.$(rpm_arch).rpm" ;;
	apk) echo "${2}_$(echo "$v" | sed 's/-/_/')_$(apk_arch).apk" ;;
	pacman) echo "${2}-$(echo "$v" | sed 's/-//; s/-/_/g')-1-$(pacman_arch).pkg.tar.zst" ;;
	esac
}

install_package_file() { # install_package_file PM PACKAGE
	f=$(package_file "$1" "$2")
	download "$f"
	# The checksum above was checked against the signed checksums.txt.
	case $1 in
	apt) as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y "$TMP/$f" ;;
	dnf) as_root dnf install -y "$TMP/$f" ;;
	yum) as_root yum install -y "$TMP/$f" ;;
	zypper) as_root zypper --non-interactive install --allow-unsigned-rpm "$TMP/$f" ;;
	apk) as_root apk add --allow-untrusted "$TMP/$f" ;;
	pacman) as_root pacman -U --noconfirm "$TMP/$f" ;;
	esac
}

# Archive installs: binaries under $PREFIX/bin; for the server and agent also
# the service helper and its templates, then the service file is installed.
install_binaries() { # install_binaries COMPONENT
	p=${PREFIX:-/usr/local}
	v=${VERSION#v}
	case $1 in
	tools) a="orion-belt_tools_${v}_${GOOS}_${ARCH}.tar.gz"; bins="osh ocp oadmin" ;;
	agent) a="orion-belt-agent_${v}_linux_${ARCH}.tar.gz"; bins=orion-belt-agent ;;
	server) a="orion-belt_${v}_linux_${ARCH}.tar.gz"; bins=orion-belt-server ;;
	esac
	download "$a"
	rm -rf "$TMP/x" && mkdir "$TMP/x" && tar -xzf "$TMP/$a" -C "$TMP/x"
	if [ -w "$p" ] || [ -w "$p/bin" ]; then sudo_if= ; else sudo_if=as_root; fi
	$sudo_if mkdir -p "$p/bin"
	for b in $bins; do
		$sudo_if cp "$TMP/x/$b" "$p/bin/$b.new"
		$sudo_if chmod 0755 "$p/bin/$b.new"
		$sudo_if mv -f "$p/bin/$b.new" "$p/bin/$b"
		say "installed $p/bin/$b"
	done
	[ "$1" = tools ] && return
	n=orion-belt-$1
	s="$p/share/orion-belt/init"
	as_root mkdir -p "$p/lib/orion-belt" "$s/runit/$n" /etc/orion-belt
	svc="$p/lib/orion-belt/$n-service"
	as_root install -m 0755 "$TMP/x/packaging/linux/orion-belt-service" "$svc"
	as_root install -m 0644 "$TMP/x/packaging/systemd/$n.service" "$s/$n.service"
	as_root install -m 0755 "$TMP/x/packaging/openrc/$n.initd" "$s/$n.openrc"
	as_root install -m 0755 "$TMP/x/packaging/sysv/$n" "$s/$n.sysv"
	as_root install -m 0755 "$TMP/x/packaging/runit/$n/run" "$s/runit/$n/run"
	if [ ! -f "/etc/orion-belt/$1.yaml" ]; then
		as_root install -m 0640 "$TMP/x/config/$1.example.yaml" "/etc/orion-belt/$1.yaml"
	fi
	if [ "$1" = server ] && ! id orionbelt >/dev/null 2>&1; then
		if have useradd; then
			as_root useradd --system --user-group --home-dir /var/lib/orion-belt --shell /sbin/nologin orionbelt
		else
			as_root addgroup -S orionbelt && as_root adduser -S -G orionbelt -h /var/lib/orion-belt -s /sbin/nologin orionbelt
		fi
	fi
	if [ "$1" = server ]; then
		as_root mkdir -p /var/lib/orion-belt/recordings /var/log/orion-belt
		as_root chown -R orionbelt:orionbelt /var/lib/orion-belt /var/log/orion-belt /etc/orion-belt
	fi
	as_root env ORION_BINDIR="$p/bin" ORION_SHARE="$p/share/orion-belt" "$svc" install
	say "$n service installed; configure /etc/orion-belt/$1.yaml, then enable it:"
	ORION_BINDIR="$p/bin" ORION_SHARE="$p/share/orion-belt" "$svc" hint >&2
}

install_linux() {
	pm=$(pkg_manager)
	pkgs=""
	for c in $COMPONENTS; do pkgs="$pkgs $(package_name "$c")"; done
	if [ "$METHOD" = auto ] || [ "$METHOD" = repo ]; then
		if [ "$pm" != none ] && add_repo "$pm"; then
			# shellcheck disable=SC2086 # a list of package names
			install_from_repo "$pm" $pkgs
			say "installed from $PKGS ($pm); upgrades come with the system's"
			return
		fi
		[ "$METHOD" = repo ] && die "no Orion Belt repository for this system ($pm, $(uname -m))"
		say "no repository for this system; installing release files"
	fi
	resolve_version
	for c in $COMPONENTS; do
		if [ "$METHOD" != binary ] && [ "$pm" != none ]; then
			install_package_file "$pm" "$(package_name "$c")"
		else
			install_binaries "$c"
		fi
	done
}

uninstall() {
	pm=$(pkg_manager)
	for c in $COMPONENTS; do
		n=$(package_name "$c")
		case $pm in
		apt) if dpkg -s "$n" >/dev/null 2>&1; then as_root env DEBIAN_FRONTEND=noninteractive apt-get remove -y "$n"; fi ;;
		dnf | yum) if rpm -q "$n" >/dev/null 2>&1; then as_root "$pm" remove -y "$n"; fi ;;
		zypper) if rpm -q "$n" >/dev/null 2>&1; then as_root zypper --non-interactive remove "$n"; fi ;;
		apk) if apk info -e "$n" >/dev/null 2>&1; then as_root apk del "$n"; fi ;;
		pacman) if pacman -Q "$n" >/dev/null 2>&1; then as_root pacman -R --noconfirm "$n"; fi ;;
		esac
		p=${PREFIX:-/usr/local}
		case $c in
		tools) for b in osh ocp oadmin; do [ -e "$p/bin/$b" ] && as_root rm -f "$p/bin/$b"; done ;;
		*)
			svc="$p/lib/orion-belt/orion-belt-$c-service"
			if [ -x "$svc" ]; then
				as_root env ORION_BINDIR="$p/bin" ORION_SHARE="$p/share/orion-belt" "$svc" teardown || true
				as_root rm -f "$svc"
				as_root rm -f "$p/bin/orion-belt-$c"
			fi
			;;
		esac
		say "removed $c (configuration in /etc/orion-belt is kept)"
	done
}

main() {
	if [ "$UNINSTALL" = 1 ]; then
		uninstall
		return
	fi
	if [ "$GOOS" = linux ]; then
		install_linux
	else
		resolve_version
		install_binaries tools
		case ":$PATH:" in *":${PREFIX:-/usr/local}/bin:"*) ;; *) say "add ${PREFIX:-/usr/local}/bin to your PATH" ;; esac
	fi
	say "done:$COMPONENTS"
}

main
