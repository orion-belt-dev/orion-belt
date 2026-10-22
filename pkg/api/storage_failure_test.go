package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// With every storage call failing (authentication aside), each endpoint must
// answer with a handled error: a status of 400 or above and a JSON body with
// an "error" field. A 2xx would mean a failure was reported as success; a
// bare 500 with no body would mean a panic was caught by the recovery
// middleware.
func TestEveryEndpointReportsStorageFailure(t *testing.T) {
	e := newEnv(t, nil)
	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, e.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	wa, err := webauthn.New(&webauthn.Config{RPDisplayName: "x", RPID: waRPID, RPOrigins: []string{waOrigin}})
	if err != nil {
		t.Fatal(err)
	}
	broken := e.brokenServerWith(func(o *Options) {
		o.CA = authority
		o.WebAuthn = wa
	})
	e.bridge.machines = map[string]*common.Machine{e.machine.Name: e.machine}
	e.srv = broken

	// Left out on purpose: certificate issuance (the CA was bootstrapped on the
	// live store) and browser bootstrap codes (held in memory, not stored).
	m, u := e.machine.ID, e.alice.ID
	requests := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/users", nil},
		{"PUT", "/api/v1/admin/users/" + u, map[string]any{"email": "x"}},
		{"DELETE", "/api/v1/admin/users/" + u, nil},
		{"POST", "/api/v1/admin/users", map[string]any{"username": "z", "email": "z@x"}},
		{"GET", "/api/v1/machines", nil},
		{"GET", "/api/v1/machines/" + m, nil},
		{"POST", "/api/v1/admin/machines", map[string]any{"name": "n", "hostname": "h"}},
		{"PUT", "/api/v1/admin/machines/" + m, map[string]any{}},
		{"DELETE", "/api/v1/admin/machines/" + m, nil},
		{"GET", "/api/v1/permissions/user/" + u, nil},
		{"GET", "/api/v1/permissions/machine/" + m, nil},
		{"GET", "/api/v1/admin/permissions", nil},
		{"POST", "/api/v1/admin/permissions", map[string]any{"user_id": u, "machine_id": m, "access_type": "ssh", "remote_users": []string{"root"}}},
		{"PATCH", "/api/v1/admin/permissions/p", map[string]any{}},
		{"DELETE", "/api/v1/admin/permissions/p", nil},
		{"GET", "/api/v1/access-requests", nil},
		{"POST", "/api/v1/access-requests", map[string]any{"machine_id": m, "remote_users": []string{"root"}, "reason": "x"}},
		{"GET", "/api/v1/access-requests/r", nil},
		{"GET", "/api/v1/admin/access-requests/pending", nil},
		{"POST", "/api/v1/admin/access-requests/r/approve", nil},
		{"POST", "/api/v1/admin/access-requests/r/reject", nil},
		{"GET", "/api/v1/sessions", nil},
		{"GET", "/api/v1/sessions?status=active", nil},
		{"GET", "/api/v1/sessions/active", nil},
		{"GET", "/api/v1/sessions/s", nil},
		{"GET", "/api/v1/sessions/s/content", nil},
		{"GET", "/api/v1/sessions/s/watch", nil},
		{"GET", "/api/v1/audit-logs", nil},
		{"GET", "/api/v1/reports/sessions/export", nil},
		{"GET", "/api/v1/reports/audit/export", nil},
		{"GET", "/api/v1/dashboard/usage", nil},
		{"GET", "/api/v1/setup/status", nil},
		{"GET", "/api/v1/capabilities", nil},
		{"GET", "/api/v1/notifications", nil},
		{"GET", "/api/v1/notifications/unread-count", nil},
		{"POST", "/api/v1/notifications/n/read", nil},
		{"POST", "/api/v1/notifications/read-all", nil},
		{"GET", "/api/v1/notifications/prefs", nil},
		{"PUT", "/api/v1/notifications/prefs", map[string]any{}},
		{"GET", "/api/v1/admin/notifications/policy", nil},
		{"PUT", "/api/v1/admin/notifications/policy", map[string]any{}},
		{"GET", "/api/v1/admin/notifications/templates", nil},
		{"PUT", "/api/v1/admin/notifications/templates/access_request.approved", map[string]any{"title": "t", "body": "b"}},
		{"DELETE", "/api/v1/admin/notifications/templates/access_request.approved", nil},
		{"POST", "/api/v1/api-keys", map[string]any{"name": "k"}},
		{"GET", "/api/v1/api-keys", nil},
		{"POST", "/api/v1/api-keys/k/revoke", nil},
		{"DELETE", "/api/v1/api-keys/k", nil},
		{"GET", "/api/v1/ssh-keys", nil},
		{"POST", "/api/v1/ssh-keys", map[string]any{"name": "k", "public_key": testPubKey}},
		{"DELETE", "/api/v1/ssh-keys/k", nil},
		{"GET", "/api/v1/admin/ssh-certificates", nil},
		{"POST", "/api/v1/admin/ssh-certificates/1/revoke", nil},
		{"POST", "/api/v1/admin/agents/install-script", map[string]any{"name": "a", "os": "linux", "gateway_host": "g", "package_base_url": "p"}},
		{"PUT", "/api/v1/admin/plugins/x/config", map[string]any{}},
		{"GET", "/api/v1/terminal/ws?machine=web-01", nil},
		{"GET", "/api/v1/files/list?machine=web-01", nil},
		{"GET", "/api/v1/webauthn/credentials", nil},
		{"DELETE", "/api/v1/webauthn/credentials/c", nil},
		{"POST", "/api/v1/webauthn/register/begin", map[string]any{}},
		{"POST", "/api/v1/webauthn/register/finish", map[string]any{}},
		{"POST", "/api/v1/mfa/confirm", map[string]any{"code": "1"}},
		{"POST", "/api/v1/auth/password", map[string]any{"password": "long enough pw", "totp_code": "1"}},
		{"POST", "/api/v1/public/register/agent", map[string]any{"name": "a", "hostname": "h", "port": 22, "public_key": testPubKey}},
		{"POST", "/api/v1/public/register/client", map[string]any{"username": "z", "email": "z@x", "public_key": testPubKey}},
		{"POST", "/api/v1/public/login/key", map[string]any{"username": "alice", "public_key": testPubKey, "challenge": "c", "signature_format": "f", "signature": "s"}},
		{"POST", "/api/v1/public/login/password", map[string]any{"username": "alice", "password": "p"}},
		{"POST", "/api/v1/public/webauthn/login/begin", map[string]any{"username": "alice"}},
	}

	e.grant(e.alice, "root") // granted on the live store; the broken server cannot see it
	for _, rq := range requests {
		r := e.do(rq.method, rq.path, e.adminKey, rq.body)
		if r.code < 400 {
			t.Errorf("%s %s = %d with storage down: %s", rq.method, rq.path, r.code, r.body)
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(r.body, &body); err != nil || body["error"] == nil {
			t.Errorf("%s %s = %d with no error body (%q): a panic?", rq.method, rq.path, r.code, r.body)
		}
	}
	if strings.Contains(e.logs.String(), "panic") {
		t.Error("a handler panicked")
	}
}
