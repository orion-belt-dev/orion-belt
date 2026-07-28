package sdk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/crypto/ssh"
)

func (c *Client) signChallenge(challenge string, signer ssh.Signer) (string, string, error) {
	if signer == nil {
		return "", "", fmt.Errorf("signer is required")
	}
	sig, err := signer.Sign(rand.Reader, []byte(challenge))
	if err != nil {
		return "", "", fmt.Errorf("sign challenge: %w", err)
	}
	return sig.Format, base64.StdEncoding.EncodeToString(sig.Blob), nil
}

func (c *Client) doMultipart(ctx context.Context, method, path string, fields map[string]string, fileField, fileName string, fileContent []byte, out any) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return fmt.Errorf("write field %s: %w", k, err)
		}
	}
	if fileField != "" {
		part, err := w.CreateFormFile(fileField, fileName)
		if err != nil {
			return fmt.Errorf("create form file: %w", err)
		}
		if _, err := part.Write(fileContent); err != nil {
			return fmt.Errorf("write form file: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalize multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.fullURL(path), &body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	} else if c.sessionToken != "" {
		req.Header.Set("X-Session-Token", c.sessionToken)
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
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

// IssueChallenge creates a one-time public-key login challenge.
func (c *Client) IssueChallenge(ctx context.Context, username string) (string, error) {
	var out struct {
		Challenge string `json:"challenge"`
	}
	req := map[string]string{"username": username}
	if err := c.DoPublic(ctx, http.MethodPost, "/public/auth/challenge", req, &out); err != nil {
		return "", err
	}
	if out.Challenge == "" {
		return "", fmt.Errorf("no challenge returned")
	}
	return out.Challenge, nil
}

// LoginWithSSHSession creates a session token using SSH challenge proof.
func (c *Client) LoginWithSSHSession(ctx context.Context, username string, signer ssh.Signer, totpCode string) (*PasswordLoginResult, error) {
	challenge, err := c.IssueChallenge(ctx, username)
	if err != nil {
		return nil, err
	}
	format, sigB64, err := c.signChallenge(challenge, signer)
	if err != nil {
		return nil, err
	}
	req := map[string]string{
		"username":         username,
		"public_key":       string(ssh.MarshalAuthorizedKey(signer.PublicKey())),
		"challenge":        challenge,
		"signature_format": format,
		"signature":        sigB64,
	}
	if strings.TrimSpace(totpCode) != "" {
		req["totp_code"] = strings.TrimSpace(totpCode)
	}

	var out PasswordLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/login", req, &out); err != nil {
		return nil, err
	}
	if out.SessionToken == "" {
		return nil, fmt.Errorf("no session token returned")
	}
	c.sessionToken = out.SessionToken
	if out.AccessToken != "" {
		c.bearerToken = out.AccessToken
	}
	return &out, nil
}

// LoginWithSSHJWT creates a JWT bearer token using SSH challenge proof.
func (c *Client) LoginWithSSHJWT(ctx context.Context, username string, signer ssh.Signer, totpCode string) (*JWTLoginResult, error) {
	challenge, err := c.IssueChallenge(ctx, username)
	if err != nil {
		return nil, err
	}
	format, sigB64, err := c.signChallenge(challenge, signer)
	if err != nil {
		return nil, err
	}
	req := map[string]string{
		"username":         username,
		"public_key":       string(ssh.MarshalAuthorizedKey(signer.PublicKey())),
		"challenge":        challenge,
		"signature_format": format,
		"signature":        sigB64,
	}
	if strings.TrimSpace(totpCode) != "" {
		req["totp_code"] = strings.TrimSpace(totpCode)
	}

	var out JWTLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/login/token", req, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("no access token returned")
	}
	c.bearerToken = out.AccessToken
	return &out, nil
}

// RedeemBrowserBootstrap exchanges a short-lived bootstrap code for a session.
func (c *Client) RedeemBrowserBootstrap(ctx context.Context, code string) (*PasswordLoginResult, error) {
	req := map[string]string{"code": code}
	var out PasswordLoginResult
	if err := c.DoPublic(ctx, http.MethodPost, "/public/auth/browser-bootstrap/redeem", req, &out); err != nil {
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

// RegisterAgent registers an agent through the public endpoint.
func (c *Client) RegisterAgent(ctx context.Context, req RegisterAgentRequest) (*RegisterAgentResponse, error) {
	var out RegisterAgentResponse
	if err := c.DoPublic(ctx, http.MethodPost, "/public/register/agent", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RegisterClient registers a new user through the public endpoint.
func (c *Client) RegisterClient(ctx context.Context, req RegisterClientRequest) (*RegisterClientResponse, error) {
	var out RegisterClientResponse
	if err := c.DoPublic(ctx, http.MethodPost, "/public/register/client", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListFiles lists files for a machine/path/user context.
func (c *Client) ListFiles(ctx context.Context, machine, path, remoteUser string) (*FileListResponse, error) {
	v := url.Values{}
	v.Set("machine", machine)
	if strings.TrimSpace(path) != "" {
		v.Set("path", path)
	}
	if strings.TrimSpace(remoteUser) != "" {
		v.Set("user", remoteUser)
	}
	var out FileListResponse
	if err := c.Do(ctx, http.MethodGet, "/files/list?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Entries == nil {
		out.Entries = []FileEntry{}
	}
	return &out, nil
}

// DownloadFile downloads raw bytes from a remote machine path.
func (c *Client) DownloadFile(ctx context.Context, machine, path, remoteUser string) ([]byte, error) {
	v := url.Values{}
	v.Set("machine", machine)
	v.Set("path", path)
	if strings.TrimSpace(remoteUser) != "" {
		v.Set("user", remoteUser)
	}
	return c.doRequestBytes(ctx, http.MethodGet, "/files/download?"+v.Encode(), nil, true)
}

// UploadFile uploads a file to a remote machine path.
func (c *Client) UploadFile(ctx context.Context, machine, path, remoteUser, fileName string, content []byte) (*FileUploadResponse, error) {
	fields := map[string]string{
		"machine": machine,
		"path":    path,
	}
	if strings.TrimSpace(remoteUser) != "" {
		fields["user"] = remoteUser
	}
	var out FileUploadResponse
	if err := c.doMultipart(ctx, http.MethodPost, "/files/upload", fields, "file", fileName, content, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MakeDir creates a remote directory path.
func (c *Client) MakeDir(ctx context.Context, machine, path, remoteUser string) error {
	req := map[string]string{
		"machine": machine,
		"path":    path,
	}
	if strings.TrimSpace(remoteUser) != "" {
		req["user"] = remoteUser
	}
	return c.Do(ctx, http.MethodPost, "/files/mkdir", req, nil)
}

// DeleteFile deletes a remote path.
func (c *Client) DeleteFile(ctx context.Context, machine, path, remoteUser string) error {
	v := url.Values{}
	v.Set("machine", machine)
	v.Set("path", path)
	if strings.TrimSpace(remoteUser) != "" {
		v.Set("user", remoteUser)
	}
	return c.Do(ctx, http.MethodDelete, "/files?"+v.Encode(), nil, nil)
}
