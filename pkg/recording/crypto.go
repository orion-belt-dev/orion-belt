package recording

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/cryptutil"
)

// Crypto wraps AES-256-GCM for session recordings.
type Crypto struct {
	env    *cryptutil.Envelope
	logger *common.Logger
}

// NewCrypto derives a 32-byte key from config (base64 or raw string).
func NewCrypto(keyMaterial string, logger *common.Logger) (*Crypto, error) {
	env, err := cryptutil.NewEnvelope(keyMaterial)
	if err != nil {
		return nil, err
	}
	return &Crypto{env: env, logger: logger}, nil
}

// Enabled reports whether encryption is active.
func (c *Crypto) Enabled() bool {
	return c != nil && c.env.Enabled()
}

// Encrypt seals plaintext with AES-GCM. Output is magic + nonce + ciphertext.
func (c *Crypto) Encrypt(plain []byte) ([]byte, error) {
	return c.env.Encrypt(plain)
}

// Decrypt opens an encrypted buffer (or returns as-is if not encrypted).
func (c *Crypto) Decrypt(data []byte) ([]byte, error) {
	out, err := c.env.Decrypt(data)
	if err != nil && !c.Enabled() {
		return nil, fmt.Errorf("recording is encrypted but encryption_key is not configured")
	}
	return out, err
}

// DecryptFile reads and decrypts a recording file.
func (c *Crypto) DecryptFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c.Decrypt(data)
}

// EncryptAndWrite encrypts content and writes to path.
func (c *Crypto) EncryptAndWrite(path string, plain []byte) error {
	out, err := c.Encrypt(plain)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0600)
}

// EnforceRetention deletes recording files older than retentionDays.
func EnforceRetention(storagePath string, retentionDays int, logger *common.Logger) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(storagePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	deleted := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".txt") && !strings.HasSuffix(name, ".rec") && !strings.HasSuffix(name, ".cast") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(storagePath, name)
			if err := os.Remove(path); err != nil {
				if logger != nil {
					logger.Warn("retention: failed to delete %s: %v", path, err)
				}
				continue
			}
			deleted++
		}
	}
	if logger != nil && deleted > 0 {
		logger.Info("retention: deleted %d recording(s) older than %d days", deleted, retentionDays)
	}
	return deleted, nil
}
