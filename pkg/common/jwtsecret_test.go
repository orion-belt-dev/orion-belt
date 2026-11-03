package common

import (
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
