package cryptutil

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func mustEnvelope(t *testing.T, material string) *Envelope {
	t.Helper()
	e, err := NewEnvelope(material)
	if err != nil {
		t.Fatalf("NewEnvelope(%q): %v", material, err)
	}
	return e
}

// --- key derivation ---------------------------------------------------------

// deriveKey has three branches whose precedence matters: a 32-byte base64
// payload wins over a 32-character literal, which wins over the SHA-256
// passphrase fallback. Getting that order wrong silently changes the key for
// existing deployments, so pin each branch.
func TestDeriveKeyBranches(t *testing.T) {
	raw32 := bytes.Repeat([]byte{0xAB}, 32)
	b64 := base64.StdEncoding.EncodeToString(raw32)

	t.Run("base64 of exactly 32 bytes is decoded", func(t *testing.T) {
		key, err := deriveKey(b64)
		if err != nil {
			t.Fatalf("deriveKey: %v", err)
		}
		if !bytes.Equal(key, raw32) {
			t.Errorf("deriveKey(base64) = %x, want the decoded bytes %x", key, raw32)
		}
	})

	t.Run("literal 32-byte string is used verbatim", func(t *testing.T) {
		// Not valid base64, so the first branch cannot claim it.
		material := strings.Repeat("!", 32)
		key, err := deriveKey(material)
		if err != nil {
			t.Fatalf("deriveKey: %v", err)
		}
		if !bytes.Equal(key, []byte(material)) {
			t.Errorf("deriveKey(literal) = %x, want %x", key, []byte(material))
		}
	})

	t.Run("base64 decoding to a non-32-byte payload falls through", func(t *testing.T) {
		// 32 base64 chars decode to 24 bytes, so the base64 branch must not
		// claim this; the 32-char literal branch should.
		material := strings.Repeat("A", 32)
		if decoded, err := base64.StdEncoding.DecodeString(material); err != nil || len(decoded) == 32 {
			t.Fatalf("test premise broken: decoded %d bytes, err=%v", len(decoded), err)
		}
		key, err := deriveKey(material)
		if err != nil {
			t.Fatalf("deriveKey: %v", err)
		}
		if !bytes.Equal(key, []byte(material)) {
			t.Errorf("deriveKey = %x, want the literal bytes %x", key, []byte(material))
		}
	})

	t.Run("arbitrary passphrase is hashed to 32 bytes", func(t *testing.T) {
		key, err := deriveKey("a short passphrase")
		if err != nil {
			t.Fatalf("deriveKey: %v", err)
		}
		if len(key) != 32 {
			t.Errorf("deriveKey(passphrase) length = %d, want 32", len(key))
		}
	})
}

func TestDeriveKeyIsDeterministicAndDistinct(t *testing.T) {
	a1, _ := deriveKey("passphrase-one")
	a2, _ := deriveKey("passphrase-one")
	b, _ := deriveKey("passphrase-two")

	if !bytes.Equal(a1, a2) {
		t.Error("deriveKey is not deterministic for identical material")
	}
	if bytes.Equal(a1, b) {
		t.Error("distinct passphrases derived the same key")
	}
}

// --- enabled / disabled state ----------------------------------------------

func TestEnvelopeDisabledWhenKeyMaterialBlank(t *testing.T) {
	for _, material := range []string{"", "   ", "\t\n "} {
		e := mustEnvelope(t, material)
		if e.Enabled() {
			t.Errorf("NewEnvelope(%q).Enabled() = true, want false", material)
		}
	}
}

func TestEnvelopeEnabledWithKeyMaterial(t *testing.T) {
	if !mustEnvelope(t, "some passphrase").Enabled() {
		t.Error("Enabled() = false for a configured envelope, want true")
	}
}

// Enabled() guards on a nil receiver so callers holding an optional *Envelope
// can ask without a nil check.
func TestNilEnvelopeIsNotEnabled(t *testing.T) {
	var e *Envelope
	if e.Enabled() {
		t.Error("(*Envelope)(nil).Enabled() = true, want false")
	}
}

// --- round trip -------------------------------------------------------------

