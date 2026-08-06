package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"golang.org/x/crypto/ssh"
)

// AuthService handles authentication and authorization
type AuthService struct {
	store      database.Store
	logger     *common.Logger
	authorizer interface {
		Check(ctx context.Context, userID, machineID, accessType string) (bool, error)
		WriteGrant(ctx context.Context, userID, machineID, accessType string) error
		DeleteGrant(ctx context.Context, userID, machineID, accessType string) error
	}
}

// privilegedAccess matches admin middleware: admins and operators bypass ReBAC for
// terminal/files. Role string is authoritative when is_admin was never set.
func privilegedAccess(user *common.User) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	switch user.EffectiveRole() {
	case common.RoleAdmin, common.RoleOperator:
		return true
	default:
		return false
	}
}

// NewAuthService creates a new authentication service
func NewAuthService(store database.Store, logger *common.Logger) *AuthService {
	return &AuthService{
		store:  store,
		logger: logger,
	}
}

// SetAuthorizer sets an optional external authorizer (e.g. OpenFGA).
func (a *AuthService) SetAuthorizer(authz interface {
	Check(ctx context.Context, userID, machineID, accessType string) (bool, error)
	WriteGrant(ctx context.Context, userID, machineID, accessType string) error
	DeleteGrant(ctx context.Context, userID, machineID, accessType string) error
}) {
	a.authorizer = authz
}

// AuthenticateUser authenticates a user with public key (RSA/Ed25519/ECDSA/FIDO sk-*).
func (a *AuthService) AuthenticateUser(ctx context.Context, username string, publicKey ssh.PublicKey) (*common.User, error) {
	// OpenSSH agentless: username may be "alice+web-01" or "alice+root%web-01"
	authUser := username
	if i := strings.IndexByte(username, '+'); i > 0 {
		authUser = username[:i]
	}

	user, err := a.store.GetUserByUsername(ctx, authUser)
	if err != nil {
		a.logger.Warn("User not found: %s", authUser)
		return nil, fmt.Errorf("authentication failed")
	}

	if matchSSHKey(publicKey, user.PublicKey) {
		a.logger.Info("User authenticated: %s (primary key, type=%s)", authUser, publicKey.Type())
		return user, nil
	}

	keys, err := a.store.ListUserSSHKeys(ctx, user.ID)
	if err == nil {
		for _, k := range keys {
			if matchSSHKey(publicKey, k.PublicKey) {
				a.logger.Info("User authenticated: %s (key=%s type=%s)", authUser, k.Name, publicKey.Type())
				return user, nil
			}
		}
	}

	a.logger.Warn("Public key mismatch for user: %s (presented type=%s)", authUser, publicKey.Type())
	return nil, fmt.Errorf("authentication failed")
}

func matchSSHKey(presented ssh.PublicKey, authorizedLine string) bool {
	storedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedLine))
	if err != nil {
		return false
	}
	return keyEquals(presented, storedKey)
}

// CheckPermission checks if a user has permission to access a machine
func (a *AuthService) CheckPermission(ctx context.Context, userID, machineID, accessType string) error {
	// Check if user is admin
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if privilegedAccess(user) {
		a.logger.Debug("Privileged access granted for user %s role=%s", userID, user.EffectiveRole())
		return nil
	}

	if a.authorizer != nil {
		allowed, err := a.authorizer.Check(ctx, userID, machineID, accessType)
		if err != nil {
			a.logger.Warn("OpenFGA check failed, falling back to ReBAC: %v", err)
		} else if allowed {
			return nil
		} else {
			a.logger.Warn("Permission denied (OpenFGA) for user %s on machine %s", userID, machineID)
			return database.ErrPermissionDenied
		}
	}

	// Check ReBAC permissions
	hasPermission, err := a.store.HasPermission(ctx, userID, machineID, accessType)
	if err != nil {
		return fmt.Errorf("failed to check permission: %w", err)
	}

	if !hasPermission {
		a.logger.Warn("Permission denied for user %s on machine %s", userID, machineID)
		return database.ErrPermissionDenied
	}

	a.logger.Debug("Permission granted for user %s on machine %s", userID, machineID)
	return nil
}

