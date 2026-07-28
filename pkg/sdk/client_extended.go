package sdk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) doRequestBytes(ctx context.Context, method, path string, body any, withAuth bool) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var reader io.Reader
	if body != nil {
		return nil, fmt.Errorf("binary request helper does not support request body")
	}

	req, err := http.NewRequestWithContext(ctx, method, c.fullURL(path), reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return respBody, nil
}

func addIntQuery(values url.Values, key string, n int) {
	if n > 0 {
		values.Set(key, strconv.Itoa(n))
	}
}

// Logout destroys the current HTTP session.
func (c *Client) Logout(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/logout", nil, nil)
}

// GetCurrentUser returns the /auth/me payload.
func (c *Client) GetCurrentUser(ctx context.Context) (*AuthUser, error) {
	var user AuthUser
	if err := c.Do(ctx, http.MethodGet, "/auth/me", nil, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateAPIKey creates a new API key for the authenticated user.
func (c *Client) CreateAPIKey(ctx context.Context, req CreateAPIKeyRequest) (*CreateAPIKeyResponse, error) {
	var out CreateAPIKeyResponse
	if err := c.Do(ctx, http.MethodPost, "/api-keys", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAPIKeys lists all API keys for the authenticated user.
func (c *Client) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	var out struct {
		APIKeys []APIKey `json:"api_keys"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api-keys", nil, &out); err != nil {
		return nil, err
	}
	if out.APIKeys == nil {
		out.APIKeys = []APIKey{}
	}
	return out.APIKeys, nil
}

// RevokeAPIKey revokes a key by ID.
func (c *Client) RevokeAPIKey(ctx context.Context, keyID string) error {
	return c.Do(ctx, http.MethodPost, "/api-keys/"+keyID+"/revoke", nil, nil)
}

// DeleteAPIKey permanently deletes a key by ID.
func (c *Client) DeleteAPIKey(ctx context.Context, keyID string) error {
	return c.Do(ctx, http.MethodDelete, "/api-keys/"+keyID, nil, nil)
}

// ListUsers returns users visible to the caller.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	if err := c.Do(ctx, http.MethodGet, "/users", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []User{}
	}
	return out, nil
}

// GetUser returns a user by ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*User, error) {
	var out User
	if err := c.Do(ctx, http.MethodGet, "/users/"+userID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAccessRequests lists access requests, optionally filtered by status.
func (c *Client) ListAccessRequests(ctx context.Context, status string) ([]AccessRequest, error) {
	path := "/access-requests"
	if strings.TrimSpace(status) != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []AccessRequest
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []AccessRequest{}
	}
	return out, nil
}

// ListSessions lists sessions, optionally filtered by status.
func (c *Client) ListSessions(ctx context.Context, status string) ([]Session, error) {
	path := "/sessions"
	if strings.TrimSpace(status) != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var out []Session
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Session{}
	}
	return out, nil
}

// ListActiveSessions lists active sessions.
func (c *Client) ListActiveSessions(ctx context.Context) ([]Session, error) {
	var out []Session
	if err := c.Do(ctx, http.MethodGet, "/sessions/active", nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Session{}
	}
	return out, nil
}

// GetSession fetches a session by ID.
func (c *Client) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	var out Session
	if err := c.Do(ctx, http.MethodGet, "/sessions/"+sessionID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSessionContent downloads raw session content bytes.
func (c *Client) GetSessionContent(ctx context.Context, sessionID string) ([]byte, error) {
	return c.doRequestBytes(ctx, http.MethodGet, "/sessions/"+sessionID+"/content", nil, true)
}

// ListAuditLogs lists audit logs and clamps limit server-side.
func (c *Client) ListAuditLogs(ctx context.Context, limit int) ([]AuditLog, error) {
	path := "/audit-logs"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []AuditLog
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []AuditLog{}
	}
	return out, nil
}

// ExportReport downloads report bytes by report name and optional format.
// Format examples: csv, pdf, siem.
func (c *Client) ExportReport(ctx context.Context, reportName, format string) ([]byte, error) {
	path := "/reports/" + url.PathEscape(reportName) + "/export"
	if strings.TrimSpace(format) != "" {
		path += "?format=" + url.QueryEscape(format)
	}
	return c.doRequestBytes(ctx, http.MethodGet, path, nil, true)
}

// GetUsageDashboard returns windowed operational usage analytics.
func (c *Client) GetUsageDashboard(ctx context.Context, windowHours, top int) (*UsageDashboard, error) {
	values := url.Values{}
	addIntQuery(values, "window_hours", windowHours)
	addIntQuery(values, "top", top)
	path := "/dashboard/usage"
	if encoded := values.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out UsageDashboard
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListNotifications lists in-app notifications for the current user.
func (c *Client) ListNotifications(ctx context.Context, limit int) ([]Notification, error) {
	path := "/notifications"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out []Notification
	if err := c.Do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Notification{}
	}
	return out, nil
}

// UnreadNotificationCount returns unread notification count.
func (c *Client) UnreadNotificationCount(ctx context.Context) (int, error) {
	var out struct {
		Unread int `json:"unread"`
	}
	if err := c.Do(ctx, http.MethodGet, "/notifications/unread-count", nil, &out); err != nil {
		return 0, err
	}
	return out.Unread, nil
}

// MarkNotificationRead marks one notification as read.
func (c *Client) MarkNotificationRead(ctx context.Context, notificationID string) error {
	return c.Do(ctx, http.MethodPost, "/notifications/"+notificationID+"/read", nil, nil)
}

// MarkAllNotificationsRead marks all notifications read for the current user.
func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	return c.Do(ctx, http.MethodPost, "/notifications/read-all", nil, nil)
}

// GetNotificationPrefs fetches channel preferences for the current user.
func (c *Client) GetNotificationPrefs(ctx context.Context) (*NotificationPrefs, error) {
	var prefs NotificationPrefs
	if err := c.Do(ctx, http.MethodGet, "/notifications/prefs", nil, &prefs); err != nil {
		return nil, err
	}
	return &prefs, nil
}

// PutNotificationPrefs upserts channel preferences for the current user.
func (c *Client) PutNotificationPrefs(ctx context.Context, prefs NotificationPrefs) (*NotificationPrefs, error) {
	var out NotificationPrefs
	if err := c.Do(ctx, http.MethodPut, "/notifications/prefs", prefs, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSetupStatus returns first-run setup checklist status.
func (c *Client) GetSetupStatus(ctx context.Context) (*SetupStatus, error) {
	var out SetupStatus
	if err := c.Do(ctx, http.MethodGet, "/setup/status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
