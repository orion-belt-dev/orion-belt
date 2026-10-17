package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestNewJWTManager_DefaultTTL(t *testing.T) {
	j := NewJWTManager("secret", 0)
	if j.ttl != 24*time.Hour {
		t.Errorf("ttl = %v, want 24h default", j.ttl)
	}
}

func TestJWTValidate_RejectsMalformedTokens(t *testing.T) {
	j := NewJWTManager("secret", time.Hour)
	signed := func(payload string) string {
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		input := header + "." + payload
		return input + "." + j.sign(input)
	}

	cases := map[string]struct{ token, want string }{
		"two parts":          {"a.b", "invalid token format"},
		"bad signature":      {"a.b.c", "invalid token signature"},
		"payload not base64": {signed("!!!"), "invalid token payload"},
		"payload not json":   {signed(base64.RawURLEncoding.EncodeToString([]byte("nope"))), "invalid token claims"},
		"expired": {signed(base64.RawURLEncoding.EncodeToString(
			[]byte(`{"uid":"u","exp":1}`))), "token expired"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := j.Validate(tc.token); err == nil || err.Error() != tc.want {
				t.Errorf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestJWTValidate_DisabledManager(t *testing.T) {
	if _, err := NewJWTManager("", time.Hour).Validate("a.b.c"); err == nil {
		t.Fatal("Validate on a manager without a secret succeeded")
	}
}

func TestValidateTOTP_EmptyAndWrongInputs(t *testing.T) {
	key, err := GenerateTOTPSecret("Orion", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ValidateTOTP("", "123456") || ValidateTOTP(key.Secret(), "") {
		t.Error("ValidateTOTP accepted an empty secret or code")
	}
	code, _ := totp.GenerateCode(key.Secret(), time.Now())
	if !ValidateTOTP(key.Secret(), " "+code+" ") {
		t.Error("ValidateTOTP rejected a valid code with surrounding space")
	}
}

func TestValidateMFACode(t *testing.T) {
	key, err := GenerateTOTPSecret("Orion", "alice")
	if err != nil {
		t.Fatal(err)
	}
	codes, store, err := GenerateBackupCodes()
	if err != nil {
		t.Fatal(err)
	}

	totpCode, _ := totp.GenerateCode(key.Secret(), time.Now())
	if updated, ok := ValidateMFACode(key.Secret(), store, totpCode); !ok || updated != "" {
		t.Errorf("TOTP code: ok=%v updated=%q, want ok with no store change", ok, updated)
	}

	updated, ok := ValidateMFACode(key.Secret(), store, strings.ToLower(codes[0]))
	if !ok || updated == "" || strings.Count(updated, "\n") != backupCodeCount-2 {
		t.Fatalf("backup code: ok=%v, %d codes left; want ok and one consumed", ok, strings.Count(updated, "\n")+1)
	}
	if _, ok := ValidateMFACode(key.Secret(), updated, codes[0]); ok {
		t.Error("a consumed backup code was accepted twice")
	}
	if _, ok := ValidateMFACode(key.Secret(), "", "ABCDEFGH"); ok {
		t.Error("ValidateMFACode accepted a code with no backup store")
	}
}

func TestConsumeBackupCode_SkipsBlankLinesAndOnlyConsumesOne(t *testing.T) {
	h := hashBackupCode("AAAA")
	store := h + "\n\n" + h + "\n"
	updated, ok := ConsumeBackupCode(store, "aaaa")
	if !ok || updated != h {
		t.Errorf("ConsumeBackupCode = %q, %v; want one duplicate left", updated, ok)
	}
}

func TestHashPassword_RejectsWeakPassword(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("HashPassword accepted a short password")
	}
}

func TestVerifyPassword_RejectsMalformedHashes(t *testing.T) {
	good, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, "$")
	with := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, "$")
	}
	cases := map[string]string{
		"too few parts": "$argon2id$v=19",
		"wrong algo":    with(1, "bcrypt"),
		"bad params":    with(3, "m=x"),
		"bad salt":      with(4, "!!"),
		"bad hash":      with(5, "!!"),
	}
	for name, encoded := range cases {
		if VerifyPassword(encoded, "correct horse battery") {
			t.Errorf("%s: VerifyPassword accepted %q", name, encoded)
		}
	}
	if !VerifyPassword(good, "correct horse battery") {
		t.Error("VerifyPassword rejected the right password")
	}
}
