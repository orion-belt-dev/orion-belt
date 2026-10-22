// Package cryptutil provides shared at-rest secret encryption used across
// otherwise-unrelated subsystems (session recordings, the SSH CA's private
// key material) that each need to keep AES-256-GCM-sealed bytes in Postgres.
package cryptutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// randReader is the nonce source. Tests replace it to force the read error;
// production always uses crypto/rand.
var randReader io.Reader = rand.Reader

const envelopeMagic = "OBENC1\n"

// Envelope wraps AES-256-GCM sealing/opening for a single key.
type Envelope struct {
	key []byte
}

// NewEnvelope derives a 32-byte key from keyMaterial (base64, raw 32-byte
// string, or arbitrary passphrase hashed via SHA-256). An empty keyMaterial
// yields a disabled envelope: Encrypt/Decrypt become no-ops passing data
// through as plaintext, matching the recording subsystem's original
// "encryption is optional" behavior.
func NewEnvelope(keyMaterial string) (*Envelope, error) {
	if strings.TrimSpace(keyMaterial) == "" {
		return &Envelope{}, nil
	}
	key, err := deriveKey(keyMaterial)
	if err != nil {
		return nil, err
	}
	return &Envelope{key: key}, nil
}

// Enabled reports whether this envelope has key material configured.
func (e *Envelope) Enabled() bool {
	return e != nil && len(e.key) == 32
}

func deriveKey(material string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(material); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(material) == 32 {
		return []byte(material), nil
	}
	sum := sha256.Sum256([]byte(material))
	return sum[:], nil
}

// Encrypt seals plaintext with AES-GCM. Output is magic + nonce + ciphertext.
// If the envelope is disabled, plain is returned unchanged.
func (e *Envelope) Encrypt(plain []byte) ([]byte, error) {
	if !e.Enabled() {
		return plain, nil
	}
	gcm, err := e.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return nil, err
	}
	out := append([]byte(envelopeMagic), nonce...)
	out = append(out, gcm.Seal(nil, nonce, plain, nil)...)
	return out, nil
}

// Decrypt opens an envelope-sealed buffer. Data without the envelope magic
// prefix is returned as-is (legacy plaintext).
func (e *Envelope) Decrypt(data []byte) ([]byte, error) {
	if !strings.HasPrefix(string(data), envelopeMagic) {
		return data, nil
	}
	if !e.Enabled() {
		return nil, fmt.Errorf("data is encrypted but no key is configured")
	}
	payload := data[len(envelopeMagic):]
	gcm, err := e.aead()
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(payload) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := payload[:ns], payload[ns:]
	return gcm.Open(nil, nonce, ct, nil)
}

func (e *Envelope) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
