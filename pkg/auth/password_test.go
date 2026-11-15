package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestHashVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct-horse-battery") {
		t.Fatal("expected match")
	}
	if VerifyPassword(h, "wrong-password-here") {
		t.Fatal("expected mismatch")
	}
}

func TestValidatePasswordStrength(t *testing.T) {
	if ValidatePasswordStrength("short") == nil {
		t.Fatal("expected error")
	}
	if err := ValidatePasswordStrength("long-enough"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPasswordRejectsMalformedParams(t *testing.T) {
	salt := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("s", argonSaltLen)))
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", argonKeyLen)))
	for name, encoded := range map[string]string{
		"zero time":    "$argon2id$v=19$m=65536,t=0,p=4$" + salt + "$" + key,
		"zero threads": "$argon2id$v=19$m=65536,t=1,p=0$" + salt + "$" + key,
		"huge memory":  "$argon2id$v=19$m=4294967295,t=1,p=4$" + salt + "$" + key,
		"empty key":    "$argon2id$v=19$m=65536,t=1,p=4$" + salt + "$",
		"short key":    "$argon2id$v=19$m=65536,t=1,p=4$" + salt + "$aGFzaA",
	} {
		t.Run(name, func(t *testing.T) {
			if VerifyPassword(encoded, "anything") {
				t.Fatalf("VerifyPassword accepted %q", encoded)
			}
		})
	}
}
