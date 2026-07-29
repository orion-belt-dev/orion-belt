package database

import (
	"context"
	"time"

	"github.com/zrougamed/orion-belt/pkg/common"
)

// Store defines the interface for database operations
type Store interface {
	// User operations
	CreateUser(ctx context.Context, user *common.User) error
	GetUser(ctx context.Context, id string) (*common.User, error)
	GetUserByUsername(ctx context.Context, username string) (*common.User, error)
	UpdateUser(ctx context.Context, user *common.User) error
	DeleteUser(ctx context.Context, id string) error
	ListUsers(ctx context.Context, limit, offset int) ([]*common.User, error)

	// Machine operations
	CreateMachine(ctx context.Context, machine *common.Machine) error
	GetMachine(ctx context.Context, id string) (*common.Machine, error)
	GetMachineByName(ctx context.Context, name string) (*common.Machine, error)
	UpdateMachine(ctx context.Context, machine *common.Machine) error
	DeleteMachine(ctx context.Context, id string) error
	ListMachines(ctx context.Context, limit, offset int) ([]*common.Machine, error)
	ListActiveMachines(ctx context.Context) ([]*common.Machine, error)

	// Session operations
	CreateSession(ctx context.Context, session *common.Session) error
	GetSession(ctx context.Context, id string) (*common.Session, error)
	UpdateSession(ctx context.Context, session *common.Session) error
	ListSessions(ctx context.Context, limit, offset int) ([]*common.Session, error)
	ListActiveSessions(ctx context.Context) ([]*common.Session, error)
	ListUserSessions(ctx context.Context, userID string, limit, offset int) ([]*common.Session, error)
	EndSession(ctx context.Context, id string, endTime time.Time) error

	// Access request operations
	CreateAccessRequest(ctx context.Context, request *common.AccessRequest) error
	GetAccessRequest(ctx context.Context, id string) (*common.AccessRequest, error)
	UpdateAccessRequest(ctx context.Context, request *common.AccessRequest) error
	ListPendingAccessRequests(ctx context.Context) ([]*common.AccessRequest, error)
	ListUserAccessRequests(ctx context.Context, userID string, limit, offset int) ([]*common.AccessRequest, error)
	ListAllAccessRequests(ctx context.Context, limit, offset int) ([]*common.AccessRequest, error)

	// Permission operations
	CreatePermission(ctx context.Context, permission *common.Permission) error
	GetPermission(ctx context.Context, id string) (*common.Permission, error)
	UpdatePermission(ctx context.Context, permission *common.Permission) error
	DeletePermission(ctx context.Context, id string) error
	ListUserPermissions(ctx context.Context, userID string) ([]*common.Permission, error)
	ListMachinePermissions(ctx context.Context, machineID string) ([]*common.Permission, error)
	ListAllPermissions(ctx context.Context, limit, offset int) ([]*common.Permission, error)
	FindActivePermission(ctx context.Context, userID, machineID, accessType string) (*common.Permission, error)
	HasPermission(ctx context.Context, userID, machineID, accessType string) (bool, error)
	HasPermissionWithRemoteUser(ctx context.Context, userID, machineID, accessType, remoteUser string) (bool, error)

	// Audit log operations
	CreateAuditLog(ctx context.Context, log *common.AuditLog) error
	ListAuditLogs(ctx context.Context, limit, offset int, filters map[string]interface{}) ([]*common.AuditLog, error)

	// Notification operations (in-app / web notifications)
	CreateNotification(ctx context.Context, n *common.Notification) error
	ListUserNotifications(ctx context.Context, userID string, limit, offset int) ([]*common.Notification, error)
	CountUnreadNotifications(ctx context.Context, userID string) (int, error)
	MarkNotificationRead(ctx context.Context, id, userID string) error
	MarkAllNotificationsRead(ctx context.Context, userID string) error
	GetNotificationPrefs(ctx context.Context, userID string) (*common.NotificationPrefs, error)
	UpsertNotificationPrefs(ctx context.Context, prefs *common.NotificationPrefs) error

	// Admin-set boundary that user preferences are resolved within (singleton).
	GetNotificationPolicy(ctx context.Context) (*common.NotificationPolicy, error)
	UpsertNotificationPolicy(ctx context.Context, policy *common.NotificationPolicy) error

	// Access request hygiene
	ExpireStalePendingAccessRequests(ctx context.Context, olderThan time.Duration) (int, error)

	// API Key operations
	CreateAPIKey(ctx context.Context, key *common.APIKey) error
	GetAPIKey(ctx context.Context, id string) (*common.APIKey, error)
	GetAPIKeyByHash(ctx context.Context, keyHash string) (*common.APIKey, error)
	ListUserAPIKeys(ctx context.Context, userID string) ([]*common.APIKey, error)
	UpdateAPIKeyLastUsed(ctx context.Context, id string, lastUsedAt time.Time) error
	RevokeAPIKey(ctx context.Context, id string) error
	DeleteAPIKey(ctx context.Context, id string) error

	// HTTP Session operations
	CreateHTTPSession(ctx context.Context, session *common.HTTPSession) error
	GetHTTPSession(ctx context.Context, id string) (*common.HTTPSession, error)
	GetHTTPSessionByToken(ctx context.Context, tokenHash string) (*common.HTTPSession, error)
	UpdateHTTPSessionLastSeen(ctx context.Context, id string, lastSeenAt time.Time) error
	DeleteHTTPSession(ctx context.Context, id string) error
	DeleteExpiredHTTPSessions(ctx context.Context) error

	// SSH keys (including FIDO/sk-*)
	CreateSSHKey(ctx context.Context, key *common.SSHKey) error
	ListUserSSHKeys(ctx context.Context, userID string) ([]*common.SSHKey, error)
	DeleteSSHKey(ctx context.Context, id string) error

	// WebAuthn / FIDO2
	CreateWebAuthnCredential(ctx context.Context, cred *common.WebAuthnCredential) error
	ListWebAuthnCredentials(ctx context.Context, userID string) ([]*common.WebAuthnCredential, error)
	GetWebAuthnCredentialByCredID(ctx context.Context, credID []byte) (*common.WebAuthnCredential, error)
	UpdateWebAuthnCredential(ctx context.Context, cred *common.WebAuthnCredential) error
	DeleteWebAuthnCredential(ctx context.Context, id string) error

	// Plugin settings (enabled state + dynamic config, editable from the UI)
	GetPluginSetting(ctx context.Context, name string) (*common.PluginSetting, error)
	ListPluginSettings(ctx context.Context) ([]*common.PluginSetting, error)
	UpsertPluginSetting(ctx context.Context, setting *common.PluginSetting) error

	// SSH Certificate Authority
	CreateCAKey(ctx context.Context, key *common.CAKey, privateKeyEncrypted []byte) error
	GetActiveCAKey(ctx context.Context, caType string) (*common.CAKey, []byte, error)
	ListCAKeys(ctx context.Context, caType string) ([]*common.CAKey, error)

	CreateSSHCertificate(ctx context.Context, cert *common.SSHCertificate) error
	GetSSHCertificateBySerial(ctx context.Context, serial string) (*common.SSHCertificate, error)
	ListSSHCertificates(ctx context.Context, filter common.SSHCertFilter, limit, offset int) ([]*common.SSHCertificate, error)
	ListRevokedCertSerials(ctx context.Context) ([]string, error)
	RevokeSSHCertificate(ctx context.Context, serial, revokedBy, reason string) error

	// Lifecycle
	Connect(ctx context.Context) error
	Close() error
	Ping(ctx context.Context) error
	Migrate(ctx context.Context) error
}

// Factory is a function that creates a new Store instance
type Factory func(connectionString string) (Store, error)

var factories = make(map[string]Factory)

// Register registers a database factory
func Register(driver string, factory Factory) {
	factories[driver] = factory
}

// NewStore creates a new Store instance based on the driver
func NewStore(driver, connectionString string) (Store, error) {
	factory, ok := factories[driver]
	if !ok {
		return nil, ErrUnsupportedDriver
	}
	return factory(connectionString)
}
