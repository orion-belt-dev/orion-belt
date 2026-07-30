package client

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// LoadSigner returns the ssh.Signer osh/ocp/oadmin should authenticate
// with. When the server has SSH CA enabled, it transparently uses a
// cached-or-freshly-issued short-lived User certificate instead of the
// raw static key — auto-detected via the API (GetTrustedCA), no local
// opt-in config needed. Any failure to reach the CA (disabled, offline,
// issuance error) falls back to the raw static key exactly like pre-CA
// behavior, so this is always safe to call. This is also the single place
// client signer-loading logic lives now, replacing three independent
// copies of "read KeyFile, ssh.ParsePrivateKey."
func LoadSigner(cfg *common.Config, usernameOverride string, logger *common.Logger) (ssh.Signer, error) {
	keyData, err := os.ReadFile(cfg.Auth.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key: %w", err)
	}
	rawSigner, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	certPath := cfg.Auth.KeyFile + "-cert.pub"
	if certSigner := loadCachedCertSigner(certPath, rawSigner); certSigner != nil {
		return certSigner, nil
	}

	certSigner, err := refreshCertSigner(cfg, usernameOverride, rawSigner, certPath, logger)
	if err != nil {
		if logger != nil {
			logger.Debug("SSH certificate unavailable, using static key: %v", err)
		}
		return rawSigner, nil
	}
	return certSigner, nil
}

// loadCachedCertSigner reuses a previously-issued, still-valid cert cached
// next to the key file, avoiding an API round trip on every connection.
func loadCachedCertSigner(certPath string, rawSigner ssh.Signer) ssh.Signer {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		return nil
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil
	}
	if string(cert.Key.Marshal()) != string(rawSigner.PublicKey().Marshal()) {
		return nil // cached cert belongs to a different keypair (key rotated)
	}
	ttl := time.Duration(cert.ValidBefore-cert.ValidAfter) * time.Second
	expiresAt := time.Unix(int64(cert.ValidBefore), 0)
	if ca.NeedsRenewal(expiresAt, ttl) {
		return nil
	}
	certSigner, err := ssh.NewCertSigner(cert, rawSigner)
	if err != nil {
		return nil
	}
	return certSigner
}

// refreshCertSigner logs in over HTTP, checks server SSH CA capability,
// requests a fresh cert, caches it to certPath, and wraps it into a Signer.
func refreshCertSigner(cfg *common.Config, usernameOverride string, rawSigner ssh.Signer, certPath string, logger *common.Logger) (ssh.Signer, error) {
	apiClient, err := LoadAPIClient(cfg, usernameOverride, logger)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	trusted, err := apiClient.GetTrustedCA()
	if err != nil {
		return nil, fmt.Errorf("check ca status: %w", err)
	}
	if !trusted.Enabled {
		return nil, fmt.Errorf("ssh certificate authority not enabled on server")
	}

	pubLine := string(ssh.MarshalAuthorizedKey(rawSigner.PublicKey()))
	issued, err := apiClient.IssueUserCert(pubLine, 0)
	if err != nil {
		return nil, fmt.Errorf("issue certificate: %w", err)
	}

	if err := os.WriteFile(certPath, []byte(issued.Certificate), 0600); err != nil && logger != nil {
		logger.Warn("failed to cache issued certificate at %s: %v", certPath, err)
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(issued.Certificate))
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("server returned a non-certificate response")
	}
	return ssh.NewCertSigner(cert, rawSigner)
}
