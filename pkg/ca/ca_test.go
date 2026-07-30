package ca

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// fakeStore is a minimal in-memory caStore for unit tests. It embeds a nil
// database.Store so it satisfies New()'s full-Store parameter type without
// implementing the ~30 methods pkg/ca never calls — only the 8 caStore
// methods below are actually exercised, so the embedded nil is never
// invoked in these tests.
type fakeStore struct {
	database.Store

	mu     sync.Mutex
	caKeys map[string]*common.CAKey // by ca_type, only "active" ones tracked here
	caPriv map[string][]byte
	certs  map[string]*common.SSHCertificate // by serial
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		caKeys: make(map[string]*common.CAKey),
		caPriv: make(map[string][]byte),
		certs:  make(map[string]*common.SSHCertificate),
	}
}

func (f *fakeStore) CreateCAKey(_ context.Context, key *common.CAKey, privEnc []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caKeys[key.CAType] = key
	f.caPriv[key.CAType] = privEnc
	return nil
}

func (f *fakeStore) GetActiveCAKey(_ context.Context, caType string) (*common.CAKey, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.caKeys[caType]
	if !ok {
		return nil, nil, database.ErrNotFound
	}
	return key, f.caPriv[caType], nil
}

func (f *fakeStore) ListCAKeys(_ context.Context, caType string) ([]*common.CAKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key, ok := f.caKeys[caType]; ok {
		return []*common.CAKey{key}, nil
	}
	return nil, nil
}

func (f *fakeStore) CreateSSHCertificate(_ context.Context, cert *common.SSHCertificate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.certs[cert.Serial] = cert
	return nil
}

func (f *fakeStore) GetSSHCertificateBySerial(_ context.Context, serial string) (*common.SSHCertificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cert, ok := f.certs[serial]
	if !ok {
		return nil, database.ErrNotFound
	}
	return cert, nil
}

