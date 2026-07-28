package sdk

import (
	"time"

	"github.com/zrougamed/orion-belt/pkg/common"
)

// User is a platform user account.
type User = common.User

// Machine is a target machine visible via the API.
type Machine = common.Machine

// AccessRequest is a temporary access request record.
type AccessRequest = common.AccessRequest

// Session is an SSH/web terminal session.
type Session = common.Session

// AuditLog is an audit log entry.
type AuditLog = common.AuditLog

// Notification is an in-app notification.
type Notification = common.Notification

// NotificationPrefs controls per-user notification channel behavior.
type NotificationPrefs = common.NotificationPrefs

// APIKey is a persisted API key record (masked form).
type APIKey = common.APIKey

// Permission is a machine access permission record.
type Permission = common.Permission

// SSHKey is a user SSH key record.
type SSHKey = common.SSHKey

// SSHCertificate is a lifecycle record for an issued SSH certificate.
type SSHCertificate = common.SSHCertificate

// CreateAccessRequestRequest is the payload for creating a JIT access request.
type CreateAccessRequestRequest struct {
	MachineID   string   `json:"machine_id"`
	Reason      string   `json:"reason"`
	Duration    int      `json:"duration"`
	AccessType  string   `json:"access_type,omitempty"`
	RemoteUsers []string `json:"remote_users,omitempty"`
}

// TrustedCA is the SSH CA public-key material for client trust setup.
type TrustedCA struct {
	Enabled bool   `json:"enabled"`
	UserCA  string `json:"user_ca"`
	HostCA  string `json:"host_ca"`
}

// IssuedCert is the response from requesting a short-lived SSH user cert.
type IssuedCert struct {
	Certificate string    `json:"certificate"`
	Serial      string    `json:"serial"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// BootstrapCode is a short-lived browser bootstrap code.
type BootstrapCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoginUser is the nested user payload included in login responses.
type LoginUser struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	IsAdmin         bool   `json:"is_admin"`
	Role            string `json:"role,omitempty"`
	MFAEnabled      bool   `json:"mfa_enabled,omitempty"`
	PasswordSet     bool   `json:"password_set,omitempty"`
	MustSetPassword bool   `json:"must_set_password,omitempty"`
}

// PasswordLoginResult is the response from password login.
type PasswordLoginResult struct {
	SessionToken string    `json:"session_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	User         LoginUser `json:"user"`
}

// APIKeyLoginResult is the response from SSH key challenge login.
type APIKeyLoginResult struct {
	APIKey    string    `json:"api_key"`
	ExpiresAt time.Time `json:"expires_at"`
	User      LoginUser `json:"user"`
}

// JWTLoginResult is the response from JWT challenge login.
type JWTLoginResult struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
	User        LoginUser `json:"user"`
}

// AuthUser describes the current authenticated user payload from /auth/me.
type AuthUser struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	Email           string    `json:"email"`
	PublicKey       string    `json:"public_key"`
	IsAdmin         bool      `json:"is_admin"`
	Role            string    `json:"role"`
	MFAEnabled      bool      `json:"mfa_enabled"`
	WebAuthnEnabled bool      `json:"webauthn_enabled"`
	PasswordSet     bool      `json:"password_set"`
	MustSetPassword bool      `json:"must_set_password"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateAPIKeyRequest creates a user-owned API key.
type CreateAPIKeyRequest struct {
	Name      string `json:"name"`
	ExpiresIn *int   `json:"expires_in,omitempty"`
}

// CreateAPIKeyResponse includes the one-time raw API key material.
type CreateAPIKeyResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	APIKey    string     `json:"api_key"`
	KeyPrefix string     `json:"key_prefix"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// SetupStatus is the first-run operator checklist response.
type SetupStatus struct {
	Complete bool `json:"complete"`
	Steps    struct {
		AdminExists        bool `json:"admin_exists"`
		HasMachines        bool `json:"has_machines"`
		HasConnectedAgents bool `json:"has_connected_agents"`
		HasUsers           bool `json:"has_users"`
		HasPermissions     bool `json:"has_permissions"`
	} `json:"steps"`
	Counts struct {
		Admins          int `json:"admins"`
		Users           int `json:"users"`
		Machines        int `json:"machines"`
		ConnectedAgents int `json:"connected_agents"`
		Permissions     int `json:"permissions"`
	} `json:"counts"`
	Next string `json:"next"`
}

// UsageDashboard is the operator usage analytics response.
type UsageDashboard struct {
	WindowHours  int       `json:"window_hours"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	GeneratedAt  time.Time `json:"generated_at"`
	AccessVolume struct {
		SessionsTotal    int `json:"sessions_total"`
		SessionsActive   int `json:"sessions_active"`
		RequestsTotal    int `json:"requests_total"`
		RequestsPending  int `json:"requests_pending"`
		RequestsApproved int `json:"requests_approved"`
		RequestsRejected int `json:"requests_rejected"`
	} `json:"access_volume"`
	ApprovalLatency struct {
		SampleSize     int     `json:"sample_size"`
		AverageSeconds float64 `json:"average_seconds"`
		P50Seconds     float64 `json:"p50_seconds"`
		P95Seconds     float64 `json:"p95_seconds"`
	} `json:"approval_latency"`
	TopTargets []struct {
		MachineID    string `json:"machine_id"`
		MachineName  string `json:"machine_name"`
		SessionCount int    `json:"session_count"`
	} `json:"top_targets"`
}

// PluginConfigField describes a plugin config schema field.
type PluginConfigField struct {
	Key         string              `json:"key"`
	Label       string              `json:"label"`
	Type        string              `json:"type"`
	Secret      bool                `json:"secret,omitempty"`
	Required    bool                `json:"required,omitempty"`
	Placeholder string              `json:"placeholder,omitempty"`
	Help        string              `json:"help,omitempty"`
	Fields      []PluginConfigField `json:"fields,omitempty"`
}

// PluginInfo is the plugin status payload returned by admin plugin endpoints.
type PluginInfo struct {
	Name       string                 `json:"name"`
	Version    string                 `json:"version"`
	Enabled    bool                   `json:"enabled"`
	Configured bool                   `json:"configured"`
	LastError  string                 `json:"last_error,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	HasWebhook bool                   `json:"has_webhook"`
	Schema     []PluginConfigField    `json:"schema,omitempty"`
}

