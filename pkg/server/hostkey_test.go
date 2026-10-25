package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// A host without ssh-keygen at package install (minimal openSUSE) has no
// host key; the server must create one instead of refusing to start.
func TestLoadOrCreateHostKeyCreatesMissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_key")

	signer, created, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("loadOrCreateHostKey: %v", err)
	}
	if !created {
		t.Fatal("created = false for a missing key")
	}
	if signer.PublicKey().Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("key type = %s, want ed25519", signer.PublicKey().Type())
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("key mode = %o, want 600", mode)
	}
	pub, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(pub)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if !bytes.Equal(parsed.Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal(".pub does not match the private key")
	}

	// A second start loads the same key rather than replacing it.
	again, created, err := loadOrCreateHostKey(path)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if created {
		t.Fatal("created = true for an existing key")
	}
	if !bytes.Equal(again.PublicKey().Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal("existing host key was replaced")
	}
}

func TestLoadOrCreateHostKeyRejectsCorruptKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_key")
	if err := os.WriteFile(path, []byte("not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateHostKey(path); err == nil {
		t.Fatal("expected an error for a corrupt key, got nil")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "not a key" {
		t.Fatalf("corrupt key was overwritten: %q, %v", got, err)
	}
}

func TestSetupSSHConfigCreatesMissingHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ssh_host_key")
	srv, _ := testServer(t, newFakeStore())
	srv.config = &common.Config{
		Server: common.ServerConfig{Host: "gateway.example.com", SSHHostKey: path},
	}

	if err := srv.setupSSHConfig(); err != nil {
		t.Fatalf("setupSSHConfig: %v", err)
	}
	if srv.hostPrivate == nil {
		t.Fatal("host key not loaded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("host key not written: %v", err)
	}
}
