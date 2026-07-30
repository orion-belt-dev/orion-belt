// Package ca implements Orion Belt's SSH Certificate Authority: a User CA
// that issues short-lived certs to human operators (replacing static
// pubkey SSH login) and a Host CA that certifies the gateway's own host
// identity and each agent's identity (replacing TOFU host-key trust and
// the "agent disguised as a user row" mechanism, respectively).
//
// Both CA keypairs are generated automatically the first time the feature
// is enabled and persisted encrypted at rest via pkg/cryptutil.
package ca

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/cryptutil"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

const (
	defaultUserCertTTLHours    = 12
	defaultMaxUserCertTTLHours = 24
	defaultHostCertTTLHours    = 8760 // 1 year
	// RenewalFraction: a host cert is due for renewal once less than this
	// fraction of its total TTL remains.
	RenewalFraction = 0.2
	// AgentCertRenewRequest is the SSH global request agents send over an
	// authenticated reverse tunnel to refresh their Host-CA identity cert.
	AgentCertRenewRequest = "orion-renew-cert@orionbelt"
)

// caStore is the minimal slice of database.Store that pkg/ca depends on.
// Authority is written against this narrow interface (rather than the
// full, 30+ method database.Store) so unit tests can supply a small fake
// instead of implementing every unrelated Store method. Any database.Store
// implementation satisfies caStore automatically since Go interfaces are
// structural.
type caStore interface {
	CreateCAKey(ctx context.Context, key *common.CAKey, privateKeyEncrypted []byte) error
	GetActiveCAKey(ctx context.Context, caType string) (*common.CAKey, []byte, error)
	ListCAKeys(ctx context.Context, caType string) ([]*common.CAKey, error)
	CreateSSHCertificate(ctx context.Context, cert *common.SSHCertificate) error
	GetSSHCertificateBySerial(ctx context.Context, serial string) (*common.SSHCertificate, error)
	ListSSHCertificates(ctx context.Context, filter common.SSHCertFilter, limit, offset int) ([]*common.SSHCertificate, error)
	ListRevokedCertSerials(ctx context.Context) ([]string, error)
	RevokeSSHCertificate(ctx context.Context, serial, revokedBy, reason string) error
}

// Authority issues, verifies, and revokes SSH certificates.
type Authority struct {
	store  caStore
	env    *cryptutil.Envelope
	logger *common.Logger
	cfg    common.SSHCAConfig

	mu         sync.RWMutex
	userSigner ssh.Signer
	hostSigner ssh.Signer

	revokedMu sync.RWMutex
	revoked   map[string]struct{}
}

// New builds and bootstraps the CA. It returns (nil, nil) when SSH CA is
// disabled, matching the optional-feature convention used by
// authz.NewOpenFGA/recording.NewCrypto elsewhere in this codebase.
func New(cfg common.SSHCAConfig, store database.Store, logger *common.Logger) (*Authority, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.MasterKey == "" {
		return nil, fmt.Errorf("ssh_ca.enabled is true but ssh_ca.master_key is not set")
	}
	env, err := cryptutil.NewEnvelope(cfg.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("ssh ca master key: %w", err)
	}
	if !env.Enabled() {
		return nil, fmt.Errorf("ssh_ca.master_key did not produce a usable 32-byte key")
	}

	a := &Authority{
		store:   store,
		env:     env,
		logger:  logger,
		cfg:     cfg,
		revoked: make(map[string]struct{}),
	}

	ctx := context.Background()
	if err := a.bootstrap(ctx, common.CATypeUser); err != nil {
		return nil, err
	}
	if err := a.bootstrap(ctx, common.CATypeHost); err != nil {
		return nil, err
	}
	if err := a.RefreshRevocationCache(ctx); err != nil && logger != nil {
		logger.Warn("ssh ca: initial revocation cache load failed: %v", err)
	}
	return a, nil
}

