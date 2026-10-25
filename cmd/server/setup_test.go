package main

import (
	"bytes"
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
