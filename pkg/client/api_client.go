package client

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// APIClient handles REST API communication with the Orion-Belt server
type APIClient struct {
	baseURL      string
	httpClient   *http.Client
	apiKey       string
	sessionToken string
	logger       *common.Logger
}

// Machine represents a machine from the API
type Machine struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Hostname   string            `json:"hostname"`
	Port       int               `json:"port"`
	Tags       map[string]string `json:"tags"`
	AgentID    string            `json:"agent_id"`
	IsActive   bool              `json:"is_active"`
	LastSeenAt *time.Time        `json:"last_seen_at"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// AccessRequest represents an access request from the API
type AccessRequest struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	MachineID   string     `json:"machine_id"`
	RemoteUsers []string   `json:"remote_users"`
	AccessType  string     `json:"access_type"`
	Reason      string     `json:"reason"`
	Duration    int        `json:"duration"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	ReviewedAt  *time.Time `json:"reviewed_at"`
	ReviewedBy  *string    `json:"reviewed_by"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// NewAPIClient creates a new API client with authentication
func NewAPIClient(baseURL, username string, signer ssh.Signer, logger *common.Logger) (*APIClient, error) {
	client := &APIClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}

	// Authenticate and get API key
	if err := client.authenticate(username, signer); err != nil {
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	return client, nil
}

// LoadAPIClient builds an authenticated APIClient from a loaded config,
// resolving the API endpoint, signing key, and username the same way
// osh/ocp/oadmin each used to do independently. usernameOverride wins over
// config/environment when non-empty (e.g. a CLI's -u/--user flag).
func LoadAPIClient(cfg *common.Config, usernameOverride string, logger *common.Logger) (*APIClient, error) {
	apiEndpoint := cfg.Server.APIEndpoint
	if apiEndpoint == "" {
		apiEndpoint = fmt.Sprintf("http://%s:8080", cfg.Server.Host)
	}

	keyData, err := os.ReadFile(cfg.Auth.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read SSH key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	username := usernameOverride
	if username == "" {
		username = cfg.Auth.User
	}
	if username == "" {
		username = os.Getenv("USER")
	}
	if username == "" {
		return nil, fmt.Errorf("username not configured")
	}

	return NewAPIClient(apiEndpoint, username, signer, logger)
}

// authenticate obtains an API key using SSH public key authentication. It
// proves possession of the private key by signing a server-issued,
// single-use challenge — a bare public key is not a secret (it's often
// public on GitHub, in server logs, etc.), so /login/key requires this
// signature, not just a matching key string.
//
// When the organization sets auth.mfa_required, the server returns mfa_required;
// authenticate then prompts for a code and retries with a fresh challenge.
func (c *APIClient) authenticate(username string, signer ssh.Signer) error {
	publicKey := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	loginOnce := func(totpCode string) error {
		var challengeResp struct {
			Challenge string `json:"challenge"`
		}
		if err := c.doRequestNoAuth("POST", "/api/v1/public/auth/challenge", map[string]string{"username": username}, &challengeResp); err != nil {
			return fmt.Errorf("failed to obtain login challenge: %w", err)
		}

		sig, err := signer.Sign(rand.Reader, []byte(challengeResp.Challenge))
		if err != nil {
			return fmt.Errorf("failed to sign login challenge: %w", err)
		}

		loginReq := map[string]interface{}{
			"username":         username,
			"public_key":       publicKey,
			"challenge":        challengeResp.Challenge,
			"signature_format": sig.Format,
			"signature":        base64.StdEncoding.EncodeToString(sig.Blob),
		}
		if totpCode != "" {
			loginReq["totp_code"] = totpCode
		}

		var loginResp struct {
			APIKey    string    `json:"api_key"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		if err := c.doRequestNoAuth("POST", "/api/v1/public/login/key", loginReq, &loginResp); err != nil {
			return err
		}
		if loginResp.APIKey == "" {
			return fmt.Errorf("no API key returned from login")
		}
		c.apiKey = loginResp.APIKey
		if c.logger != nil {
			c.logger.Info("Successfully authenticated, API key expires at: %s", loginResp.ExpiresAt.Format(time.RFC3339))
		}
		return nil
	}

	err := loginOnce("")
	if err == nil {
		return nil
	}
	if !isMFARequired(err) {
		return err
	}

	totp, promptErr := promptTOTP()
	if promptErr != nil {
		return fmt.Errorf("mfa required: %w", promptErr)
	}
	if totp == "" {
		return fmt.Errorf("mfa code required")
	}
	return loginOnce(totp)
}

