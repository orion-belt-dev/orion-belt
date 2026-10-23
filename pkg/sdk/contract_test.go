package sdk

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// recordingServer answers every request with status and body, keeping the
// last request so a test can check what the client sent.
type recordingServer struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	body   string
	last   *http.Request
	sent   string
}

func newRecordingServer(t *testing.T) *recordingServer {
	t.Helper()
	rs := &recordingServer{status: http.StatusOK, body: "{}"}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rs.mu.Lock()
		rs.last, rs.sent = r, string(b)
		status, body := rs.status, rs.body
		rs.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (rs *recordingServer) reply(status int, body string) {
	rs.mu.Lock()
	rs.status, rs.body = status, body
	rs.mu.Unlock()
}

func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// endpoint is one SDK method's wire contract.
type endpoint struct {
	name     string
	call     func(ctx context.Context, c *Client) error
	method   string
	target   string // path plus query, as the server sees it
	public   bool   // sent without credentials
	bodyHas  string // substring the JSON request body must contain
	response string // body returned on success
}

func endpoints() []endpoint {
	ignore := func(_ any, err error) error { return err }
	return []endpoint{
		{"ListMachines", func(ctx context.Context, c *Client) error { return ignore(c.ListMachines(ctx)) }, "GET", "/api/v1/machines", false, "", `[]`},
		{"GetMachine", func(ctx context.Context, c *Client) error { return ignore(c.GetMachine(ctx, "m1")) }, "GET", "/api/v1/machines/m1", false, "", `{}`},
		{"CreateAccessRequest", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateAccessRequest(ctx, CreateAccessRequestRequest{Reason: "deploy"}))
		}, "POST", "/api/v1/access-requests", false, `"deploy"`, `{}`},
		{"GetAccessRequest", func(ctx context.Context, c *Client) error { return ignore(c.GetAccessRequest(ctx, "r1")) }, "GET", "/api/v1/access-requests/r1", false, "", `{}`},
		{"ListPendingAccessRequests", func(ctx context.Context, c *Client) error { return ignore(c.ListPendingAccessRequests(ctx)) }, "GET", "/api/v1/admin/access-requests/pending", false, "", `[]`},
		{"ApproveAccessRequest", func(ctx context.Context, c *Client) error { return c.ApproveAccessRequest(ctx, "r1", "rev") }, "POST", "/api/v1/admin/access-requests/r1/approve", false, `"reviewer_id":"rev"`, ``},
		{"RejectAccessRequest", func(ctx context.Context, c *Client) error { return c.RejectAccessRequest(ctx, "r1", "rev") }, "POST", "/api/v1/admin/access-requests/r1/reject", false, `"reviewer_id":"rev"`, ``},
		{"GetTrustedCA", func(ctx context.Context, c *Client) error { return ignore(c.GetTrustedCA(ctx)) }, "GET", "/api/v1/ssh-cert/ca", false, "", `{}`},
		{"IssueUserCert", func(ctx context.Context, c *Client) error { return ignore(c.IssueUserCert(ctx, "ssh-ed25519 AAA", 4)) }, "POST", "/api/v1/ssh-cert", false, `"ttl_hours":4`, `{}`},
		{"ExportCA", func(ctx context.Context, c *Client) error { return ignore(c.ExportCA(ctx)) }, "GET", "/api/v1/admin/ca/export", false, "", `{}`},
		{"ListSSHCertificates", func(ctx context.Context, c *Client) error { return ignore(c.ListSSHCertificates(ctx, "user")) }, "GET", "/api/v1/admin/ssh-certificates?cert_type=user", false, "", `{"certificates":[]}`},
		{"ListSSHCertificatesAll", func(ctx context.Context, c *Client) error { return ignore(c.ListSSHCertificates(ctx, " ")) }, "GET", "/api/v1/admin/ssh-certificates", false, "", `{}`},
		{"RevokeSSHCertificate", func(ctx context.Context, c *Client) error { return c.RevokeSSHCertificate(ctx, "42", "lost") }, "POST", "/api/v1/admin/ssh-certificates/42/revoke", false, `"reason":"lost"`, ``},
		{"RequestBrowserBootstrap", func(ctx context.Context, c *Client) error { return ignore(c.RequestBrowserBootstrap(ctx)) }, "POST", "/api/v1/auth/browser-bootstrap", false, "", `{"code":"abc"}`},

		{"GetUserPermissions", func(ctx context.Context, c *Client) error { return ignore(c.GetUserPermissions(ctx, "u1")) }, "GET", "/api/v1/permissions/user/u1", false, "", `null`},
		{"GetMachinePermissions", func(ctx context.Context, c *Client) error { return ignore(c.GetMachinePermissions(ctx, "m1")) }, "GET", "/api/v1/permissions/machine/m1", false, "", `null`},
		{"CreateUser", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateUser(ctx, CreateUserRequest{Username: "bob"}))
		}, "POST", "/api/v1/admin/users", false, `"bob"`, `{}`},
		{"UpdateUser", func(ctx context.Context, c *Client) error {
			return ignore(c.UpdateUser(ctx, "u1", UpdateUserRequest{}))
		}, "PUT", "/api/v1/admin/users/u1", false, "", `{}`},
		{"DeleteUser", func(ctx context.Context, c *Client) error { return c.DeleteUser(ctx, "u1") }, "DELETE", "/api/v1/admin/users/u1", false, "", ``},
		{"CreateMachine", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateMachine(ctx, CreateMachineRequest{Name: "db"}))
		}, "POST", "/api/v1/admin/machines", false, `"db"`, `{}`},
		{"UpdateMachine", func(ctx context.Context, c *Client) error {
			return ignore(c.UpdateMachine(ctx, "m1", UpdateMachineRequest{}))
		}, "PUT", "/api/v1/admin/machines/m1", false, "", `{}`},
		{"DeleteMachine", func(ctx context.Context, c *Client) error { return c.DeleteMachine(ctx, "m1", false) }, "DELETE", "/api/v1/admin/machines/m1", false, "", ``},
		{"ListAllPermissions", func(ctx context.Context, c *Client) error { return ignore(c.ListAllPermissions(ctx, 5)) }, "GET", "/api/v1/admin/permissions?limit=5", false, "", `null`},
		{"ListAllPermissionsNoLimit", func(ctx context.Context, c *Client) error { return ignore(c.ListAllPermissions(ctx, 0)) }, "GET", "/api/v1/admin/permissions", false, "", `[]`},
		{"GrantPermission", func(ctx context.Context, c *Client) error {
			return ignore(c.GrantPermission(ctx, GrantPermissionRequest{UserID: "u1"}))
		}, "POST", "/api/v1/admin/permissions", false, `"u1"`, `{}`},
		{"UpdatePermission", func(ctx context.Context, c *Client) error {
			return ignore(c.UpdatePermission(ctx, "p1", UpdatePermissionRequest{}))
		}, "PATCH", "/api/v1/admin/permissions/p1", false, "", `{}`},
		{"RevokePermission", func(ctx context.Context, c *Client) error { return c.RevokePermission(ctx, "p1") }, "DELETE", "/api/v1/admin/permissions/p1", false, "", ``},
		{"ListPlugins", func(ctx context.Context, c *Client) error { return ignore(c.ListPlugins(ctx)) }, "GET", "/api/v1/admin/plugins", false, "", `{}`},
		{"EnablePlugin", func(ctx context.Context, c *Client) error { return ignore(c.EnablePlugin(ctx, "audit")) }, "POST", "/api/v1/admin/plugins/audit/enable", false, "", `{"plugin":{}}`},
		{"DisablePlugin", func(ctx context.Context, c *Client) error { return ignore(c.DisablePlugin(ctx, "audit")) }, "POST", "/api/v1/admin/plugins/audit/disable", false, "", `{"plugin":{}}`},
		{"ListConnectedAgents", func(ctx context.Context, c *Client) error { return ignore(c.ListConnectedAgents(ctx)) }, "GET", "/api/v1/admin/agents/connected", false, "", `{}`},
		{"SendAgentCommand", func(ctx context.Context, c *Client) error { return ignore(c.SendAgentCommand(ctx, "m1", "status")) }, "POST", "/api/v1/admin/agents/m1/command", false, `"command":"status"`, `{"output":"ok"}`},
		{"DisconnectAgent", func(ctx context.Context, c *Client) error { return c.DisconnectAgent(ctx, "m1") }, "POST", "/api/v1/admin/agents/m1/disconnect", false, "", ``},
		{"GenerateAgentInstallScript", func(ctx context.Context, c *Client) error {
			return ignore(c.GenerateAgentInstallScript(ctx, AgentInstallScriptRequest{}))
		}, "POST", "/api/v1/admin/agents/install-script", false, "", `{}`},
		{"MFAEnroll", func(ctx context.Context, c *Client) error { return ignore(c.MFAEnroll(ctx)) }, "POST", "/api/v1/mfa/enroll", false, "{}", `{}`},
		{"MFAConfirm", func(ctx context.Context, c *Client) error { return c.MFAConfirm(ctx, "123456") }, "POST", "/api/v1/mfa/confirm", false, `"code":"123456"`, ``},
		{"MFADisable", func(ctx context.Context, c *Client) error { return c.MFADisable(ctx, "123456") }, "POST", "/api/v1/mfa/disable", false, `"code":"123456"`, ``},
		{"MFAStatus", func(ctx context.Context, c *Client) error { return ignore(c.MFAStatus(ctx)) }, "GET", "/api/v1/mfa/status", false, "", `{}`},
		{"SetPassword", func(ctx context.Context, c *Client) error { return c.SetPassword(ctx, "pw", "123456") }, "POST", "/api/v1/auth/password", false, `"password":"pw"`, ``},
		{"ClearPassword", func(ctx context.Context, c *Client) error { return c.ClearPassword(ctx, "123456") }, "DELETE", "/api/v1/auth/password", false, `"totp_code":"123456"`, ``},
		{"WebAuthnRegisterBegin", func(ctx context.Context, c *Client) error { return ignore(c.WebAuthnRegisterBegin(ctx)) }, "POST", "/api/v1/webauthn/register/begin", false, "{}", `{}`},
		{"WebAuthnRegisterFinish", func(ctx context.Context, c *Client) error {
			return ignore(c.WebAuthnRegisterFinish(ctx, map[string]interface{}{"id": "cred"}))
		}, "POST", "/api/v1/webauthn/register/finish", false, `"cred"`, `{"id":"c1"}`},
		{"WebAuthnCredentials", func(ctx context.Context, c *Client) error { return ignore(c.WebAuthnCredentials(ctx)) }, "GET", "/api/v1/webauthn/credentials", false, "", `null`},
		{"WebAuthnDeleteCredential", func(ctx context.Context, c *Client) error { return c.WebAuthnDeleteCredential(ctx, "c1") }, "DELETE", "/api/v1/webauthn/credentials/c1", false, "", ``},
		{"WebAuthnLoginBegin", func(ctx context.Context, c *Client) error { return ignore(c.WebAuthnLoginBegin(ctx, "alice")) }, "POST", "/api/v1/public/webauthn/login/begin", true, `"alice"`, `{}`},
		{"WebAuthnLoginFinish", func(ctx context.Context, c *Client) error {
			return ignore(c.WebAuthnLoginFinish(ctx, "alice", map[string]interface{}{}))
		}, "POST", "/api/v1/public/webauthn/login/finish", true, `"username":"alice"`, `{}`},
		{"ListSSHKeys", func(ctx context.Context, c *Client) error { return ignore(c.ListSSHKeys(ctx)) }, "GET", "/api/v1/ssh-keys", false, "", `null`},
		{"AddSSHKey", func(ctx context.Context, c *Client) error { return ignore(c.AddSSHKey(ctx, "yubi", "sk-ssh")) }, "POST", "/api/v1/ssh-keys", false, `"name":"yubi"`, `{}`},
		{"DeleteSSHKey", func(ctx context.Context, c *Client) error { return c.DeleteSSHKey(ctx, "k1") }, "DELETE", "/api/v1/ssh-keys/k1", false, "", ``},

		{"Logout", func(ctx context.Context, c *Client) error { return c.Logout(ctx) }, "POST", "/api/v1/logout", false, "", ``},
		{"CreateAPIKey", func(ctx context.Context, c *Client) error {
			return ignore(c.CreateAPIKey(ctx, CreateAPIKeyRequest{Name: "ci"}))
		}, "POST", "/api/v1/api-keys", false, `"ci"`, `{}`},
		{"ListAPIKeys", func(ctx context.Context, c *Client) error { return ignore(c.ListAPIKeys(ctx)) }, "GET", "/api/v1/api-keys", false, "", `{}`},
		{"RevokeAPIKey", func(ctx context.Context, c *Client) error { return c.RevokeAPIKey(ctx, "k1") }, "POST", "/api/v1/api-keys/k1/revoke", false, "", ``},
		{"DeleteAPIKey", func(ctx context.Context, c *Client) error { return c.DeleteAPIKey(ctx, "k1") }, "DELETE", "/api/v1/api-keys/k1", false, "", ``},
		{"ListUsers", func(ctx context.Context, c *Client) error { return ignore(c.ListUsers(ctx)) }, "GET", "/api/v1/users", false, "", `null`},
		{"GetUser", func(ctx context.Context, c *Client) error { return ignore(c.GetUser(ctx, "u1")) }, "GET", "/api/v1/users/u1", false, "", `{}`},
		{"ListAccessRequests", func(ctx context.Context, c *Client) error { return ignore(c.ListAccessRequests(ctx, "pending")) }, "GET", "/api/v1/access-requests?status=pending", false, "", `null`},
		{"ListAccessRequestsAll", func(ctx context.Context, c *Client) error { return ignore(c.ListAccessRequests(ctx, "")) }, "GET", "/api/v1/access-requests", false, "", `[]`},
		{"ListSessionsAll", func(ctx context.Context, c *Client) error { return ignore(c.ListSessions(ctx, "")) }, "GET", "/api/v1/sessions", false, "", `null`},
		{"ListActiveSessions", func(ctx context.Context, c *Client) error { return ignore(c.ListActiveSessions(ctx)) }, "GET", "/api/v1/sessions/active", false, "", `null`},
		{"GetSession", func(ctx context.Context, c *Client) error { return ignore(c.GetSession(ctx, "s1")) }, "GET", "/api/v1/sessions/s1", false, "", `{}`},
		{"GetSessionContent", func(ctx context.Context, c *Client) error { return ignore(c.GetSessionContent(ctx, "s1")) }, "GET", "/api/v1/sessions/s1/content", false, "", `raw`},
		{"ListAuditLogs", func(ctx context.Context, c *Client) error { return ignore(c.ListAuditLogs(ctx, 3)) }, "GET", "/api/v1/audit-logs?limit=3", false, "", `null`},
		{"ListAuditLogsAll", func(ctx context.Context, c *Client) error { return ignore(c.ListAuditLogs(ctx, 0)) }, "GET", "/api/v1/audit-logs", false, "", `[]`},
		{"ExportReportDefault", func(ctx context.Context, c *Client) error { return ignore(c.ExportReport(ctx, "access", "")) }, "GET", "/api/v1/reports/access/export", false, "", `csv`},
		{"ExportReportWithParams", func(ctx context.Context, c *Client) error {
			return ignore(c.ExportReportWithParams(ctx, "soc2-access", "csv", url.Values{"from": {"2026-07-01"}}))
		}, "GET", "/api/v1/reports/soc2-access/export?format=csv&from=2026-07-01", false, "", `csv`},
		{"GetUsageDashboardDefault", func(ctx context.Context, c *Client) error { return ignore(c.GetUsageDashboard(ctx, 0, 0)) }, "GET", "/api/v1/dashboard/usage", false, "", `{}`},
		{"ListNotifications", func(ctx context.Context, c *Client) error { return ignore(c.ListNotifications(ctx, 2)) }, "GET", "/api/v1/notifications?limit=2", false, "", `null`},
		{"ListNotificationsAll", func(ctx context.Context, c *Client) error { return ignore(c.ListNotifications(ctx, 0)) }, "GET", "/api/v1/notifications", false, "", `[]`},
		{"UnreadNotificationCount", func(ctx context.Context, c *Client) error { return ignore(c.UnreadNotificationCount(ctx)) }, "GET", "/api/v1/notifications/unread-count", false, "", `{"unread":3}`},
		{"MarkNotificationRead", func(ctx context.Context, c *Client) error { return c.MarkNotificationRead(ctx, "n1") }, "POST", "/api/v1/notifications/n1/read", false, "", ``},
		{"MarkAllNotificationsRead", func(ctx context.Context, c *Client) error { return c.MarkAllNotificationsRead(ctx) }, "POST", "/api/v1/notifications/read-all", false, "", ``},
		{"GetNotificationPrefs", func(ctx context.Context, c *Client) error { return ignore(c.GetNotificationPrefs(ctx)) }, "GET", "/api/v1/notifications/prefs", false, "", `{}`},
		{"PutNotificationPrefs", func(ctx context.Context, c *Client) error {
			return ignore(c.PutNotificationPrefs(ctx, NotificationPrefs{EmailEnabled: true}))
		}, "PUT", "/api/v1/notifications/prefs", false, `"email_enabled":true`, `{}`},
		{"GetNotificationPrefsWithBounds", func(ctx context.Context, c *Client) error { return ignore(c.GetNotificationPrefsWithBounds(ctx)) }, "GET", "/api/v1/notifications/prefs", false, "", `{}`},
		{"PutNotificationPrefsWithBounds", func(ctx context.Context, c *Client) error {
			return ignore(c.PutNotificationPrefsWithBounds(ctx, NotificationPrefs{}))
		}, "PUT", "/api/v1/notifications/prefs", false, "", `{}`},
		{"GetSetupStatus", func(ctx context.Context, c *Client) error { return ignore(c.GetSetupStatus(ctx)) }, "GET", "/api/v1/setup/status", false, "", `{}`},

		{"RedeemBrowserBootstrap", func(ctx context.Context, c *Client) error { return ignore(c.RedeemBrowserBootstrap(ctx, "abc")) }, "POST", "/api/v1/public/auth/browser-bootstrap/redeem", true, `"code":"abc"`, `{}`},
		{"RegisterAgent", func(ctx context.Context, c *Client) error {
			return ignore(c.RegisterAgent(ctx, RegisterAgentRequest{}))
		}, "POST", "/api/v1/public/register/agent", false, "", `{}`},
		{"RegisterClient", func(ctx context.Context, c *Client) error {
			return ignore(c.RegisterClient(ctx, RegisterClientRequest{}))
		}, "POST", "/api/v1/public/register/client", false, "", `{}`},
		{"ListFilesRoot", func(ctx context.Context, c *Client) error { return ignore(c.ListFiles(ctx, "m1", "", "")) }, "GET", "/api/v1/files/list?machine=m1", false, "", `{}`},
		{"DownloadFile", func(ctx context.Context, c *Client) error {
			return ignore(c.DownloadFile(ctx, "m1", "/etc/hosts", "root"))
		}, "GET", "/api/v1/files/download?machine=m1&path=%2Fetc%2Fhosts&user=root", false, "", `bytes`},
		{"DownloadFileDefaultUser", func(ctx context.Context, c *Client) error { return ignore(c.DownloadFile(ctx, "m1", "/x", "")) }, "GET", "/api/v1/files/download?machine=m1&path=%2Fx", false, "", `bytes`},
		{"MakeDir", func(ctx context.Context, c *Client) error { return c.MakeDir(ctx, "m1", "/tmp/d", "root") }, "POST", "/api/v1/files/mkdir", false, `"user":"root"`, ``},
		{"MakeDirDefaultUser", func(ctx context.Context, c *Client) error { return c.MakeDir(ctx, "m1", "/tmp/d", "") }, "POST", "/api/v1/files/mkdir", false, `"path":"/tmp/d"`, ``},
		{"DeleteFile", func(ctx context.Context, c *Client) error { return c.DeleteFile(ctx, "m1", "/tmp/f", "root") }, "DELETE", "/api/v1/files?machine=m1&path=%2Ftmp%2Ff&user=root", false, "", ``},
		{"DeleteFileDefaultUser", func(ctx context.Context, c *Client) error { return c.DeleteFile(ctx, "m1", "/tmp/f", "") }, "DELETE", "/api/v1/files?machine=m1&path=%2Ftmp%2Ff", false, "", ``},
		{"UploadFileDefaultUser", func(ctx context.Context, c *Client) error {
			return ignore(c.UploadFile(ctx, "m1", "/tmp", "", "a.txt", []byte("hi")))
		}, "POST", "/api/v1/files/upload", false, "", `{}`},
		{"IssueChallenge", func(ctx context.Context, c *Client) error { return ignore(c.IssueChallenge(ctx, "alice")) }, "POST", "/api/v1/public/auth/challenge", true, `"alice"`, `{"challenge":"c"}`},
	}
}

