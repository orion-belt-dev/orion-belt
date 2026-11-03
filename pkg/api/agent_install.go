package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/packaging/keys"
	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"golang.org/x/crypto/ssh"
)

// AgentInstallScriptRequest builds a one-shot install+join script for a new agent.
type AgentInstallScriptRequest struct {
	Name           string            `json:"name" binding:"required"`
	Hostname       string            `json:"hostname"`
	Port           int               `json:"port"`
	Tags           map[string]string `json:"tags"`
	OS             string            `json:"os" binding:"required"` // debian | rhel | suse | alpine | linux
	GatewayHost    string            `json:"gateway_host" binding:"required"`
	GatewayPort    int               `json:"gateway_port"`
	PackageBaseURL string            `json:"package_base_url" binding:"required"`
	Version        string            `json:"version"`
	// AllowUnsignedBuild lets the script install a local snapshot build that
	// the release key did not sign (make packages && make serve-packages).
	// It is audited and the script warns on every unverified step.
	AllowUnsignedBuild bool `json:"allow_unsigned_build"`
}

// AgentInstallScriptResponse is returned to the UI for copy/paste on the target host.
type AgentInstallScriptResponse struct {
	Script    string `json:"script"`
	MachineID string `json:"machine_id"`
	UserID    string `json:"user_id"`
	AgentName string `json:"agent_name"`
	PublicKey string `json:"public_key"`
	Filename  string `json:"filename"`
	Message   string `json:"message"`
	// UnsignedBuild is true when the script may install packages the release
	// key did not sign.
	UnsignedBuild bool `json:"unsigned_build"`
}

