package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestChallengeStoreSingleUse(t *testing.T) {
	s := newChallengeStore()
	c, err := s.Issue("alice")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !s.Verify("alice", c) {
		t.Fatal("expected first Verify to succeed")
	}
	if s.Verify("alice", c) {
		t.Fatal("expected second Verify of the same challenge to fail (single-use)")
	}
}

func TestChallengeStoreWrongValueRejected(t *testing.T) {
	s := newChallengeStore()
	if _, err := s.Issue("alice"); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if s.Verify("alice", "not-the-real-challenge") {
		t.Fatal("expected Verify with wrong value to fail")
	}
}

func TestChallengeStoreUnknownUserRejected(t *testing.T) {
	s := newChallengeStore()
	if s.Verify("nobody", "anything") {
		t.Fatal("expected Verify for a user with no issued challenge to fail")
	}
}

// Issuing a new challenge must not cancel one already handed out, or anyone
// could abort a victim's in-flight login by requesting challenges for them.
func TestChallengeStoreReissueKeepsPriorValid(t *testing.T) {
	s := newChallengeStore()
	first, _ := s.Issue("alice")
	second, _ := s.Issue("alice")
	if first == second {
		t.Fatal("expected two Issue calls to produce different challenges")
	}
	if !s.Verify("alice", first) {
		t.Fatal("an earlier challenge should still verify after a reissue")
	}
	if !s.Verify("alice", second) {
		t.Fatal("expected the latest challenge to verify")
	}
}

func TestChallengeStoreBoundToUsername(t *testing.T) {
	s := newChallengeStore()
	c, _ := s.Issue("alice")
	if s.Verify("bob", c) {
		t.Fatal("a challenge issued for alice must not verify for bob")
	}
	if !s.Verify("alice", c) {
		t.Fatal("a failed attempt for another user must not consume alice's challenge")
	}
}

func TestChallengeStoreRejectsExpired(t *testing.T) {
	s := newChallengeStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	c, _ := s.Issue("alice")
	s.now = func() time.Time { return now.Add(challengeTTL + time.Second) }
	if s.Verify("alice", c) {
		t.Fatal("expected an expired challenge to be rejected")
	}
}

func TestChallengeStoreRejectsTampered(t *testing.T) {
	s := newChallengeStore()
	c, _ := s.Issue("alice")
	payload, mac, _ := strings.Cut(c, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	raw[len(raw)-1]++ // push the expiry out by a second
	forged := base64.RawURLEncoding.EncodeToString(raw) + "." + mac
	if s.Verify("alice", forged) {
		t.Fatal("expected a challenge with a modified payload to be rejected")
	}
	if s.Verify("alice", c+"x") {
		t.Fatal("expected a challenge with a modified MAC to be rejected")
	}
	if s.Verify("alice", newChallengeStore().mustIssue(t, "alice")) {
		t.Fatal("expected a challenge from another server key to be rejected")
	}
}

// Anonymous callers can request challenges for any username; that must not
// consume server memory.
func TestChallengeStoreIssueIsStateless(t *testing.T) {
	s := newChallengeStore()
	for i := 0; i < 1000; i++ {
		if _, err := s.Issue(fmt.Sprintf("user-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.redeemed); n != 0 {
		t.Fatalf("Issue retained %d entries; it should store nothing", n)
	}
}

func TestChallengeStoreForgetsExpiredRedemptions(t *testing.T) {
	s := newChallengeStore()
	now := time.Now()
	s.now = func() time.Time { return now }
	c, _ := s.Issue("alice")
	if !s.Verify("alice", c) {
		t.Fatal("first Verify should succeed")
	}
	s.gcLocked(now.Add(challengeTTL + time.Second))
	if n := len(s.redeemed); n != 0 {
		t.Fatalf("expired redemption not collected (%d left)", n)
	}
}

func (s *challengeStore) mustIssue(t *testing.T, username string) string {
	t.Helper()
	c, err := s.Issue(username)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func genSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("wrap signer: %v", err)
	}
	return signer
}

func TestVerifyPossessionValidSignature(t *testing.T) {
	s := &APIServer{challenges: newChallengeStore()}
	challenge, _ := s.challenges.Issue("alice")
	signer := genSigner(t)

	sig, err := signer.Sign(rand.Reader, []byte(challenge))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = s.verifyPossession("alice", challenge, sig.Format, base64.StdEncoding.EncodeToString(sig.Blob), signer.PublicKey())
	if err != nil {
		t.Fatalf("expected valid signature to verify, got: %v", err)
	}
}

func TestVerifyPossessionWrongKeyRejected(t *testing.T) {
	s := &APIServer{challenges: newChallengeStore()}
	challenge, _ := s.challenges.Issue("alice")
	signer := genSigner(t)
	otherSigner := genSigner(t)

	sig, err := signer.Sign(rand.Reader, []byte(challenge))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Signed by `signer`, but verified against `otherSigner`'s public key —
	// this is the scenario that matters: an attacker who only knows the
	// victim's *public* key (which is not secret) cannot forge this.
	err = s.verifyPossession("alice", challenge, sig.Format, base64.StdEncoding.EncodeToString(sig.Blob), otherSigner.PublicKey())
	if err == nil {
		t.Fatal("expected signature from a different key to be rejected")
	}
}

func TestVerifyPossessionReplayRejected(t *testing.T) {
	s := &APIServer{challenges: newChallengeStore()}
	challenge, _ := s.challenges.Issue("alice")
	signer := genSigner(t)
	sig, _ := signer.Sign(rand.Reader, []byte(challenge))
	sigB64 := base64.StdEncoding.EncodeToString(sig.Blob)

	if err := s.verifyPossession("alice", challenge, sig.Format, sigB64, signer.PublicKey()); err != nil {
		t.Fatalf("first verification should succeed: %v", err)
	}
	if err := s.verifyPossession("alice", challenge, sig.Format, sigB64, signer.PublicKey()); err == nil {
		t.Fatal("expected replay of the same (challenge, signature) pair to be rejected")
	}
}

func TestBootstrapStoreSingleUse(t *testing.T) {
	s := newBootstrapStore()
	code, _, err := s.Issue("user-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	userID, ok := s.Redeem(code)
	if !ok || userID != "user-1" {
		t.Fatalf("expected first Redeem to succeed with user-1, got ok=%v userID=%q", ok, userID)
	}
	if _, ok := s.Redeem(code); ok {
		t.Fatal("expected second Redeem of the same code to fail (single-use)")
	}
}

func TestBootstrapStoreUnknownCodeRejected(t *testing.T) {
	s := newBootstrapStore()
	if _, ok := s.Redeem("not-a-real-code"); ok {
		t.Fatal("expected Redeem of an unissued code to fail")
	}
}
