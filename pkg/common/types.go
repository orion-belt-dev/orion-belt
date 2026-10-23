package common

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// User represents a system user
type User struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	Email           string    `json:"email"`
	PublicKey       string    `json:"public_key"`
	IsAdmin         bool      `json:"is_admin"`
	Role            string    `json:"role"` // admin | operator | auditor | user
	MFAEnabled      bool      `json:"mfa_enabled"`
	WebAuthnEnabled bool      `json:"webauthn_enabled"`
	TOTPSecret      string    `json:"-"`
	BackupCodesHash string    `json:"-"`
	PasswordHash    string    `json:"-"` // argon2id; empty = no password login
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// HasPassword reports whether a password has been set for password+TOTP login.
func (u *User) HasPassword() bool {
	return u != nil && u.PasswordHash != ""
}

// Role constants
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleAuditor  = "auditor"
	RoleUser     = "user"
)

// EffectiveRole returns the user's role, mapping legacy is_admin to admin.
// A stale role="user" with is_admin=true (CreateUser historically omitted role)
// is treated as admin.
func (u *User) EffectiveRole() string {
	switch u.Role {
	case RoleAdmin, RoleOperator, RoleAuditor:
		return u.Role
	}
	if u.IsAdmin {
		return RoleAdmin
	}
	if u.Role != "" {
		return u.Role
	}
	return RoleUser
}

// HasRole reports whether the user has at least the given privilege level.
func (u *User) HasRole(min string) bool {
	order := map[string]int{RoleUser: 1, RoleAuditor: 2, RoleOperator: 3, RoleAdmin: 4}
	return order[u.EffectiveRole()] >= order[min]
}

// SSHKey is an authorized public key (including FIDO/sk-* keys).
type SSHKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	KeyType   string    `json:"key_type"` // ssh-ed25519, sk-ssh-ed25519@openssh.com, etc.
	CreatedAt time.Time `json:"created_at"`
}

// WebAuthnCredential stores a FIDO2/WebAuthn authenticator credential.
type WebAuthnCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	Name            string    `json:"name"`
	CredentialID    []byte    `json:"-"`
	PublicKey       []byte    `json:"-"`
	AttestationType string    `json:"attestation_type"`
	AAGUID          []byte    `json:"-"`
	SignCount       uint32    `json:"sign_count"`
	CloneWarning    bool      `json:"clone_warning"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	FlagsKnown      bool      `json:"-"` // false for credentials registered before flags were persisted
	CreatedAt       time.Time `json:"created_at"`
}

// CAKey is an SSH Certificate Authority signing keypair (User CA or Host
// CA). The private key is never exposed here — it is stored encrypted at
// rest and only ever loaded by pkg/ca into an in-memory ssh.Signer.
type CAKey struct {
	ID          string     `json:"id"`
	CAType      string     `json:"ca_type"` // "user" | "host"
	KeyAlgo     string     `json:"key_algo"`
	PublicKey   string     `json:"public_key"` // authorized_keys-format CA pubkey line
	Fingerprint string     `json:"fingerprint"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	RotatedAt   *time.Time `json:"rotated_at,omitempty"`
}

// CA type constants.
const (
	CATypeUser = "user"
	CATypeHost = "host"
)

// SSHCertificate records the lifecycle of a certificate issued by an
// Orion Belt CA: who/what it was issued to, when it expires, and whether
// it has been explicitly revoked ahead of its natural TTL expiry.
type SSHCertificate struct {
	ID                   string     `json:"id"`
	Serial               string     `json:"serial"`
	CertType             string     `json:"cert_type"` // "user" | "host"
	SubjectID            string     `json:"subject_id,omitempty"`
	KeyID                string     `json:"key_id"`
	Principals           []string   `json:"principals"`
	PublicKeyFingerprint string     `json:"public_key_fingerprint"`
	IssuedAt             time.Time  `json:"issued_at"`
	ExpiresAt            time.Time  `json:"expires_at"`
	RevokedAt            *time.Time `json:"revoked_at,omitempty"`
	RevokedBy            *string    `json:"revoked_by,omitempty"`
	RevokeReason         string     `json:"revoke_reason,omitempty"`
}

// SSHCertFilter narrows ListSSHCertificates results.
type SSHCertFilter struct {
	CertType  string // "" = any
	SubjectID string // "" = any
	Active    *bool  // nil = any, true = not revoked and not expired, false = revoked or expired
}

// Machine represents a target machine
type Machine struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Hostname   string            `json:"hostname"`
	Port       int               `json:"port"`
	Tags       map[string]string `json:"tags"`
	AgentID    string            `json:"agent_id"`
	IsActive   bool              `json:"is_active"`
	LastSeenAt *time.Time        `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// Session represents an SSH or web-terminal session
type Session struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	MachineID     string     `json:"machine_id"`
	RemoteUser    string     `json:"remote_user"` // User on target machine (root, user, etc)
	Source        string     `json:"source"`      // ssh | web
	StartTime     time.Time  `json:"start_time"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	RecordingPath string     `json:"recording_path"`
	Status        string     `json:"status"` // active, completed, terminated
	CreatedAt     time.Time  `json:"created_at"`
}