// bootstrap loads the active keypair for caType, generating and persisting
// one on first use.
func (a *Authority) bootstrap(ctx context.Context, caType string) error {
	existing, privEnc, err := a.store.GetActiveCAKey(ctx, caType)
	if err != nil && err != database.ErrNotFound {
		return fmt.Errorf("load %s CA: %w", caType, err)
	}

	var signer ssh.Signer
	if err == database.ErrNotFound {
		newSigner, pemBytes, genErr := generateEd25519Signer()
		if genErr != nil {
			return fmt.Errorf("generate %s CA keypair: %w", caType, genErr)
		}
		encPriv, encErr := a.env.Encrypt(pemBytes)
		if encErr != nil {
			return fmt.Errorf("encrypt %s CA private key: %w", caType, encErr)
		}
		pubLine := string(ssh.MarshalAuthorizedKey(newSigner.PublicKey()))
		fingerprint := ssh.FingerprintSHA256(newSigner.PublicKey())
		record := common.NewCAKey(caType, "ed25519", pubLine, fingerprint)
		if createErr := a.store.CreateCAKey(ctx, record, encPriv); createErr != nil {
			return fmt.Errorf("persist %s CA keypair: %w", caType, createErr)
		}
		if a.logger != nil {
			a.logger.Info("SSH CA bootstrapped: %s CA generated (fingerprint=%s)", caType, fingerprint)
		}
		signer = newSigner
	} else {
		pemBytes, decErr := a.env.Decrypt(privEnc)
		if decErr != nil {
			return fmt.Errorf("decrypt %s CA private key (check ssh_ca.master_key): %w", caType, decErr)
		}
		parsed, parseErr := ssh.ParsePrivateKey(pemBytes)
		if parseErr != nil {
			return fmt.Errorf("parse %s CA private key: %w", caType, parseErr)
		}
		signer = parsed
		_ = existing // metadata (fingerprint, created_at) already in DB; signer is re-derived, not re-verified against it
	}

	a.mu.Lock()
	switch caType {
	case common.CATypeUser:
		a.userSigner = signer
	case common.CATypeHost:
		a.hostSigner = signer
	}
	a.mu.Unlock()
	return nil
}

func generateEd25519Signer() (ssh.Signer, []byte, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal private key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse generated private key: %w", err)
	}
	return signer, pemBytes, nil
}

// UserCAPublicKey returns the User CA's public key.
func (a *Authority) UserCAPublicKey() ssh.PublicKey {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.userSigner == nil {
		return nil
	}
	return a.userSigner.PublicKey()
}

// HostCAPublicKey returns the Host CA's public key.
func (a *Authority) HostCAPublicKey() ssh.PublicKey {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.hostSigner == nil {
		return nil
	}
	return a.hostSigner.PublicKey()
}

// ExportPublicKeys returns both CA public keys in authorized_keys format,
// this is what an operator distributes to clients/agents
//
//	as trusted CA material.
func (a *Authority) ExportPublicKeys() (userLine, hostLine string) {
	if pub := a.UserCAPublicKey(); pub != nil {
		userLine = string(ssh.MarshalAuthorizedKey(pub))
	}
	if pub := a.HostCAPublicKey(); pub != nil {
		hostLine = string(ssh.MarshalAuthorizedKey(pub))
	}
	return userLine, hostLine
}

// CertChecker returns a checker preconfigured against this Authority's CA
// keys and revocation cache. Callers still need to branch on cert.CertType
// themselves before calling CheckCert — see pkg/server's handlePublicKeyAuth,
// since Host certs authenticating an agent's PublicKeyCallback is
// intentionally non-standard usage that ssh.CertChecker.Authenticate itself
// refuses (it hardcodes UserCert-only).
func (a *Authority) CertChecker() *ssh.CertChecker {
	return &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			pub := a.UserCAPublicKey()
			return pub != nil && bytesEqualKey(auth, pub)
		},
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool {
			pub := a.HostCAPublicKey()
			return pub != nil && bytesEqualKey(auth, pub)
		},
		IsRevoked: a.isRevokedCached,
	}
}

func bytesEqualKey(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return string(a.Marshal()) == string(b.Marshal())
}

// userCertTTLs resolves the default and max TTL for user cert issuance
// (config values with package-default fallbacks).
func (a *Authority) userCertTTLs() (def, max time.Duration) {
	def = time.Duration(a.cfg.UserCertTTLHours) * time.Hour
	if def <= 0 {
		def = defaultUserCertTTLHours * time.Hour
	}
	max = time.Duration(a.cfg.MaxUserCertTTLHours) * time.Hour
	if max <= 0 {
		max = defaultMaxUserCertTTLHours * time.Hour
	}
	return def, max
}

// HostCertTTL resolves the configured (or default) host/agent cert TTL.
func (a *Authority) HostCertTTL() time.Duration {
	ttl := time.Duration(a.cfg.HostCertTTLHours) * time.Hour
	if ttl <= 0 {
		ttl = defaultHostCertTTLHours * time.Hour
	}
	return ttl
}

func clampRequestedTTL(requested, def, max time.Duration) time.Duration {
	if requested <= 0 {
		return def
	}
	if requested > max {
		return max
	}
	return requested
}

