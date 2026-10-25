package common

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// The packaged server.yaml ships rp_id and origins for orion.example.com.
// When setup sets the real public address they must move with it, or
// security-key sign-in fails on every new install.
func TestWebAuthnFollowPublicURLMovesShippedExample(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	cfg, err := LoadConfig(filepath.Join(filepath.Dir(file), "..", "..", "config", "server.example.yaml"))
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	old := cfg.Server.AdvertisedURL()
	cfg.Server.PublicURL = "http://orion.test:8080"
	cfg.Server.WebAuthnFollowPublicURL(&cfg.Auth.WebAuthn, old)

	if cfg.Auth.WebAuthn.RPID != "orion.test" {
		t.Fatalf("RPID = %q, want orion.test", cfg.Auth.WebAuthn.RPID)
	}
	if want := []string{"http://orion.test:8080"}; !reflect.DeepEqual(cfg.Auth.WebAuthn.Origins, want) {
		t.Fatalf("Origins = %q, want %q", cfg.Auth.WebAuthn.Origins, want)
	}
}

func TestWebAuthnFollowPublicURL(t *testing.T) {
	tests := []struct {
		name        string
		oldBase     string
		rpid        string
		origins     []string
		wantRPID    string
		wantOrigins []string
	}{
		{
			name:     "empty values are filled",
			oldBase:  "https://old.example",
			wantRPID: "gw.example.com", wantOrigins: []string{"https://gw.example.com"},
		},
		{
			name:    "values for the old address move",
			oldBase: "https://old.example/",
			rpid:    "old.example", origins: []string{"https://old.example"},
			wantRPID: "gw.example.com", wantOrigins: []string{"https://gw.example.com"},
		},
		{
			name:    "other origins are kept",
			oldBase: "https://old.example",
			rpid:    "old.example", origins: []string{"https://old.example/", "https://extra.example"},
			wantRPID: "gw.example.com", wantOrigins: []string{"https://gw.example.com", "https://extra.example"},
		},
		{
			name:    "explicit values are left alone",
			oldBase: "https://old.example",
			rpid:    "example.com", origins: []string{"https://console.example.com"},
			wantRPID: "example.com", wantOrigins: []string{"https://console.example.com"},
		},
		{
			name:    "no previous address only fills empty values",
			oldBase: "",
			rpid:    "custom.example", origins: []string{"https://custom.example"},
			wantRPID: "custom.example", wantOrigins: []string{"https://custom.example"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := &WebAuthnConfig{RPID: tt.rpid, Origins: tt.origins}
			ServerConfig{PublicURL: "https://gw.example.com"}.WebAuthnFollowPublicURL(wa, tt.oldBase)
			if wa.RPID != tt.wantRPID {
				t.Errorf("RPID = %q, want %q", wa.RPID, tt.wantRPID)
			}
			if !reflect.DeepEqual(wa.Origins, tt.wantOrigins) {
				t.Errorf("Origins = %q, want %q", wa.Origins, tt.wantOrigins)
			}
		})
	}
}

func TestWebAuthnFollowPublicURLNoPublicURL(t *testing.T) {
	ServerConfig{PublicURL: "https://x"}.WebAuthnFollowPublicURL(nil, "") // must not panic
	wa := &WebAuthnConfig{RPID: "old.example", Origins: []string{"https://old.example"}}
	ServerConfig{}.WebAuthnFollowPublicURL(wa, "https://old.example")
	if wa.RPID != "old.example" || len(wa.Origins) != 1 || wa.Origins[0] != "https://old.example" {
		t.Fatalf("changed without a public URL: %+v", wa)
	}
}