// AccessRequest represents a temporary access request
type AccessRequest struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	MachineID   string     `json:"machine_id"`
	RemoteUsers []string   `json:"remote_users"` // Allowed remote users ["root", "user"]
	AccessType  string     `json:"access_type"`  // ssh, scp, both
	Reason      string     `json:"reason"`
	Duration    int        `json:"duration"` // in seconds
	Status      string     `json:"status"`   // pending, approved, rejected, expired
	RequestedAt time.Time  `json:"requested_at"`
	ReviewedAt  *time.Time `json:"reviewed_at,omitempty"`
	ReviewedBy  *string    `json:"reviewed_by,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// RiskSignals are captured when the request is made; nil for requests
	// that predate signal collection.
	RiskSignals *RiskSignals `json:"risk_signals,omitempty"`
}

// Permission represents a ReBAC permission
type Permission struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	MachineID   string     `json:"machine_id"`
	AccessType  string     `json:"access_type"`  // ssh, scp, both
	RemoteUsers []string   `json:"remote_users"` // Allowed remote users ["root", "user", "postgres"]
	GrantedBy   string     `json:"granted_by"`
	GrantedAt   time.Time  `json:"granted_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// FromDBTimestamp turns a value read from a TIMESTAMP (without time zone)
// column back into the instant that was written. Writers store the server's
// local wall clock and lib/pq returns it labelled UTC; on a UTC host this is
// a no-op.
func FromDBTimestamp(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local).UTC()
}

// Why a grant version stopped applying (PermissionVersion.EndReason).
const (
	PermissionEndUpdated = "updated"
	PermissionEndRevoked = "revoked"
)

// PermissionVersion is one version of a grant. SupersededAt is nil while the
// version is the current row; otherwise it records when the grant was changed
// (EndReason "updated") or removed (EndReason "revoked").
type PermissionVersion struct {
	Permission
	SupersededAt *time.Time `json:"superseded_at,omitempty"`
	EndReason    string     `json:"end_reason,omitempty"`
}

// ActiveAt reports whether this version authorised access at t.
func (v *PermissionVersion) ActiveAt(t time.Time) bool {
	if v.GrantedAt.After(t) {
		return false
	}
	if v.ExpiresAt != nil && !v.ExpiresAt.After(t) {
		return false
	}
	return v.SupersededAt == nil || v.SupersededAt.After(t)
}

// Relations an AccessLink can describe.
const (
	LinkRole          = "role"
	LinkGrant         = "grant"
	LinkAccessRequest = "access_request"
	LinkOpenFGA       = "openfga"
	LinkNone          = "none"
)

// AccessExplanation is an access decision together with the relationships
// that produced it, for "why can (or can't) I reach this machine?".
type AccessExplanation struct {
	Allowed    bool         `json:"allowed"`
	Summary    string       `json:"summary"`
	UserID     string       `json:"user_id"`
	Username   string       `json:"username"`
	MachineID  string       `json:"machine_id"`
	Machine    string       `json:"machine"`
	AccessType string       `json:"access_type"`
	RemoteUser string       `json:"remote_user"`
	Chain      []AccessLink `json:"chain"`
}