// IssueUserCert signs a short-lived user certificate for pub, scoped to a
// single valid principal (the gateway username). Remote-user authorization
// (which Unix account on the target machine) stays entirely independent,
// enforced by ReBAC/OpenFGA as it is today — certs only answer "who is
// this gateway user."
func (a *Authority) IssueUserCert(ctx context.Context, userID, username string, pub ssh.PublicKey, requestedTTL time.Duration) (*ssh.Certificate, error) {
	a.mu.RLock()
	signer := a.userSigner
	a.mu.RUnlock()
	if signer == nil {
		return nil, fmt.Errorf("user CA not initialized")
	}

	def, max := a.userCertTTLs()
	ttl := clampRequestedTTL(requestedTTL, def, max)

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          serial,
		CertType:        ssh.UserCert,
		KeyId:           username,
		ValidPrincipals: []string{username},
		ValidAfter:      uint64(now.Add(-1 * time.Minute).Unix()), // clock-skew buffer
		ValidBefore:     uint64(now.Add(ttl).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty":             "",
				"permit-port-forwarding": "",
				"permit-session":         "",
			},
		},
	}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		return nil, fmt.Errorf("sign user certificate: %w", err)
	}

	record := common.NewSSHCertificate(
		strconv.FormatUint(serial, 10), common.CATypeUser, userID, username,
		[]string{username}, ssh.FingerprintSHA256(pub), now, now.Add(ttl))
	if err := a.store.CreateSSHCertificate(ctx, record); err != nil {
		return nil, fmt.Errorf("record issued certificate: %w", err)
	}
	return cert, nil
}

// IssueHostCert signs a Host-CA certificate identifying either the
// gateway itself (subjectID == "") or a specific agent/machine
// (subjectID == machine.ID), for the given principals (hostnames or the
// machine's registered name).
func (a *Authority) IssueHostCert(ctx context.Context, subjectID string, principals []string, pub ssh.PublicKey, requestedTTL time.Duration) (*ssh.Certificate, error) {
	a.mu.RLock()
	signer := a.hostSigner
	a.mu.RUnlock()
	if signer == nil {
		return nil, fmt.Errorf("host CA not initialized")
	}
	if len(principals) == 0 {
		return nil, fmt.Errorf("host certificate requires at least one principal")
	}

	ttl := requestedTTL
	if ttl <= 0 {
		ttl = a.HostCertTTL()
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          serial,
		CertType:        ssh.HostCert,
		KeyId:           principals[0],
		ValidPrincipals: principals,
		ValidAfter:      uint64(now.Add(-1 * time.Minute).Unix()),
		ValidBefore:     uint64(now.Add(ttl).Unix()),
	}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		return nil, fmt.Errorf("sign host certificate: %w", err)
	}

	record := common.NewSSHCertificate(
		strconv.FormatUint(serial, 10), common.CATypeHost, subjectID, principals[0],
		principals, ssh.FingerprintSHA256(pub), now, now.Add(ttl))
	if err := a.store.CreateSSHCertificate(ctx, record); err != nil {
		return nil, fmt.Errorf("record issued certificate: %w", err)
	}
	return cert, nil
}

// RevokeCertificate marks a certificate revoked ahead of its TTL expiry
// and updates the in-memory cache immediately (the caller doesn't have to
// wait for the periodic RefreshRevocationCache tick).
func (a *Authority) RevokeCertificate(ctx context.Context, serial, revokedBy, reason string) error {
	if err := a.store.RevokeSSHCertificate(ctx, serial, revokedBy, reason); err != nil {
		return err
	}
	a.revokedMu.Lock()
	a.revoked[serial] = struct{}{}
	a.revokedMu.Unlock()
	return nil
}

// RefreshRevocationCache reloads the set of revoked-but-not-yet-expired
// certificate serials from the store. Call this periodically (see
// pkg/server's runCARevocationRefreshLoop) so revocation propagates to
// this process without a restart.
func (a *Authority) RefreshRevocationCache(ctx context.Context) error {
	serials, err := a.store.ListRevokedCertSerials(ctx)
	if err != nil {
		return err
	}
	set := make(map[string]struct{}, len(serials))
	for _, s := range serials {
		set[s] = struct{}{}
	}
	a.revokedMu.Lock()
	a.revoked = set
	a.revokedMu.Unlock()
	return nil
}

func (a *Authority) isRevokedCached(cert *ssh.Certificate) bool {
	a.revokedMu.RLock()
	defer a.revokedMu.RUnlock()
	_, ok := a.revoked[strconv.FormatUint(cert.Serial, 10)]
	return ok
}

// NeedsRenewal reports whether a cert expiring at expiresAt (issued with
// total lifetime ttl) has crossed the renewal threshold.
func NeedsRenewal(expiresAt time.Time, ttl time.Duration) bool {
	remaining := time.Until(expiresAt)
	if remaining <= 0 {
		return true
	}
	return remaining < time.Duration(float64(ttl)*RenewalFraction)
}

func randomSerial() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("generate certificate serial: %w", err)
	}
	return binary.BigEndian.Uint64(b[:]), nil
}