func promptTOTP() (string, error) {
	fmt.Fprint(os.Stderr, "TOTP code: ")
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func isMFARequired(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, `"mfa_required":true`) || strings.Contains(s, `"mfa_required": true`)
}

// doRequestNoAuth performs an HTTP request without authentication (for login)
func (c *APIClient) doRequestNoAuth(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// doRequest performs an HTTP request to the API
func (c *APIClient) doRequest(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else if c.sessionToken != "" {
		req.Header.Set("X-Session-Token", c.sessionToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// ListMachines retrieves all available machines
func (c *APIClient) ListMachines() ([]Machine, error) {
	var machines []Machine
	if err := c.doRequest("GET", "/api/v1/machines", nil, &machines); err != nil {
		return nil, err
	}
	return machines, nil
}

// GetMachineByName retrieves a machine by its name
func (c *APIClient) GetMachineByName(name string) (*Machine, error) {
	machines, err := c.ListMachines()
	if err != nil {
		return nil, err
	}

	for _, machine := range machines {
		if machine.Name == name {
			return &machine, nil
		}
	}

	return nil, fmt.Errorf("machine %s not found", name)
}

// CreateAccessRequest creates a new access request
func (c *APIClient) CreateAccessRequest(request map[string]interface{}) (*AccessRequest, error) {
	var accessReq AccessRequest
	if err := c.doRequest("POST", "/api/v1/access-requests", request, &accessReq); err != nil {
		return nil, err
	}
	return &accessReq, nil
}

// GetAccessRequest retrieves an access request by ID
func (c *APIClient) GetAccessRequest(requestID string) (*AccessRequest, error) {
	var accessReq AccessRequest
	if err := c.doRequest("GET", "/api/v1/access-requests/"+requestID, nil, &accessReq); err != nil {
		return nil, err
	}
	return &accessReq, nil
}

// ListPendingAccessRequests retrieves all pending access requests (admin only)
func (c *APIClient) ListPendingAccessRequests() ([]AccessRequest, error) {
	var requests []AccessRequest
	if err := c.doRequest("GET", "/api/v1/admin/access-requests/pending", nil, &requests); err != nil {
		return nil, err
	}
	return requests, nil
}

// ApproveAccessRequest approves an access request (admin only)
func (c *APIClient) ApproveAccessRequest(requestID, reviewerID string) error {
	req := map[string]string{"reviewer_id": reviewerID}
	return c.doRequest("POST", fmt.Sprintf("/api/v1/admin/access-requests/%s/approve", requestID), req, nil)
}

// RejectAccessRequest rejects an access request (admin only)
func (c *APIClient) RejectAccessRequest(requestID, reviewerID string) error {
	req := map[string]string{"reviewer_id": reviewerID}
	return c.doRequest("POST", fmt.Sprintf("/api/v1/admin/access-requests/%s/reject", requestID), req, nil)
}

// TrustedCA is the SSH CA public-key material a client needs to trust the
// gateway's Host CA and (if requesting one) User CA-issued certs.
type TrustedCA struct {
	Enabled bool   `json:"enabled"`
	UserCA  string `json:"user_ca"`
	HostCA  string `json:"host_ca"`
}

// GetTrustedCA discovers whether the server has SSH CA enabled, and if so
// returns its CA public keys. Any authenticated client can call this —
// it's how osh/ocp/oadmin auto-detect server capability instead of
// requiring a local opt-in flag.
func (c *APIClient) GetTrustedCA() (*TrustedCA, error) {
	var ca TrustedCA
	if err := c.doRequest("GET", "/api/v1/ssh-cert/ca", nil, &ca); err != nil {
		return nil, err
	}
	return &ca, nil
}

// IssuedCert is the response from requesting a fresh user certificate.
type IssuedCert struct {
	Certificate string    `json:"certificate"` // authorized_keys-format cert line
	Serial      string    `json:"serial"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// IssueUserCert requests a short-lived signed SSH certificate for pub
// (authorized_keys format). ttlHours of 0 uses the server's default TTL.
func (c *APIClient) IssueUserCert(pub string, ttlHours int) (*IssuedCert, error) {
	req := map[string]interface{}{"public_key": pub, "ttl_hours": ttlHours}
	var cert IssuedCert
	if err := c.doRequest("POST", "/api/v1/ssh-cert", req, &cert); err != nil {
		return nil, err
	}
	return &cert, nil
}

// ExportCA fetches CA trust material via the admin export endpoint for
// out-of-band distribution to clients/agents (admin only).
func (c *APIClient) ExportCA() (*TrustedCA, error) {
	var ca TrustedCA
	if err := c.doRequest("GET", "/api/v1/admin/ca/export", nil, &ca); err != nil {
		return nil, err
	}
	return &ca, nil
}

// IssuedSSHCertificate is a certificate lifecycle record as returned by
// the admin certificate-listing endpoint.
type IssuedSSHCertificate struct {
	ID                   string     `json:"id"`
	Serial               string     `json:"serial"`
	CertType             string     `json:"cert_type"`
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

// ListSSHCertificates lists issued-certificate lifecycle records (admin only).
func (c *APIClient) ListSSHCertificates(certType string) ([]IssuedSSHCertificate, error) {
	path := "/api/v1/admin/ssh-certificates"
	if certType != "" {
		path += "?cert_type=" + certType
	}
	var resp struct {
		Certificates []IssuedSSHCertificate `json:"certificates"`
	}
	if err := c.doRequest("GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Certificates, nil
}

// RevokeSSHCertificate revokes a certificate ahead of its TTL expiry (admin only).
func (c *APIClient) RevokeSSHCertificate(serial, reason string) error {
	req := map[string]string{"reason": reason}
	return c.doRequest("POST", fmt.Sprintf("/api/v1/admin/ssh-certificates/%s/revoke", serial), req, nil)
}

// BootstrapCode is a short-lived, single-use code for bootstrapping a
// browser session from an already-authenticated CLI identity — see
// RequestBrowserBootstrap and `osh login`.
type BootstrapCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// NewUnauthenticatedAPIClient builds a client that has not yet authenticated.
// Use LoginWithPassword (or call authenticate via NewAPIClient) before protected routes.
func NewUnauthenticatedAPIClient(baseURL string, logger *common.Logger) *APIClient {
	return &APIClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		logger:     logger,
	}
}

// PasswordLoginResult is returned by LoginWithPassword.
type PasswordLoginResult struct {
	SessionToken string    `json:"session_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// LoginWithPassword authenticates with username + password + TOTP in one shot
// (CLI style). The client keeps the session token for subsequent API calls
// such as RequestBrowserBootstrap.
func (c *APIClient) LoginWithPassword(username, password, totpCode string) (*PasswordLoginResult, error) {
	var resp PasswordLoginResult
	body := map[string]string{
		"username":  username,
		"password":  password,
		"totp_code": totpCode,
	}
	if err := c.doRequestNoAuth("POST", "/api/v1/public/login/password", body, &resp); err != nil {
		return nil, err
	}
	if resp.SessionToken == "" {
		return nil, fmt.Errorf("no session returned from password login")
	}
	c.sessionToken = resp.SessionToken
	if c.logger != nil {
		c.logger.Info("Authenticated with password+TOTP (session expires %s)", resp.ExpiresAt.Format(time.RFC3339))
	}
	return &resp, nil
}

// RequestBrowserBootstrap asks the server for a one-time code the caller
// can redeem in a browser to get a signed-in session, without the browser
// ever needing to prove possession of an SSH private key itself (which it
// structurally cannot do). This is the CLI-driven replacement for the web
// console's old "paste your public key" login form: the CLI already
// proved identity via real SSH-signature login (see authenticate above),
// so it can vouch for a short-lived browser session instead.
func (c *APIClient) RequestBrowserBootstrap() (*BootstrapCode, error) {
	var code BootstrapCode
	if err := c.doRequest("POST", "/api/v1/auth/browser-bootstrap", nil, &code); err != nil {
		return nil, err
	}
	return &code, nil
}
