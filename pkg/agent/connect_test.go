package agent

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func hostCert(t *testing.T, key gossh.Signer, validAfter, validBefore time.Time) *gossh.Certificate {
	t.Helper()
	cert := &gossh.Certificate{
		Key:             key.PublicKey(),
		Serial:          1,
		CertType:        gossh.HostCert,
		KeyId:           "agent-1",
		ValidPrincipals: []string{"agent-1"},
		ValidAfter:      uint64(validAfter.Unix()),
		ValidBefore:     uint64(validBefore.Unix()),
	}
	if err := cert.SignCert(rand.Reader, newSigner(t)); err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestConnect_PresentsCachedHostCert(t *testing.T) {
	g := startGateway(t)
	a, key := agentFixture(t, g)
	cert := hostCert(t, key, time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err := os.WriteFile(a.config.Auth.KeyFile+"-cert.pub", gossh.MarshalAuthorizedKey(cert), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.connectToServer(); err != nil {
		t.Fatalf("connectToServer: %v", err)
	}
	g.waitConn(t)
	g.mu.Lock()
	ids := append([]string(nil), g.certKeyIDs...)
	g.mu.Unlock()
	if len(ids) == 0 || ids[0] != "agent-1" || a.hostCert == nil {
		t.Errorf("gateway saw cert key ids %v; agent hostCert=%v", ids, a.hostCert)
	}
}

func TestConnect_IgnoresUnusableCachedCert(t *testing.T) {
	g := startGateway(t)
	a, _ := agentFixture(t, g)
	// A cert for some other key cannot be paired with this agent's key.
	other := hostCert(t, newSigner(t), time.Now().Add(-time.Minute), time.Now().Add(time.Hour))
	if err := os.WriteFile(a.config.Auth.KeyFile+"-cert.pub", gossh.MarshalAuthorizedKey(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.connectToServer(); err != nil {
		t.Fatalf("connectToServer: %v", err)
	}
	g.waitConn(t)
	if a.hostCert != nil {
		t.Error("agent kept a cert that does not match its key")
	}
}

func TestConnect_Failures(t *testing.T) {
	g := startGateway(t)
	cases := map[string]func(a *Agent){
		"missing key": func(a *Agent) { a.config.Auth.KeyFile = filepath.Join(t.TempDir(), "absent") },
		"bad key": func(a *Agent) {
			p := filepath.Join(t.TempDir(), "bad")
			_ = os.WriteFile(p, []byte("garbage"), 0o600)
			a.config.Auth.KeyFile = p
		},
		"bad host CA":  func(a *Agent) { a.config.Auth.HostCAPublicKey = "garbage" },
		"gateway down": func(a *Agent) { a.config.Server.Port = 1 },
		"rejected":     func(*Agent) { g.mu.Lock(); g.reject = true; g.mu.Unlock() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g.mu.Lock()
			g.reject = false
			g.mu.Unlock()
			a, _ := agentFixture(t, g)
			mutate(a)
			if err := a.connectToServer(); err == nil {
				t.Error("connectToServer succeeded")
			}
			if err := a.Start(); err == nil || !strings.Contains(err.Error(), "failed to connect to server") {
				t.Errorf("Start = %v", err)
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	a, _ := agentFixture(t, nil)
	if err := a.sendHeartbeat(); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Errorf("heartbeat without connection = %v", err)
	}

	a, _, g := connected(t)
	if err := a.sendHeartbeat(); err != nil {
		t.Fatalf("sendHeartbeat: %v", err)
	}
	g.mu.Lock()
	g.heartbeatOK = false
	g.mu.Unlock()
	if err := a.sendHeartbeat(); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("rejected heartbeat = %v", err)
	}

	_ = a.Stop(context.Background())
	if err := a.sendHeartbeat(); err == nil {
		t.Error("heartbeat on a stopped connection succeeded")
	}
}

func TestMaybeRenewHostCert(t *testing.T) {
	a, _, g := connected(t)
	if err := a.maybeRenewHostCert(); err != nil {
		t.Errorf("no cert to renew = %v", err)
	}

	fresh := hostCert(t, a.privKey, time.Now().Add(-time.Minute), time.Now().Add(10*time.Hour))
	a.hostCert = fresh
	if err := a.maybeRenewHostCert(); err != nil {
		t.Errorf("fresh cert = %v", err)
	}

	a.hostCert = hostCert(t, a.privKey, time.Now().Add(-9*time.Hour), time.Now().Add(time.Hour))
	renewed := hostCert(t, a.privKey, time.Now().Add(-time.Minute), time.Now().Add(24*time.Hour))
	setRenew := func(ok bool, payload []byte) {
		g.mu.Lock()
		g.renewOK, g.renewPayload = ok, payload
		g.mu.Unlock()
	}

	setRenew(false, nil)
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "renew rejected: rejected") {
		t.Errorf("bare rejection = %v", err)
	}
	setRenew(false, []byte("machine archived"))
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "machine archived") {
		t.Errorf("rejection with reason = %v", err)
	}
	setRenew(true, []byte("garbage"))
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "parse renewed cert") {
		t.Errorf("garbage payload = %v", err)
	}
	setRenew(true, gossh.MarshalAuthorizedKey(a.privKey.PublicKey()))
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "not an SSH certificate") {
		t.Errorf("plain key payload = %v", err)
	}

	setRenew(true, gossh.MarshalAuthorizedKey(renewed))
	certPath := a.certPath
	a.certPath = filepath.Join(t.TempDir(), "missing", "cert.pub")
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "write renewed cert") {
		t.Errorf("unwritable dir = %v", err)
	}
	a.certPath = t.TempDir() // a directory: rename onto it fails
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "install renewed cert") {
		t.Errorf("rename onto dir = %v", err)
	}

	a.certPath = certPath
	if err := a.maybeRenewHostCert(); err != nil {
		t.Fatalf("renewal = %v", err)
	}
	if a.hostCert.ValidBefore != renewed.ValidBefore {
		t.Error("agent did not adopt the renewed cert")
	}
	if data, _ := os.ReadFile(certPath); string(data) != string(gossh.MarshalAuthorizedKey(renewed)) {
		t.Error("renewed cert not written to disk")
	}

	_ = a.Stop(context.Background())
	a.hostCert = hostCert(t, a.privKey, time.Now().Add(-9*time.Hour), time.Now().Add(time.Hour))
	if err := a.maybeRenewHostCert(); err == nil || !strings.Contains(err.Error(), "renew request") {
		t.Errorf("renewal on closed connection = %v", err)
	}
}

