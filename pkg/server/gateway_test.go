package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// --- client sessions ------------------------------------------------------------------

func TestGateway_ExecThroughAgentIsRecordedAndAudited(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	h.grant(t, "deploy")

	res := h.runClient(t, "alice", "deploy@web-01 uptime -p", "")
	if !strings.Contains(res.out, `ran "uptime -p" as deploy`) || res.status != 5 {
		t.Fatalf("exec through gateway = %+v", res)
	}
	if res := h.runClient(t, "alice", "deploy@web-01 cat", "piped"); res.out != "piped" {
		t.Errorf("stdin through gateway = %+v", res)
	}
	agent.mu.Lock()
	execs := append([]string(nil), agent.execs...)
	agent.mu.Unlock()
	if len(execs) != 2 || execs[0] != "uptime -p" {
		t.Errorf("agent saw %v", execs)
	}

	sessions, err := h.srv.store.ListUserSessions(context.Background(), h.alice.ID, 10, 0)
	if err != nil || len(sessions) != 2 || sessions[1].Status != "completed" || sessions[1].RemoteUser != "deploy" {
		t.Fatalf("session records = %+v, %v", sessions, err)
	}
	cast, err := os.ReadFile(filepath.Join(h.cfg.Recording.StoragePath, sessions[1].ID+".cast"))
	if err != nil || !strings.Contains(string(cast), "ran") {
		t.Errorf("recording = %q, %v; want the agent output", cast, err)
	}
}

func TestGateway_InteractiveShellViaOpenSSHUsername(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	h.grant(t, "deploy")

	res := h.runClient(t, "alice+deploy%web-01", "", "ls\n")
	if !strings.Contains(res.out, "shell for deploy") || !strings.Contains(res.out, "echo:ls") || res.status != 7 {
		t.Fatalf("shell = %+v", res)
	}
	agent.mu.Lock()
	ptys := agent.ptys
	agent.mu.Unlock()
	if ptys != 1 {
		t.Errorf("pty requests forwarded = %d, want 1", ptys)
	}
}

func TestGateway_ExecOnMachineNamedInUsername(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	h.grant(t, "deploy")
	h.grant(t, "root")

	cases := []struct {
		name, user, command, want string
	}{
		{"remote user and machine", "alice+deploy%web-01", "uptime", `ran "uptime" as deploy`},
		{"remote user defaults to root", "alice+web-01", "uptime", `ran "uptime" as root`},
		// The whole command reaches the agent: no word is taken as a target.
		{"shell metacharacters", "alice+web-01", `/bin/sh -c 'echo "$HOME" | wc -c'`, `as root`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.runClient(t, tc.user, tc.command, "")
			if !strings.Contains(res.out, tc.want) || res.status != 5 {
				t.Fatalf("exec = %+v, want %q", res, tc.want)
			}
			agent.mu.Lock()
			last := agent.execs[len(agent.execs)-1]
			agent.mu.Unlock()
			if last != tc.command {
				t.Errorf("agent ran %q, want %q", last, tc.command)
			}
		})
	}

	sessions, err := h.srv.store.ListUserSessions(context.Background(), h.alice.ID, 10, 0)
	if err != nil || len(sessions) != len(cases) {
		t.Fatalf("session records = %+v, %v", sessions, err)
	}
	remoteUsers := map[string]bool{}
	for _, sess := range sessions {
		remoteUsers[sess.RemoteUser] = true
		if _, err := os.Stat(filepath.Join(h.cfg.Recording.StoragePath, sess.ID+".cast")); err != nil {
			t.Errorf("session %s has no recording: %v", sess.ID, err)
		}
	}
	if !remoteUsers["deploy"] || !remoteUsers["root"] {
		t.Errorf("sessions recorded remote users %v, want deploy and root", remoteUsers)
	}
}

