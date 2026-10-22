package ca

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

var errStore = errors.New("store unavailable")

// failingStore wraps fakeStore and fails the method named in failOn.
type failingStore struct {
	*fakeStore
	failOn string
}

func (f *failingStore) GetActiveCAKey(ctx context.Context, caType string) (*common.CAKey, []byte, error) {
	if f.failOn == "GetActiveCAKey:"+caType {
		return nil, nil, errStore
	}
	return f.fakeStore.GetActiveCAKey(ctx, caType)
}

func (f *failingStore) CreateCAKey(ctx context.Context, key *common.CAKey, priv []byte) error {
	if f.failOn == "CreateCAKey" {
		return errStore
	}
	return f.fakeStore.CreateCAKey(ctx, key, priv)
}

func (f *failingStore) CreateSSHCertificate(ctx context.Context, cert *common.SSHCertificate) error {
	if f.failOn == "CreateSSHCertificate" {
		return errStore
	}
	return f.fakeStore.CreateSSHCertificate(ctx, cert)
}

func (f *failingStore) ListRevokedCertSerials(ctx context.Context) ([]string, error) {
	if f.failOn == "ListRevokedCertSerials" {
		return nil, errStore
	}
	return f.fakeStore.ListRevokedCertSerials(ctx)
}

func TestNew_BlankMasterKey_ReturnsError(t *testing.T) {
	// Whitespace passes the empty check but yields a disabled envelope.
	_, err := New(common.SSHCAConfig{Enabled: true, MasterKey: "   "}, newFakeStore(), nil)
	if err == nil || !strings.Contains(err.Error(), "usable 32-byte key") {
		t.Fatalf("New() error = %v, want the unusable-key error", err)
	}
}

