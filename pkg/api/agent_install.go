package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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
	case "debian", "ubuntu", "rhel", "rocky", "centos", "fedora", "suse", "opensuse", "alpine", "linux", "generic":
		switch osID {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "os must be one of: debian, rhel, suse, alpine, linux"})
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

	script := buildAgentInstallScript(osID, name, gwHost, gwPort, pkgBase, ver, privPEM, hostCertLine, hostCAPublicKey, tags)

	uid, _ := c.Get("user_id")
	uidStr, _ := uid.(string)
	s.recordAudit(c, "agent.install_script", "machine:"+machine.ID, map[string]interface{}{
		"agent_name": name,
		"os":         osID,
		"user_id":    uidStr,
		"ssh_ca":     s.ca != nil,
	})

	c.JSON(http.StatusCreated, AgentInstallScriptResponse{
		Script:    script,
		MachineID: machine.ID,
		UserID:    agentUserID,
		AgentName: name,
		PublicKey: pubKey,
		Filename:  fmt.Sprintf("orion-belt-install-%s.sh", name),
		Message:   "Agent registered. Run the script on the target host as root.",
	})
}

func buildAgentInstallScript(osID, name, gwHost string, gwPort int, pkgBase, ver, privPEM, hostCertLine, hostCAPublicKey string, tags map[string]string) string {
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

	b.WriteString(fmt.Sprintf("PKG_BASE=%q\n", pkgBase))
	b.WriteString(fmt.Sprintf("VERSION=%q\n\n", ver))

	switch osID {
	case "debian":
		b.WriteString(`echo "==> Installing orion-belt-agent (deb)"
DEB=""
for cand in \
  "$PKG_BASE/orion-belt-agent_${VERSION}_amd64.deb" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_amd64.deb"
do
  if curl -fsSL -o /tmp/orion-belt-agent.deb "$cand"; then DEB=1; break; fi
done
if [ -n "$DEB" ]; then
  dpkg -i /tmp/orion-belt-agent.deb || apt-get install -f -y
  rm -f /tmp/orion-belt-agent.deb
elif curl -fsSL -o /usr/bin/orion-belt-agent "$PKG_BASE/orion-belt-agent"; then
  chmod 0755 /usr/bin/orion-belt-agent
else
  echo "failed to download agent package from $PKG_BASE" >&2
  exit 1
fi
`)
	case "rhel":
		b.WriteString(`echo "==> Installing orion-belt-agent (rpm)"
RPM=""
for cand in \
  "$PKG_BASE/orion-belt-agent-${VERSION}-1.x86_64.rpm" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_amd64.rpm"
do
  if curl -fsSL -o /tmp/orion-belt-agent.rpm "$cand"; then RPM=1; break; fi
done
if [ -n "$RPM" ]; then
  if command -v dnf >/dev/null 2>&1; then dnf -y install /tmp/orion-belt-agent.rpm
  elif command -v yum >/dev/null 2>&1; then yum -y localinstall /tmp/orion-belt-agent.rpm
  else rpm -Uvh /tmp/orion-belt-agent.rpm; fi
  rm -f /tmp/orion-belt-agent.rpm
elif curl -fsSL -o /usr/bin/orion-belt-agent "$PKG_BASE/orion-belt-agent"; then
  chmod 0755 /usr/bin/orion-belt-agent
else
  echo "failed to download agent package from $PKG_BASE" >&2
  exit 1
fi
`)
	case "suse":
		b.WriteString(`echo "==> Installing orion-belt-agent (rpm / zypper)"
RPM=""
for cand in \
  "$PKG_BASE/orion-belt-agent-${VERSION}-1.x86_64.rpm" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_amd64.rpm"
do
  if curl -fsSL -o /tmp/orion-belt-agent.rpm "$cand"; then RPM=1; break; fi
done
if [ -n "$RPM" ]; then
  zypper -n install /tmp/orion-belt-agent.rpm || rpm -Uvh /tmp/orion-belt-agent.rpm
  rm -f /tmp/orion-belt-agent.rpm
elif curl -fsSL -o /usr/bin/orion-belt-agent "$PKG_BASE/orion-belt-agent"; then
  chmod 0755 /usr/bin/orion-belt-agent
else
  echo "failed to download agent package from $PKG_BASE" >&2
  exit 1
fi
`)
	case "alpine":
		b.WriteString(`echo "==> Installing orion-belt-agent (apk)"
APK=""
for cand in \
  "$PKG_BASE/orion-belt-agent_${VERSION}_x86_64.apk" \
  "$PKG_BASE/orion-belt-agent_${VERSION}_linux_amd64.apk"
do
  if curl -fsSL -o /tmp/orion-belt-agent.apk "$cand"; then APK=1; break; fi
done
if [ -n "$APK" ]; then
  apk add --allow-untrusted /tmp/orion-belt-agent.apk
  rm -f /tmp/orion-belt-agent.apk
elif curl -fsSL -o /usr/bin/orion-belt-agent "$PKG_BASE/orion-belt-agent"; then
  chmod 0755 /usr/bin/orion-belt-agent
else
  echo "failed to download agent package from $PKG_BASE" >&2
  exit 1
fi
`)
	default:
		b.WriteString(`echo "==> Installing orion-belt-agent (binary)"
if ! curl -fsSL -o /usr/bin/orion-belt-agent "$PKG_BASE/orion-belt-agent"; then
  echo "failed to download $PKG_BASE/orion-belt-agent" >&2
  exit 1
fi
chmod 0755 /usr/bin/orion-belt-agent
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
if command -v systemctl >/dev/null 2>&1 && [ -f /lib/systemd/system/orion-belt-agent.service ]; then
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
