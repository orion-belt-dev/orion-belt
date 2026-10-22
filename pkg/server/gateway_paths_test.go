package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func fastLoops(t *testing.T) {
	t.Helper()
	prev := []time.Duration{revocationRefreshInterval, hostCertRenewalInterval, sessionCleanupInterval, retentionInterval}
	revocationRefreshInterval, hostCertRenewalInterval, sessionCleanupInterval, retentionInterval =
		5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() {
		revocationRefreshInterval, hostCertRenewalInterval, sessionCleanupInterval, retentionInterval = prev[0], prev[1], prev[2], prev[3]
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestGateway_BackgroundLoops(t *testing.T) {
	fastLoops(t)
	var before *ssh.ServerConfig
	h := startHarnessWith(t, func(cfg *common.Config) {
		caConfig(cfg)
		cfg.Recording.RetentionDays = 1
	}, func(srv *Server) {
		// Put the gateway's own host cert inside its renewal window.
		srv.hostCert.ValidAfter = uint64(time.Now().Add(-100 * time.Hour).Unix())
		srv.hostCert.ValidBefore = uint64(time.Now().Add(time.Hour).Unix())
		before = srv.currentSSHConfig()
	})
	ctx := context.Background()

	stale := common.NewAccessRequest(h.alice.ID, h.machine.ID, []string{"root"}, "old", 60)
	stale.RequestedAt = time.Now().Add(-8 * 24 * time.Hour)
	if err := h.srv.store.CreateAccessRequest(ctx, stale); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stale access request to expire", func() bool {
		r, err := h.srv.store.GetAccessRequest(ctx, stale.ID)
		return err == nil && r.Status == "expired"
	})

	expired := common.NewHTTPSession(h.alice.ID, "tok", "1.2.3.4", "ua", time.Now().Add(-time.Hour))
	if err := h.srv.store.CreateHTTPSession(ctx, expired); err != nil {
		t.Fatal(err)
	}
	eventually(t, "expired HTTP session cleanup", func() bool {
		_, err := h.srv.store.GetHTTPSession(ctx, expired.ID)
		return err != nil
	})

	cert, err := h.srv.ca.IssueUserCert(ctx, h.alice.ID, "alice", h.aliceKey.PublicKey(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.store.RevokeSSHCertificate(ctx, fmt.Sprint(cert.Serial), h.alice.ID, "test"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "revocation to reach the cache", func() bool {
		return h.srv.ca.CertChecker().IsRevoked(cert)
	})

	eventually(t, "gateway host cert renewal", func() bool { return h.srv.currentSSHConfig() != before })
}

func TestGateway_SessionVariants(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	for _, u := range []string{"root", "mute", "chatty"} {
		h.grant(t, u)
	}

	if res := h.runClient(t, "alice", "web-01 scp -t /tmp/x", ""); !strings.Contains(res.out, `ran "scp -t /tmp/x" as root`) {
		t.Errorf("scp exec = %+v", res)
	}
	if res := h.runClient(t, "alice", "web-01 chatty", ""); !strings.Contains(res.out, "chatty") || res.status != 5 {
		t.Errorf("exec with extra agent request = %+v", res)
	}
	if res := h.runClient(t, "alice+chatty%web-01", "", "hi\n"); !strings.Contains(res.out, "shell for chatty") {
		t.Errorf("shell with extra agent request = %+v", res)
	}
	if res := h.runClient(t, "alice+mute%web-01", "", "hi\n"); !strings.Contains(res.out, "echo:hi") {
		t.Errorf("shell without exit-status = %+v", res)
	}

	agent.refusePTY = true
	if res := h.runClient(t, "alice+root%web-01", "", "x\n"); !strings.Contains(res.out, "shell for root") {
		t.Errorf("shell after refused pty = %+v", res)
	}

	// Window changes and other requests sent during an exec reach or are
	// answered by the gateway rather than backing up.
	c, _ := h.dial(t, "alice", h.aliceKey)
	sess, _ := c.NewSession()
	_ = sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{})
	in, _ := sess.StdinPipe()
	if err := sess.Start("web-01 cat"); err != nil {
		t.Fatal(err)
	}
	_ = sess.WindowChange(40, 120)
	if ok, _ := sess.SendRequest("signal", true, ssh.Marshal(&struct{ Signal string }{"INT"})); ok {
		t.Error("signal request acknowledged")
	}
	_ = in.Close()
	_ = sess.Wait()
	eventually(t, "window-change to reach the agent", func() bool {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		return agent.resizes > 0
	})
}

func TestGateway_UserNamedLikeMachineIsAClient(t *testing.T) {
	h := startHarness(t, nil)
	ctx := context.Background()
	_, key := writePrivateKey(t, t.TempDir(), "db")
	dbUser := common.NewUser("db-01", "db@example.com", string(ssh.MarshalAuthorizedKey(key.PublicKey())), false)
	if err := h.srv.store.CreateUser(ctx, dbUser); err != nil {
		t.Fatal(err)
	}
	m := common.NewMachine("db-01", "10.0.0.2", 22, nil)
	m.AgentID = "someone-else"
	if err := h.srv.store.CreateMachine(ctx, m); err != nil {
		t.Fatal(err)
	}

	c, err := h.dial(t, "db-01", key)
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := c.NewSession()
	// As a client, an exec with no target reaches the machine lookup.
	out, _ := sess.CombinedOutput("")
	if !strings.Contains(string(out), "Machine not found") {
		t.Errorf("user named after a machine got %q, want the client path", out)
	}
	if !strings.Contains(h.logs.String(), "is not the agent for machine db-01") {
		t.Error("connection was not classified as a client")
	}
}

func TestGateway_CertRejections(t *testing.T) {
	h := startHarness(t, caConfig)
	ctx := context.Background()

	ghostCert, err := h.srv.ca.IssueUserCert(ctx, "ghost-id", "ghost", h.aliceKey.PublicKey(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.dial(t, "ghost", mustCertSigner(t, ghostCert, h.aliceKey)); err == nil {
		t.Error("user cert for an unknown user accepted")
	}

	wrongPrincipal, err := h.srv.ca.IssueHostCert(ctx, h.machine.ID, []string{"other"}, h.agentKey.PublicKey(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.dial(t, h.machine.Name, mustCertSigner(t, wrongPrincipal, h.agentKey)); err == nil {
		t.Error("host cert for another principal accepted")
	}

	selfSigned := &ssh.Certificate{
		Key: h.agentKey.PublicKey(), CertType: ssh.HostCert, ValidPrincipals: []string{h.machine.Name},
		ValidBefore: ssh.CertTimeInfinity,
	}
	rogue, _ := genKey(t)
	if err := selfSigned.SignCert(rand.Reader, rogue); err != nil {
		t.Fatal(err)
	}
	if _, err := h.dial(t, h.machine.Name, mustCertSigner(t, selfSigned, h.agentKey)); err == nil {
		t.Error("host cert from an untrusted CA accepted")
	}

	odd := &ssh.Certificate{Key: h.aliceKey.PublicKey(), CertType: 3, ValidBefore: ssh.CertTimeInfinity}
	if err := odd.SignCert(rand.Reader, rogue); err != nil {
		t.Fatal(err)
	}
	if _, err := h.dial(t, "alice", mustCertSigner(t, odd, h.aliceKey)); err == nil {
		t.Error("certificate of an unknown type accepted")
	}
}

func TestGateway_AgentSessionRequests(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)
	ch, reqs, err := agent.conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	if ok, _ := ch.SendRequest("exec", true, []byte{1}); ok {
		t.Error("malformed agent exec accepted")
	}
	if ok, _ := ch.SendRequest("pty-req", true, nil); ok {
		t.Error("unsupported agent request accepted")
	}
	_ = ch.Close()
}

func TestGateway_SendAgentCommandTimesOut(t *testing.T) {
	prev := agentCommandTimeout
	agentCommandTimeout = 100 * time.Millisecond
	t.Cleanup(func() { agentCommandTimeout = prev })

	h := startHarness(t, nil)
	h.connectAgent(t)
	if _, err := h.srv.SendAgentCommand(h.machine.ID, "hang"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("SendAgentCommand(hang) = %v", err)
	}
}

func TestStart_PortConflicts(t *testing.T) {
	resetDB(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port

	cfg := baseConfig(t)
	cfg.Server.Port = busyPort
	srv, err := New(cfg, common.NewLoggerTo(common.ERROR, &syncBuffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err == nil || !strings.Contains(err.Error(), "failed to listen") {
		t.Errorf("Start on a busy SSH port = %v", err)
	}
	_ = srv.Stop(context.Background())

	logs := &syncBuffer{}
	cfg = baseConfig(t)
	cfg.Server.APIPort = busyPort
	srv, err = New(cfg, common.NewLoggerTo(common.ERROR, logs))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Start() }()
	waitListening(t, cfg.Server.Port)
	eventually(t, "API server error to be logged", func() bool { return strings.Contains(logs.String(), "API server error") })
	_ = srv.Stop(context.Background())
}

func TestNew_OpenFGAAuthorizer(t *testing.T) {
	logs := &syncBuffer{}
	cfg := baseConfig(t)
	cfg.Auth.OpenFGA = common.OpenFGAConfig{Enabled: true, APIURL: "http://127.0.0.1:1", StoreID: "store"}
	srv, err := New(cfg, common.NewLoggerTo(common.INFO, logs))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Stop(context.Background())
	if !strings.Contains(logs.String(), "OpenFGA authorizer enabled (http://127.0.0.1:1)") {
		t.Errorf("logs lack the OpenFGA line:\n%s", logs.String())
	}
}
