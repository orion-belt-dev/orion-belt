package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// MinJWTSecretLen is the shortest auth.jwt_secret the server accepts: a
// token signed with a short secret can be brute-forced offline, and any
// token forged that way can claim admin.
const MinJWTSecretLen = 32

// publishedJWTSecrets are placeholders shipped in this repository's example
// and lab configs. Anyone can read them, so a server signing with one
// accepts forged tokens.
var publishedJWTSecrets = map[string]bool{
	"change-me-to-a-long-random-secret": true,
	"lab-only-change-me":                true,
	"qemu-lab-secret-change-me":         true,
}

// ValidateJWTSecret checks auth.jwt_secret. Empty is allowed and leaves JWT
// sign-in off.
func ValidateJWTSecret(secret string) error {
	if secret == "" {
		return nil
	}
	const fix = "generate one with: openssl rand -hex 32"
	if publishedJWTSecrets[strings.TrimSpace(secret)] {
		return fmt.Errorf("auth.jwt_secret is the example placeholder, which is public; %s", fix)
	}
	if len(secret) < MinJWTSecretLen {
		return fmt.Errorf("auth.jwt_secret is %d bytes; at least %d are required (%s)", len(secret), MinJWTSecretLen, fix)
	}
	return nil
}

// GenerateJWTSecret returns a random secret (32 bytes, hex-encoded).
func GenerateJWTSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate jwt secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

var jwtSecretLine = regexp.MustCompile(`(?m)^([ \t]*jwt_secret:[ \t]*).*$`)

// ReplaceJWTSecret sets auth.jwt_secret in the YAML file at path by editing
// that one line, so comments and layout survive (SaveConfig rewrites the
// whole file). It fails if the file has no jwt_secret line.
func ReplaceJWTSecret(path, secret string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	loc := jwtSecretLine.FindSubmatchIndex(data)
	if loc == nil {
		return fmt.Errorf("%s has no jwt_secret line", path)
	}
	var out []byte
	out = append(out, data[:loc[3]]...)
	out = append(out, fmt.Sprintf("%q", secret)...)
	out = append(out, data[loc[1]:]...)
	return os.WriteFile(path, out, info.Mode().Perm())
}
