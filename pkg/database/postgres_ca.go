package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// CreateCAKey persists a new CA keypair. privateKeyEncrypted is the
// envelope-encrypted PKCS8 PEM produced by pkg/ca — this store never sees
// (or needs to understand) the plaintext key material.
func (s *PostgresStore) CreateCAKey(ctx context.Context, key *common.CAKey, privateKeyEncrypted []byte) error {
	query := `INSERT INTO ssh_ca_keys (id, ca_type, key_algo, public_key, private_key_encrypted, fingerprint, active, created_at, rotated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.db.ExecContext(ctx, query,
		key.ID, key.CAType, key.KeyAlgo, key.PublicKey, privateKeyEncrypted,
		key.Fingerprint, key.Active, key.CreatedAt, key.RotatedAt)
	if err != nil {
		return fmt.Errorf("failed to create ca key: %w", err)
	}
	return nil
}

// GetActiveCAKey returns the currently active keypair for a CA type
// (there is at most one, enforced by a partial unique index) along with
// its encrypted private key blob.
func (s *PostgresStore) GetActiveCAKey(ctx context.Context, caType string) (*common.CAKey, []byte, error) {
	query := `SELECT id, ca_type, key_algo, public_key, private_key_encrypted, fingerprint, active, created_at, rotated_at
			  FROM ssh_ca_keys WHERE ca_type = $1 AND active = true`

	key := &common.CAKey{}
	var privEnc []byte
	err := s.db.QueryRowContext(ctx, query, caType).Scan(
		&key.ID, &key.CAType, &key.KeyAlgo, &key.PublicKey, &privEnc,
		&key.Fingerprint, &key.Active, &key.CreatedAt, &key.RotatedAt)

	if err == sql.ErrNoRows {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get active ca key: %w", err)
	}
	return key, privEnc, nil
}

// ListCAKeys lists all keypairs (active and rotated-out) for a CA type,
// most recent first.
func (s *PostgresStore) ListCAKeys(ctx context.Context, caType string) ([]*common.CAKey, error) {
	query := `SELECT id, ca_type, key_algo, public_key, fingerprint, active, created_at, rotated_at
			  FROM ssh_ca_keys WHERE ca_type = $1 ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, caType)
	if err != nil {
		return nil, fmt.Errorf("failed to list ca keys: %w", err)
	}
	defer rows.Close()

	var keys []*common.CAKey
	for rows.Next() {
		key := &common.CAKey{}
		if err := rows.Scan(&key.ID, &key.CAType, &key.KeyAlgo, &key.PublicKey,
			&key.Fingerprint, &key.Active, &key.CreatedAt, &key.RotatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ca key: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// CreateSSHCertificate records a freshly-issued certificate for lifecycle
// tracking (listing, revocation).
func (s *PostgresStore) CreateSSHCertificate(ctx context.Context, cert *common.SSHCertificate) error {
	query := `INSERT INTO ssh_certificates (id, serial, cert_type, subject_id, key_id, principals, public_key_fingerprint, issued_at, expires_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`
	_, err := s.db.ExecContext(ctx, query,
		cert.ID, cert.Serial, cert.CertType, nullableString(cert.SubjectID), cert.KeyID,
		pq.Array(cert.Principals), cert.PublicKeyFingerprint, cert.IssuedAt, cert.ExpiresAt)
	if err != nil {
		return fmt.Errorf("failed to create ssh certificate record: %w", err)
	}
	return nil
}

// GetSSHCertificateBySerial retrieves a certificate's lifecycle record by
// its cert serial number.
func (s *PostgresStore) GetSSHCertificateBySerial(ctx context.Context, serial string) (*common.SSHCertificate, error) {
	query := `SELECT id, serial, cert_type, COALESCE(subject_id, ''), key_id, principals, public_key_fingerprint,
			  issued_at, expires_at, revoked_at, revoked_by, COALESCE(revoke_reason, '')
			  FROM ssh_certificates WHERE serial = $1`

	cert := &common.SSHCertificate{}
	var principals pq.StringArray
	err := s.db.QueryRowContext(ctx, query, serial).Scan(
		&cert.ID, &cert.Serial, &cert.CertType, &cert.SubjectID, &cert.KeyID, &principals,
		&cert.PublicKeyFingerprint, &cert.IssuedAt, &cert.ExpiresAt, &cert.RevokedAt, &cert.RevokedBy, &cert.RevokeReason)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get ssh certificate: %w", err)
	}
	cert.Principals = []string(principals)
	return cert, nil
}

// ListSSHCertificates lists issued-certificate lifecycle records, optionally
// filtered by type, subject, and revoked/expired status.
func (s *PostgresStore) ListSSHCertificates(ctx context.Context, filter common.SSHCertFilter, limit, offset int) ([]*common.SSHCertificate, error) {
	query := `SELECT id, serial, cert_type, COALESCE(subject_id, ''), key_id, principals, public_key_fingerprint,
			  issued_at, expires_at, revoked_at, revoked_by, COALESCE(revoke_reason, '')
			  FROM ssh_certificates`

	var (
		conditions []string
		args       []interface{}
	)
	if filter.CertType != "" {
		args = append(args, filter.CertType)
		conditions = append(conditions, fmt.Sprintf("cert_type = $%d", len(args)))
	}
	if filter.SubjectID != "" {
		args = append(args, filter.SubjectID)
		conditions = append(conditions, fmt.Sprintf("subject_id = $%d", len(args)))
	}
	if filter.Active != nil {
		if *filter.Active {
			conditions = append(conditions, "revoked_at IS NULL AND expires_at > NOW()")
		} else {
			conditions = append(conditions, "(revoked_at IS NOT NULL OR expires_at <= NOW())")
		}
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY issued_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list ssh certificates: %w", err)
	}
	defer rows.Close()

	var certs []*common.SSHCertificate
	for rows.Next() {
		cert := &common.SSHCertificate{}
		var principals pq.StringArray
		if err := rows.Scan(&cert.ID, &cert.Serial, &cert.CertType, &cert.SubjectID, &cert.KeyID, &principals,
			&cert.PublicKeyFingerprint, &cert.IssuedAt, &cert.ExpiresAt, &cert.RevokedAt, &cert.RevokedBy, &cert.RevokeReason); err != nil {
			return nil, fmt.Errorf("failed to scan ssh certificate: %w", err)
		}
		cert.Principals = []string(principals)
		certs = append(certs, cert)
	}
	return certs, nil
}

// ListRevokedCertSerials returns the serial numbers of every certificate
// that has been explicitly revoked (independent of TTL expiry) and has not
// yet naturally expired — this is the set pkg/ca's CertChecker.IsRevoked
// needs to consult; once a cert expires it's rejected on TTL alone and
// doesn't need to stay in the revocation set.
func (s *PostgresStore) ListRevokedCertSerials(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT serial FROM ssh_certificates WHERE revoked_at IS NOT NULL AND expires_at > NOW()`)
	if err != nil {
		return nil, fmt.Errorf("failed to list revoked cert serials: %w", err)
	}
	defer rows.Close()

	var serials []string
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, fmt.Errorf("failed to scan revoked serial: %w", err)
		}
		serials = append(serials, serial)
	}
	return serials, nil
}

// RevokeSSHCertificate marks a certificate revoked ahead of its TTL expiry.
func (s *PostgresStore) RevokeSSHCertificate(ctx context.Context, serial, revokedBy, reason string) error {
	query := `UPDATE ssh_certificates SET revoked_at = NOW(), revoked_by = $1, revoke_reason = $2
			  WHERE serial = $3 AND revoked_at IS NULL`
	result, err := s.db.ExecContext(ctx, query, revokedBy, reason, serial)
	if err != nil {
		return fmt.Errorf("failed to revoke ssh certificate: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
