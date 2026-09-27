package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
)

const (
	challengeTTL = 60 * time.Second

	// maxRedeemedChallenges caps the replay-protection set. Entries only
	// enter it after a valid MAC, i.e. for challenges this server issued,
	// and leave it once they expire. Reaching the cap means something is
	// hammering the login endpoints, so verification fails closed.
	maxRedeemedChallenges = 100_000
)

var errChallengeInvalid = errors.New("invalid or expired challenge")

// challengeStore issues and verifies single-use login challenges, proving a
// login request's caller holds the private key for the public key they
// present (a public key alone is not a secret).
//
// Challenges are stateless: each one is a random nonce and expiry bound to
// the username with an HMAC under a per-process key. Issuing stores
// nothing, so anonymous callers cannot grow server memory by requesting
// challenges, and issuing a new challenge never invalidates one already
// handed to someone else (an attacker cannot cancel a victim's in-flight
// login by requesting challenges for their username). Single use is
// enforced by remembering redeemed nonces until they expire.
//
// The key lives only in memory, so a challenge must be redeemed on the
// same server process that issued it, as before.
type challengeStore struct {
	key []byte

	mu       sync.Mutex
	redeemed map[string]time.Time // nonce -> expiry
	now      func() time.Time
}

func newChallengeStore() *challengeStore {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("challenge key: %v", err))
	}
	return &challengeStore{key: key, redeemed: make(map[string]time.Time), now: time.Now}
}

func (s *challengeStore) mac(username string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(username))
	m.Write([]byte{0})
	m.Write(payload)
	return m.Sum(nil)
}

// Issue returns a fresh challenge bound to username, valid for challengeTTL.
func (s *challengeStore) Issue(username string) (string, error) {
	payload := make([]byte, 24) // 16-byte nonce + 8-byte expiry (unix seconds)
	if _, err := rand.Read(payload[:16]); err != nil {
		return "", fmt.Errorf("generate challenge: %w", err)
	}
	binary.BigEndian.PutUint64(payload[16:], uint64(s.now().Add(challengeTTL).Unix()))
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(s.mac(username, payload)), nil
}

// Verify consumes value if it is an unexpired, unused challenge that this
// store issued for username.
func (s *challengeStore) Verify(username, value string) bool {
	return s.verify(username, value) == nil
}

func (s *challengeStore) verify(username, value string) error {
	payloadB64, macB64, ok := strings.Cut(value, ".")
	if !ok {
		return errChallengeInvalid
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(payloadB64)
	if err != nil || len(payload) != 24 {
		return errChallengeInvalid
	}
	gotMAC, err := enc.DecodeString(macB64)
	if err != nil || !hmac.Equal(gotMAC, s.mac(username, payload)) {
		return errChallengeInvalid
	}
	expiresAt := time.Unix(int64(binary.BigEndian.Uint64(payload[16:])), 0)
	now := s.now()
	if !now.Before(expiresAt) {
		return errChallengeInvalid
	}

	nonce := string(payload[:16])
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, used := s.redeemed[nonce]; used {
		return errChallengeInvalid
	}
	if len(s.redeemed) >= maxRedeemedChallenges {
		s.gcLocked(now)
		if len(s.redeemed) >= maxRedeemedChallenges {
			return errors.New("too many pending logins; retry shortly")
		}
	}
	s.redeemed[nonce] = expiresAt
	return nil
}

// gcLocked drops redeemed nonces whose challenge has expired; an expired
// challenge is rejected on its timestamp alone. Caller holds s.mu.
func (s *challengeStore) gcLocked(now time.Time) {
	for nonce, exp := range s.redeemed {
		if !now.Before(exp) {
			delete(s.redeemed, nonce)
		}
	}
}

// issueChallenge is POST /public/auth/challenge — the first step of every
// SSH-pubkey-based login flow (login, loginWithKey, loginJWT).
func (s *APIServer) issueChallenge(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	challenge, err := s.challenges.Issue(req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue challenge"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": challenge})
}

// verifyPossession proves the caller holds the private key for presented:
// the challenge must have been issued to username by this server, be unused
// and unexpired (60s TTL), and sigBlobB64/sigFormat must be a valid
// signature over the raw challenge bytes from that same key.
func (s *APIServer) verifyPossession(username, challenge, sigFormat, sigBlobB64 string, presented ssh.PublicKey) error {
	if err := s.challenges.verify(username, challenge); err != nil {
		return err
	}
	sigBlob, err := base64.StdEncoding.DecodeString(sigBlobB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	sig := &ssh.Signature{Format: sigFormat, Blob: sigBlob}
	if err := presented.Verify([]byte(challenge), sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}