// CheckPermissionWithRemoteUser checks if a user has permission with specific remote user
func (a *AuthService) CheckPermissionWithRemoteUser(ctx context.Context, userID, machineID, accessType, remoteUser string) error {
	// Check if user is admin
	user, err := a.store.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if privilegedAccess(user) {
		a.logger.Debug("Privileged access granted for user %s role=%s", userID, user.EffectiveRole())
		return nil
	}

	if a.authorizer != nil {
		allowed, err := a.authorizer.Check(ctx, userID, machineID, accessType)
		if err != nil {
			a.logger.Warn("OpenFGA check failed, falling back to ReBAC: %v", err)
		} else if allowed {
			// Still enforce remote_users via local ReBAC when available
			hasLocal, localErr := a.store.HasPermissionWithRemoteUser(ctx, userID, machineID, accessType, remoteUser)
			if localErr == nil && hasLocal {
				return nil
			}
			if localErr == nil && !hasLocal {
				// OpenFGA allowed machine access; remote user restriction from local store
				a.logger.Warn("Permission denied for remote user %s on machine %s", remoteUser, machineID)
				return fmt.Errorf("permission denied: %s not allowed to access %s", remoteUser, machineID)
			}
			return nil
		} else {
			return fmt.Errorf("permission denied: %s not allowed to access %s", remoteUser, machineID)
		}
	}

	// Check ReBAC permissions with remote user
	hasPermission, err := a.store.HasPermissionWithRemoteUser(ctx, userID, machineID, accessType, remoteUser)
	if err != nil {
		return fmt.Errorf("failed to check permission: %w", err)
	}

	if !hasPermission {
		a.logger.Warn("Permission denied for user %s on machine %s as remote user %s", userID, machineID, remoteUser)
		return fmt.Errorf("permission denied: %s not allowed to access %s", remoteUser, machineID)
	}

	a.logger.Debug("Permission granted for user %s on machine %s as remote user %s", userID, machineID, remoteUser)
	return nil
}

// GrantPermission grants a user permission to access a machine
func (a *AuthService) GrantPermission(ctx context.Context, userID, machineID, accessType string, remoteUsers []string, grantedBy string, duration *time.Duration) error {
	var expiresAt *time.Time
	if duration != nil {
		t := time.Now().Add(*duration)
		expiresAt = &t
	}

	// Upsert: replace an existing active grant for the same user/machine/type.
	if existing, err := a.store.FindActivePermission(ctx, userID, machineID, accessType); err == nil && existing != nil {
		existing.RemoteUsers = remoteUsers
		existing.ExpiresAt = expiresAt
		existing.GrantedBy = grantedBy
		existing.GrantedAt = time.Now()
		if err := a.store.UpdatePermission(ctx, existing); err != nil {
			return fmt.Errorf("failed to update permission: %w", err)
		}
		a.logger.Info("Permission updated: user=%s, machine=%s, type=%s, remote_users=%v",
			userID, machineID, accessType, remoteUsers)
		return nil
	}

	permission := common.NewPermission(userID, machineID, accessType, remoteUsers, grantedBy, expiresAt)

	if err := a.store.CreatePermission(ctx, permission); err != nil {
		return fmt.Errorf("failed to create permission: %w", err)
	}

	if a.authorizer != nil {
		if err := a.authorizer.WriteGrant(ctx, userID, machineID, accessType); err != nil {
			a.logger.Warn("Failed to write OpenFGA tuple: %v", err)
		}
	}

	a.logger.Info("Permission granted: user=%s, machine=%s, type=%s, remote_users=%v",
		userID, machineID, accessType, remoteUsers)
	return nil
}

// RevokePermission revokes a user's permission
func (a *AuthService) RevokePermission(ctx context.Context, permissionID string) error {
	perm, err := a.store.GetPermission(ctx, permissionID)
	if err != nil {
		return fmt.Errorf("failed to get permission: %w", err)
	}

	if err := a.store.DeletePermission(ctx, permissionID); err != nil {
		return fmt.Errorf("failed to revoke permission: %w", err)
	}

	if a.authorizer != nil {
		if err := a.authorizer.DeleteGrant(ctx, perm.UserID, perm.MachineID, perm.AccessType); err != nil {
			a.logger.Warn("Failed to delete OpenFGA tuple: %v", err)
		}
	}

	a.logger.Info("Permission revoked: %s", permissionID)
	return nil
}