func TestEndpointContracts(t *testing.T) {
	rs := newRecordingServer(t)
	client, err := NewClient(rs.URL+"/", WithAPIKey(" key "), WithBearerToken("jwt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	for _, ep := range endpoints() {
		t.Run(ep.name, func(t *testing.T) {
			rs.reply(http.StatusOK, ep.response)
			if err := ep.call(ctx, client); err != nil {
				t.Fatalf("%s: %v", ep.name, err)
			}
			r := rs.last
			if r.Method != ep.method || r.URL.RequestURI() != ep.target {
				t.Errorf("sent %s %s, want %s %s", r.Method, r.URL.RequestURI(), ep.method, ep.target)
			}
			if !strings.Contains(rs.sent, ep.bodyHas) {
				t.Errorf("body %q lacks %q", rs.sent, ep.bodyHas)
			}
			hasAuth := r.Header.Get("X-API-Key") == "key" && r.Header.Get("Authorization") == "Bearer jwt"
			if ep.public && r.Header.Get("X-API-Key") != "" {
				t.Error("public endpoint sent the API key")
			}
			if !ep.public && !hasAuth {
				t.Errorf("authenticated endpoint headers = %v", r.Header)
			}
		})
	}
}

// Every method must hand a non-2xx answer back as *APIError instead of
// decoding the error body into an empty result.
func TestEndpointsSurfaceAPIErrors(t *testing.T) {
	rs := newRecordingServer(t)
	rs.reply(http.StatusForbidden, `{"error":"forbidden"}`)
	client, _ := NewClient(rs.URL, WithSessionToken("sess"))
	ctx := context.Background()

	for _, ep := range endpoints() {
		t.Run(ep.name, func(t *testing.T) {
			err := ep.call(ctx, client)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
				t.Fatalf("%s error = %v, want APIError 403", ep.name, err)
			}
			if !ep.public && rs.last.Header.Get("X-Session-Token") != "sess" {
				t.Errorf("session token not sent: %v", rs.last.Header)
			}
		})
	}
	for _, call := range []func() error{
		func() error { _, err := client.LoginWithSSHKey(ctx, "a", newTestSigner(t), ""); return err },
		func() error { _, err := client.LoginWithSSHSession(ctx, "a", newTestSigner(t), ""); return err },
		func() error { _, err := client.LoginWithSSHJWT(ctx, "a", newTestSigner(t), ""); return err },
		func() error { _, err := client.LoginWithPassword(ctx, "a", "p", ""); return err },
		func() error { _, err := client.GetMachineByName(ctx, "x"); return err },
		func() error { _, _, err := client.UpdatePluginConfig(ctx, "p", true, nil); return err },
		func() error { _, err := client.GetUsageDashboard(ctx, 1, 1); return err },
		func() error { _, err := client.ListCapabilities(ctx, "", ""); return err },
		func() error { _, err := client.GetGatewayInfo(ctx); return err },
		func() error { _, err := client.ListSessions(ctx, "active"); return err },
		func() error { _, err := client.ListNotificationTemplates(ctx); return err },
		func() error { _, err := client.PutNotificationTemplate(ctx, "e", NotificationTemplate{}); return err },
		func() error { _, err := client.GetNotificationPolicy(ctx); return err },
		func() error { _, err := client.PutNotificationPolicy(ctx, NotificationPolicy{}); return err },
		func() error { _, err := client.UploadFile(ctx, "m", "/", "root", "f", nil); return err },
		func() error { _, err := client.ListFiles(ctx, "m", "/a", "root"); return err },
	} {
		var apiErr *APIError
		if err := call(); !errors.As(err, &apiErr) {
			t.Errorf("call error = %v, want APIError", err)
		}
	}
}