// AgentInstallScriptRequest requests a one-shot agent install script.
type AgentInstallScriptRequest struct {
	Name           string            `json:"name"`
	Hostname       string            `json:"hostname,omitempty"`
	Port           int               `json:"port,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	OS             string            `json:"os"`
	GatewayHost    string            `json:"gateway_host"`
	GatewayPort    int               `json:"gateway_port,omitempty"`
	PackageBaseURL string            `json:"package_base_url"`
	Version        string            `json:"version,omitempty"`
}

// RegisterAgentRequest is the public payload for direct agent registration.
type RegisterAgentRequest struct {
	Name      string            `json:"name"`
	Hostname  string            `json:"hostname"`
	Port      int               `json:"port"`
	PublicKey string            `json:"public_key"`
	Tags      map[string]string `json:"tags,omitempty"`
}

// RegisterAgentResponse is returned after direct agent registration.
type RegisterAgentResponse struct {
	UserID          string `json:"user_id,omitempty"`
	MachineID       string `json:"machine_id"`
	Message         string `json:"message"`
	HostCertificate string `json:"host_certificate,omitempty"`
	HostCAPublicKey string `json:"host_ca_public_key,omitempty"`
}

// RegisterClientRequest is the public payload for user registration.
type RegisterClientRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	PublicKey string `json:"public_key"`
	IsAdmin   bool   `json:"is_admin,omitempty"`
}

// RegisterClientResponse is returned after client registration.
type RegisterClientResponse struct {
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

// AgentInstallScriptResponse returns generated install script content.
type AgentInstallScriptResponse struct {
	Script    string `json:"script"`
	MachineID string `json:"machine_id"`
	UserID    string `json:"user_id"`
	AgentName string `json:"agent_name"`
	PublicKey string `json:"public_key"`
	Filename  string `json:"filename"`
	Message   string `json:"message"`
}

// MFAEnrollResponse returns the enrollment challenge material.
type MFAEnrollResponse struct {
	Secret      string   `json:"secret"`
	OTPAuthURL  string   `json:"otpauth_url"`
	BackupCodes []string `json:"backup_codes"`
	Message     string   `json:"message"`
}

// MFAStatus returns MFA state for the current user.
type MFAStatus struct {
	MFAEnabled  bool `json:"mfa_enabled"`
	MFARequired bool `json:"mfa_required"`
}

// WebAuthnCredentialInfo is the credential list payload shown in UI.
type WebAuthnCredentialInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CredID    string    `json:"cred_id"`
}

// CreateUserRequest is the admin payload for creating users.
type CreateUserRequest struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	PublicKey string `json:"public_key,omitempty"`
	Role      string `json:"role,omitempty"`
	IsAdmin   bool   `json:"is_admin,omitempty"`
}

// UpdateUserRequest is the admin payload for updating users.
type UpdateUserRequest struct {
	Email     *string `json:"email,omitempty"`
	PublicKey *string `json:"public_key,omitempty"`
	IsAdmin   *bool   `json:"is_admin,omitempty"`
	Role      *string `json:"role,omitempty"`
}

// CreateMachineRequest is the admin payload for creating machines.
type CreateMachineRequest struct {
	Name     string            `json:"name"`
	Hostname string            `json:"hostname"`
	Port     int               `json:"port,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	AgentID  string            `json:"agent_id,omitempty"`
	IsActive *bool             `json:"is_active,omitempty"`
}

// UpdateMachineRequest is the admin payload for updating machines.
type UpdateMachineRequest struct {
	Name     *string            `json:"name,omitempty"`
	Hostname *string            `json:"hostname,omitempty"`
	Port     *int               `json:"port,omitempty"`
	Tags     *map[string]string `json:"tags,omitempty"`
	AgentID  *string            `json:"agent_id,omitempty"`
	IsActive *bool              `json:"is_active,omitempty"`
}

// GrantPermissionRequest is the admin payload for new permissions.
type GrantPermissionRequest struct {
	UserID      string   `json:"user_id"`
	MachineID   string   `json:"machine_id"`
	AccessType  string   `json:"access_type"`
	RemoteUsers []string `json:"remote_users,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	DurationSec *int     `json:"duration_seconds,omitempty"`
}

// UpdatePermissionRequest is the admin payload for updating permissions.
type UpdatePermissionRequest struct {
	AccessType  *string  `json:"access_type,omitempty"`
	RemoteUsers []string `json:"remote_users,omitempty"`
	ExpiresAt   *string  `json:"expires_at,omitempty"`
	DurationSec *int     `json:"duration_seconds,omitempty"`
}

// FileEntry describes one path entry in file-browser listings.
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	MTime   int64  `json:"mtime"`
	IsError bool   `json:"-"`
}

// FileListResponse is returned from /files/list.
type FileListResponse struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries,omitempty"`
	Raw     string      `json:"raw,omitempty"`
}

// FileUploadResponse is returned from /files/upload.
type FileUploadResponse struct {
	Message string `json:"message"`
	Path    string `json:"path"`
	Size    int    `json:"size"`
}