// RequestTemporaryAccess creates a temporary access request
func (a *AuthService) RequestTemporaryAccess(ctx context.Context, userID, machineID string, remoteUsers []string, reason string, duration int) (*common.AccessRequest, error) {
	request := common.NewAccessRequest(userID, machineID, remoteUsers, reason, duration)

	if err := a.store.CreateAccessRequest(ctx, request); err != nil {
		return nil, fmt.Errorf("failed to create access request: %w", err)
	}

	a.logger.Info("Access request created: user=%s, machine=%s, remote_users=%v, duration=%d", userID, machineID, remoteUsers, duration)
	return request, nil
}

// ApproveAccessRequest approves a temporary access request using the TTL
// (in seconds) the request was created with. Use ApproveAccessRequestWithTTL
// to let the approver override that TTL.
func (a *AuthService) ApproveAccessRequest(ctx context.Context, requestID, reviewerID string) error {
	return a.ApproveAccessRequestWithTTL(ctx, requestID, reviewerID, nil)
}

// ApproveAccessRequestWithTTL approves a temporary access request. If
// durationOverride is non-nil, it replaces the requester's originally
// requested duration (in seconds). A duration of 0 means unlimited (no
// expiry), matching the JIT convention: default 30 minutes, 0 = unlimited.
func (a *AuthService) ApproveAccessRequestWithTTL(ctx context.Context, requestID, reviewerID string, durationOverride *int) error {
	request, err := a.store.GetAccessRequest(ctx, requestID)
	if err != nil {
		return fmt.Errorf("failed to get access request: %w", err)
	}

	if request.Status != "pending" {
		return fmt.Errorf("request is not pending")
	}

	durationSeconds := request.Duration
	if durationOverride != nil {
		durationSeconds = *durationOverride
	}

	// Update request status
	now := time.Now()
	var expiresAt *time.Time
	var permDuration *time.Duration
	if durationSeconds > 0 {
		d := time.Duration(durationSeconds) * time.Second
		t := now.Add(d)
		expiresAt = &t
		permDuration = &d
	}
	request.Duration = durationSeconds
	request.Status = "approved"
	request.ReviewedAt = &now
	request.ReviewedBy = &reviewerID
	request.ExpiresAt = expiresAt

	if err := a.store.UpdateAccessRequest(ctx, request); err != nil {
		return fmt.Errorf("failed to update access request: %w", err)
	}

	// Grant temporary permission with the requested remote users. A nil
	// permDuration grants unlimited (no expiry) access.
	accessType := request.AccessType
	if accessType == "" {
		accessType = "both"
	}
	if err := a.GrantPermission(ctx, request.UserID, request.MachineID, accessType, request.RemoteUsers, reviewerID, permDuration); err != nil {
		return fmt.Errorf("failed to grant permission: %w", err)
	}

	a.logger.Info("Access request approved: %s by %s (ttl=%ds)", requestID, reviewerID, durationSeconds)
	return nil
}

// RejectAccessRequest rejects a temporary access request
func (a *AuthService) RejectAccessRequest(ctx context.Context, requestID, reviewerID string) error {
	request, err := a.store.GetAccessRequest(ctx, requestID)
	if err != nil {
		return fmt.Errorf("failed to get access request: %w", err)
	}

	if request.Status != "pending" {
		return fmt.Errorf("request is not pending")
	}

	now := time.Now()
	request.Status = "rejected"
	request.ReviewedAt = &now
	request.ReviewedBy = &reviewerID

	if err := a.store.UpdateAccessRequest(ctx, request); err != nil {
		return fmt.Errorf("failed to update access request: %w", err)
	}

	a.logger.Info("Access request rejected: %s by %s", requestID, reviewerID)
	return nil
}

// GetPendingRequests returns all pending access requests
func (a *AuthService) GetPendingRequests(ctx context.Context) ([]*common.AccessRequest, error) {
	return a.store.ListPendingAccessRequests(ctx)
}

// GenerateSSHKeyPair generates a new SSH key pair (Ed25519 by default).
func GenerateSSHKeyPair() (privateKey, publicKey string, err error) {
	return GenerateSSHKeyPairAlgo("ed25519")
}

