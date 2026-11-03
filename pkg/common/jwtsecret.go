package common

import (
	"fmt"
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