func TestGateway_ExecOnMachineNamedInUsernameNeedsAGrant(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	h.grant(t, "deploy")

	res := h.runClient(t, "alice+root%web-01", "id", "")
	if !strings.Contains(res.out, "Permission denied") || !strings.Contains(res.out, "root@web-01") || res.status != 1 {
		t.Errorf("exec without a grant = %+v", res)
	}
	// The denial says why: the grant covers deploy, not root.
	if !strings.Contains(res.out, "grant allows deploy, not root") {
		t.Errorf("denial does not explain itself: %q", res.out)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if len(agent.execs) != 0 {
		t.Errorf("agent ran %v for a denied user", agent.execs)
	}
}

func TestGateway_ExecForwardsPTYRequest(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	h.grant(t, "deploy")

	c, err := h.dial(t, "alice+deploy%web-01", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	out, err := sess.Output("sudo -n true")
	if !strings.Contains(string(out), `ran "sudo -n true" as deploy`) {
		t.Fatalf("exec with a PTY = %q, %v", out, err)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.ptys != 1 {
		t.Errorf("pty requests forwarded = %d, want 1", agent.ptys)
	}
}

// OpenSSH ends an exec session when the command exits. Clients that keep
// stdin open (Ansible, ssh with ControlMaster) hung until the gateway waited
// for client EOF as well.
func TestGateway_ExecEndsWhenCommandExitsWithStdinOpen(t *testing.T) {
	h := startHarness(t, nil)
	h.connectAgent(t)
	h.grant(t, "deploy")

	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var out bytes.Buffer
	sess.Stdout = &out
	if err := sess.Start("deploy@web-01 uptime"); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	select {
	case err := <-done:
		if exitErr, ok := err.(*ssh.ExitError); !ok || exitErr.ExitStatus() != 5 {
			t.Errorf("exit = %v, want status 5", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("exec session did not end after the command exited")
	}
	if !strings.Contains(out.String(), `ran "uptime" as deploy`) {
		t.Errorf("output = %q", out.String())
	}
}

func TestGateway_ShellRefusedByAgent(t *testing.T) {
	h := startHarness(t, nil)
	h.connectAgent(t)
	h.grant(t, "nobody")
	res := h.runClient(t, "alice+nobody%web-01", "", "")
	if strings.Contains(res.out, "shell for") {
		t.Errorf("shell refused by the agent produced output: %+v", res)
	}
}

func TestGateway_ExecEdgeCases(t *testing.T) {
	h := startHarness(t, nil)
	h.connectAgent(t)
	h.grant(t, "root")

	res := h.runClient(t, "alice", "web-01 refuse", "")
	if !strings.Contains(res.out, "Command execution rejected") || res.status != 1 {
		t.Errorf("agent-refused exec = %+v", res)
	}

	start := time.Now()
	res = h.runClient(t, "alice", "web-01 no-status", "")
	if !strings.Contains(res.out, "silent") || res.status != 1 {
		t.Errorf("exec without exit-status = %+v", res)
	}
	if time.Since(start) < 4*time.Second {
		t.Error("gateway did not wait for a late exit-status")
	}
}

func TestGateway_ClientRejections(t *testing.T) {
	h := startHarness(t, nil)
	cases := []struct {
		name, user, command, want string
	}{
		{"no target shows usage", "alice", "", "Orion-Belt Gateway (OpenSSH compatible)"},
		{"unknown machine", "alice", "db-99", "Machine not found: db-99"},
		{"no permission", "alice", "root@web-01 id", "Permission denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.runClient(t, tc.user, tc.command, "")
			if !strings.Contains(res.out, tc.want) {
				t.Errorf("output = %q, want %q", res.out, tc.want)
			}
		})
	}

	h.grant(t, "root")
	res := h.runClient(t, "alice", "web-01 id", "")
	if !strings.Contains(res.out, "Machine is not available") || res.status != 1 {
		t.Errorf("agent offline = %+v", res)
	}
}

func TestGateway_ClientRequestHandling(t *testing.T) {
	h := startHarness(t, nil)
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := c.OpenChannel("direct-tcpip", nil); err == nil {
		t.Error("gateway accepted a port-forwarding channel")
	}

	sess, _ := c.NewSession()
	if err := sess.Setenv("LANG", "C"); err != nil {
		t.Errorf("env request = %v", err)
	}
	if ok, _ := sess.SendRequest("x11-req", true, nil); ok {
		t.Error("unsupported request accepted")
	}
	if ok, _ := sess.SendRequest("subsystem", true, ssh.Marshal(&struct{ Name string }{"netconf"})); ok {
		t.Error("unknown subsystem accepted")
	}
	out, _ := sess.StdoutPipe()
	if err := sess.RequestSubsystem("sftp"); err == nil {
		t.Error("sftp subsystem accepted")
	}
	buf := make([]byte, 128)
	n, _ := out.Read(buf)
	if !strings.Contains(string(buf[:n]), "SFTP subsystem not available") {
		t.Errorf("sftp refusal = %q", buf[:n])
	}

	sess2, _ := c.NewSession()
	if ok, _ := sess2.SendRequest("exec", true, []byte{1}); ok {
		t.Error("malformed exec accepted")
	}
}

func TestGateway_AuthFailures(t *testing.T) {
	h := startHarness(t, func(cfg *common.Config) { cfg.Auth.MFARequired = true })
	if _, err := h.dial(t, "alice", h.aliceKey); err == nil {
		t.Error("login without MFA enrollment succeeded while MFA is required")
	}
	_, stranger := writePrivateKey(t, t.TempDir(), "stranger")
	if _, err := h.dial(t, "alice", stranger); err == nil {
		t.Error("login with an unknown key succeeded")
	}
}

// --- agents --------------------------------------------------------------------------

func TestGateway_AgentLifecycle(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	ctx := context.Background()

	if m, _ := h.srv.store.GetMachine(ctx, h.machine.ID); !m.IsActive || m.LastSeenAt == nil {
		t.Errorf("machine after agent connect = %+v", m)
	}

	if ok, _ := agent.agentExec(t, "heartbeat"); !ok {
		t.Error("heartbeat rejected")
	}
	if ok, out := agent.agentExec(t, "agent-register"); !ok || out != "Agent registered\n" {
		t.Errorf("agent-register = %v %q", ok, out)
	}
	if ok, _ := agent.agentExec(t, "rm -rf /"); ok {
		t.Error("unknown agent command accepted")
	}
	if ok, _, _ := agent.conn.SendRequest("keepalive@openssh.com", true, nil); ok {
		t.Error("unknown global request accepted")
	}
	if ok, payload, _ := agent.conn.SendRequest(ca.AgentCertRenewRequest, true, nil); ok || string(payload) != "ssh ca disabled" {
		t.Errorf("renewal without CA = %v %q", ok, payload)
	}
	if _, _, err := agent.conn.OpenChannel("direct-tcpip", nil); err == nil {
		t.Error("gateway accepted a non-session channel from the agent")
	}

	if m, err := h.srv.ResolveMachine("web-01"); err != nil || m.ID != h.machine.ID {
		t.Errorf("ResolveMachine = %+v, %v", m, err)
	}
	ch, reqs, err := h.srv.OpenAgentSession(h.machine.ID, "root")
	if err != nil {
		t.Fatalf("OpenAgentSession: %v", err)
	}
	go ssh.DiscardRequests(reqs)
	_ = ch.Close()

	out, err := h.srv.SendAgentCommand(h.machine.ID, "orion:ping")
	if err != nil || !strings.Contains(string(out), `ran "orion:ping"`) {
		t.Errorf("SendAgentCommand = %q, %v", out, err)
	}
	if _, err := h.srv.SendAgentCommand(h.machine.ID, "refuse"); err == nil {
		t.Error("refused agent command reported success")
	}

	if err := h.srv.DisconnectAgent(h.machine.ID); err != nil {
		t.Fatalf("DisconnectAgent: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m, _ := h.srv.store.GetMachine(ctx, h.machine.ID); !m.IsActive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m, _ := h.srv.store.GetMachine(ctx, h.machine.ID); m.IsActive {
		t.Error("machine still active after the agent was disconnected")
	}

	for name, call := range map[string]func() error{
		"DisconnectAgent":  func() error { return h.srv.DisconnectAgent(h.machine.ID) },
		"SendAgentCommand": func() error { _, err := h.srv.SendAgentCommand(h.machine.ID, "x"); return err },
		"OpenAgentSession": func() error { _, _, err := h.srv.OpenAgentSession(h.machine.ID, "root"); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "not connected") {
			t.Errorf("%s with no agent = %v", name, err)
		}
	}
}

func TestGateway_Accessors(t *testing.T) {
	h := startHarness(t, nil)
	if h.srv.GetStore() == nil || h.srv.GetAuthService() == nil || h.srv.GetPluginManager() == nil {
		t.Error("accessor returned nil")
	}
	if len(h.srv.GetPluginManager().List()) != 5 {
		t.Errorf("built-in plugins = %d, want 5", len(h.srv.GetPluginManager().List()))
	}
}

// --- SSH CA ----------------------------------------------------------------------------

func caConfig(cfg *common.Config) {
	cfg.SSHCA = common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef", HostPrincipals: []string{"gw.test"}}
}

func TestGateway_HostCertAgentAndRenewal(t *testing.T) {
	h := startHarness(t, caConfig)
	ctx := context.Background()

	cert, err := h.srv.ca.IssueHostCert(ctx, h.machine.ID, []string{h.machine.Name}, h.agentKey.PublicKey(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certSigner, _ := ssh.NewCertSigner(cert, h.agentKey)
	c, err := h.dial(t, h.machine.Name, certSigner)
	if err != nil {
		t.Fatalf("host-cert agent dial: %v", err)
	}
	waitAgent(t, h)

	ok, payload, err := c.SendRequest(ca.AgentCertRenewRequest, true, ssh.MarshalAuthorizedKey(h.agentKey.PublicKey()))
	if err != nil || !ok {
		t.Fatalf("renewal = %v %q %v", ok, payload, err)
	}
	if pub, _, _, _, err := ssh.ParseAuthorizedKey(payload); err != nil {
		t.Errorf("renewed cert does not parse: %v", err)
	} else if _, isCert := pub.(*ssh.Certificate); !isCert {
		t.Error("renewal payload is not a certificate")
	}

	_, other := writePrivateKey(t, t.TempDir(), "other")
	if ok, payload, _ := c.SendRequest(ca.AgentCertRenewRequest, true, ssh.MarshalAuthorizedKey(other.PublicKey())); ok || string(payload) != "public key mismatch" {
		t.Errorf("renewal for another key = %v %q", ok, payload)
	}
	if ok, payload, _ := c.SendRequest(ca.AgentCertRenewRequest, true, []byte("garbage")); ok || string(payload) != "invalid public key" {
		t.Errorf("renewal with garbage = %v %q", ok, payload)
	}
	if ok, _, _ := c.SendRequest(ca.AgentCertRenewRequest, false, nil); ok {
		t.Error("no-reply renewal returned ok")
	}
}

func waitAgent(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.srv.ListConnectedAgents()) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent never registered")
}

func TestGateway_UserCertLoginAndMFA(t *testing.T) {
	h := startHarness(t, caConfig)
	cert, err := h.srv.ca.IssueUserCert(context.Background(), h.alice.ID, "alice", h.aliceKey.PublicKey(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	certSigner, _ := ssh.NewCertSigner(cert, h.aliceKey)
	if _, err := h.dial(t, "alice", certSigner); err != nil {
		t.Fatalf("user cert login: %v", err)
	}

	h.srv.config.Auth.MFARequired = true
	if _, err := h.dial(t, "alice", certSigner); err == nil {
		t.Error("user cert login without MFA succeeded while MFA is required")
	}
}

func TestGateway_RenewsOwnHostCertWhenDue(t *testing.T) {
	h := startHarness(t, caConfig)
	if h.srv.hostCert == nil {
		t.Fatal("gateway host cert not issued at startup")
	}
	before := h.srv.currentSSHConfig()

	h.srv.renewGatewayHostCertIfNeeded() // fresh: nothing to do
	if h.srv.currentSSHConfig() != before {
		t.Error("a fresh host cert was renewed")
	}

	h.srv.hostCert.ValidAfter = uint64(time.Now().Add(-100 * time.Hour).Unix())
	h.srv.hostCert.ValidBefore = uint64(time.Now().Add(time.Hour).Unix())
	h.srv.renewGatewayHostCertIfNeeded()
	if h.srv.currentSSHConfig() == before {
		t.Error("a host cert inside the renewal window was not renewed")
	}
}

// --- construction ---------------------------------------------------------------------

func TestNew_ConfigurationErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, []byte("not a key"), 0o600)

	cases := map[string]func(cfg *common.Config){
		"unknown driver":       func(cfg *common.Config) { cfg.Database.Driver = "mysql" },
		"database unreachable": func(cfg *common.Config) { cfg.Database.ConnectionString = "postgres://x@127.0.0.1:1/x?sslmode=disable" },
		"openfga incomplete":   func(cfg *common.Config) { cfg.Auth.OpenFGA.Enabled = true },
		"ssh ca without key":   func(cfg *common.Config) { cfg.SSHCA.Enabled = true },
		"no host key":          func(cfg *common.Config) { cfg.Server.SSHHostKey = "" },
		"unreadable host key":  func(cfg *common.Config) { cfg.Server.SSHHostKey = filepath.Join(t.TempDir(), "absent") },
		"invalid host key":     func(cfg *common.Config) { cfg.Server.SSHHostKey = file },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig(t)
			mutate(cfg)
			if _, err := New(cfg, common.NewLoggerTo(common.ERROR, &syncBuffer{})); err == nil {
				t.Error("New succeeded")
			}
		})
	}
}

func TestNew_OptionalFeaturesAndPluginSeeding(t *testing.T) {
	logs := &syncBuffer{}
	cfg := baseConfig(t)
	cfg.Recording.EncryptionKey = "recording key"
	cfg.Recording.RetentionDays = 30
	cfg.Auth.WebAuthn = common.WebAuthnConfig{Enabled: true, RPID: "", Origins: nil}
	cfg.Plugins = map[string]map[string]interface{}{"webhook-notifications": {"url": "https://hooks.example.com/x"}}
	resetDB(t)

	srv, err := New(cfg, common.NewLoggerTo(common.DEBUG, logs))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Stop(context.Background())
	for _, want := range []string{"Session recording encryption enabled", "WebAuthn/FIDO2 enabled (rp_id=localhost)"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q", want)
		}
	}
	if s, err := srv.store.GetPluginSetting(context.Background(), "webhook-notifications"); err != nil || s.Config["url"] != "https://hooks.example.com/x" {
		t.Errorf("server.yaml plugin config not seeded: %+v, %v", s, err)
	}

	// An RP ID the library rejects (here, one with a port) leaves the gateway
	// running with WebAuthn off instead of failing to start.
	logs2 := &syncBuffer{}
	cfg2 := baseConfig(t)
	cfg2.Auth.WebAuthn = common.WebAuthnConfig{Enabled: true, RPID: "gw.example.com:8443"}
	srv2, err := New(cfg2, common.NewLoggerTo(common.DEBUG, logs2))
	if err != nil {
		t.Fatalf("New with bad WebAuthn config: %v", err)
	}
	defer srv2.Stop(context.Background())
	if !strings.Contains(logs2.String(), "WebAuthn disabled") {
		t.Errorf("log lacks the WebAuthn warning:\n%s", logs2.String())
	}
}

func TestResolveDirWithCreate(t *testing.T) {
	logger := common.NewLoggerTo(common.ERROR, &syncBuffer{})
	dir := filepath.Join(t.TempDir(), "rec")
	if got := resolveDirWithCreate(dir, "/fallback", logger); got != dir {
		t.Errorf("resolveDirWithCreate = %q", got)
	}
	if got := resolveDirWithCreate("", dir, logger); got != dir {
		t.Errorf("empty path = %q, want fallback", got)
	}
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, nil, 0o600)
	if got := resolveDirWithCreate(filepath.Join(file, "sub"), "/fallback", logger); got != "/fallback" {
		t.Errorf("uncreatable dir = %q, want fallback", got)
	}
}

func TestSplitFirst(t *testing.T) {
	if got := splitFirst("web-01 uptime -p"); len(got) != 2 || got[1] != "uptime -p" {
		t.Errorf("splitFirst = %q", got)
	}
	if got := splitFirst("web-01"); len(got) != 1 {
		t.Errorf("splitFirst(no space) = %q", got)
	}
}

// The first session on a sensitive machine is flagged for review, and only
// the first; the session itself runs normally.
func TestGateway_FlagsFirstSessionOnSensitiveMachine(t *testing.T) {
	h := startHarness(t, nil)
	ctx := context.Background()
	h.machine.Tags = map[string]string{common.SensitivityTagKey: common.SensitivityHigh}
	if err := h.srv.store.UpdateMachine(ctx, h.machine); err != nil {
		t.Fatal(err)
	}
	h.connectAgent(t)
	h.grant(t, "root")

	for i := 0; i < 2; i++ {
		if res := h.runClient(t, "alice+web-01", "uptime", ""); res.status != 5 {
			t.Fatalf("session %d = %+v", i, res)
		}
	}

	flags, err := h.srv.store.ListAuditLogs(ctx, 10, 0, map[string]interface{}{"action_prefix": "anomaly."})
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 1 || flags[0].Action != "anomaly.first_time_access" || flags[0].UserID != h.alice.ID {
		t.Fatalf("flags = %+v", flags)
	}
	if signal, _ := flags[0].Metadata["signal"].(string); !strings.Contains(signal, "web-01") {
		t.Errorf("signal = %q", signal)
	}
}
