package sdk

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// GetMachine fetches a machine by ID.
func (c *Client) GetMachine(ctx context.Context, machineID string) (*Machine, error) {
	var out Machine
	if err := c.Do(ctx, http.MethodGet, "/machines/"+machineID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUserPermissions returns permissions scoped to a user.
func (c *Client) GetUserPermissions(ctx context.Context, userID string) ([]Permission, error) {
	var out []Permission
	if err := c.Do(ctx, http.MethodGet, "/permissions/user/"+userID, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Permission{}
	}
	return out, nil
}

// GetMachinePermissions returns permissions scoped to a machine.
func (c *Client) GetMachinePermissions(ctx context.Context, machineID string) ([]Permission, error) {
	var out []Permission
	if err := c.Do(ctx, http.MethodGet, "/permissions/machine/"+machineID, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Permission{}
	}
	return out, nil
}

// CreateUser creates a user (admin endpoint).
func (c *Client) CreateUser(ctx context.Context, req CreateUserRequest) (*User, error) {
	var out User
	if err := c.Do(ctx, http.MethodPost, "/admin/users", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateUser updates user fields (admin endpoint).
func (c *Client) UpdateUser(ctx context.Context, userID string, req UpdateUserRequest) (*User, error) {
	var out User
	if err := c.Do(ctx, http.MethodPut, "/admin/users/"+userID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUser deletes a user (admin endpoint).
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	return c.Do(ctx, http.MethodDelete, "/admin/users/"+userID, nil, nil)
}

// CreateMachine creates a machine (admin endpoint).
func (c *Client) CreateMachine(ctx context.Context, req CreateMachineRequest) (*Machine, error) {
	var out Machine
	if err := c.Do(ctx, http.MethodPost, "/admin/machines", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMachine updates a machine (admin endpoint).
func (c *Client) UpdateMachine(ctx context.Context, machineID string, req UpdateMachineRequest) (*Machine, error) {
	var out Machine
	if err := c.Do(ctx, http.MethodPut, "/admin/machines/"+machineID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMachine deletes or archives a machine (admin endpoint).
func (c *Client) DeleteMachine(ctx context.Context, machineID string, archive bool) error {
	path := "/admin/machines/" + machineID
	if archive {
		path += "?archive=true"
	}
	return c.Do(ctx, http.MethodDelete, path, nil, nil)
}

// ListAllPermissions lists permissions (admin endpoint).
func (c *Client) ListAllPermissions(ctx context.Context, limit int) ([]Permission, error) {
	path := "/admin/permissions"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []Permission
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Permission{}
	}
	return out, nil
}

// GrantPermission creates a permission (admin endpoint).
func (c *Client) GrantPermission(ctx context.Context, req GrantPermissionRequest) (*Permission, error) {
	var out Permission
	if err := c.Do(ctx, http.MethodPost, "/admin/permissions", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdatePermission updates a permission (admin endpoint).
func (c *Client) UpdatePermission(ctx context.Context, permissionID string, req UpdatePermissionRequest) (*Permission, error) {
	var out Permission
	if err := c.Do(ctx, http.MethodPatch, "/admin/permissions/"+permissionID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokePermission revokes a permission (admin endpoint).
func (c *Client) RevokePermission(ctx context.Context, permissionID string) error {
	return c.Do(ctx, http.MethodDelete, "/admin/permissions/"+permissionID, nil, nil)
}

// ListPlugins lists plugin info records (admin endpoint).
func (c *Client) ListPlugins(ctx context.Context) ([]PluginInfo, error) {
	var out struct {
		Plugins []PluginInfo `json:"plugins"`
	}
	if err := c.Do(ctx, http.MethodGet, "/admin/plugins", nil, &out); err != nil {
		return nil, err
	}
	if out.Plugins == nil {
		out.Plugins = []PluginInfo{}
	}
	return out.Plugins, nil
}

// UpdatePluginConfig updates plugin config and enabled status.
func (c *Client) UpdatePluginConfig(ctx context.Context, name string, enabled bool, config map[string]interface{}) (*PluginInfo, string, error) {
	req := map[string]interface{}{
		"enabled": enabled,
		"config":  config,
	}
	var out struct {
		Plugin         PluginInfo `json:"plugin"`
		ConfigureError string     `json:"configure_error,omitempty"`
	}
	if err := c.Do(ctx, http.MethodPut, "/admin/plugins/"+url.PathEscape(name)+"/config", req, &out); err != nil {
		return nil, "", err
	}
	return &out.Plugin, out.ConfigureError, nil
}

// EnablePlugin enables a plugin.
func (c *Client) EnablePlugin(ctx context.Context, name string) (*PluginInfo, error) {
	var out struct {
		Plugin PluginInfo `json:"plugin"`
	}
	if err := c.Do(ctx, http.MethodPost, "/admin/plugins/"+url.PathEscape(name)+"/enable", nil, &out); err != nil {
		return nil, err
	}
	return &out.Plugin, nil
}

// DisablePlugin disables a plugin.
func (c *Client) DisablePlugin(ctx context.Context, name string) (*PluginInfo, error) {
	var out struct {
		Plugin PluginInfo `json:"plugin"`
	}
	if err := c.Do(ctx, http.MethodPost, "/admin/plugins/"+url.PathEscape(name)+"/disable", nil, &out); err != nil {
		return nil, err
	}
	return &out.Plugin, nil
}

// ListConnectedAgents returns connected machine IDs.
func (c *Client) ListConnectedAgents(ctx context.Context) ([]string, error) {
	var out struct {
		Agents []string `json:"agents"`
	}
	if err := c.Do(ctx, http.MethodGet, "/admin/agents/connected", nil, &out); err != nil {
		return nil, err
	}
	if out.Agents == nil {
		out.Agents = []string{}
	}
	return out.Agents, nil
}

// SendAgentCommand sends a control command to a connected agent.
func (c *Client) SendAgentCommand(ctx context.Context, machineID, command string) (string, error) {
	var out struct {
		Output string `json:"output"`
	}
	req := map[string]string{"command": command}
	if err := c.Do(ctx, http.MethodPost, "/admin/agents/"+machineID+"/command", req, &out); err != nil {
		return "", err
	}
	return out.Output, nil
}

// DisconnectAgent disconnects an active agent connection.
func (c *Client) DisconnectAgent(ctx context.Context, machineID string) error {
	return c.Do(ctx, http.MethodPost, "/admin/agents/"+machineID+"/disconnect", nil, nil)
}

// GenerateAgentInstallScript creates a one-shot agent install script.
func (c *Client) GenerateAgentInstallScript(ctx context.Context, req AgentInstallScriptRequest) (*AgentInstallScriptResponse, error) {
	var out AgentInstallScriptResponse
	if err := c.Do(ctx, http.MethodPost, "/admin/agents/install-script", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MFAEnroll begins TOTP enrollment.
func (c *Client) MFAEnroll(ctx context.Context) (*MFAEnrollResponse, error) {
	var out MFAEnrollResponse
	if err := c.Do(ctx, http.MethodPost, "/mfa/enroll", map[string]interface{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MFAConfirm confirms TOTP enrollment with a code.
func (c *Client) MFAConfirm(ctx context.Context, code string) error {
	req := map[string]string{"code": code}
	return c.Do(ctx, http.MethodPost, "/mfa/confirm", req, nil)
}

// MFADisable disables MFA using a valid code.
func (c *Client) MFADisable(ctx context.Context, code string) error {
	req := map[string]string{"code": code}
	return c.Do(ctx, http.MethodPost, "/mfa/disable", req, nil)
}

// MFAStatus returns MFA enabled/required state.
func (c *Client) MFAStatus(ctx context.Context) (*MFAStatus, error) {
	var out MFAStatus
	if err := c.Do(ctx, http.MethodGet, "/mfa/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetPassword sets or rotates password and confirms MFA at the same time.
func (c *Client) SetPassword(ctx context.Context, password, totpCode string) error {
	req := map[string]string{"password": password, "totp_code": totpCode}
	return c.Do(ctx, http.MethodPost, "/auth/password", req, nil)
}

// ClearPassword clears password login, requiring MFA code.
func (c *Client) ClearPassword(ctx context.Context, totpCode string) error {
	req := map[string]string{"totp_code": totpCode}
	return c.Do(ctx, http.MethodDelete, "/auth/password", req, nil)
}

// WebAuthnRegisterBegin starts a WebAuthn registration ceremony.
func (c *Client) WebAuthnRegisterBegin(ctx context.Context) (map[string]interface{}, error) {
	var out map[string]interface{}
	if err := c.Do(ctx, http.MethodPost, "/webauthn/register/begin", map[string]interface{}{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WebAuthnRegisterFinish completes registration with authenticator response.
func (c *Client) WebAuthnRegisterFinish(ctx context.Context, response map[string]interface{}) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Do(ctx, http.MethodPost, "/webauthn/register/finish", response, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// WebAuthnCredentials lists current user's registered WebAuthn credentials.
func (c *Client) WebAuthnCredentials(ctx context.Context) ([]WebAuthnCredentialInfo, error) {
	var out []WebAuthnCredentialInfo
	if err := c.Do(ctx, http.MethodGet, "/webauthn/credentials", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []WebAuthnCredentialInfo{}
	}
	return out, nil
}

// WebAuthnDeleteCredential deletes a credential by ID.
func (c *Client) WebAuthnDeleteCredential(ctx context.Context, credentialID string) error {
	return c.Do(ctx, http.MethodDelete, "/webauthn/credentials/"+credentialID, nil, nil)
}

// WebAuthnLoginBegin begins public WebAuthn login flow for a username.
func (c *Client) WebAuthnLoginBegin(ctx context.Context, username string) (map[string]interface{}, error) {
	var out map[string]interface{}
	req := map[string]string{"username": username}
	if err := c.DoPublic(ctx, http.MethodPost, "/public/webauthn/login/begin", req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WebAuthnLoginFinish completes public WebAuthn login flow.
func (c *Client) WebAuthnLoginFinish(ctx context.Context, username string, response map[string]interface{}) (*PasswordLoginResult, error) {
	req := map[string]interface{}{
		"username": username,
		"response": response,
	}
	var out PasswordLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/webauthn/login/finish", req, &out); err != nil {
		return nil, err
	}
	if out.SessionToken != "" {
		c.sessionToken = out.SessionToken
	}
	if out.AccessToken != "" {
		c.bearerToken = out.AccessToken
	}
	return &out, nil
}

// ListSSHKeys lists SSH keys for the current user.
func (c *Client) ListSSHKeys(ctx context.Context) ([]SSHKey, error) {
	var out []SSHKey
	if err := c.Do(ctx, http.MethodGet, "/ssh-keys", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []SSHKey{}
	}
	return out, nil
}

// AddSSHKey adds a new SSH key for the current user.
func (c *Client) AddSSHKey(ctx context.Context, name, publicKey string) (*SSHKey, error) {
	var out SSHKey
	req := map[string]string{"name": name, "public_key": publicKey}
	if err := c.Do(ctx, http.MethodPost, "/ssh-keys", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSSHKey deletes an SSH key by ID.
func (c *Client) DeleteSSHKey(ctx context.Context, keyID string) error {
	return c.Do(ctx, http.MethodDelete, "/ssh-keys/"+keyID, nil, nil)
}
