package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// writeTestHostKey generates a fresh ed25519 host key, PEM-encodes it to
// path (the same on-disk format setupSSHConfig reads via
// config.Server.SSHHostKey), and returns its ssh.PublicKey.
func writeTestHostKey(t *testing.T, path string) ssh.PublicKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		t.Fatalf("write host key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap pubkey: %v", err)
	}
	return sshPub
}

func TestSetupSSHConfigPresentsHostCertWhenCAEnabled(t *testing.T) {
	dir := t.TempDir()
	hostKeyPath := filepath.Join(dir, "ssh_host_key")

	store := newFakeStore()
	srv, authority := testServer(t, store)
	writeTestHostKey(t, hostKeyPath)
	srv.config = &common.Config{
		Server: common.ServerConfig{Host: "gateway.example.com", SSHHostKey: hostKeyPath},
	}

	if err := srv.setupSSHConfig(); err != nil {
		t.Fatalf("setupSSHConfig: %v", err)
	}
	srv.sshConfig.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		return &ssh.Permissions{}, nil // auth isn't what's under test here
	}

	// Dial a real client and record whatever key/cert the server presents
	// during KEX — proves AddHostKey registered the Host-CA-signed cert,
	// not just the raw key.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	clientKey, _ := genKey(t)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		sc, chans, reqs, err := ssh.NewServerConn(conn, srv.sshConfig)
		if err != nil {
			return
		}
		defer sc.Close()
		go ssh.DiscardRequests(reqs)
		go func() {
			for nc := range chans {
				_ = nc.Reject(ssh.UnknownChannelType, "test")
			}
		}()
	}()

	var presented ssh.PublicKey
	clientConfig := &ssh.ClientConfig{
		User: "gateway.example.com",
		Auth: []ssh.AuthMethod{ssh.PublicKeys(clientKey)},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			presented = key
			return nil
		},
		Timeout: 5 * time.Second,
	}
	conn, err := ssh.Dial("tcp", ln.Addr().String(), clientConfig)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	defer conn.Close()

	cert, ok := presented.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected the server to present a host certificate by default (client negotiates cert algorithms automatically), got a raw %T", presented)
	}
	if cert.CertType != ssh.HostCert {
		t.Errorf("cert type = %d, want HostCert", cert.CertType)
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "gateway.example.com" {
		t.Errorf("principals = %v, want [gateway.example.com]", cert.ValidPrincipals)
	}
	if string(cert.SignatureKey.Marshal()) != string(authority.HostCAPublicKey().Marshal()) {
		t.Error("host cert was not signed by this server's Host CA")
	}
}

func TestSetupSSHConfigStillWorksWithoutCA(t *testing.T) {
	dir := t.TempDir()
	hostKeyPath := filepath.Join(dir, "ssh_host_key")
	writeTestHostKey(t, hostKeyPath)

	logger := common.NewLogger(common.INFO)
	srv := &Server{
		logger: logger,
		config: &common.Config{
			Server: common.ServerConfig{Host: "gateway.example.com", SSHHostKey: hostKeyPath},
		},
		// ca left nil: SSH CA disabled
	}

	if err := srv.setupSSHConfig(); err != nil {
		t.Fatalf("setupSSHConfig: %v", err)
	}
	if srv.sshConfig == nil {
		t.Fatal("expected sshConfig to be initialized")
	}
}
