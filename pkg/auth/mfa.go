package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const backupCodeCount = 8

// GenerateTOTPSecret creates a new TOTP key for enrollment.
func GenerateTOTPSecret(issuer, accountName string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// ValidateTOTP validates a 6-digit TOTP code against the secret.
func ValidateTOTP(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}
	ok, err := totp.ValidateCustom(strings.TrimSpace(code), secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// GenerateBackupCodes returns plaintext codes and a newline-joined SHA256 hash store.
func GenerateBackupCodes() (codes []string, hashStore string, err error) {
	codes = make([]string, backupCodeCount)
	hashes := make([]string, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		b := make([]byte, 5)
		if _, err = rand.Read(b); err != nil {
			return nil, "", err
		}
		code := strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
		if len(code) > 10 {
			code = code[:10]
		}
		codes[i] = code
		hashes[i] = hashBackupCode(code)
	}
	return codes, strings.Join(hashes, "\n"), nil
}

// ConsumeBackupCode validates a backup code and returns the updated hash store (code removed).
func ConsumeBackupCode(hashStore, code string) (newStore string, ok bool) {
	code = strings.TrimSpace(strings.ToUpper(code))
	want := hashBackupCode(code)
	lines := strings.Split(hashStore, "\n")
	kept := make([]string, 0, len(lines))
	matched := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !matched && line == want {
			matched = true
			continue
		}
		kept = append(kept, line)
	}
	if !matched {
		return hashStore, false
	}
	return strings.Join(kept, "\n"), true
}

func hashBackupCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// ValidateMFACode accepts either a TOTP code or a backup code.
// updatedHashStore is the backup code store after the check: unchanged for a
// TOTP code, without the used code for a backup code (empty once the last
// one is used). Callers persist it whenever it differs from the stored one.
func ValidateMFACode(totpSecret, backupHashStore, code string) (updatedHashStore string, ok bool) {
	if ValidateTOTP(totpSecret, code) {
		return backupHashStore, true
	}
	if backupHashStore == "" {
		return "", false
	}
	newStore, matched := ConsumeBackupCode(backupHashStore, code)
	if !matched {
		return "", false
	}
	return newStore, true
}

// ErrMFARequired is returned when MFA verification is needed.
var ErrMFARequired = fmt.Errorf("mfa verification required")
