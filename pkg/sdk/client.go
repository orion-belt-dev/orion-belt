package sdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client is a reusable Go SDK client for Orion Belt's HTTP API.
type Client struct {
	baseURL      string
	httpClient   *http.Client
	apiKey       string
	sessionToken string
	bearerToken  string
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

// WithTimeout sets the HTTP timeout on the client's internal HTTP client.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if timeout > 0 {
			c.httpClient.Timeout = timeout
		}
	}
}

// WithAPIKey configures X-API-Key authentication.
func WithAPIKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = strings.TrimSpace(apiKey)
	}
}

// WithSessionToken configures X-Session-Token authentication.
func WithSessionToken(sessionToken string) Option {
	return func(c *Client) {
		c.sessionToken = strings.TrimSpace(sessionToken)
	}
}

// WithBearerToken configures Authorization: Bearer authentication.
func WithBearerToken(token string) Option {
	return func(c *Client) {
		c.bearerToken = strings.TrimSpace(token)
	}
}

// NewClient creates an SDK client for a server base URL, for example:
// http://localhost:8080 or https://pam.example.com.
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}

	return c, nil
}

// SetAPIKey updates X-API-Key auth for subsequent requests.
func (c *Client) SetAPIKey(apiKey string) {
	c.apiKey = strings.TrimSpace(apiKey)
}

// SetSessionToken updates X-Session-Token auth for subsequent requests.
func (c *Client) SetSessionToken(sessionToken string) {
	c.sessionToken = strings.TrimSpace(sessionToken)
}

// SetBearerToken updates Authorization: Bearer auth for subsequent requests.
func (c *Client) SetBearerToken(token string) {
	c.bearerToken = strings.TrimSpace(token)
}

// APIError captures non-2xx API responses.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e == nil {
		return "api error"
	}
	if e.Message != "" {
		return fmt.Sprintf("api error (%d): %s", e.StatusCode, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("api error (%d): %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("api error (%d)", e.StatusCode)
}

func (c *Client) fullURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasPrefix(path, "/api/") {
		path = "/api/v1" + path
	}
	return c.baseURL + path
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, out any, withAuth bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.fullURL(path), reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if withAuth {
		if c.apiKey != "" {
			req.Header.Set("X-API-Key", c.apiKey)
		} else if c.sessionToken != "" {
			req.Header.Set("X-Session-Token", c.sessionToken)
		}
		if c.bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+c.bearerToken)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
		if len(respBody) > 0 {
			var payload struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(respBody, &payload); err == nil {
				if payload.Error != "" {
					apiErr.Message = payload.Error
				} else if payload.Message != "" {
					apiErr.Message = payload.Message
				}
			}
		}
		return apiErr
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}

	return nil
}

// Do executes an authenticated API request.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	return c.doRequest(ctx, method, path, body, out, true)
}

// DoPublic executes an unauthenticated API request.
func (c *Client) DoPublic(ctx context.Context, method, path string, body any, out any) error {
	return c.doRequest(ctx, method, path, body, out, false)
}

// LoginWithPassword authenticates with username/password/TOTP and stores the
// returned session token on the client.
func (c *Client) LoginWithPassword(ctx context.Context, username, password, totpCode string) (*PasswordLoginResult, error) {
	body := map[string]string{
		"username":  username,
		"password":  password,
		"totp_code": totpCode,
	}
	var resp PasswordLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/login/password", body, &resp); err != nil {
		return nil, err
	}
	if resp.SessionToken == "" {
		return nil, fmt.Errorf("no session token returned")
	}
	c.sessionToken = resp.SessionToken
	if resp.AccessToken != "" {
		c.bearerToken = resp.AccessToken
	}
	return &resp, nil
}

// LoginWithSSHKey authenticates using proof-of-possession against the public
// key login flow and stores the returned API key on the client.
func (c *Client) LoginWithSSHKey(ctx context.Context, username string, signer ssh.Signer, totpCode string) (*APIKeyLoginResult, error) {
	if signer == nil {
		return nil, fmt.Errorf("signer is required")
	}
	publicKey := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))

	var challengeResp struct {
		Challenge string `json:"challenge"`
	}
	if err := c.DoPublic(ctx, http.MethodPost, "/public/auth/challenge", map[string]string{"username": username}, &challengeResp); err != nil {
		return nil, err
	}
	if challengeResp.Challenge == "" {
		return nil, fmt.Errorf("empty challenge returned")
	}

	sig, err := signer.Sign(rand.Reader, []byte(challengeResp.Challenge))
	if err != nil {
		return nil, fmt.Errorf("sign challenge: %w", err)
	}

	loginReq := map[string]any{
		"username":         username,
		"public_key":       publicKey,
		"challenge":        challengeResp.Challenge,
		"signature_format": sig.Format,
		"signature":        base64.StdEncoding.EncodeToString(sig.Blob),
	}
	if strings.TrimSpace(totpCode) != "" {
		loginReq["totp_code"] = strings.TrimSpace(totpCode)
	}

	var loginResp APIKeyLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/login/key", loginReq, &loginResp); err != nil {
		return nil, err
	}
	if loginResp.APIKey == "" {
		return nil, fmt.Errorf("no API key returned")
	}
	c.apiKey = loginResp.APIKey
	return &loginResp, nil
}