// GenerateSSHKeyPairAlgo generates an SSH key pair for algo: ed25519 (default) or rsa.
func GenerateSSHKeyPairAlgo(algo string) (privateKey, publicKey string, err error) {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "", "ed25519":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate ed25519 key: %w", err)
		}
		privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return "", "", fmt.Errorf("failed to marshal private key: %w", err)
		}
		privateKeyBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate SSH public key: %w", err)
		}
		return string(privateKeyBytes), string(ssh.MarshalAuthorizedKey(sshPub)), nil
	case "rsa":
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate rsa key pair: %w", err)
		}
		privateKeyBytes := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})
		sshPub, err := ssh.NewPublicKey(&key.PublicKey)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate SSH public key: %w", err)
		}
		return string(privateKeyBytes), string(ssh.MarshalAuthorizedKey(sshPub)), nil
	default:
		return "", "", fmt.Errorf("unsupported key algorithm %q (use ed25519 or rsa)", algo)
	}
}

// keyEquals compares two SSH public keys
func keyEquals(a, b ssh.PublicKey) bool {
	return string(a.Marshal()) == string(b.Marshal())
}

// GenerateAPIKey creates a new API key for a user
func (a *AuthService) GenerateAPIKey(ctx context.Context, userID, name string, expiresAt *time.Time) (*common.APIKey, string, error) {
	// Generate a cryptographically secure random key
	rawKey, err := generateSecureToken(32)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate API key: %w", err)
	}

	// Hash the key for storage
	keyHash := hashKey(rawKey)

	// Store only the prefix for display (first 8 chars)
	keyPrefix := rawKey[:8]

	// Create the API key record
	apiKey := common.NewAPIKey(userID, name, keyHash, keyPrefix, expiresAt)

	// Save to database
	if err := a.store.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, "", fmt.Errorf("failed to create API key: %w", err)
	}

	a.logger.Info("API key created: user=%s name=%s prefix=%s", userID, name, keyPrefix)

	// Return the API key record and the raw key
	return apiKey, rawKey, nil
}

// RevokeAPIKey revokes an API key
func (a *AuthService) RevokeAPIKey(ctx context.Context, keyID string) error {
	if err := a.store.RevokeAPIKey(ctx, keyID); err != nil {
		return fmt.Errorf("failed to revoke API key: %w", err)
	}

	a.logger.Info("API key revoked: %s", keyID)
	return nil
}

// ListUserAPIKeys returns all API keys for a user
func (a *AuthService) ListUserAPIKeys(ctx context.Context, userID string) ([]*common.APIKey, error) {
	keys, err := a.store.ListUserAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list API keys: %w", err)
	}
	return keys, nil
}

// CreateSession creates a new HTTP session for a user
func (a *AuthService) CreateSession(ctx context.Context, userID, ipAddress, userAgent string, duration time.Duration) (*common.HTTPSession, string, error) {
	// Generate a cryptographically secure session token
	rawToken, err := generateSecureToken(32)
	if err != nil {
		return nil, "", fmt.Errorf("failed to generate session token: %w", err)
	}

	// Hash the token for storage
	tokenHash := hashKey(rawToken)

	// Calculate expiration
	expiresAt := time.Now().Add(duration)

	// Create the session record
	session := common.NewHTTPSession(userID, tokenHash, ipAddress, userAgent, expiresAt)

	// Save to database
	if err := a.store.CreateHTTPSession(ctx, session); err != nil {
		return nil, "", fmt.Errorf("failed to create session: %w", err)
	}

	a.logger.Info("HTTP session created: user=%s ip=%s duration=%v", userID, ipAddress, duration)

	return session, rawToken, nil
}

// DestroySession destroys an HTTP session
func (a *AuthService) DestroySession(ctx context.Context, sessionID string) error {
	if err := a.store.DeleteHTTPSession(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to destroy session: %w", err)
	}

	a.logger.Info("HTTP session destroyed: %s", sessionID)
	return nil
}

// CleanupExpiredSessions removes all expired HTTP sessions
func (a *AuthService) CleanupExpiredSessions(ctx context.Context) error {
	if err := a.store.DeleteExpiredHTTPSessions(ctx); err != nil {
		return fmt.Errorf("failed to cleanup expired sessions: %w", err)
	}
	return nil
}

// generateSecureToken generates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Use base64 URL encoding (safe for URLs and headers)
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// hashKey creates a SHA256 hash of a key/token
func hashKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// ValidateAPIKeyFormat checks if an API key has the correct format
func ValidateAPIKeyFormat(key string) bool {
	// Decode from base64
	decoded, err := base64.URLEncoding.DecodeString(key)
	if err != nil {
		return false
	}
	// Should be 32 bytes
	return len(decoded) == 32
}