// AccessLink is one relationship considered for a decision. Holds is true
// for the link(s) that authorise the access.
type AccessLink struct {
	Relation  string     `json:"relation"`
	Ref       string     `json:"ref,omitempty"` // permission:<id>, access_request:<id>, role:<role>
	Holds     bool       `json:"holds"`
	Detail    string     `json:"detail"`
	Via       string     `json:"via,omitempty"` // the access request a grant came from
	Reason    string     `json:"reason,omitempty"`
	GrantedBy string     `json:"granted_by,omitempty"`
	At        *time.Time `json:"at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// AuditLog represents an audit log entry
type AuditLog struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Action    string                 `json:"action"`
	Resource  string                 `json:"resource"`
	Metadata  map[string]interface{} `json:"metadata"`
	IPAddress string                 `json:"ip_address"`
	Timestamp time.Time              `json:"timestamp"`
}

// Notification represents an in-app (web) notification delivered to a
// specific user, e.g. "your access request was approved".
type Notification struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Type      string                 `json:"type"` // e.g. "access_request.approved"
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	Metadata  map[string]interface{} `json:"metadata"`
	ReadAt    *time.Time             `json:"read_at,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// NewNotification creates a new unread notification for a user.
func NewNotification(userID, notifType, title, body string, metadata map[string]interface{}) *Notification {
	return &Notification{
		ID:        uuid.New().String(),
		UserID:    userID,
		Type:      notifType,
		Title:     title,
		Body:      body,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	}
}

// APIKey represents an API key for authentication
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`       // Human-readable name for the key
	KeyHash    string     `json:"-"`          // SHA256 hash of the actual key
	KeyPrefix  string     `json:"key_prefix"` // First 8 chars for identification
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// HTTPSession represents an HTTP session
type HTTPSession struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Token      string    `json:"-"`
	IPAddress  string    `json:"ip_address"`
	UserAgent  string    `json:"user_agent"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// NewUser creates a new user
func NewUser(username, email, publicKey string, isAdmin bool) *User {
	now := time.Now()
	role := RoleUser
	if isAdmin {
		role = RoleAdmin
	}
	return &User{
		ID:        uuid.New().String(),
		Username:  username,
		Email:     email,
		PublicKey: publicKey,
		IsAdmin:   isAdmin,
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewMachine creates a new machine
func NewMachine(name, hostname string, port int, tags map[string]string) *Machine {
	now := time.Now()
	return &Machine{
		ID:        uuid.New().String(),
		Name:      name,
		Hostname:  hostname,
		Port:      port,
		Tags:      tags,
		IsActive:  false,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewCAKey creates a new CA keypair record. publicKey is the
// authorized_keys-format line; the caller persists the encrypted private
// key separately (pkg/ca owns that encoding, not this package).
func NewCAKey(caType, keyAlgo, publicKey, fingerprint string) *CAKey {
	return &CAKey{
		ID:          uuid.New().String(),
		CAType:      caType,
		KeyAlgo:     keyAlgo,
		PublicKey:   publicKey,
		Fingerprint: fingerprint,
		Active:      true,
		CreatedAt:   time.Now(),
	}
}

// NewSSHCertificate records a freshly-issued certificate for lifecycle
// tracking (listing, revocation) independent of the cert bytes themselves.
func NewSSHCertificate(serial, certType, subjectID, keyID string, principals []string, pubKeyFingerprint string, issuedAt, expiresAt time.Time) *SSHCertificate {
	return &SSHCertificate{
		ID:                   uuid.New().String(),
		Serial:               serial,
		CertType:             certType,
		SubjectID:            subjectID,
		KeyID:                keyID,
		Principals:           principals,
		PublicKeyFingerprint: pubKeyFingerprint,
		IssuedAt:             issuedAt,
		ExpiresAt:            expiresAt,
	}
}

// NewSession creates a new session (source defaults to "ssh").
func NewSession(userID, machineID, remoteUser, storagePath string) *Session {
	return NewSessionWithSource(userID, machineID, remoteUser, storagePath, "ssh")
}

// NewSessionWithSource creates a session tagged with a source (ssh|web).
func NewSessionWithSource(userID, machineID, remoteUser, storagePath, source string) *Session {
	sessionID := uuid.New().String()
	recordingPath := filepath.Join(storagePath, fmt.Sprintf("%s.cast", sessionID))
	if source == "" {
		source = "ssh"
	}
	return &Session{
		ID:            sessionID,
		UserID:        userID,
		MachineID:     machineID,
		RemoteUser:    remoteUser,
		Source:        source,
		StartTime:     time.Now(),
		RecordingPath: recordingPath,
		Status:        "active",
		CreatedAt:     time.Now(),
	}
}

// NewAccessRequest creates a new access request
func NewAccessRequest(userID, machineID string, remoteUsers []string, reason string, duration int) *AccessRequest {
	return &AccessRequest{
		ID:          uuid.New().String(),
		UserID:      userID,
		MachineID:   machineID,
		RemoteUsers: remoteUsers,
		AccessType:  "both",
		Reason:      reason,
		Duration:    duration,
		Status:      "pending",
		RequestedAt: time.Now(),
	}
}

// NewPermission creates a new permission
func NewPermission(userID, machineID, accessType string, remoteUsers []string, grantedBy string, expiresAt *time.Time) *Permission {
	return &Permission{
		ID:          uuid.New().String(),
		UserID:      userID,
		MachineID:   machineID,
		AccessType:  accessType,
		RemoteUsers: remoteUsers,
		GrantedBy:   grantedBy,
		GrantedAt:   time.Now(),
		ExpiresAt:   expiresAt,
	}
}

// NewAuditLog creates a new audit log entry
func NewAuditLog(userID, action, resource, ipAddress string, metadata map[string]interface{}) *AuditLog {
	return &AuditLog{
		ID:        uuid.New().String(),
		UserID:    userID,
		Action:    action,
		Resource:  resource,
		Metadata:  metadata,
		IPAddress: ipAddress,
		Timestamp: time.Now(),
	}
}

// NewAPIKey creates a new API key
func NewAPIKey(userID, name, keyHash, keyPrefix string, expiresAt *time.Time) *APIKey {
	return &APIKey{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      name,
		KeyHash:   keyHash,
		KeyPrefix: keyPrefix,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
}

// PluginSetting persists the enabled/config state for a registered plugin,
// letting the UI toggle and reconfigure plugins without touching server.yaml
// or restarting the process.
type PluginSetting struct {
	Name      string                 `json:"name"`
	Enabled   bool                   `json:"enabled"`
	Config    map[string]interface{} `json:"config"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// NewHTTPSession creates a new HTTP session
func NewHTTPSession(userID, token, ipAddress, userAgent string, expiresAt time.Time) *HTTPSession {
	now := time.Now()
	return &HTTPSession{
		ID:         uuid.New().String(),
		UserID:     userID,
		Token:      token,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		ExpiresAt:  expiresAt,
		CreatedAt:  now,
		LastSeenAt: now,
	}
}
