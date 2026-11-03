package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Setup on a fresh package install: server.yaml still has the shipped
// orion.example.com values, and WebAuthn must follow the real address.
func TestApplyPublicAddressMovesShippedWebAuthn(t *testing.T) {
	cfg, err := common.LoadConfig(filepath.Join("..", "..", "config", "server.example.yaml"))
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}

	if !applyPublicAddress(cfg, "http://orion.test:8080", "orion.test", 2222) {
		t.Fatal("changed = false for a new address")
	}
	if cfg.Server.PublicURL != "http://orion.test:8080" || cfg.Server.PublicSSHHost != "orion.test" || cfg.Server.PublicSSHPort != 2222 {
		t.Fatalf("address not applied: %+v", cfg.Server)
	}
	if cfg.Auth.WebAuthn.RPID != "orion.test" {
		t.Fatalf("rp_id = %q, want orion.test", cfg.Auth.WebAuthn.RPID)
	}
	if want := []string{"http://orion.test:8080"}; !reflect.DeepEqual(cfg.Auth.WebAuthn.Origins, want) {
		t.Fatalf("origins = %q, want %q", cfg.Auth.WebAuthn.Origins, want)
	}

	if applyPublicAddress(cfg, "http://orion.test:8080", "orion.test", 2222) {
		t.Fatal("changed = true when the address is the same")
	}

	var out bytes.Buffer
	printPublicAddress(&out, cfg)
	for _, want := range []string{
		`server.public_url: "http://orion.test:8080"`,
		`server.public_ssh_port: 2222`,
		`auth.webauthn.rp_id: "orion.test"`,
		`auth.webauthn.origins: ["http://orion.test:8080"]`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

// A fresh package install keeps the shipped placeholder until something
// replaces it; setup does, and leaves real or empty secrets alone.
func TestEnsureJWTSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "server.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, _ := common.LoadConfig(path)

	var out bytes.Buffer
	if !ensureJWTSecret(&out, path, cfg) || !strings.Contains(out.String(), "Generated a random auth.jwt_secret") {
		t.Fatalf("placeholder not replaced: %s", out.String())
	}
	reloaded, err := common.LoadConfig(path)
	if err != nil || reloaded.Auth.JWTSecret != cfg.Auth.JWTSecret || common.ValidateJWTSecret(reloaded.Auth.JWTSecret) != nil {
		t.Fatalf("saved secret %q (config %q), %v", reloaded.Auth.JWTSecret, cfg.Auth.JWTSecret, err)
	}
	if got, _ := os.ReadFile(path); strings.Count(string(got), "#") != strings.Count(string(raw), "#") {
		t.Error("comments lost")
	}

	// Already valid, or deliberately empty: untouched.
	for _, secret := range []string{reloaded.Auth.JWTSecret, ""} {
		c := &common.Config{}
		c.Auth.JWTSecret = secret
		if ensureJWTSecret(&out, path, c) || c.Auth.JWTSecret != secret {
			t.Errorf("secret %q was replaced", secret)
		}
	}

	// No config file to write: say what to do instead.
	out.Reset()
	c := &common.Config{}
	c.Auth.JWTSecret = "short"
	if ensureJWTSecret(&out, "", c) || !strings.Contains(out.String(), "openssl rand -hex 32") {
		t.Errorf("no path: %s", out.String())
	}
	// A file without a jwt_secret line: same.
	bare := filepath.Join(t.TempDir(), "bare.yaml")
	_ = os.WriteFile(bare, []byte("auth:\n  mfa_required: false\n"), 0o600)
	out.Reset()
	if ensureJWTSecret(&out, bare, c) || !strings.Contains(out.String(), "Could not set auth.jwt_secret") {
		t.Errorf("no line: %s", out.String())
	}
}