func TestClientOptionsAndSetters(t *testing.T) {
	if _, err := NewClient("  "); err == nil {
		t.Error("NewClient with a blank URL succeeded")
	}
	custom := &http.Client{}
	c, err := NewClient("http://x", WithHTTPClient(custom), WithHTTPClient(nil), WithTimeout(7*time.Second), WithTimeout(0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.httpClient != custom || custom.Timeout != 7*time.Second {
		t.Errorf("options not applied: client=%p timeout=%v", c.httpClient, c.httpClient.Timeout)
	}

	c.SetAPIKey(" k ")
	c.SetSessionToken(" s ")
	c.SetBearerToken(" b ")
	if c.apiKey != "k" || c.sessionToken != "s" || c.bearerToken != "b" {
		t.Errorf("setters did not trim: %q %q %q", c.apiKey, c.sessionToken, c.bearerToken)
	}
}

func TestFullURL(t *testing.T) {
	c, _ := NewClient("https://gw.example.com")
	cases := map[string]string{
		"machines":                  "https://gw.example.com/api/v1/machines",
		"/api/v2/x":                 "https://gw.example.com/api/v2/x",
		"https://other.example/raw": "https://other.example/raw",
		"http://other.example/raw":  "http://other.example/raw",
	}
	for in, want := range cases {
		if got := c.fullURL(in); got != want {
			t.Errorf("fullURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPIError_Messages(t *testing.T) {
	var nilErr *APIError
	cases := map[string]error{
		"api error":                 nilErr,
		"api error (500): boom":     &APIError{StatusCode: 500, Message: "boom", Body: "raw"},
		"api error (502): raw text": &APIError{StatusCode: 502, Body: "raw text"},
		"api error (504)":           &APIError{StatusCode: 504},
	}
	for want, err := range cases {
		if got := err.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	}
}

func TestDoRequest_ErrorShapes(t *testing.T) {
	rs := newRecordingServer(t)
	c, _ := NewClient(rs.URL)
	ctx := context.Background()

	rs.reply(http.StatusBadRequest, `{"message":"bad input"}`)
	var apiErr *APIError
	if err := c.Do(ctx, "GET", "/x", nil, nil); !errors.As(err, &apiErr) || apiErr.Message != "bad input" {
		t.Errorf("message field = %v", err)
	}
	rs.reply(http.StatusBadGateway, `<html>gateway</html>`)
	if err := c.Do(ctx, "GET", "/x", nil, nil); !errors.As(err, &apiErr) || apiErr.Message != "" || apiErr.Body != "<html>gateway</html>" {
		t.Errorf("non-JSON error body = %+v", apiErr)
	}
	rs.reply(http.StatusOK, `not json`)
	var out map[string]any
	if err := c.Do(ctx, "GET", "/x", nil, &out); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("bad JSON = %v", err)
	}
	if err := c.Do(ctx, "GET", "/x", map[string]any{"ch": make(chan int)}, nil); err == nil || !strings.Contains(err.Error(), "marshal request") {
		t.Errorf("unencodable body = %v", err)
	}
	if err := c.Do(ctx, "BAD METHOD", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "create request") {
		t.Errorf("invalid method = %v", err)
	}

	// Callers that pass no context get context.Background rather than a panic.
	var noCtx context.Context
	rs.reply(http.StatusOK, `{}`)
	if err := c.Do(noCtx, "GET", "/x", nil, nil); err != nil {
		t.Errorf("Do with a nil context = %v", err)
	}
	rs.reply(http.StatusOK, `raw`)
	if b, err := c.GetSessionContent(noCtx, "s1"); err != nil || string(b) != "raw" {
		t.Errorf("GetSessionContent with a nil context = %q, %v", b, err)
	}
}
