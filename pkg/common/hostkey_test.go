package common

import (
	"crypto/rand"
	"crypto/rsa"
	"golang.org/x/crypto/ssh/knownhosts"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHostKeyCallbackTOFU(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")

	cb, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "ask",
	}, nil)
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}

	key := mustTestPublicKey(t)
	addr := &mockAddr{s: "127.0.0.1:2222"}
	if err := cb("gateway.example.com:2222", addr, key); err != nil {
		t.Fatalf("first connect (TOFU): %v", err)
	}

	data, err := os.ReadFile(kh)
	if err != nil || len(data) == 0 {
		t.Fatalf("known_hosts not written: %v %q", err, data)
	}

	if err := cb("gateway.example.com:2222", addr, key); err != nil {
		t.Fatalf("second connect: %v", err)
	}
}

func TestHostKeyCallbackStrictRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")

	cb, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "yes",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	err = cb("gateway.example.com:2222", &mockAddr{s: "127.0.0.1:2222"}, mustTestPublicKey(t))
	if err == nil {
		t.Fatal("expected rejection for unknown host")
	}
}

func mustTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestHostKeyCallbackTrustsHostCASignedCert(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")

	hostCA := mustTestSigner(t)
	hostKey := mustTestSigner(t)

	cert := &ssh.Certificate{
		Key:             hostKey.PublicKey(),
		Serial:          1,
		CertType:        ssh.HostCert,
		ValidPrincipals: []string{"gateway.example.com"},
		ValidAfter:      0,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, hostCA); err != nil {
		t.Fatalf("SignCert: %v", err)
	}

	cb, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "yes", // would reject an unknown raw key — proves the CA path is what's accepting this
		HostCAPublicKey:       string(ssh.MarshalAuthorizedKey(hostCA.PublicKey())),
	}, nil)
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}

	if err := cb("gateway.example.com:2222", &mockAddr{s: "127.0.0.1:2222"}, cert); err != nil {
		t.Fatalf("expected cert signed by the trusted Host CA to verify, got: %v", err)
	}
}

func TestHostKeyCallbackRejectsUntrustedCASignedCert(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")

	trustedCA := mustTestSigner(t)
	rogueCA := mustTestSigner(t)
	hostKey := mustTestSigner(t)

	cert := &ssh.Certificate{
		Key:             hostKey.PublicKey(),
		Serial:          1,
		CertType:        ssh.HostCert,
		ValidPrincipals: []string{"gateway.example.com"},
		ValidAfter:      0,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, rogueCA); err != nil {
		t.Fatalf("SignCert: %v", err)
	}

	cb, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "ask",
		HostCAPublicKey:       string(ssh.MarshalAuthorizedKey(trustedCA.PublicKey())),
	}, nil)
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}

	if err := cb("gateway.example.com:2222", &mockAddr{s: "127.0.0.1:2222"}, cert); err == nil {
		t.Fatal("expected cert signed by an untrusted CA to be rejected")
	}
}

// With a Host CA, a raw key is accepted only if already pinned; an unknown
// one is refused rather than learned (an impostor could omit the cert).
func TestHostKeyCallbackRawKeyWhenCAConfigured(t *testing.T) {
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")

	hostCA := mustTestSigner(t)

	cb, err := NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "ask",
		HostCAPublicKey:       string(ssh.MarshalAuthorizedKey(hostCA.PublicKey())),
	}, nil)
	if err != nil {
		t.Fatalf("NewHostKeyCallback: %v", err)
	}

	rawKey := mustTestPublicKey(t)
	addr := &mockAddr{s: "127.0.0.1:2222"}
	if err := cb("impostor.example.com:2222", addr, rawKey); err == nil {
		t.Fatal("unknown raw key accepted although a Host CA is configured")
	}
	pinned := knownhosts.Line([]string{knownhosts.Normalize("legacy-host.example.com:2222")}, rawKey)
	if err := os.WriteFile(kh, []byte(pinned+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cb, err = NewHostKeyCallback(HostKeyConfig{
		KnownHosts:            kh,
		StrictHostKeyChecking: "ask",
		HostCAPublicKey:       string(ssh.MarshalAuthorizedKey(hostCA.PublicKey())),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cb("legacy-host.example.com:2222", addr, rawKey); err != nil {
		t.Errorf("pinned raw key refused: %v", err)
	}
}

type mockAddr struct{ s string }

func (m *mockAddr) Network() string { return "tcp" }
func (m *mockAddr) String() string  { return m.s }

func mustTestPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