func TestNew_StoreFailures_ReturnWrappedErrors(t *testing.T) {
	cases := []struct {
		failOn string
		want   string
	}{
		{"GetActiveCAKey:" + common.CATypeUser, "load user CA"},
		{"GetActiveCAKey:" + common.CATypeHost, "load host CA"},
		{"CreateCAKey", "persist user CA keypair"},
	}
	for _, tc := range cases {
		t.Run(tc.failOn, func(t *testing.T) {
			store := &failingStore{fakeStore: newFakeStore(), failOn: tc.failOn}
			_, err := New(testConfig(), store, nil)
			if !errors.Is(err, errStore) {
				t.Fatalf("New() error = %v, want it to wrap the store error", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("New() error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestNew_RevocationLoadFailure_IsLoggedNotFatal(t *testing.T) {
	var logs bytes.Buffer
	logger := common.NewLoggerTo(common.DEBUG, &logs)
	store := &failingStore{fakeStore: newFakeStore(), failOn: "ListRevokedCertSerials"}

	a, err := New(testConfig(), store, logger)
	if err != nil {
		t.Fatalf("New() error = %v, want startup to tolerate a revocation load failure", err)
	}
	if a == nil {
		t.Fatal("New() returned nil Authority")
	}
	if !strings.Contains(logs.String(), "initial revocation cache load failed") {
		t.Errorf("log output %q lacks the revocation warning", logs.String())
	}
	if !strings.Contains(logs.String(), "SSH CA bootstrapped") {
		t.Errorf("log output %q lacks the bootstrap message", logs.String())
	}
}

func TestNew_WrongMasterKey_FailsToDecryptExistingCA(t *testing.T) {
	store := newFakeStore()
	if _, err := New(testConfig(), store, nil); err != nil {
		t.Fatalf("first New: %v", err)
	}

	cfg := testConfig()
	cfg.MasterKey = "a completely different master key"
	_, err := New(cfg, store, nil)
	if err == nil || !strings.Contains(err.Error(), "check ssh_ca.master_key") {
		t.Fatalf("New() with wrong key error = %v, want a decrypt error naming the config key", err)
	}
}

func TestNew_CorruptStoredKey_FailsToParse(t *testing.T) {
	store := newFakeStore()
	// No envelope magic, so Decrypt passes it through and parsing must reject it.
	store.caKeys[common.CATypeUser] = common.NewCAKey(common.CATypeUser, "ed25519", "", "")
	store.caPriv[common.CATypeUser] = []byte("not a private key")

	_, err := New(testConfig(), store, nil)
	if err == nil || !strings.Contains(err.Error(), "parse user CA private key") {
		t.Fatalf("New() error = %v, want a parse error", err)
	}
}

func TestUninitializedAuthority_HasNoKeysAndRefusesToIssue(t *testing.T) {
	a := &Authority{store: newFakeStore(), revoked: map[string]struct{}{}}

	if a.UserCAPublicKey() != nil || a.HostCAPublicKey() != nil {
		t.Fatal("expected nil CA public keys before bootstrap")
	}
	user, host := a.ExportPublicKeys()
	if user != "" || host != "" {
		t.Errorf("ExportPublicKeys() = (%q, %q), want both empty", user, host)
	}
	if a.CertChecker().IsUserAuthority(genTestKey(t)) {
		t.Error("IsUserAuthority accepted a key with no User CA configured")
	}
	if a.CertChecker().IsHostAuthority(genTestKey(t), "") {
		t.Error("IsHostAuthority accepted a key with no Host CA configured")
	}

	ctx := context.Background()
	if _, err := a.IssueUserCert(ctx, "u", "alice", genTestKey(t), 0); err == nil {
		t.Error("IssueUserCert succeeded without a User CA")
	}
	if _, err := a.IssueHostCert(ctx, "m", []string{"web"}, genTestKey(t), 0); err == nil {
		t.Error("IssueHostCert succeeded without a Host CA")
	}
}

func TestExportPublicKeys_ReturnsAuthorizedKeyLines(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	userLine, hostLine := a.ExportPublicKeys()

	for name, line := range map[string]string{"user": userLine, "host": hostLine} {
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			t.Fatalf("%s line %q does not parse: %v", name, line, err)
		}
		if pub.Type() != ssh.KeyAlgoED25519 {
			t.Errorf("%s CA key type = %s, want ed25519", name, pub.Type())
		}
	}
	if userLine == hostLine {
		t.Error("user and host CA keys are identical")
	}
}

func TestBytesEqualKey_NilKeysAreNeverEqual(t *testing.T) {
	k := genTestKey(t)
	if bytesEqualKey(nil, k) || bytesEqualKey(k, nil) || bytesEqualKey(nil, nil) {
		t.Error("bytesEqualKey reported equality with a nil key")
	}
}

func TestClampRequestedTTL(t *testing.T) {
	def, max := 12*time.Hour, 24*time.Hour
	cases := []struct {
		name      string
		requested time.Duration
		want      time.Duration
	}{
		{"zero uses default", 0, def},
		{"negative uses default", -time.Hour, def},
		{"within range is kept", 3 * time.Hour, 3 * time.Hour},
		{"exactly max is kept", max, max},
		{"above max is clamped", 48 * time.Hour, max},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampRequestedTTL(tc.requested, def, max); got != tc.want {
				t.Errorf("clampRequestedTTL(%v) = %v, want %v", tc.requested, got, tc.want)
			}
		})
	}
}

func TestUserCertTTLs_DefaultsWhenUnset(t *testing.T) {
	a := &Authority{}
	def, max := a.userCertTTLs()
	if def != defaultUserCertTTLHours*time.Hour || max != defaultMaxUserCertTTLHours*time.Hour {
		t.Errorf("userCertTTLs() = (%v, %v), want package defaults", def, max)
	}
	if got := a.HostCertTTL(); got != defaultHostCertTTLHours*time.Hour {
		t.Errorf("HostCertTTL() = %v, want default", got)
	}

	a.cfg.HostCertTTLHours = 2
	if got := a.HostCertTTL(); got != 2*time.Hour {
		t.Errorf("HostCertTTL() = %v, want configured 2h", got)
	}
}

func TestIssueUserCert_WithinRangeTTL_IsHonouredAndRecorded(t *testing.T) {
	store := newFakeStore()
	a, err := New(testConfig(), store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cert, err := a.IssueUserCert(context.Background(), "user-1", "alice", genTestKey(t), 2*time.Hour)
	if err != nil {
		t.Fatalf("IssueUserCert: %v", err)
	}
	lifetime := time.Duration(cert.ValidBefore-cert.ValidAfter) * time.Second
	// ValidAfter is backdated one minute for clock skew.
	if want := 2*time.Hour + time.Minute; lifetime != want {
		t.Errorf("cert lifetime = %v, want %v", lifetime, want)
	}
	certs, _ := store.ListSSHCertificates(context.Background(), common.SSHCertFilter{CertType: common.CATypeUser}, 0, 0)
	if len(certs) != 1 || certs[0].KeyID != "alice" || certs[0].SubjectID != "user-1" {
		t.Errorf("recorded certs = %+v, want one for alice", certs)
	}
}

func TestIssueCert_RecordFailure_ReturnsError(t *testing.T) {
	store := &failingStore{fakeStore: newFakeStore()}
	a, err := New(testConfig(), store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	store.failOn = "CreateSSHCertificate"
	ctx := context.Background()

	if _, err := a.IssueUserCert(ctx, "u", "alice", genTestKey(t), 0); !errors.Is(err, errStore) {
		t.Errorf("IssueUserCert error = %v, want the store error", err)
	}
	if _, err := a.IssueHostCert(ctx, "m", []string{"web"}, genTestKey(t), 0); !errors.Is(err, errStore) {
		t.Errorf("IssueHostCert error = %v, want the store error", err)
	}
}

func TestIssueHostCert_RequiresPrincipal(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.IssueHostCert(context.Background(), "m", nil, genTestKey(t), 0); err == nil {
		t.Fatal("IssueHostCert with no principals succeeded")
	}
}

func TestIssueHostCert_ExplicitTTL(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cert, err := a.IssueHostCert(context.Background(), "", []string{"gw", "gw.local"}, genTestKey(t), time.Hour)
	if err != nil {
		t.Fatalf("IssueHostCert: %v", err)
	}
	if cert.KeyId != "gw" || len(cert.ValidPrincipals) != 2 {
		t.Errorf("cert KeyId=%q principals=%v, want first principal as key id and both principals", cert.KeyId, cert.ValidPrincipals)
	}
	if lifetime := time.Duration(cert.ValidBefore-cert.ValidAfter) * time.Second; lifetime != time.Hour+time.Minute {
		t.Errorf("cert lifetime = %v, want 1h1m", lifetime)
	}
}

func TestRevokeCertificate_UnknownSerial_ReturnsStoreError(t *testing.T) {
	a, err := New(testConfig(), newFakeStore(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.RevokeCertificate(context.Background(), "12345", "admin", "x"); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("RevokeCertificate error = %v, want ErrNotFound", err)
	}
}

func TestRefreshRevocationCache_StoreError_KeepsPreviousCache(t *testing.T) {
	store := &failingStore{fakeStore: newFakeStore()}
	a, err := New(testConfig(), store, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a.revoked["42"] = struct{}{}
	store.failOn = "ListRevokedCertSerials"

	if err := a.RefreshRevocationCache(context.Background()); !errors.Is(err, errStore) {
		t.Fatalf("RefreshRevocationCache error = %v, want the store error", err)
	}
	if !a.isRevokedCached(&ssh.Certificate{Serial: 42}) {
		t.Error("a failed refresh dropped the existing revocation cache")
	}
}

func TestNeedsRenewal(t *testing.T) {
	ttl := 10 * time.Hour
	cases := []struct {
		name      string
		expiresIn time.Duration
		want      bool
	}{
		{"already expired", -time.Minute, true},
		{"below renewal fraction", time.Hour, true},
		{"above renewal fraction", 5 * time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsRenewal(time.Now().Add(tc.expiresIn), ttl); got != tc.want {
				t.Errorf("NeedsRenewal(+%v, %v) = %v, want %v", tc.expiresIn, ttl, got, tc.want)
			}
		})
	}
}