func TestEncryptDecryptRoundTrip(t *testing.T) {
	e := mustEnvelope(t, "correct horse battery staple")

	cases := map[string][]byte{
		"empty":  {},
		"short":  []byte("hello"),
		"binary": {0x00, 0xFF, 0x00, 0x10, 0x80},
		"large":  bytes.Repeat([]byte("session-recording-frame;"), 4096),
	}

	for name, plain := range cases {
		t.Run(name, func(t *testing.T) {
			sealed, err := e.Encrypt(plain)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			opened, err := e.Decrypt(sealed)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(opened, plain) {
				t.Errorf("round trip changed the payload: got %d bytes, want %d", len(opened), len(plain))
			}
		})
	}
}

func TestEncryptEmitsMagicPrefixAndHidesPlaintext(t *testing.T) {
	e := mustEnvelope(t, "key material")
	plain := []byte("super secret value")

	sealed, err := e.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.HasPrefix(sealed, []byte(envelopeMagic)) {
		t.Errorf("sealed output %q lacks the envelope magic prefix", sealed[:min(len(sealed), 16)])
	}
	if bytes.Contains(sealed, plain) {
		t.Error("sealed output still contains the plaintext")
	}
}

// A fresh nonce per call is what keeps AES-GCM safe across repeated writes of
// the same recording frame; identical output for identical input would be a
// key-recovery-grade bug.
func TestEncryptUsesFreshNoncePerCall(t *testing.T) {
	e := mustEnvelope(t, "key material")
	plain := []byte("identical plaintext")

	first, err := e.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := e.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Error("two Encrypt calls on the same plaintext produced identical ciphertext; nonce is being reused")
	}
}

// --- disabled-envelope pass-through ----------------------------------------

func TestDisabledEnvelopePassesDataThrough(t *testing.T) {
	e := mustEnvelope(t, "")
	plain := []byte("not encrypted at all")

	sealed, err := e.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Equal(sealed, plain) {
		t.Errorf("disabled Encrypt = %q, want the input unchanged", sealed)
	}

	opened, err := e.Decrypt(plain)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Errorf("disabled Decrypt = %q, want the input unchanged", opened)
	}
}

// Recordings written before encryption was switched on have no magic prefix
// and must keep opening cleanly even on a configured envelope.
func TestDecryptPassesThroughLegacyPlaintext(t *testing.T) {
	e := mustEnvelope(t, "key material")
	legacy := []byte("plaintext written before encryption was enabled")

	opened, err := e.Decrypt(legacy)
	if err != nil {
		t.Fatalf("Decrypt(legacy): %v", err)
	}
	if !bytes.Equal(opened, legacy) {
		t.Errorf("Decrypt(legacy) = %q, want the input unchanged", opened)
	}
}

// --- failure paths ----------------------------------------------------------

func TestDecryptEncryptedDataWithoutKeyFails(t *testing.T) {
	sealed, err := mustEnvelope(t, "key material").Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := mustEnvelope(t, "").Decrypt(sealed); err == nil {
		t.Fatal("Decrypt of sealed data on a disabled envelope succeeded, want an error")
	} else if !strings.Contains(err.Error(), "no key is configured") {
		t.Errorf("error = %v, want it to mention the missing key", err)
	}
}

func TestDecryptRejectsTruncatedCiphertext(t *testing.T) {
	e := mustEnvelope(t, "key material")

	// Magic present but the nonce is incomplete.
	truncated := append([]byte(envelopeMagic), 0x01, 0x02, 0x03)
	if _, err := e.Decrypt(truncated); err == nil {
		t.Fatal("Decrypt of truncated ciphertext succeeded, want an error")
	} else if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error = %v, want a 'too short' error", err)
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	e := mustEnvelope(t, "key material")
	sealed, err := e.Encrypt([]byte("integrity protected payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Flip a bit in the final ciphertext byte; GCM's tag must reject it.
	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01

	if _, err := e.Decrypt(tampered); err == nil {
		t.Error("Decrypt of tampered ciphertext succeeded, want an authentication failure")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	sealed, err := mustEnvelope(t, "the right key").Encrypt([]byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := mustEnvelope(t, "the wrong key").Decrypt(sealed); err == nil {
		t.Error("Decrypt with the wrong key succeeded, want an authentication failure")
	}
}
