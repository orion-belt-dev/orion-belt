package sdk

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// BulkGrantRequest grants one access type to every user × machine pair.
type BulkGrantRequest struct {
	UserIDs     []string `json:"user_ids"`
	MachineIDs  []string `json:"machine_ids"`
	AccessType  string   `json:"access_type"`
	RemoteUsers []string `json:"remote_users,omitempty"`
	ExpiresAt   string   `json:"expires_at,omitempty"`
	DurationSec *int     `json:"duration_seconds,omitempty"`
}

// BulkFailure is one pair or grant a bulk request could not apply.
type BulkFailure struct {
	UserID       string `json:"user_id,omitempty"`
	MachineID    string `json:"machine_id,omitempty"`
	PermissionID string `json:"permission_id,omitempty"`
	Error        string `json:"error"`
}

// BulkResult reports a bulk grant or revoke.
type BulkResult struct {
	Granted int           `json:"granted,omitempty"`
	Revoked int           `json:"revoked,omitempty"`
	Failed  []BulkFailure `json:"failed"`
}

// EffectiveAccess is one thing a user can actually do on a machine once roles
// and grants are resolved. AccessType "*" (from a role) means every type.
type EffectiveAccess struct {
	UserID      string     `json:"user_id"`
	Username    string     `json:"username"`
	MachineID   string     `json:"machine_id"`
	MachineName string     `json:"machine_name"`
	AccessType  string     `json:"access_type"`
	RemoteUsers []string   `json:"remote_users"`
	Source      string     `json:"source"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// BulkGrantPermissions grants access to many users on many machines (admin
// endpoint).
func (c *Client) BulkGrantPermissions(ctx context.Context, req BulkGrantRequest) (*BulkResult, error) {
	var out BulkResult
	if err := c.Do(ctx, http.MethodPost, "/admin/permissions/bulk", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BulkRevokePermissions revokes several grants (admin endpoint).
func (c *Client) BulkRevokePermissions(ctx context.Context, ids []string) (*BulkResult, error) {
	var out BulkResult
	if err := c.Do(ctx, http.MethodPost, "/admin/permissions/bulk-revoke", map[string][]string{"ids": ids}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EffectivePermissions resolves access for a user (across machines) or a
// machine (across users); set exactly one (admin endpoint).
func (c *Client) EffectivePermissions(ctx context.Context, userID, machineID string) ([]EffectiveAccess, error) {
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	if machineID != "" {
		q.Set("machine_id", machineID)
	}
	var out []EffectiveAccess
	if err := c.Do(ctx, http.MethodGet, "/admin/permissions/effective?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []EffectiveAccess{}
	}
	return out, nil
}