func TestStop_WithoutConnection(t *testing.T) {
	a, _ := agentFixture(t, nil)
	if err := a.Stop(context.Background()); err != nil {
		t.Errorf("Stop = %v", err)
	}
}

func TestResolveUnixIdentity_Current(t *testing.T) {
	username := fakeLoginShell(t, "")
	id, err := resolveUnixIdentity(username)
	if err != nil || id.Username != username || id.UID != uint32(os.Getuid()) || !strings.HasSuffix(id.Shell, "fake-shell") {
		t.Errorf("resolveUnixIdentity = %+v, %v", id, err)
	}
}

func TestKeepAlive(t *testing.T) {
	a, _, g := connected(t)

	a.keepAlive() // healthy: heartbeat only
	g.mu.Lock()
	beats := g.heartbeats
	g.mu.Unlock()
	if beats != 1 {
		t.Fatalf("heartbeats = %d, want 1", beats)
	}

	// A dropped connection is re-established on the next tick.
	_ = a.Stop(context.Background())
	a.keepAlive()
	g.waitConn(t)
	if err := a.sendHeartbeat(); err != nil {
		t.Errorf("heartbeat after reconnect = %v", err)
	}

	// Renewal and reconnect failures are logged, not fatal.
	a.hostCert = hostCert(t, a.privKey, time.Now().Add(-9*time.Hour), time.Now().Add(time.Hour))
	_ = a.Stop(context.Background())
	a.config.Server.Port = 1
	a.keepAlive()
}
