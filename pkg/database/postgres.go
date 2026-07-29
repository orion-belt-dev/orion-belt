package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	_ "github.com/lib/pq"
	"github.com/zrougamed/orion-belt/pkg/common"
)

func init() {
	Register("postgres", NewPostgresStore)
}

// PostgresStore implements Store interface for PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL store
func NewPostgresStore(connectionString string) (Store, error) {
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	return &PostgresStore{db: db}, nil
}

// Connect establishes database connection
func (s *PostgresStore) Connect(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the database connection
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// Ping checks database connectivity
func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Migrate runs database migrations
func (s *PostgresStore) Migrate(ctx context.Context) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id VARCHAR(36) PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			email VARCHAR(255) NOT NULL,
			public_key TEXT NOT NULL,
			is_admin BOOLEAN DEFAULT FALSE,
			role VARCHAR(50) DEFAULT 'user',
			mfa_enabled BOOLEAN DEFAULT FALSE,
			webauthn_enabled BOOLEAN DEFAULT FALSE,
			totp_secret TEXT DEFAULT '',
			backup_codes_hash TEXT DEFAULT '',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS machines (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(255) UNIQUE NOT NULL,
			hostname VARCHAR(255) NOT NULL,
			port INTEGER NOT NULL,
			tags JSONB,
			agent_id VARCHAR(36),
			is_active BOOLEAN DEFAULT FALSE,
			last_seen_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id),
			machine_id VARCHAR(36) NOT NULL REFERENCES machines(id),
			remote_user VARCHAR(255) NOT NULL,
			source VARCHAR(32) NOT NULL DEFAULT 'ssh',
			start_time TIMESTAMP NOT NULL,
			end_time TIMESTAMP,
			recording_path TEXT NOT NULL,
			status VARCHAR(50) NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS access_requests (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id),
			machine_id VARCHAR(36) NOT NULL REFERENCES machines(id),
			remote_users TEXT[] NOT NULL,
			access_type VARCHAR(50) NOT NULL DEFAULT 'both',
			reason TEXT NOT NULL,
			duration INTEGER NOT NULL,
			status VARCHAR(50) NOT NULL,
			requested_at TIMESTAMP NOT NULL,
			reviewed_at TIMESTAMP,
			reviewed_by VARCHAR(36) REFERENCES users(id),
			expires_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS permissions (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id),
			machine_id VARCHAR(36) NOT NULL REFERENCES machines(id),
			access_type VARCHAR(50) NOT NULL,
			remote_users TEXT[] NOT NULL,
			granted_by VARCHAR(36) NOT NULL REFERENCES users(id),
			granted_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) REFERENCES users(id),
			action VARCHAR(255) NOT NULL,
			resource VARCHAR(255) NOT NULL,
			metadata JSONB,
			ip_address VARCHAR(45) NOT NULL,
			timestamp TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			key_hash VARCHAR(64) NOT NULL UNIQUE,
			key_prefix VARCHAR(16) NOT NULL,
			last_used_at TIMESTAMP,
			expires_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL,
			revoked_at TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS http_sessions (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash VARCHAR(64) NOT NULL UNIQUE,
			ip_address VARCHAR(45) NOT NULL,
			user_agent TEXT,
			expires_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL,
			last_seen_at TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_machine_id ON sessions(machine_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_permissions_user_id ON permissions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permissions_machine_id ON permissions(machine_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_key_hash ON api_keys(key_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_expires_at ON api_keys(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_http_sessions_user_id ON http_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_http_sessions_token_hash ON http_sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_http_sessions_expires_at ON http_sessions(expires_at)`,
		// MFA columns (idempotent for existing installs)
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS mfa_enabled BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS backup_codes_hash TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'user'`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS webauthn_enabled BOOLEAN DEFAULT FALSE`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT DEFAULT ''`,
		// Repair admins created before role was persisted on INSERT (DEFAULT 'user' left them as role=user).
		`UPDATE users SET role = 'admin' WHERE is_admin = true AND (role IS NULL OR role = '' OR role = 'user')`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS source VARCHAR(32) NOT NULL DEFAULT 'ssh'`,
		`ALTER TABLE access_requests ADD COLUMN IF NOT EXISTS access_type VARCHAR(50) NOT NULL DEFAULT 'both'`,
		`CREATE TABLE IF NOT EXISTS user_ssh_keys (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			public_key TEXT NOT NULL,
			key_type VARCHAR(100) NOT NULL,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS plugin_settings (
			name VARCHAR(255) PRIMARY KEY,
			enabled BOOLEAN NOT NULL DEFAULT FALSE,
			config JSONB NOT NULL DEFAULT '{}',
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS webauthn_credentials (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name VARCHAR(255) NOT NULL,
			credential_id BYTEA NOT NULL UNIQUE,
			public_key BYTEA NOT NULL,
			attestation_type VARCHAR(100) NOT NULL DEFAULT '',
			aaguid BYTEA,
			sign_count INTEGER NOT NULL DEFAULT 0,
			clone_warning BOOLEAN DEFAULT FALSE,
			backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
			backup_state BOOLEAN NOT NULL DEFAULT FALSE,
			flags_known BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_ssh_keys_user_id ON user_ssh_keys(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user_id ON webauthn_credentials(user_id)`,
		`ALTER TABLE webauthn_credentials ADD COLUMN IF NOT EXISTS backup_eligible BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE webauthn_credentials ADD COLUMN IF NOT EXISTS backup_state BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE webauthn_credentials ADD COLUMN IF NOT EXISTS flags_known BOOLEAN NOT NULL DEFAULT FALSE`,
		`CREATE TABLE IF NOT EXISTS notification_prefs (
			user_id VARCHAR(36) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			in_app_enabled BOOLEAN NOT NULL DEFAULT TRUE,
			email_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			event_types TEXT[] NOT NULL DEFAULT '{}',
			updated_at TIMESTAMP NOT NULL
		)`,
		// Admin-set boundary for user notification preferences. Singleton:
		// the CHECK pins it to a single row so there is exactly one policy.
		`CREATE TABLE IF NOT EXISTS notification_policy (
			id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
			allowed_channels TEXT[] NOT NULL DEFAULT '{}',
			mandatory_events JSONB NOT NULL DEFAULT '{}',
			updated_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS notifications (
			id VARCHAR(36) PRIMARY KEY,
			user_id VARCHAR(36) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			type VARCHAR(100) NOT NULL,
			title VARCHAR(255) NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			metadata JSONB,
			read_at TIMESTAMP,
			created_at TIMESTAMP NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user_unread ON notifications(user_id, read_at)`,
		`CREATE TABLE IF NOT EXISTS ssh_ca_keys (
			id VARCHAR(36) PRIMARY KEY,
			ca_type VARCHAR(20) NOT NULL,
			key_algo VARCHAR(20) NOT NULL DEFAULT 'ed25519',
			public_key TEXT NOT NULL,
			private_key_encrypted BYTEA NOT NULL,
			fingerprint VARCHAR(255) NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL,
			rotated_at TIMESTAMP
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ssh_ca_keys_type_active ON ssh_ca_keys(ca_type) WHERE active = true`,
		`CREATE TABLE IF NOT EXISTS ssh_certificates (
			id VARCHAR(36) PRIMARY KEY,
			serial VARCHAR(64) NOT NULL UNIQUE,
			cert_type VARCHAR(20) NOT NULL,
			subject_id VARCHAR(36),
			key_id VARCHAR(255) NOT NULL,
			principals TEXT[] NOT NULL,
			public_key_fingerprint VARCHAR(255) NOT NULL,
			issued_at TIMESTAMP NOT NULL,
			expires_at TIMESTAMP NOT NULL,
			revoked_at TIMESTAMP,
			revoked_by VARCHAR(36) REFERENCES users(id),
			revoke_reason TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ssh_certificates_subject ON ssh_certificates(subject_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ssh_certificates_expires_at ON ssh_certificates(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_ssh_certificates_revoked_at ON ssh_certificates(revoked_at)`,
	}

	for _, migration := range migrations {
		if _, err := s.db.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("%w: %v", ErrMigrationFailed, err)
		}
	}

	return nil
}

// CreateUser creates a new user
func (s *PostgresStore) CreateUser(ctx context.Context, user *common.User) error {
	query := `INSERT INTO users (id, username, email, public_key, is_admin, role, created_at, updated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	role := user.Role
	if role == "" {
		if user.IsAdmin {
			role = common.RoleAdmin
		} else {
			role = common.RoleUser
		}
	}

	_, err := s.db.ExecContext(ctx, query,
		user.ID, user.Username, user.Email, user.PublicKey,
		user.IsAdmin, role, user.CreatedAt, user.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	return nil
}

// GetUser retrieves a user by ID
func (s *PostgresStore) GetUser(ctx context.Context, id string) (*common.User, error) {
	query := `SELECT id, username, email, public_key, is_admin,
			  COALESCE(role, CASE WHEN is_admin THEN 'admin' ELSE 'user' END),
			  COALESCE(mfa_enabled, false), COALESCE(webauthn_enabled, false),
			  COALESCE(totp_secret, ''), COALESCE(backup_codes_hash, ''),
			  COALESCE(password_hash, ''), created_at, updated_at
			  FROM users WHERE id = $1`

	user := &common.User{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.PublicKey,
		&user.IsAdmin, &user.Role, &user.MFAEnabled, &user.WebAuthnEnabled,
		&user.TOTPSecret, &user.BackupCodesHash, &user.PasswordHash,
		&user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserByUsername retrieves a user by username
func (s *PostgresStore) GetUserByUsername(ctx context.Context, username string) (*common.User, error) {
	query := `SELECT id, username, email, public_key, is_admin,
			  COALESCE(role, CASE WHEN is_admin THEN 'admin' ELSE 'user' END),
			  COALESCE(mfa_enabled, false), COALESCE(webauthn_enabled, false),
			  COALESCE(totp_secret, ''), COALESCE(backup_codes_hash, ''),
			  COALESCE(password_hash, ''), created_at, updated_at
			  FROM users WHERE username = $1`

	user := &common.User{}
	err := s.db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.PublicKey,
		&user.IsAdmin, &user.Role, &user.MFAEnabled, &user.WebAuthnEnabled,
		&user.TOTPSecret, &user.BackupCodesHash, &user.PasswordHash,
		&user.CreatedAt, &user.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// UpdateUser updates an existing user
func (s *PostgresStore) UpdateUser(ctx context.Context, user *common.User) error {
	query := `UPDATE users SET email = $1, public_key = $2, is_admin = $3, role = $4,
			  mfa_enabled = $5, webauthn_enabled = $6, totp_secret = $7, backup_codes_hash = $8,
			  password_hash = $9, updated_at = $10
			  WHERE id = $11`

	result, err := s.db.ExecContext(ctx, query,
		user.Email, user.PublicKey, user.IsAdmin, user.Role,
		user.MFAEnabled, user.WebAuthnEnabled, user.TOTPSecret, user.BackupCodesHash,
		user.PasswordHash, time.Now(), user.ID)

	if err != nil {
		return fmt.Errorf("failed to update user: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser deletes a user
func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = $1`
	result, err := s.db.ExecContext(ctx, query, id)

	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUsers lists users with pagination
func (s *PostgresStore) ListUsers(ctx context.Context, limit, offset int) ([]*common.User, error) {
	query := `SELECT id, username, email, public_key, is_admin,
			  COALESCE(role, CASE WHEN is_admin THEN 'admin' ELSE 'user' END),
			  COALESCE(mfa_enabled, false), COALESCE(webauthn_enabled, false),
			  COALESCE(totp_secret, ''), COALESCE(backup_codes_hash, ''),
			  COALESCE(password_hash, ''), created_at, updated_at
			  FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	var users []*common.User
	for rows.Next() {
		user := &common.User{}
		if err := rows.Scan(&user.ID, &user.Username, &user.Email, &user.PublicKey,
			&user.IsAdmin, &user.Role, &user.MFAEnabled, &user.WebAuthnEnabled,
			&user.TOTPSecret, &user.BackupCodesHash, &user.PasswordHash,
			&user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		users = append(users, user)
	}

	return users, nil
}

// CreateMachine creates a new machine
func (s *PostgresStore) CreateMachine(ctx context.Context, machine *common.Machine) error {
	tags, _ := json.Marshal(machine.Tags)
	query := `INSERT INTO machines (id, name, hostname, port, tags, agent_id, is_active, last_seen_at, created_at, updated_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := s.db.ExecContext(ctx, query,
		machine.ID, machine.Name, machine.Hostname, machine.Port, tags,
		machine.AgentID, machine.IsActive, machine.LastSeenAt,
		machine.CreatedAt, machine.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create machine: %w", err)
	}
	return nil
}

// GetMachine retrieves a machine by ID
func (s *PostgresStore) GetMachine(ctx context.Context, id string) (*common.Machine, error) {
	query := `SELECT id, name, hostname, port, tags, agent_id, is_active, last_seen_at, created_at, updated_at
			  FROM machines WHERE id = $1`

	machine := &common.Machine{}
	var tags []byte
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&machine.ID, &machine.Name, &machine.Hostname, &machine.Port, &tags,
		&machine.AgentID, &machine.IsActive, &machine.LastSeenAt,
		&machine.CreatedAt, &machine.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get machine: %w", err)
	}

	json.Unmarshal(tags, &machine.Tags)
	return machine, nil
}

// GetMachineByName retrieves a machine by name
func (s *PostgresStore) GetMachineByName(ctx context.Context, name string) (*common.Machine, error) {
	query := `SELECT id, name, hostname, port, tags, agent_id, is_active, last_seen_at, created_at, updated_at
			  FROM machines WHERE name = $1`

	machine := &common.Machine{}
	var tags []byte
	err := s.db.QueryRowContext(ctx, query, name).Scan(
		&machine.ID, &machine.Name, &machine.Hostname, &machine.Port, &tags,
		&machine.AgentID, &machine.IsActive, &machine.LastSeenAt,
		&machine.CreatedAt, &machine.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get machine: %w", err)
	}

	json.Unmarshal(tags, &machine.Tags)
	return machine, nil
}

// UpdateMachine updates an existing machine
func (s *PostgresStore) UpdateMachine(ctx context.Context, machine *common.Machine) error {
	tags, _ := json.Marshal(machine.Tags)
	query := `UPDATE machines SET name = $1, hostname = $2, port = $3, tags = $4, agent_id = $5,
			  is_active = $6, last_seen_at = $7, updated_at = $8 WHERE id = $9`

	result, err := s.db.ExecContext(ctx, query,
		machine.Name, machine.Hostname, machine.Port, tags, machine.AgentID,
		machine.IsActive, machine.LastSeenAt, time.Now(), machine.ID)

	if err != nil {
		return fmt.Errorf("failed to update machine: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMachine deletes a machine
func (s *PostgresStore) DeleteMachine(ctx context.Context, id string) error {
	query := `DELETE FROM machines WHERE id = $1`
	result, err := s.db.ExecContext(ctx, query, id)

	if err != nil {
		return fmt.Errorf("failed to delete machine: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListMachines lists machines with pagination
func (s *PostgresStore) ListMachines(ctx context.Context, limit, offset int) ([]*common.Machine, error) {
	query := `SELECT id, name, hostname, port, tags, agent_id, is_active, last_seen_at, created_at, updated_at
			  FROM machines ORDER BY created_at DESC LIMIT $1 OFFSET $2`

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}
	defer rows.Close()

	var machines []*common.Machine
	for rows.Next() {
		machine := &common.Machine{}
		var tags []byte
		if err := rows.Scan(&machine.ID, &machine.Name, &machine.Hostname, &machine.Port, &tags,
			&machine.AgentID, &machine.IsActive, &machine.LastSeenAt,
			&machine.CreatedAt, &machine.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan machine: %w", err)
		}
		json.Unmarshal(tags, &machine.Tags)
		machines = append(machines, machine)
	}

	return machines, nil
}

// ListActiveMachines lists active machines
func (s *PostgresStore) ListActiveMachines(ctx context.Context) ([]*common.Machine, error) {
	query := `SELECT id, name, hostname, port, tags, agent_id, is_active, last_seen_at, created_at, updated_at
			  FROM machines WHERE is_active = true ORDER BY name`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list active machines: %w", err)
	}
	defer rows.Close()

	var machines []*common.Machine
	for rows.Next() {
		machine := &common.Machine{}
		var tags []byte
		if err := rows.Scan(&machine.ID, &machine.Name, &machine.Hostname, &machine.Port, &tags,
			&machine.AgentID, &machine.IsActive, &machine.LastSeenAt,
			&machine.CreatedAt, &machine.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan machine: %w", err)
		}
		json.Unmarshal(tags, &machine.Tags)
		machines = append(machines, machine)
	}

	return machines, nil
}

// CreateSession creates a new session
func (s *PostgresStore) CreateSession(ctx context.Context, session *common.Session) error {
	source := session.Source
	if source == "" {
		source = "ssh"
	}
	query := `INSERT INTO sessions (id, user_id, machine_id, remote_user, source, start_time, end_time, recording_path, status, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.MachineID, session.RemoteUser, source,
		session.StartTime, session.EndTime, session.RecordingPath,
		session.Status, session.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID
func (s *PostgresStore) GetSession(ctx context.Context, id string) (*common.Session, error) {
	query := `SELECT id, user_id, machine_id, remote_user, COALESCE(source, 'ssh'), start_time, end_time, recording_path, status, created_at
			  FROM sessions WHERE id = $1`

	session := &common.Session{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&session.ID, &session.UserID, &session.MachineID, &session.RemoteUser, &session.Source,
		&session.StartTime, &session.EndTime, &session.RecordingPath,
		&session.Status, &session.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return session, nil
}

// UpdateSession updates an existing session
func (s *PostgresStore) UpdateSession(ctx context.Context, session *common.Session) error {
	query := `UPDATE sessions SET end_time = $1, status = $2 WHERE id = $3`

	result, err := s.db.ExecContext(ctx, query, session.EndTime, session.Status, session.ID)

	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSessions lists recent sessions (active and historical).
func (s *PostgresStore) ListSessions(ctx context.Context, limit, offset int) ([]*common.Session, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, user_id, machine_id, remote_user, COALESCE(source, 'ssh'), start_time, end_time, recording_path, status, created_at
			  FROM sessions ORDER BY start_time DESC LIMIT $1 OFFSET $2`

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*common.Session
	sessions = make([]*common.Session, 0)
	for rows.Next() {
		session := &common.Session{}
		if err := rows.Scan(&session.ID, &session.UserID, &session.MachineID, &session.RemoteUser, &session.Source,
			&session.StartTime, &session.EndTime, &session.RecordingPath,
			&session.Status, &session.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// ListActiveSessions lists all active sessions
func (s *PostgresStore) ListActiveSessions(ctx context.Context) ([]*common.Session, error) {
	query := `SELECT id, user_id, machine_id, remote_user, COALESCE(source, 'ssh'), start_time, end_time, recording_path, status, created_at
			  FROM sessions WHERE status = 'active' ORDER BY start_time DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list active sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]*common.Session, 0)
	for rows.Next() {
		session := &common.Session{}
		if err := rows.Scan(&session.ID, &session.UserID, &session.MachineID, &session.RemoteUser, &session.Source,
			&session.StartTime, &session.EndTime, &session.RecordingPath,
			&session.Status, &session.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// ListUserSessions lists sessions for a specific user
func (s *PostgresStore) ListUserSessions(ctx context.Context, userID string, limit, offset int) ([]*common.Session, error) {
	query := `SELECT id, user_id, machine_id, remote_user, COALESCE(source, 'ssh'), start_time, end_time, recording_path, status, created_at
			  FROM sessions WHERE user_id = $1 ORDER BY start_time DESC LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list user sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*common.Session
	for rows.Next() {
		session := &common.Session{}
		if err := rows.Scan(&session.ID, &session.UserID, &session.MachineID, &session.RemoteUser, &session.Source,
			&session.StartTime, &session.EndTime, &session.RecordingPath,
			&session.Status, &session.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// EndSession marks a session as ended
func (s *PostgresStore) EndSession(ctx context.Context, id string, endTime time.Time) error {
	query := `UPDATE sessions SET end_time = $1, status = 'completed' WHERE id = $2`

	result, err := s.db.ExecContext(ctx, query, endTime, id)

	if err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateAccessRequest creates a new access request
func (s *PostgresStore) CreateAccessRequest(ctx context.Context, request *common.AccessRequest) error {
	if request.AccessType == "" {
		request.AccessType = "both"
	}
	query := `INSERT INTO access_requests (id, user_id, machine_id, remote_users, access_type, reason, duration, status, requested_at, reviewed_at, reviewed_by, expires_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	remoteUsers := pq.Array(request.RemoteUsers)

	_, err := s.db.ExecContext(ctx, query,
		request.ID, request.UserID, request.MachineID,
		remoteUsers, request.AccessType,
		request.Reason, request.Duration, request.Status, request.RequestedAt,
		request.ReviewedAt, request.ReviewedBy, request.ExpiresAt)

	if err != nil {
		return fmt.Errorf("failed to create access request: %w", err)
	}
	return nil
}

// GetAccessRequest retrieves an access request by ID
func (s *PostgresStore) GetAccessRequest(ctx context.Context, id string) (*common.AccessRequest, error) {
	query := `SELECT id, user_id, machine_id, remote_users, access_type, reason, duration, status, requested_at, reviewed_at, reviewed_by, expires_at
			  FROM access_requests WHERE id = $1`
	var remoteUsers pq.StringArray
	request := &common.AccessRequest{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&request.ID, &request.UserID, &request.MachineID, &remoteUsers, &request.AccessType, &request.Reason,
		&request.Duration, &request.Status, &request.RequestedAt,
		&request.ReviewedAt, &request.ReviewedBy, &request.ExpiresAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get access request: %w", err)
	}
	request.RemoteUsers = []string(remoteUsers)
	return request, nil
}

// UpdateAccessRequest updates an existing access request
func (s *PostgresStore) UpdateAccessRequest(ctx context.Context, request *common.AccessRequest) error {
	query := `UPDATE access_requests SET status = $1, reviewed_at = $2, reviewed_by = $3, expires_at = $4, duration = $5
			  WHERE id = $6`

	result, err := s.db.ExecContext(ctx, query,
		request.Status, request.ReviewedAt, request.ReviewedBy,
		request.ExpiresAt, request.Duration, request.ID)

	if err != nil {
		return fmt.Errorf("failed to update access request: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPendingAccessRequests lists all pending access requests
func (s *PostgresStore) ListPendingAccessRequests(ctx context.Context) ([]*common.AccessRequest, error) {
	query := `SELECT id, user_id, machine_id, remote_users, access_type, reason, duration, status, requested_at, reviewed_at, reviewed_by, expires_at
			  FROM access_requests WHERE status = 'pending' ORDER BY requested_at DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending access requests: %w", err)
	}
	defer rows.Close()

	var requests []*common.AccessRequest
	var remoteUsers pq.StringArray
	for rows.Next() {
		request := &common.AccessRequest{}
		if err := rows.Scan(&request.ID, &request.UserID, &request.MachineID, &remoteUsers, &request.AccessType, &request.Reason,
			&request.Duration, &request.Status, &request.RequestedAt,
			&request.ReviewedAt, &request.ReviewedBy, &request.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan access request: %w", err)
		}
		request.RemoteUsers = []string(remoteUsers)
		requests = append(requests, request)
	}

	return requests, nil
}

// ListAllAccessRequests lists access requests of any status, paginated.
func (s *PostgresStore) ListAllAccessRequests(ctx context.Context, limit, offset int) ([]*common.AccessRequest, error) {
	query := `SELECT id, user_id, machine_id, remote_users, access_type, reason, duration, status, requested_at, reviewed_at, reviewed_by, expires_at
			  FROM access_requests ORDER BY requested_at DESC LIMIT $1 OFFSET $2`

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list access requests: %w", err)
	}
	defer rows.Close()

	var requests []*common.AccessRequest
	for rows.Next() {
		request := &common.AccessRequest{}
		var remoteUsers pq.StringArray
		if err := rows.Scan(&request.ID, &request.UserID, &request.MachineID, &remoteUsers, &request.AccessType, &request.Reason,
			&request.Duration, &request.Status, &request.RequestedAt,
			&request.ReviewedAt, &request.ReviewedBy, &request.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan access request: %w", err)
		}
		request.RemoteUsers = []string(remoteUsers)
		requests = append(requests, request)
	}

	return requests, nil
}

// ListUserAccessRequests lists access requests for a specific user
func (s *PostgresStore) ListUserAccessRequests(ctx context.Context, userID string, limit, offset int) ([]*common.AccessRequest, error) {
	query := `SELECT id, user_id, machine_id, remote_users, access_type, reason, duration, status, requested_at, reviewed_at, reviewed_by, expires_at
			  FROM access_requests WHERE user_id = $1 ORDER BY requested_at DESC LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list user access requests: %w", err)
	}
	defer rows.Close()

	var requests []*common.AccessRequest
	for rows.Next() {
		request := &common.AccessRequest{}
		var remoteUsers pq.StringArray
		if err := rows.Scan(&request.ID, &request.UserID, &request.MachineID, &remoteUsers, &request.AccessType, &request.Reason,
			&request.Duration, &request.Status, &request.RequestedAt,
			&request.ReviewedAt, &request.ReviewedBy, &request.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan access request: %w", err)
		}
		request.RemoteUsers = []string(remoteUsers)
		requests = append(requests, request)
	}

	return requests, nil
}

// CreatePermission with remote_users array
func (s *PostgresStore) CreatePermission(ctx context.Context, permission *common.Permission) error {
	query := `INSERT INTO permissions (id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	// Convert []string to PostgreSQL array
	remoteUsers := pq.Array(permission.RemoteUsers)

	_, err := s.db.ExecContext(ctx, query,
		permission.ID, permission.UserID, permission.MachineID,
		permission.AccessType, remoteUsers, permission.GrantedBy,
		permission.GrantedAt, permission.ExpiresAt)

	if err != nil {
		return fmt.Errorf("failed to create permission: %w", err)
	}
	return nil
}

// GetPermission - scan remote_users array
func (s *PostgresStore) GetPermission(ctx context.Context, id string) (*common.Permission, error) {
	query := `SELECT id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at
			  FROM permissions WHERE id = $1`

	permission := &common.Permission{}
	var remoteUsers pq.StringArray

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&permission.ID, &permission.UserID, &permission.MachineID,
		&permission.AccessType, &remoteUsers, &permission.GrantedBy,
		&permission.GrantedAt, &permission.ExpiresAt)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get permission: %w", err)
	}

	permission.RemoteUsers = []string(remoteUsers)
	return permission, nil
}

// Check permission with specific remote user
func (s *PostgresStore) HasPermissionWithRemoteUser(ctx context.Context, userID, machineID, accessType, remoteUser string) (bool, error) {
	query := `SELECT COUNT(*) FROM permissions
			  WHERE user_id = $1 AND machine_id = $2 AND
			  (access_type = $3 OR access_type = 'both') AND
			  $4 = ANY(remote_users) AND
			  (expires_at IS NULL OR expires_at > NOW())`

	var count int
	err := s.db.QueryRowContext(ctx, query, userID, machineID, accessType, remoteUser).Scan(&count)

	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

// DeletePermission deletes a permission
func (s *PostgresStore) DeletePermission(ctx context.Context, id string) error {
	query := `DELETE FROM permissions WHERE id = $1`
	result, err := s.db.ExecContext(ctx, query, id)

	if err != nil {
		return fmt.Errorf("failed to delete permission: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListUserPermissions lists permissions for a specific user
func (s *PostgresStore) ListUserPermissions(ctx context.Context, userID string) ([]*common.Permission, error) {
	query := `SELECT id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at
			  FROM permissions WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
			  ORDER BY granted_at DESC`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list user permissions: %w", err)
	}
	defer rows.Close()

	var permissions []*common.Permission
	for rows.Next() {
		permission := &common.Permission{}
		var remoteUsers pq.StringArray
		if err := rows.Scan(&permission.ID, &permission.UserID, &permission.MachineID,
			&permission.AccessType, &remoteUsers, &permission.GrantedBy, &permission.GrantedAt,
			&permission.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan permission: %w", err)
		}
		permission.RemoteUsers = []string(remoteUsers)
		permissions = append(permissions, permission)
	}

	return permissions, nil
}

// ListMachinePermissions lists permissions for a specific machine
func (s *PostgresStore) ListMachinePermissions(ctx context.Context, machineID string) ([]*common.Permission, error) {
	query := `SELECT id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at
			  FROM permissions WHERE machine_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
			  ORDER BY granted_at DESC`

	rows, err := s.db.QueryContext(ctx, query, machineID)
	if err != nil {
		return nil, fmt.Errorf("failed to list machine permissions: %w", err)
	}
	defer rows.Close()

	var permissions []*common.Permission
	for rows.Next() {
		permission := &common.Permission{}
		var remoteUsers pq.StringArray
		if err := rows.Scan(&permission.ID, &permission.UserID, &permission.MachineID,
			&permission.AccessType, &remoteUsers, &permission.GrantedBy, &permission.GrantedAt,
			&permission.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan permission: %w", err)
		}
		permission.RemoteUsers = []string(remoteUsers)
		permissions = append(permissions, permission)
	}

	return permissions, nil
}

// ListAllPermissions returns active grants (admin list / matrix spine).
func (s *PostgresStore) ListAllPermissions(ctx context.Context, limit, offset int) ([]*common.Permission, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `SELECT id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at
			  FROM permissions WHERE (expires_at IS NULL OR expires_at > NOW())
			  ORDER BY granted_at DESC LIMIT $1 OFFSET $2`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list permissions: %w", err)
	}
	defer rows.Close()
	var permissions []*common.Permission
	for rows.Next() {
		permission := &common.Permission{}
		var remoteUsers pq.StringArray
		if err := rows.Scan(&permission.ID, &permission.UserID, &permission.MachineID,
			&permission.AccessType, &remoteUsers, &permission.GrantedBy, &permission.GrantedAt,
			&permission.ExpiresAt); err != nil {
			return nil, fmt.Errorf("failed to scan permission: %w", err)
		}
		permission.RemoteUsers = []string(remoteUsers)
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

// UpdatePermission updates mutable grant fields.
func (s *PostgresStore) UpdatePermission(ctx context.Context, permission *common.Permission) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE permissions SET access_type=$1, remote_users=$2, expires_at=$3, granted_by=$4, granted_at=$5
		WHERE id=$6`,
		permission.AccessType, pq.Array(permission.RemoteUsers), permission.ExpiresAt,
		permission.GrantedBy, permission.GrantedAt, permission.ID)
	if err != nil {
		return fmt.Errorf("failed to update permission: %w", err)
	}
	return nil
}

// FindActivePermission returns an existing non-expired grant for the user/machine/access_type trio.
func (s *PostgresStore) FindActivePermission(ctx context.Context, userID, machineID, accessType string) (*common.Permission, error) {
	query := `SELECT id, user_id, machine_id, access_type, remote_users, granted_by, granted_at, expires_at
			  FROM permissions
			  WHERE user_id=$1 AND machine_id=$2 AND access_type=$3
			    AND (expires_at IS NULL OR expires_at > NOW())
			  ORDER BY granted_at DESC LIMIT 1`
	permission := &common.Permission{}
	var remoteUsers pq.StringArray
	err := s.db.QueryRowContext(ctx, query, userID, machineID, accessType).Scan(
		&permission.ID, &permission.UserID, &permission.MachineID,
		&permission.AccessType, &remoteUsers, &permission.GrantedBy,
		&permission.GrantedAt, &permission.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	permission.RemoteUsers = []string(remoteUsers)
	return permission, nil
}

// HasPermission checks if a user has permission to access a machine
func (s *PostgresStore) HasPermission(ctx context.Context, userID, machineID, accessType string) (bool, error) {
	query := `SELECT COUNT(*) FROM permissions
			  WHERE user_id = $1 AND machine_id = $2 AND
			  (access_type = $3 OR access_type = 'both') AND
			  (expires_at IS NULL OR expires_at > NOW())`

	var count int
	err := s.db.QueryRowContext(ctx, query, userID, machineID, accessType).Scan(&count)

	if err != nil {
		return false, fmt.Errorf("failed to check permission: %w", err)
	}

	return count > 0, nil
}

// CreateAuditLog creates a new audit log entry
func (s *PostgresStore) CreateAuditLog(ctx context.Context, log *common.AuditLog) error {
	metadata, _ := json.Marshal(log.Metadata)
	query := `INSERT INTO audit_logs (id, user_id, action, resource, metadata, ip_address, timestamp)
			  VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := s.db.ExecContext(ctx, query,
		log.ID, log.UserID, log.Action, log.Resource,
		metadata, log.IPAddress, log.Timestamp)

	if err != nil {
		return fmt.Errorf("failed to create audit log: %w", err)
	}
	return nil
}

// ListAuditLogs lists audit logs with pagination and filters. Supported
// filter keys: "user_id", "action", "resource".
func (s *PostgresStore) ListAuditLogs(ctx context.Context, limit, offset int, filters map[string]interface{}) ([]*common.AuditLog, error) {
	query := `SELECT id, user_id, action, resource, metadata, ip_address, timestamp FROM audit_logs`

	var (
		conditions []string
		args       []interface{}
	)
	for _, key := range []string{"user_id", "action", "resource"} {
		v, ok := filters[key]
		if !ok {
			continue
		}
		strVal, ok := v.(string)
		if !ok || strVal == "" {
			continue
		}
		args = append(args, strVal)
		conditions = append(conditions, fmt.Sprintf("%s = $%d", key, len(args)))
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*common.AuditLog
	for rows.Next() {
		log := &common.AuditLog{}
		var metadata []byte
		if err := rows.Scan(&log.ID, &log.UserID, &log.Action, &log.Resource,
			&metadata, &log.IPAddress, &log.Timestamp); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		json.Unmarshal(metadata, &log.Metadata)
		logs = append(logs, log)
	}

	return logs, nil
}

// CreateNotification inserts a new in-app notification.
func (s *PostgresStore) CreateNotification(ctx context.Context, n *common.Notification) error {
	metadata, err := json.Marshal(n.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal notification metadata: %w", err)
	}
	query := `INSERT INTO notifications (id, user_id, type, title, body, metadata, read_at, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err = s.db.ExecContext(ctx, query, n.ID, n.UserID, n.Type, n.Title, n.Body, metadata, n.ReadAt, n.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}
	return nil
}

// ListUserNotifications lists a user's notifications, most recent first.
func (s *PostgresStore) ListUserNotifications(ctx context.Context, userID string, limit, offset int) ([]*common.Notification, error) {
	query := `SELECT id, user_id, type, title, body, metadata, read_at, created_at
			  FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list notifications: %w", err)
	}
	defer rows.Close()

	var notifications []*common.Notification
	for rows.Next() {
		n := &common.Notification{}
		var metadata []byte
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Body, &metadata, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan notification: %w", err)
		}
		if len(metadata) > 0 {
			json.Unmarshal(metadata, &n.Metadata)
		}
		notifications = append(notifications, n)
	}
	return notifications, nil
}

// CountUnreadNotifications returns how many unread notifications a user has.
func (s *PostgresStore) CountUnreadNotifications(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count unread notifications: %w", err)
	}
	return count, nil
}

// MarkNotificationRead marks a single notification (scoped to userID) as read.
func (s *PostgresStore) MarkNotificationRead(ctx context.Context, id, userID string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE id = $1 AND user_id = $2 AND read_at IS NULL`, id, userID)
	if err != nil {
		return fmt.Errorf("failed to mark notification read: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllNotificationsRead marks every unread notification for a user as read.
func (s *PostgresStore) MarkAllNotificationsRead(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = NOW() WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return fmt.Errorf("failed to mark all notifications read: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetNotificationPrefs(ctx context.Context, userID string) (*common.NotificationPrefs, error) {
	p := &common.NotificationPrefs{UserID: userID}
	var types pq.StringArray
	err := s.db.QueryRowContext(ctx, `
		SELECT in_app_enabled, email_enabled, event_types FROM notification_prefs WHERE user_id=$1`, userID).
		Scan(&p.InAppEnabled, &p.EmailEnabled, &types)
	if err == sql.ErrNoRows {
		return common.DefaultNotificationPrefs(userID), nil
	}
	if err != nil {
		return nil, err
	}
	p.EventTypes = []string(types)
	return p, nil
}

func (s *PostgresStore) UpsertNotificationPrefs(ctx context.Context, prefs *common.NotificationPrefs) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_prefs (user_id, in_app_enabled, email_enabled, event_types, updated_at)
		VALUES ($1,$2,$3,$4,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
		  in_app_enabled=EXCLUDED.in_app_enabled,
		  email_enabled=EXCLUDED.email_enabled,
		  event_types=EXCLUDED.event_types,
		  updated_at=NOW()`,
		prefs.UserID, prefs.InAppEnabled, prefs.EmailEnabled, pq.Array(prefs.EventTypes))
	return err
}

// GetNotificationPolicy returns the admin-set boundary for user preferences.
// An install that has never configured one gets the permissive default, which
// reproduces pre-policy behavior.
//
// The row is Normalized on read so a stale unknown channel left in the DB
// (channel removed in a later build, manual edit) cannot seed the admin UI
// and then fail the next save with an error that isn't visible on screen.
func (s *PostgresStore) GetNotificationPolicy(ctx context.Context) (*common.NotificationPolicy, error) {
	p := &common.NotificationPolicy{}
	var channels pq.StringArray
	var mandatory []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT allowed_channels, mandatory_events, updated_at FROM notification_policy WHERE id=1`).
		Scan(&channels, &mandatory, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return common.DefaultNotificationPolicy(), nil
	}
	if err != nil {
		return nil, err
	}
	p.AllowedChannels = []string(channels)
	if len(mandatory) > 0 {
		if err := json.Unmarshal(mandatory, &p.MandatoryEvents); err != nil {
			return nil, err
		}
	}
	if p.MandatoryEvents == nil {
		p.MandatoryEvents = map[string][]string{}
	}
	p.Normalize()
	return p, nil
}

// UpsertNotificationPolicy replaces the singleton policy row.
func (s *PostgresStore) UpsertNotificationPolicy(ctx context.Context, policy *common.NotificationPolicy) error {
	if policy == nil {
		return fmt.Errorf("policy is required")
	}
	mandatory, err := json.Marshal(policy.MandatoryEvents)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notification_policy (id, allowed_channels, mandatory_events, updated_at)
		VALUES (1,$1,$2,NOW())
		ON CONFLICT (id) DO UPDATE SET
		  allowed_channels=EXCLUDED.allowed_channels,
		  mandatory_events=EXCLUDED.mandatory_events,
		  updated_at=NOW()`,
		pq.Array(policy.AllowedChannels), mandatory)
	return err
}

// ExpireStalePendingAccessRequests marks old pending requests as expired.
func (s *PostgresStore) ExpireStalePendingAccessRequests(ctx context.Context, olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		olderThan = 7 * 24 * time.Hour
	}
	cutoff := time.Now().Add(-olderThan)
	res, err := s.db.ExecContext(ctx, `
		UPDATE access_requests SET status='expired'
		WHERE status='pending' AND requested_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetSessionDetails retrieves a session with user and machine names
func (s *PostgresStore) GetSessionDetails(ctx context.Context, id string) (map[string]interface{}, error) {
	query := `
        SELECT 
            s.id, s.start_time, s.end_time, s.recording_path, s.status,
            u.username, u.email,
            m.name as machine_name, m.hostname
        FROM sessions s
        JOIN users u ON s.user_id = u.id
        JOIN machines m ON s.machine_id = m.id
        WHERE s.id = $1`

	var startTime time.Time
	var endTime sql.NullTime
	var idStr, path, status, username, email, mName, mHost string

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&idStr, &startTime, &endTime, &path, &status,
		&username, &email, &mName, &mHost,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session details: %w", err)
	}

	return map[string]interface{}{
		"id":             idStr,
		"username":       username,
		"machine_name":   mName,
		"hostname":       mHost,
		"start_time":     startTime,
		"end_time":       endTime.Time,
		"recording_path": path,
		"status":         status,
	}, nil
}

func (s *PostgresStore) ListUserSessionsWithMachineNames(ctx context.Context, userID string, limit, offset int) ([]map[string]interface{}, error) {
	query := `
        SELECT s.id, s.start_time, s.status, m.name
        FROM sessions s
        JOIN machines m ON s.machine_id = m.id
        WHERE s.user_id = $1 
        ORDER BY s.start_time DESC LIMIT $2 OFFSET $3`

	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var id, status, machineName string
		var start time.Time
		rows.Scan(&id, &start, &status, &machineName)
		results = append(results, map[string]interface{}{
			"id":           id,
			"start_time":   start,
			"status":       status,
			"machine_name": machineName,
		})
	}
	return results, nil
}

// CreateAPIKey creates a new API key
func (s *PostgresStore) CreateAPIKey(ctx context.Context, key *common.APIKey) error {
	query := `
		INSERT INTO api_keys (id, user_id, name, key_hash, key_prefix, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := s.db.ExecContext(ctx, query,
		key.ID, key.UserID, key.Name, key.KeyHash, key.KeyPrefix, key.ExpiresAt, key.CreatedAt)
	return err
}

// GetAPIKey retrieves an API key by ID
func (s *PostgresStore) GetAPIKey(ctx context.Context, id string) (*common.APIKey, error) {
	query := `
		SELECT id, user_id, name, key_hash, key_prefix, last_used_at, expires_at, created_at, revoked_at
		FROM api_keys
		WHERE id = $1
	`

	key := &common.APIKey{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&key.ID, &key.UserID, &key.Name, &key.KeyHash, &key.KeyPrefix,
		&key.LastUsedAt, &key.ExpiresAt, &key.CreatedAt, &key.RevokedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return key, err
}

// GetAPIKeyByHash retrieves an API key by its hash
func (s *PostgresStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*common.APIKey, error) {
	query := `
		SELECT id, user_id, name, key_hash, key_prefix, last_used_at, expires_at, created_at, revoked_at
		FROM api_keys
		WHERE key_hash = $1 AND revoked_at IS NULL
	`

	key := &common.APIKey{}
	err := s.db.QueryRowContext(ctx, query, keyHash).Scan(
		&key.ID, &key.UserID, &key.Name, &key.KeyHash, &key.KeyPrefix,
		&key.LastUsedAt, &key.ExpiresAt, &key.CreatedAt, &key.RevokedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return key, err
}

// ListUserAPIKeys retrieves all API keys for a user
func (s *PostgresStore) ListUserAPIKeys(ctx context.Context, userID string) ([]*common.APIKey, error) {
	query := `
		SELECT id, user_id, name, key_hash, key_prefix, last_used_at, expires_at, created_at, revoked_at
		FROM api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*common.APIKey
	for rows.Next() {
		key := &common.APIKey{}
		err := rows.Scan(
			&key.ID, &key.UserID, &key.Name, &key.KeyHash, &key.KeyPrefix,
			&key.LastUsedAt, &key.ExpiresAt, &key.CreatedAt, &key.RevokedAt,
		)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}

	return keys, rows.Err()
}

// UpdateAPIKeyLastUsed updates the last used timestamp for an API key
func (s *PostgresStore) UpdateAPIKeyLastUsed(ctx context.Context, id string, lastUsedAt time.Time) error {
	query := `UPDATE api_keys SET last_used_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, lastUsedAt, id)
	return err
}

// RevokeAPIKey marks an API key as revoked
func (s *PostgresStore) RevokeAPIKey(ctx context.Context, id string) error {
	query := `UPDATE api_keys SET revoked_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// DeleteAPIKey permanently deletes an API key
func (s *PostgresStore) DeleteAPIKey(ctx context.Context, id string) error {
	query := `DELETE FROM api_keys WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// CreateHTTPSession creates a new HTTP session
func (s *PostgresStore) CreateHTTPSession(ctx context.Context, session *common.HTTPSession) error {
	query := `
		INSERT INTO http_sessions (id, user_id, token_hash, ip_address, user_agent, expires_at, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := s.db.ExecContext(ctx, query,
		session.ID, session.UserID, session.Token, session.IPAddress, session.UserAgent,
		session.ExpiresAt, session.CreatedAt, session.LastSeenAt)
	return err
}

// GetHTTPSession retrieves an HTTP session by ID
func (s *PostgresStore) GetHTTPSession(ctx context.Context, id string) (*common.HTTPSession, error) {
	query := `
		SELECT id, user_id, token_hash, ip_address, user_agent, expires_at, created_at, last_seen_at
		FROM http_sessions
		WHERE id = $1
	`

	session := &common.HTTPSession{}
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&session.ID, &session.UserID, &session.Token, &session.IPAddress,
		&session.UserAgent, &session.ExpiresAt, &session.CreatedAt, &session.LastSeenAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return session, err
}

// GetHTTPSessionByToken retrieves an HTTP session by its token hash
func (s *PostgresStore) GetHTTPSessionByToken(ctx context.Context, tokenHash string) (*common.HTTPSession, error) {
	query := `
		SELECT id, user_id, token_hash, ip_address, user_agent, expires_at, created_at, last_seen_at
		FROM http_sessions
		WHERE token_hash = $1 AND expires_at > NOW()
	`

	session := &common.HTTPSession{}
	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(
		&session.ID, &session.UserID, &session.Token, &session.IPAddress,
		&session.UserAgent, &session.ExpiresAt, &session.CreatedAt, &session.LastSeenAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return session, err
}

// UpdateHTTPSessionLastSeen updates the last seen timestamp for an HTTP session
func (s *PostgresStore) UpdateHTTPSessionLastSeen(ctx context.Context, id string, lastSeenAt time.Time) error {
	query := `UPDATE http_sessions SET last_seen_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, lastSeenAt, id)
	return err
}

// DeleteHTTPSession deletes an HTTP session
func (s *PostgresStore) DeleteHTTPSession(ctx context.Context, id string) error {
	query := `DELETE FROM http_sessions WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// DeleteExpiredHTTPSessions deletes all expired HTTP sessions
func (s *PostgresStore) DeleteExpiredHTTPSessions(ctx context.Context) error {
	query := `DELETE FROM http_sessions WHERE expires_at < NOW()`
	result, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		fmt.Printf("Cleaned up %d expired sessions\n", rowsAffected)
	}
	return nil
}

func (s *PostgresStore) CreateSSHKey(ctx context.Context, key *common.SSHKey) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_ssh_keys (id, user_id, name, public_key, key_type, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		key.ID, key.UserID, key.Name, key.PublicKey, key.KeyType, key.CreatedAt)
	return err
}

func (s *PostgresStore) ListUserSSHKeys(ctx context.Context, userID string) ([]*common.SSHKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, public_key, key_type, created_at FROM user_ssh_keys WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []*common.SSHKey
	for rows.Next() {
		k := &common.SSHKey{}
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.PublicKey, &k.KeyType, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *PostgresStore) DeleteSSHKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_ssh_keys WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateWebAuthnCredential(ctx context.Context, cred *common.WebAuthnCredential) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO webauthn_credentials
		(id, user_id, name, credential_id, public_key, attestation_type, aaguid, sign_count, clone_warning,
		 backup_eligible, backup_state, flags_known, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		cred.ID, cred.UserID, cred.Name, cred.CredentialID, cred.PublicKey,
		cred.AttestationType, cred.AAGUID, cred.SignCount, cred.CloneWarning,
		cred.BackupEligible, cred.BackupState, cred.FlagsKnown, cred.CreatedAt)
	return err
}

func (s *PostgresStore) ListWebAuthnCredentials(ctx context.Context, userID string) ([]*common.WebAuthnCredential, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, credential_id, public_key, attestation_type, aaguid, sign_count, clone_warning,
		       COALESCE(backup_eligible, false), COALESCE(backup_state, false), COALESCE(flags_known, false), created_at
		FROM webauthn_credentials WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*common.WebAuthnCredential
	for rows.Next() {
		c := &common.WebAuthnCredential{}
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.CredentialID, &c.PublicKey,
			&c.AttestationType, &c.AAGUID, &c.SignCount, &c.CloneWarning,
			&c.BackupEligible, &c.BackupState, &c.FlagsKnown, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *PostgresStore) GetWebAuthnCredentialByCredID(ctx context.Context, credID []byte) (*common.WebAuthnCredential, error) {
	c := &common.WebAuthnCredential{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, name, credential_id, public_key, attestation_type, aaguid, sign_count, clone_warning,
		       COALESCE(backup_eligible, false), COALESCE(backup_state, false), COALESCE(flags_known, false), created_at
		FROM webauthn_credentials WHERE credential_id=$1`, credID).Scan(
		&c.ID, &c.UserID, &c.Name, &c.CredentialID, &c.PublicKey,
		&c.AttestationType, &c.AAGUID, &c.SignCount, &c.CloneWarning,
		&c.BackupEligible, &c.BackupState, &c.FlagsKnown, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *PostgresStore) UpdateWebAuthnCredential(ctx context.Context, cred *common.WebAuthnCredential) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE webauthn_credentials SET sign_count=$1, clone_warning=$2,
		 backup_eligible=$3, backup_state=$4, flags_known=$5 WHERE id=$6`,
		cred.SignCount, cred.CloneWarning, cred.BackupEligible, cred.BackupState, cred.FlagsKnown, cred.ID)
	return err
}

func (s *PostgresStore) DeleteWebAuthnCredential(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPluginSetting retrieves the stored enabled/config state for a plugin.
func (s *PostgresStore) GetPluginSetting(ctx context.Context, name string) (*common.PluginSetting, error) {
	setting := &common.PluginSetting{Name: name}
	var config []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled, config, updated_at FROM plugin_settings WHERE name=$1`, name).
		Scan(&setting.Enabled, &config, &setting.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(config, &setting.Config); err != nil {
		return nil, err
	}
	return setting, nil
}

// ListPluginSettings returns the stored state for every plugin that has ever
// been configured or toggled.
func (s *PostgresStore) ListPluginSettings(ctx context.Context) ([]*common.PluginSetting, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, enabled, config, updated_at FROM plugin_settings ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*common.PluginSetting
	for rows.Next() {
		setting := &common.PluginSetting{}
		var config []byte
		if err := rows.Scan(&setting.Name, &setting.Enabled, &config, &setting.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(config, &setting.Config); err != nil {
			return nil, err
		}
		out = append(out, setting)
	}
	return out, rows.Err()
}

// UpsertPluginSetting persists a plugin's enabled state and config, creating
// or replacing the row for that plugin name.
func (s *PostgresStore) UpsertPluginSetting(ctx context.Context, setting *common.PluginSetting) error {
	config, err := json.Marshal(setting.Config)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO plugin_settings (name, enabled, config, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET enabled=$2, config=$3, updated_at=$4`,
		setting.Name, setting.Enabled, config, setting.UpdatedAt)
	return err
}