func (s *APIServer) generateAgentInstallScript(c *gin.Context) {
	var req AgentInstallScriptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || strings.ContainsAny(name, " \t\n/\\\"'") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent name"})
		return
	}
	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = name
	}
	port := req.Port
	if port <= 0 {
		port = 22
	}
	gwPort := req.GatewayPort
	if gwPort <= 0 {
		gwPort = 2222
	}
	osID := strings.ToLower(strings.TrimSpace(req.OS))
	switch osID {
	case "debian", "ubuntu", "rhel", "rocky", "centos", "fedora", "suse", "opensuse", "alpine", "arch", "archlinux", "linux", "generic":
		switch osID {
		case "archlinux":
			osID = "arch"
		case "ubuntu":
			osID = "debian"
		case "rocky", "centos", "fedora":
			osID = "rhel"
		case "opensuse":
			osID = "suse"
		case "generic":
			osID = "linux"
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "os must be one of: debian, rhel, suse, alpine, arch, linux"})
		return
	}

	pkgBase := strings.TrimRight(strings.TrimSpace(req.PackageBaseURL), "/")
	if pkgBase == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "package_base_url required"})
		return
	}
	ver := strings.TrimSpace(req.Version)
	if ver == "" || ver == "dev" {
		ver = version.Version
	}
	if ver == "" || ver == "dev" {
		ver = "0.0.0"
	}
	ver = strings.TrimPrefix(ver, "v")

	gwHost := strings.TrimSpace(req.GatewayHost)
	if gwHost == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gateway_host required"})
		return
	}

	ctx := c.Request.Context()
	if existing, _ := s.store.GetMachineByName(ctx, name); existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "agent already registered"})
		return
	}

	privPEM, pubKey, err := auth.GenerateSSHKeyPair()
	if err != nil {
		s.logger.Error("Failed to generate agent key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate agent key"})
		return
	}
	pubKey = strings.TrimSpace(pubKey)

	tags := req.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	if _, ok := tags["os"]; !ok {
		tags["os"] = osID
	}
	machine := common.NewMachine(name, hostname, port, tags)

	var agentUserID string
	var hostCertLine, hostCAPublicKey string

	if s.ca != nil {
		// SSH CA enabled: identify the agent by a Host-CA-signed cert
		// instead of the legacy synthetic-user mechanism — no `users` row
		// is created for it at all.
		if err := s.store.CreateMachine(ctx, machine); err != nil {
			s.logger.Error("Failed to create machine: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKey))
		if err != nil {
			_ = s.store.DeleteMachine(ctx, machine.ID)
			s.logger.Error("Failed to parse generated agent key: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		hostCert, err := s.ca.IssueHostCert(ctx, machine.ID, []string{name}, pub, s.ca.HostCertTTL())
		if err != nil {
			_ = s.store.DeleteMachine(ctx, machine.ID)
			s.logger.Error("Failed to issue agent host certificate: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		hostCertLine = string(ssh.MarshalAuthorizedKey(hostCert))
		_, hostCAPublicKey = s.ca.ExportPublicKeys()
	} else {
		// Legacy path: unchanged synthetic "agent user" mechanism.
		agentUser := common.NewUser(name, fmt.Sprintf("%s@agent.orion-belt", name), pubKey, false)
		if err := s.store.CreateUser(ctx, agentUser); err != nil {
			s.logger.Error("Failed to create agent user: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		agentUserID = agentUser.ID
		machine.AgentID = agentUser.ID
		if err := s.store.CreateMachine(ctx, machine); err != nil {
			_ = s.store.DeleteUser(ctx, agentUser.ID)
			s.logger.Error("Failed to create machine: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
	}

	script := buildAgentInstallScript(osID, name, gwHost, gwPort, pkgBase, ver, privPEM, hostCertLine, hostCAPublicKey, tags, req.AllowUnsignedBuild)

	uid, _ := c.Get("user_id")
	uidStr, _ := uid.(string)
	s.recordAudit(c, "agent.install_script", "machine:"+machine.ID, map[string]interface{}{
		"agent_name": name,
		"os":         osID,
		"user_id":              uidStr,
		"ssh_ca":               s.ca != nil,
		"package_base_url":     pkgBase,
		"version":              ver,
		"allow_unsigned_build": req.AllowUnsignedBuild,
	})
	message := "Agent registered. Run the script on the target host as root."
	if req.AllowUnsignedBuild {
		message = "Agent registered. This script may install packages the release key did not sign: use it only with a local build you trust. Run it on the target host as root."
	}

	c.JSON(http.StatusCreated, AgentInstallScriptResponse{
		Script:    script,
		MachineID: machine.ID,
		UserID:    agentUserID,
		AgentName: name,
		PublicKey: pubKey,
		Filename:      fmt.Sprintf("orion-belt-install-%s.sh", name),
		Message:       message,
		UnsignedBuild: req.AllowUnsignedBuild,
	})
}

func buildAgentInstallScript(osID, name, gwHost string, gwPort int, pkgBase, ver, privPEM, hostCertLine, hostCAPublicKey string, tags map[string]string, allowUnsigned bool) string {
	var tagLines strings.Builder
	wrote := false
	for k, v := range tags {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		tagLines.WriteString(fmt.Sprintf("    %s: %q\n", k, v))
		wrote = true
	}
	if !wrote {
		tagLines.WriteString("    environment: \"production\"\n")
	}

	authBody := `auth:
  key_file: "/etc/orion-belt/agent_key"
  known_hosts: "/etc/orion-belt/known_hosts"
  strict_host_key_checking: "ask"
`
	if hostCAPublicKey != "" {
		authBody += fmt.Sprintf("  host_ca_public_key: %q\n", strings.TrimSpace(hostCAPublicKey))
	}

	yamlBody := fmt.Sprintf(`server:
  host: %q
  port: %d
agent:
  name: %q
  tags:
%s%s`, gwHost, gwPort, name, tagLines.String(), authBody)

	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# Orion Belt agent install — generated " + time.Now().UTC().Format(time.RFC3339) + "\n")
	b.WriteString("# Configures this host as agent " + strconv.Quote(name) + " and joins the gateway.\n")
	b.WriteString("set -euo pipefail\n\n")
	b.WriteString("if [ \"$(id -u)\" -ne 0 ]; then\n")
	b.WriteString("  echo \"run as root\" >&2\n")
	b.WriteString("  exit 1\n")
	b.WriteString("fi\n\n")
	b.WriteString("export DEBIAN_FRONTEND=noninteractive\n")
	b.WriteString("command -v curl >/dev/null 2>&1 || {\n")
	b.WriteString("  if command -v apt-get >/dev/null 2>&1; then apt-get update -y && apt-get install -y curl ca-certificates;\n")
	b.WriteString("  elif command -v dnf >/dev/null 2>&1; then dnf install -y curl ca-certificates;\n")
	b.WriteString("  elif command -v zypper >/dev/null 2>&1; then zypper -n install curl ca-certificates;\n")
	b.WriteString("  elif command -v apk >/dev/null 2>&1; then apk add --no-cache curl ca-certificates;\n")
	b.WriteString("  else echo \"curl required\" >&2; exit 1; fi\n")
	b.WriteString("}\n\n")
	b.WriteString("mkdir -p /etc/orion-belt\n")
	b.WriteString("chmod 0750 /etc/orion-belt\n\n")

	// Downloads go to a private directory: under a shared /tmp another local
	// user could pre-create the file and change it after it is verified.
	b.WriteString("WORK=$(mktemp -d)\n")
	b.WriteString("trap 'rm -rf \"$WORK\"' EXIT\n\n")

	b.WriteString(fmt.Sprintf("PKG_BASE=%q\n", pkgBase))
	b.WriteString(fmt.Sprintf("VERSION=%q\n\n", ver))

	// The release key is pinned here rather than fetched, so whoever serves
	// the packages cannot also supply the key that vouches for them.
	b.WriteString(fmt.Sprintf("ORION_RELEASE_FPR=%q\n", keys.ReleaseFingerprint()))
	if allowUnsigned {
		b.WriteString("# Generated with allow_unsigned_build: packages the release key did not sign\n")
		b.WriteString("# are accepted (local snapshot builds). The server audit log records this.\n")
		b.WriteString("ORION_ALLOW_UNSIGNED_BUILD=1\n")
		b.WriteString("ZYPPER_UNSIGNED=--allow-unsigned-rpm\n")
		b.WriteString("echo \"WARNING: this installer accepts unsigned local builds of orion-belt-agent\" >&2\n")
	} else {
		b.WriteString("ORION_ALLOW_UNSIGNED_BUILD=0\n")
		b.WriteString("ZYPPER_UNSIGNED=\n")
	}
	b.WriteString("cat > \"$WORK/orion-belt.asc\" <<'ORION_RELEASE_KEY'\n")
	b.WriteString(strings.TrimSpace(keys.ReleaseKey()))
	b.WriteString("\nORION_RELEASE_KEY\n\n")

	// The package for this host's architecture, under the names the package
	// site and release use (pkgs.orion-belt.dev, or a mirror of dist/).
	b.WriteString(agentInstallArch)
	b.WriteString(agentInstallVerify)
	// Each package is checked against the signed checksums.txt before it is
	// installed, which is what makes apk's --allow-untrusted safe: apk is not
	// asked to trust the file on its own.
	switch osID {
	case "debian":
		b.WriteString(`echo "==> Installing orion-belt-agent (deb, $DEB_ARCH)"
if fetch_verified "$WORK/orion-belt-agent.deb" "orion-belt-agent_${DEB_V}_${DEB_ARCH}.deb" \
  "$PKG_BASE/orion-belt-agent_${DEB_V}_${DEB_ARCH}.deb" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_${GO_ARCH}.deb" \
  "$RELEASE/orion-belt-agent_${DEB_V}_${DEB_ARCH}.deb"; then
  dpkg -i "$WORK/orion-belt-agent.deb" || apt-get install -f -y
else
  install_agent_archive
fi
`)
	case "rhel", "suse":
		b.WriteString(`echo "==> Installing orion-belt-agent (rpm, $RPM_ARCH)"
if fetch_verified "$WORK/orion-belt-agent.rpm" "orion-belt-agent-${RPM_V}-1.${RPM_ARCH}.rpm" \
  "$PKG_BASE/orion-belt-agent-${RPM_V}-1.${RPM_ARCH}.rpm" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_${GO_ARCH}.rpm" \
  "$RELEASE/orion-belt-agent-${RPM_V}-1.${RPM_ARCH}.rpm"; then
  # The rpm is signed with the same pinned key; zypper refuses a package
  # signed by a key rpm does not know, even with --allow-unsigned-rpm.
  rpm --import "$WORK/orion-belt.asc"
  if command -v dnf >/dev/null 2>&1; then dnf -y install "$WORK/orion-belt-agent.rpm"
  elif command -v zypper >/dev/null 2>&1; then zypper -n install $ZYPPER_UNSIGNED "$WORK/orion-belt-agent.rpm"
  elif command -v yum >/dev/null 2>&1; then yum -y localinstall "$WORK/orion-belt-agent.rpm"
  else rpm -Uvh "$WORK/orion-belt-agent.rpm"; fi
else
  install_agent_archive
fi
`)
	case "alpine":
		b.WriteString(`echo "==> Installing orion-belt-agent (apk, $APK_ARCH)"
if fetch_verified "$WORK/orion-belt-agent.apk" "orion-belt-agent_${APK_V}_${APK_ARCH}.apk" \
  "$PKG_BASE/apk/${APK_ARCH}/orion-belt-agent-${APK_V}.apk" \
  "$PKG_BASE/orion-belt-agent_${APK_V}_${APK_ARCH}.apk" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_${GO_ARCH}.apk" \
  "$RELEASE/orion-belt-agent_${APK_V}_${APK_ARCH}.apk"; then
  apk add --allow-untrusted "$WORK/orion-belt-agent.apk"
else
  install_agent_archive
fi
`)
	case "arch":
		b.WriteString(`echo "==> Installing orion-belt-agent (pacman, $PAC_ARCH)"
if fetch_verified "$WORK/orion-belt-agent.pkg.tar.zst" "orion-belt-agent-${PAC_V}-1-${PAC_ARCH}.pkg.tar.zst" \
  "$PKG_BASE/archlinux/${PAC_ARCH}/orion-belt-agent-${PAC_V}-1-${PAC_ARCH}.pkg.tar.zst" \
  "$PKG_BASE/orion-belt-agent-${PAC_V}-1-${PAC_ARCH}.pkg.tar.zst" \
  "$RELEASE/orion-belt-agent-${PAC_V}-1-${PAC_ARCH}.pkg.tar.zst"; then
  pacman -U --noconfirm "$WORK/orion-belt-agent.pkg.tar.zst"
else
  install_agent_archive
fi
`)
	default:
		b.WriteString(`echo "==> Installing orion-belt-agent (binary, $GO_ARCH)"
install_agent_archive
`)
	}

	b.WriteString("\necho \"==> Writing agent config and identity\"\n")
	b.WriteString("cat > /etc/orion-belt/agent.yaml <<'ORION_AGENT_YAML'\n")
	b.WriteString(yamlBody)
	b.WriteString("ORION_AGENT_YAML\n")
	b.WriteString("chmod 0640 /etc/orion-belt/agent.yaml\n")
	b.WriteString("cat > /etc/orion-belt/agent_key <<'ORION_AGENT_KEY'\n")
	b.WriteString(strings.TrimSpace(privPEM))
	b.WriteString("\nORION_AGENT_KEY\n")
	b.WriteString("chmod 0600 /etc/orion-belt/agent_key\n")
	if hostCertLine != "" {
		b.WriteString("cat > /etc/orion-belt/agent_key-cert.pub <<'ORION_AGENT_CERT'\n")
		b.WriteString(strings.TrimSpace(hostCertLine))
		b.WriteString("\nORION_AGENT_CERT\n")
		b.WriteString("chmod 0644 /etc/orion-belt/agent_key-cert.pub\n")
	}
	b.WriteString("\n")

	b.WriteString(`echo "==> Starting orion-belt-agent"
# orion-belt-agent-service sets the service up for systemd, OpenRC, runit or SysV.
svc=/usr/lib/orion-belt/orion-belt-agent-service
if [ -x "$svc" ] && [ "$("$svc" detect)" != none ]; then
  "$svc" setup
  "$svc" status || true
elif command -v systemctl >/dev/null 2>&1 && [ -f /lib/systemd/system/orion-belt-agent.service ]; then
  systemctl daemon-reload || true
  systemctl enable --now orion-belt-agent
  systemctl --no-pager --full status orion-belt-agent || true
else
  mkdir -p /var/log
  nohup /usr/bin/orion-belt-agent -c /etc/orion-belt/agent.yaml >>/var/log/orion-agent.log 2>&1 &
  echo "agent started in background (pid $!) — logs: /var/log/orion-agent.log"
fi

echo
echo "Agent configured as ` + strconv.Quote(name) + `"
echo "It should appear under Agents in the Orion Belt console once the tunnel is up."
`)

	return b.String()
}

// agentInstallArch is the part of the agent install script that maps the
// host's CPU to the architecture names each package format uses, and the
// helpers the package steps share. Each file is tried from the package base
// URL (the package site, or a mirror) first, then from the GitHub release,
// which has every package. install_agent_archive is the fallback for hosts
// without a matching package. The bare binary earlier package sites served is
// no longer tried: no checksums.txt lists it, so it cannot be verified.
const agentInstallArch = `RELEASE="https://github.com/orion-belt-dev/orion-belt/releases/download/v${VERSION}"
# Each package format spells a pre-release differently (1.4.0-rc.1: deb
# 1.4.0~rc.1, rpm 1.4.0~rc.1 with later dashes as _, apk 1.4.0_rc.1, pacman
# 1.4.0rc.1); a plain version is the same everywhere.
DEB_V=$(echo "$VERSION" | sed 's/-/~/')
RPM_V=$(echo "$VERSION" | sed 's/-/~/; s/-/_/g')
APK_V=$(echo "$VERSION" | sed 's/-/_/')
PAC_V=$(echo "$VERSION" | sed 's/-//; s/-/_/g')
case "$(uname -m)" in
  x86_64|amd64) GO_ARCH=amd64 DEB_ARCH=amd64 RPM_ARCH=x86_64 APK_ARCH=x86_64 PAC_ARCH=x86_64 ;;
  aarch64|arm64) GO_ARCH=arm64 DEB_ARCH=arm64 RPM_ARCH=aarch64 APK_ARCH=aarch64 PAC_ARCH=aarch64 ;;
  armv7*|armv8l) GO_ARCH=armv7 DEB_ARCH=armhf RPM_ARCH=armv7hl APK_ARCH=armv7 PAC_ARCH=armv7h ;;
  armv6*) GO_ARCH=armv6 DEB_ARCH=armhf RPM_ARCH=armv6hl APK_ARCH=armhf PAC_ARCH=armv6h ;;
  armv5*) GO_ARCH=armv5 DEB_ARCH=armel RPM_ARCH=armv5tel APK_ARCH=armel PAC_ARCH=armv5 ;;
  i386|i486|i586|i686) GO_ARCH=386 DEB_ARCH=i386 RPM_ARCH=i386 APK_ARCH=x86 PAC_ARCH=i686 ;;
  ppc64le) GO_ARCH=ppc64le DEB_ARCH=ppc64el RPM_ARCH=ppc64le APK_ARCH=ppc64le PAC_ARCH=ppc64le ;;
  s390x) GO_ARCH=s390x DEB_ARCH=s390x RPM_ARCH=s390x APK_ARCH=s390x PAC_ARCH=s390x ;;
  riscv64) GO_ARCH=riscv64 DEB_ARCH=riscv64 RPM_ARCH=riscv64 APK_ARCH=riscv64 PAC_ARCH=riscv64 ;;
  loongarch64) GO_ARCH=loong64 DEB_ARCH=loong64 RPM_ARCH=loongarch64 APK_ARCH=loongarch64 PAC_ARCH=loong64 ;;
  *) echo "unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac
# A Debian armhf userland on an ARMv7 CPU still uses the armhf package.
if [ "$GO_ARCH" = armv7 ] && command -v dpkg >/dev/null 2>&1; then DEB_ARCH=$(dpkg --print-architecture); fi

install_agent_archive() {
  tmp="$WORK/archive"
  mkdir -p "$tmp"
  if fetch_verified "$tmp/agent.tar.gz" "orion-belt-agent_${VERSION}_linux_${GO_ARCH}.tar.gz" \
    "$PKG_BASE/orion-belt-agent_${VERSION}_linux_${GO_ARCH}.tar.gz" \
    "$RELEASE/orion-belt-agent_${VERSION}_linux_${GO_ARCH}.tar.gz"; then
    tar -xzf "$tmp/agent.tar.gz" -C "$tmp"
    install -m 0755 "$tmp/orion-belt-agent" /usr/bin/orion-belt-agent
    install -d /usr/lib/orion-belt /usr/share/orion-belt/init/runit/orion-belt-agent
    install -m 0755 "$tmp/packaging/linux/orion-belt-service" /usr/lib/orion-belt/orion-belt-agent-service
    install -m 0644 "$tmp/packaging/systemd/orion-belt-agent.service" /usr/share/orion-belt/init/orion-belt-agent.service
    install -m 0755 "$tmp/packaging/openrc/orion-belt-agent.initd" /usr/share/orion-belt/init/orion-belt-agent.openrc
    install -m 0755 "$tmp/packaging/sysv/orion-belt-agent" /usr/share/orion-belt/init/orion-belt-agent.sysv
    install -m 0755 "$tmp/packaging/runit/orion-belt-agent/run" /usr/share/orion-belt/init/runit/orion-belt-agent/run
  else
    echo "no verified orion-belt-agent package for $(uname -m) at $PKG_BASE or $RELEASE" >&2
    exit 1
  fi
}

`

// agentInstallVerify checks downloads against the release's checksums.txt,
// whose GPG signature must come from the pinned release key
// (ORION_RELEASE_FPR, written to $WORK/orion-belt.asc by the script). The
// checksums come from the package base first (a mirror of dist/ has them),
// then from the GitHub release. gpg is installed when it is missing: no
// download is installed unverified, unless the script was generated with
// allow_unsigned_build (ORION_ALLOW_UNSIGNED_BUILD=1). Then a package base
// without signed checksums may supply plain ones, which still catch a
// corrupted download, or none, and each fallback prints a warning.
const agentInstallVerify = `ensure_gpg() {
  command -v gpg >/dev/null 2>&1 && return 0
  echo "==> Installing gnupg to verify the release signature"
  if command -v apt-get >/dev/null 2>&1; then apt-get update -y && apt-get install -y gnupg
  elif command -v dnf >/dev/null 2>&1; then dnf install -y gnupg2
  elif command -v yum >/dev/null 2>&1; then yum install -y gnupg2
  elif command -v zypper >/dev/null 2>&1; then zypper -n install gpg2
  elif command -v apk >/dev/null 2>&1; then apk add --no-cache gnupg
  elif command -v pacman >/dev/null 2>&1; then pacman -S --noconfirm gnupg
  fi
  command -v gpg >/dev/null 2>&1 || { echo "gpg is required to verify the agent package" >&2; exit 1; }
}

# checksums_signed: true when $WORK/checksums.txt.sig is a good signature of
# $WORK/checksums.txt by the pinned key (gpg's VALIDSIG names the primary
# key's fingerprint last).
checksums_signed() {
  GNUPGHOME="$WORK/gnupg"
  export GNUPGHOME
  rm -rf "$GNUPGHOME" && mkdir -m 0700 "$GNUPGHOME"
  gpg --batch --quiet --import "$WORK/orion-belt.asc" 2>/dev/null || return 1
  gpg --batch --status-fd 1 --verify "$WORK/checksums.txt.sig" "$WORK/checksums.txt" 2>/dev/null |
    awk -v fpr="$ORION_RELEASE_FPR" '$1 == "[GNUPG:]" && $2 == "VALIDSIG" && $NF == fpr { ok = 1 } END { exit !ok }'
}

load_checksums() {
  [ -f "$WORK/checksums.ok" ] || [ -f "$WORK/checksums.none" ] && return 0
  if [ "$ORION_ALLOW_UNSIGNED_BUILD" = 1 ] && ! command -v gpg >/dev/null 2>&1; then
    echo "warning: gpg not found; not checking release signatures" >&2
  else
    ensure_gpg
  fi
  for base in "$PKG_BASE" "$RELEASE"; do
    command -v gpg >/dev/null 2>&1 || break
    rm -f "$WORK/checksums.txt" "$WORK/checksums.txt.sig"
    curl -fsSL -o "$WORK/checksums.txt" "$base/checksums.txt" 2>/dev/null || continue
    curl -fsSL -o "$WORK/checksums.txt.sig" "$base/checksums.txt.sig" 2>/dev/null || continue
    if checksums_signed; then
      echo "==> Release checksums verified ($base)"
      touch "$WORK/checksums.ok"
      return 0
    fi
    echo "warning: checksums.txt from $base is not signed by the release key; ignoring it" >&2
  done
  if [ "$ORION_ALLOW_UNSIGNED_BUILD" = 1 ]; then
    if curl -fsSL -o "$WORK/checksums.txt" "$PKG_BASE/checksums.txt" 2>/dev/null; then
      echo "WARNING: using UNSIGNED checksums.txt from $PKG_BASE (allow_unsigned_build)" >&2
      touch "$WORK/checksums.ok"
    else
      echo "WARNING: no checksums.txt at $PKG_BASE; installing WITHOUT any verification (allow_unsigned_build)" >&2
      touch "$WORK/checksums.none"
    fi
    return 0
  fi
  echo "no signed checksums.txt for v$VERSION at $PKG_BASE or $RELEASE" >&2
  exit 1
}

fetch_verified() { # fetch_verified DEST RELEASE_NAME URL...: the first URL whose file matches RELEASE_NAME in checksums.txt
  dest=$1; name=$2; shift 2
  load_checksums
  if [ -f "$WORK/checksums.none" ]; then
    for u in "$@"; do
      if curl -fsSL -o "$dest" "$u" 2>/dev/null; then
        echo "WARNING: installing $u unverified (allow_unsigned_build)" >&2
        return 0
      fi
    done
    return 1
  fi
  want=$(awk -v f="$name" '$2 == f || $2 == "*"f { print $1 }' "$WORK/checksums.txt")
  if [ -z "$want" ]; then
    echo "warning: $name is not in the release checksums" >&2
    return 1
  fi
  for u in "$@"; do
    rm -f "$dest"
    curl -fsSL -o "$dest" "$u" 2>/dev/null || continue
    if [ "$(sha256sum "$dest" | cut -d' ' -f1)" = "$want" ]; then return 0; fi
    echo "warning: $u does not match the signed checksum for $name; skipping it" >&2
  done
  rm -f "$dest"
  return 1
}

`