func (f *fakeStore) ListSSHCertificates(_ context.Context, filter common.SSHCertFilter, _, _ int) ([]*common.SSHCertificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*common.SSHCertificate
	for _, c := range f.certs {
		if filter.CertType != "" && c.CertType != filter.CertType {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeStore) ListRevokedCertSerials(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for serial, c := range f.certs {
		if c.RevokedAt != nil && c.ExpiresAt.After(time.Now()) {
			out = append(out, serial)
		}
	}
	return out, nil
}

func (f *fakeStore) RevokeSSHCertificate(_ context.Context, serial, revokedBy, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cert, ok := f.certs[serial]
	if !ok {
		return database.ErrNotFound
	}
	now := time.Now()
	cert.RevokedAt = &now
	cert.RevokedBy = &revokedBy
	cert.RevokeReason = reason
	return nil
}

func testConfig() common.SSHCAConfig {
	return common.SSHCAConfig{
		Enabled:   true,
		MasterKey: "0123456789abcdef0123456789abcdef", // 32 raw bytes-ish; deriveKey hashes non-32-length input anyway
	}
}

func TestNewRequiresMasterKeyWhenEnabled(t *testing.T) {
	_, err := New(common.SSHCAConfig{Enabled: true}, newFakeStore(), nil)
	if err == nil {
		t.Fatal("expected error when ssh_ca.enabled=true with no master_key")
	}
}

func TestNewDisabledReturnsNil(t *testing.T) {
	a, err := New(common.SSHCAConfig{Enabled: false}, newFakeStore(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a != nil {
		t.Fatal("expected nil Authority when disabled")
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	store := newFakeStore()
	cfg := testConfig()

	a1, err := New(cfg, store, nil)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	userPub1 := a1.UserCAPublicKey()
	hostPub1 := a1.HostCAPublicKey()
	if userPub1 == nil || hostPub1 == nil {
		t.Fatal("expected both CA pubkeys to be initialized")
	}

	a2, err := New(cfg, store, nil)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if !bytesEqualKey(a1.UserCAPublicKey(), a2.UserCAPublicKey()) {
		t.Error("user CA pubkey changed across restarts — bootstrap is not idempotent")
	}
	if !bytesEqualKey(a1.HostCAPublicKey(), a2.HostCAPublicKey()) {
		t.Error("host CA pubkey changed across restarts — bootstrap is not idempotent")
	}
}

func genTestKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	signer, _, err := generateEd25519Signer()
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return signer.PublicKey()
}

func TestIssueAndVerifyUserCert(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	userPub := genTestKey(t)
	cert, err := a.IssueUserCert(context.Background(), "user-1", "alice", userPub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	if cert.CertType != ssh.UserCert {
		t.Fatalf("expected UserCert, got %d", cert.CertType)
	}

	checker := a.CertChecker()
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Fatalf("CheckCert(alice): %v", err)
	}
	if err := checker.CheckCert("bob", cert); err == nil {
		t.Fatal("expected CheckCert to reject a principal not on the cert")
	}
	if !checker.IsUserAuthority(cert.SignatureKey) {
		t.Fatal("expected cert to be signed by the recognized User CA")
	}
}

func TestIssueAndVerifyHostCert(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hostPub := genTestKey(t)
	cert, err := a.IssueHostCert(context.Background(), "machine-1", []string{"web-01"}, hostPub, 0)
	if err != nil {
		t.Fatalf("IssueHostCert: %v", err)
	}
	if cert.CertType != ssh.HostCert {
		t.Fatalf("expected HostCert, got %d", cert.CertType)
	}

	checker := a.CertChecker()
	if !checker.IsHostAuthority(cert.SignatureKey, "") {
		t.Fatal("expected cert to be signed by the recognized Host CA")
	}
	if err := checker.CheckCert("web-01", cert); err != nil {
		t.Fatalf("CheckCert(web-01): %v", err)
	}
}

func TestTTLClampedToMax(t *testing.T) {
	cfg := testConfig()
	cfg.UserCertTTLHours = 12
	cfg.MaxUserCertTTLHours = 24
	a, err := New(cfg, newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pub := genTestKey(t)
	cert, err := a.IssueUserCert(context.Background(), "user-1", "alice", pub, 100*time.Hour)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	maxExpiry := time.Now().Add(24 * time.Hour).Add(5 * time.Minute).Unix()
	if int64(cert.ValidBefore) > maxExpiry {
		t.Fatalf("expected TTL to be clamped to 24h max, got ValidBefore=%d (now+24h=%d)", cert.ValidBefore, maxExpiry)
	}
}

func TestRevocation(t *testing.T) {
	store := newFakeStore()
	a, err := New(testConfig(), store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pub := genTestKey(t)
	cert, err := a.IssueUserCert(context.Background(), "user-1", "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}

	checker := a.CertChecker()
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Fatalf("expected valid cert to pass before revocation: %v", err)
	}

	serialStr := strconv.FormatUint(cert.Serial, 10)
	if err := a.RevokeCertificate(context.Background(), serialStr, "admin-1", "compromised"); err != nil {
		t.Fatalf("RevokeCertificate: %v", err)
	}

	if err := checker.CheckCert("alice", cert); err == nil {
		t.Fatal("expected revoked cert to fail CheckCert immediately (no refresh needed)")
	}

	// A fresh checker (as if a new connection came in later) must also see the revocation.
	if err := a.CertChecker().CheckCert("alice", cert); err == nil {
		t.Fatal("expected revoked cert to fail on a freshly-obtained CertChecker too")
	}
}

func TestRefreshRevocationCachePicksUpStoreState(t *testing.T) {
	store := newFakeStore()
	a, err := New(testConfig(), store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pub := genTestKey(t)
	cert, err := a.IssueUserCert(context.Background(), "user-1", "alice", pub, 0)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}

	// Revoke directly at the store layer (simulating another server process
	// in a multi-instance deployment revoking via its own Authority).
	if err := store.RevokeSSHCertificate(context.Background(), strconv.FormatUint(cert.Serial, 10), "admin-1", "test"); err != nil {
		t.Fatalf("store.RevokeSSHCertificate: %v", err)
	}

	checker := a.CertChecker()
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Fatal("expected cert to still verify before cache refresh (revocation not yet propagated)")
	}

	if err := a.RefreshRevocationCache(context.Background()); err != nil {
		t.Fatalf("RefreshRevocationCache: %v", err)
	}

	if err := a.CertChecker().CheckCert("alice", cert); err == nil {
		t.Fatal("expected revocation to be picked up after RefreshRevocationCache")
	}
}
