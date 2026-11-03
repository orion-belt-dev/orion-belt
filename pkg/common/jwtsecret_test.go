package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateJWTSecret(t *testing.T) {
	ok := []string{"", strings.Repeat("a", MinJWTSecretLen), "3f1c0e8b9a7d4c2e5f6a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c1d0e"}
	for _, s := range ok {
		if err := ValidateJWTSecret(s); err != nil {
			t.Errorf("ValidateJWTSecret(%q) = %v", s, err)
		}
	}
	bad := []string{"change-me-to-a-long-random-secret", " lab-only-change-me ", "qemu-lab-secret-change-me", "short", strings.Repeat("a", MinJWTSecretLen-1)}
	for _, s := range bad {
		if err := ValidateJWTSecret(s); err == nil || !strings.Contains(err.Error(), "openssl rand -hex 32") {
			t.Errorf("ValidateJWTSecret(%q) = %v, want a refusal saying how to fix it", s, err)
		}
	}
}

func TestGenerateJWTSecret(t *testing.T) {
	a, err := GenerateJWTSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateJWTSecret()
	if len(a) != 64 || a == b || ValidateJWTSecret(a) != nil {
		t.Errorf("generated %q and %q", a, b)
	}
}

func TestReplaceJWTSecret_EditsOnlyThatLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yaml")
	orig := "# top comment\nauth:\n  # the secret\n  jwt_secret: \"change-me-to-a-long-random-secret\"  \n  jwt_expiry_hours: 24\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("ab", 32)
	if err := ReplaceJWTSecret(path, secret); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := strings.Replace(orig, `"change-me-to-a-long-random-secret"  `, `"`+secret+`"`, 1)
	if string(got) != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
	cfg, err := LoadConfig(path)
	if err != nil || cfg.Auth.JWTSecret != secret {
		t.Errorf("reloaded secret = %q, %v", cfg.Auth.JWTSecret, err)
	}

	bare := filepath.Join(t.TempDir(), "bare.yaml")
	_ = os.WriteFile(bare, []byte("auth:\n  mfa_required: false\n"), 0o600)
	if err := ReplaceJWTSecret(bare, secret); err == nil {
		t.Error("ReplaceJWTSecret succeeded on a file without jwt_secret")
	}
	if err := ReplaceJWTSecret(filepath.Join(t.TempDir(), "absent.yaml"), secret); err == nil {
		t.Error("ReplaceJWTSecret succeeded on a missing file")
	}
}