// ListMachines returns all target machines visible to the caller.
func (c *Client) ListMachines(ctx context.Context) ([]Machine, error) {
	var machines []Machine
	if err := c.Do(ctx, http.MethodGet, "/machines", nil, &machines); err != nil {
		return nil, err
	}
	return machines, nil
}

// GetMachineByName resolves a machine by Name from ListMachines.
func (c *Client) GetMachineByName(ctx context.Context, name string) (*Machine, error) {
	machines, err := c.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	for i := range machines {
		if machines[i].Name == name {
			return &machines[i], nil
		}
	}
	return nil, fmt.Errorf("machine %q not found", name)
}

// CreateAccessRequest creates a JIT access request.
func (c *Client) CreateAccessRequest(ctx context.Context, req CreateAccessRequestRequest) (*AccessRequest, error) {
	var accessReq AccessRequest
	if err := c.Do(ctx, http.MethodPost, "/access-requests", req, &accessReq); err != nil {
		return nil, err
	}
	return &accessReq, nil
}

// GetAccessRequest fetches an access request by ID.
func (c *Client) GetAccessRequest(ctx context.Context, requestID string) (*AccessRequest, error) {
	var accessReq AccessRequest
	if err := c.Do(ctx, http.MethodGet, "/access-requests/"+requestID, nil, &accessReq); err != nil {
		return nil, err
	}
	return &accessReq, nil
}

// ListPendingAccessRequests lists pending access requests (admin/operator).
func (c *Client) ListPendingAccessRequests(ctx context.Context) ([]AccessRequest, error) {
	var requests []AccessRequest
	if err := c.Do(ctx, http.MethodGet, "/admin/access-requests/pending", nil, &requests); err != nil {
		return nil, err
	}
	return requests, nil
}

// ApproveAccessRequest approves an access request.
func (c *Client) ApproveAccessRequest(ctx context.Context, requestID, reviewerID string) error {
	req := map[string]string{"reviewer_id": reviewerID}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("/admin/access-requests/%s/approve", requestID), req, nil)
}

// RejectAccessRequest rejects an access request.
func (c *Client) RejectAccessRequest(ctx context.Context, requestID, reviewerID string) error {
	req := map[string]string{"reviewer_id": reviewerID}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("/admin/access-requests/%s/reject", requestID), req, nil)
}

// GetTrustedCA returns public CA material for automatic client trust setup.
func (c *Client) GetTrustedCA(ctx context.Context) (*TrustedCA, error) {
	var ca TrustedCA
	if err := c.Do(ctx, http.MethodGet, "/ssh-cert/ca", nil, &ca); err != nil {
		return nil, err
	}
	return &ca, nil
}

// IssueUserCert requests a short-lived SSH user certificate for a public key.
func (c *Client) IssueUserCert(ctx context.Context, publicKey string, ttlHours int) (*IssuedCert, error) {
	req := map[string]any{"public_key": publicKey, "ttl_hours": ttlHours}
	var cert IssuedCert
	if err := c.Do(ctx, http.MethodPost, "/ssh-cert", req, &cert); err != nil {
		return nil, err
	}
	return &cert, nil
}

// ExportCA returns CA trust material via the admin export endpoint.
func (c *Client) ExportCA(ctx context.Context) (*TrustedCA, error) {
	var ca TrustedCA
	if err := c.Do(ctx, http.MethodGet, "/admin/ca/export", nil, &ca); err != nil {
		return nil, err
	}
	return &ca, nil
}

// ListSSHCertificates lists issued SSH certificate lifecycle records.
func (c *Client) ListSSHCertificates(ctx context.Context, certType string) ([]SSHCertificate, error) {
	path := "/admin/ssh-certificates"
	if strings.TrimSpace(certType) != "" {
		path += "?cert_type=" + certType
	}
	var resp struct {
		Certificates []SSHCertificate `json:"certificates"`
	}
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Certificates, nil
}

// RevokeSSHCertificate revokes a certificate before TTL expiry.
func (c *Client) RevokeSSHCertificate(ctx context.Context, serial, reason string) error {
	req := map[string]string{"reason": reason}
	return c.Do(ctx, http.MethodPost, fmt.Sprintf("/admin/ssh-certificates/%s/revoke", serial), req, nil)
}

// RequestBrowserBootstrap creates a one-time code to bootstrap a browser session.
func (c *Client) RequestBrowserBootstrap(ctx context.Context) (*BootstrapCode, error) {
	var code BootstrapCode
	if err := c.Do(ctx, http.MethodPost, "/auth/browser-bootstrap", nil, &code); err != nil {
		return nil, err
	}
	return &code, nil
}
