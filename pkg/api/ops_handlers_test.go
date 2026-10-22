package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/notify"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// --- notifications ------------------------------------------------------------------

func TestNotifications_InboxAndPrefs(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = e.store.CreateNotification(ctx, common.NewNotification(e.alice.ID, notify.EventAccessRequestApproved, "t", "b", nil))
	}
	_ = e.store.CreateNotification(ctx, common.NewNotification(e.bob.ID, notify.EventAccessRequestApproved, "t", "b", nil))

	list := e.do("GET", "/api/v1/notifications", e.aliceKey, nil).list(t)
	if len(list) != 3 {
		t.Fatalf("alice inbox = %d", len(list))
	}
	if l := e.do("GET", "/api/v1/notifications?limit=1", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("limit=1 = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/notifications?limit=999", e.adminKey, nil).list(t); l == nil || len(l) != 0 {
		t.Errorf("admin inbox = %v, want an empty list", l)
	}
	if n := e.do("GET", "/api/v1/notifications/unread-count", e.aliceKey, nil).obj(t)["unread"]; n != float64(3) {
		t.Errorf("unread = %v", n)
	}

	id := list[0]["id"].(string)
	e.expect(e.do("POST", "/api/v1/notifications/"+id+"/read", e.bobKey, nil), 404) // not bob's
	e.expect(e.do("POST", "/api/v1/notifications/"+id+"/read", e.aliceKey, nil), 200)
	e.expect(e.do("POST", "/api/v1/notifications/read-all", e.aliceKey, nil), 200)
	if n := e.do("GET", "/api/v1/notifications/unread-count", e.aliceKey, nil).obj(t)["unread"]; n != float64(0) {
		t.Errorf("unread after read-all = %v", n)
	}

	prefs := e.do("GET", "/api/v1/notifications/prefs", e.aliceKey, nil).obj(t)
	if prefs["in_app_enabled"] != true {
		t.Errorf("default prefs = %v", prefs)
	}
	e.expect(e.do("PUT", "/api/v1/notifications/prefs", e.aliceKey, `{`), 400)
	r := e.do("PUT", "/api/v1/notifications/prefs", e.aliceKey, map[string]any{"in_app_enabled": false, "email_enabled": true})
	e.expect(r, 200)
	if got := r.obj(t); got["email_enabled"] != true || got["adjusted"] != nil {
		t.Errorf("saved prefs = %v", got)
	}
}

func TestNotifications_PolicyClampsPrefs(t *testing.T) {
	e := newEnv(t, nil)

	got := e.do("GET", "/api/v1/admin/notifications/policy", e.adminKey, nil).obj(t)
	if len(got["known_channels"].([]any)) == 0 || len(got["known_events"].([]any)) == 0 {
		t.Errorf("policy catalog = %v", got)
	}
	e.expect(e.do("PUT", "/api/v1/admin/notifications/policy", e.adminKey, `{`), 400)
	r := e.do("PUT", "/api/v1/admin/notifications/policy", e.adminKey, map[string]any{"allowed_channels": []string{"in_app", "pager"}})
	e.expect(r, 400)
	if !strings.Contains(string(r.body), "pager") {
		t.Errorf("unknown channel error = %s", r.body)
	}
	e.expect(e.do("PUT", "/api/v1/admin/notifications/policy", e.aliceKey, map[string]any{}), 403)

	policy := map[string]any{
		"allowed_channels": []string{"in_app"},
		"mandatory_events": map[string][]string{notify.EventAccessRequestApproved: {"in_app"}},
	}
	e.expect(e.do("PUT", "/api/v1/admin/notifications/policy", e.adminKey, policy), 200)

	r = e.do("PUT", "/api/v1/notifications/prefs", e.aliceKey, map[string]any{"in_app_enabled": false, "email_enabled": true})
	e.expect(r, 200)
	saved := r.obj(t)
	if saved["adjusted"] != true || saved["email_enabled"] != false || saved["in_app_enabled"] != true {
		t.Errorf("clamped prefs = %v", saved)
	}
	if got := e.do("GET", "/api/v1/notifications/prefs", e.aliceKey, nil).obj(t); got["mandatory_events"] == nil {
		t.Errorf("prefs lack the mandatory events: %v", got)
	}
}

func TestNotifications_TemplatesThroughRouter(t *testing.T) {
	e := newEnv(t, nil)
	path := "/api/v1/admin/notifications/templates/" + notify.EventAccessRequestApproved

	var listed struct{ Templates []map[string]any }
	e.do("GET", "/api/v1/admin/notifications/templates", e.adminKey, nil).json(t, &listed)
	if len(listed.Templates) != len(notify.KnownEventTypes()) {
		t.Errorf("templates = %d", len(listed.Templates))
	}

	e.expect(e.do("PUT", path, e.adminKey, `{`), 400)
	e.expect(e.do("PUT", path, e.adminKey, map[string]any{"event_type": "other", "title": "x", "body": "y"}), 400)
	r := e.do("PUT", path, e.adminKey, map[string]any{"title": "Approved {{machine}}", "body": "You may now connect."})
	e.expect(r, 200)
	if r.obj(t)["customized"] != true {
		t.Errorf("put template = %s", r.body)
	}

	// The new copy applies to the next delivery.
	req := e.do("POST", "/api/v1/access-requests", e.aliceKey, map[string]any{"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "x"}).obj(t)
	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+req["id"].(string)+"/approve", e.adminKey, nil), 200)
	if l := e.do("GET", "/api/v1/notifications", e.aliceKey, nil).list(t); len(l) != 1 || l[0]["title"] != "Approved web-01" {
		t.Errorf("delivered = %v", l)
	}

	e.expect(e.do("DELETE", "/api/v1/admin/notifications/templates/nope", e.adminKey, nil), 400)
	e.expect(e.do("DELETE", path, e.adminKey, nil), 200)
	if !e.audited("notification_template.update") || !e.audited("notification_template.delete") {
		t.Errorf("audit = %v", e.auditActions())
	}
}

func TestNotifications_DisabledChannelSuppressesDelivery(t *testing.T) {
	e := newEnv(t, nil)
	e.expect(e.do("PUT", "/api/v1/notifications/prefs", e.aliceKey, map[string]any{"in_app_enabled": false}), 200)
	req := e.do("POST", "/api/v1/access-requests", e.aliceKey, map[string]any{"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "x"}).obj(t)
	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+req["id"].(string)+"/reject", e.adminKey, nil), 200)
	if n := e.do("GET", "/api/v1/notifications/unread-count", e.aliceKey, nil).obj(t)["unread"]; n != float64(0) {
		t.Errorf("notification delivered on a disabled channel: %v", n)
	}
}

func TestWebAuthnConfigNotice(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()

	e.srv.NotifyAdminsWebAuthnDisabled(ctx, "  ", "x") // blank error: nothing to report
	e.srv.NotifyAdminsWebAuthnDisabled(ctx, "rp_id must be a domain", "10.0.0.1")
	if n, _ := e.store.CountUnreadNotifications(ctx, e.admin.ID); n != 1 {
		t.Fatalf("admin notices = %d", n)
	}
	if n, _ := e.store.CountUnreadNotifications(ctx, e.alice.ID); n != 0 {
		t.Errorf("non-admin got the notice")
	}

	// Polling does not stack duplicates while one is unread...
	e.do("GET", "/api/v1/notifications/unread-count", e.adminKey, nil)
	e.do("GET", "/api/v1/notifications", e.adminKey, nil)
	if n, _ := e.store.CountUnreadNotifications(ctx, e.admin.ID); n != 1 {
		t.Errorf("notices after polling = %d", n)
	}
	// ...and reappears once the admin has read it, until the config is fixed.
	e.expect(e.do("POST", "/api/v1/notifications/read-all", e.adminKey, nil), 200)
	if n := e.do("GET", "/api/v1/notifications/unread-count", e.adminKey, nil).obj(t)["unread"]; n != float64(1) {
		t.Errorf("notice after read = %v", n)
	}
	var nilSrv *APIServer
	nilSrv.NotifyAdminsWebAuthnDisabled(ctx, "x", "y")
}

func TestExpireStaleAccessRequests(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	old := common.NewAccessRequest(e.alice.ID, e.machine.ID, []string{"root"}, "x", 60)
	old.RequestedAt = time.Now().Add(-10 * 24 * time.Hour)
	_ = e.store.CreateAccessRequest(ctx, old)

	e.srv.expireStaleAccessRequests(ctx)
	if r, _ := e.store.GetAccessRequest(ctx, old.ID); r.Status != "expired" {
		t.Errorf("status = %q", r.Status)
	}
	if !strings.Contains(e.logs.String(), "Expired 1 stale pending access requests") {
		t.Error("expiry not logged")
	}
	e.srv.expireStaleAccessRequests(ctx) // nothing left: no log line, no error
}

// --- plugins ----------------------------------------------------------------------------

type testPlugin struct {
	initErr error
	config  map[string]interface{}
}

func (p *testPlugin) Name() string    { return "test-plugin" }
func (p *testPlugin) Version() string { return "1.0.0" }
func (p *testPlugin) Initialize(_ context.Context, cfg map[string]interface{}) error {
	p.config = cfg
	return p.initErr
}
func (p *testPlugin) Shutdown(context.Context) error { return nil }
func (p *testPlugin) ConfigSchema() []plugin.ConfigField {
	return []plugin.ConfigField{{Key: "endpoint", Type: "string"}, {Key: "credential", Type: "string", Secret: true}}
}
func (p *testPlugin) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Path", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
	})
}

func TestPlugins_ConfigureEnableDisableAndWebhook(t *testing.T) {
	e := newEnv(t, nil)
	p := &testPlugin{}
	if err := e.plugins.Register(p); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/admin/plugins/test-plugin"

	var listed struct{ Plugins []map[string]any }
	e.do("GET", "/api/v1/admin/plugins", e.adminKey, nil).json(t, &listed)
	if len(listed.Plugins) != 1 {
		t.Errorf("plugins = %v", listed.Plugins)
	}

	e.expect(e.do("PUT", base+"/config", e.adminKey, `{`), 400)
	e.expect(e.do("PUT", "/api/v1/admin/plugins/ghost/config", e.adminKey, map[string]any{}), 404)
	r := e.do("PUT", base+"/config", e.adminKey, map[string]any{"enabled": true, "config": map[string]any{"endpoint": "https://x", "credential": "s3cr3t-value-123"}})
	e.expect(r, 200)
	cfg := r.obj(t)["plugin"].(map[string]any)["config"].(map[string]any)
	if cfg["credential"] == "s3cr3t-value-123" {
		t.Error("secret returned in clear")
	}

	// Resubmitting the masked placeholder keeps the stored secret.
	r = e.do("PUT", base+"/config", e.adminKey, map[string]any{"enabled": true, "config": map[string]any{"endpoint": "https://y", "credential": cfg["credential"]}})
	e.expect(r, 200)
	if p.config["credential"] != "s3cr3t-value-123" || p.config["endpoint"] != "https://y" {
		t.Errorf("plugin got %v", p.config)
	}

	// A config the plugin rejects is still saved and reported.
	p.initErr = errors.New("endpoint unreachable")
	r = e.do("PUT", base+"/config", e.adminKey, map[string]any{"enabled": false})
	e.expect(r, 200)
	if !strings.Contains(r.obj(t)["configure_error"].(string), "endpoint unreachable") {
		t.Errorf("configure error = %s", r.body)
	}
	p.initErr = nil
	e.expect(e.do("PUT", base+"/config", e.adminKey, map[string]any{"enabled": false, "config": map[string]any{"endpoint": "https://y"}}), 200)

	hook := e.do("POST", "/api/v1/public/plugins/test-plugin/callback", "", nil)
	e.expect(hook, 404) // disabled
	e.expect(e.do("POST", base+"/enable", e.adminKey, nil), 200)
	hook = e.do("POST", "/api/v1/public/plugins/test-plugin/callback", "", nil)
	e.expect(hook, 202)
	if hook.hdr.Get("X-Path") != "/callback" {
		t.Errorf("proxied path = %q", hook.hdr.Get("X-Path"))
	}
	e.expect(e.do("POST", "/api/v1/public/plugins/ghost/x", "", nil), 404)

	e.expect(e.do("POST", base+"/disable", e.adminKey, nil), 200)
	if s, _ := e.store.GetPluginSetting(context.Background(), "test-plugin"); s.Enabled || s.Config["endpoint"] != "https://y" {
		t.Errorf("disable lost the stored config: %+v", s)
	}
	e.expect(e.do("POST", "/api/v1/admin/plugins/ghost/enable", e.adminKey, nil), 404)
	for _, a := range []string{"plugin.configure", "plugin.enable", "plugin.disable"} {
		if !e.audited(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

func TestPlugins_EnableWithoutStoredSetting(t *testing.T) {
	e := newEnv(t, nil)
	_ = e.plugins.Register(&testPlugin{})
	e.expect(e.do("POST", "/api/v1/admin/plugins/test-plugin/disable", e.adminKey, nil), 200)
	if s, err := e.store.GetPluginSetting(context.Background(), "test-plugin"); err != nil || s.Enabled {
		t.Errorf("setting = %+v, %v", s, err)
	}
}

func TestPlugins_WithoutManager(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.pluginManager = nil
	var listed struct{ Plugins []any }
	e.do("GET", "/api/v1/admin/plugins", e.adminKey, nil).json(t, &listed)
	if listed.Plugins == nil || len(listed.Plugins) != 0 {
		t.Errorf("plugins = %v", listed.Plugins)
	}
	e.expect(e.do("PUT", "/api/v1/admin/plugins/x/config", e.adminKey, map[string]any{}), 503)
	e.expect(e.do("POST", "/api/v1/admin/plugins/x/enable", e.adminKey, nil), 503)
	e.expect(e.do("POST", "/api/v1/public/plugins/x/y", "", nil), 404)
}

// --- setup checklist ----------------------------------------------------------------------

func TestSetupStatus_WalksTheChecklist(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	next := func() (string, bool) {
		r := e.do("GET", "/api/v1/setup/status", e.adminKey, nil).obj(t)
		return r["next"].(string), r["complete"].(bool)
	}

	if n, done := next(); !strings.Contains(n, "Start the agent") || done {
		t.Errorf("no agents: next=%q complete=%v", n, done)
	}
	e.agents.connected = []string{e.machine.ID}
	if n, done := next(); !strings.Contains(n, "Grant users access") || !done {
		t.Errorf("no permissions: next=%q complete=%v", n, done)
	}
	_ = e.store.CreatePermission(ctx, common.NewPermission(e.alice.ID, e.machine.ID, "ssh", []string{"root"}, e.admin.ID, nil))
	if n, _ := next(); !strings.Contains(n, "You're set") {
		t.Errorf("all done: next=%q", n)
	}

	for _, u := range []*common.User{e.operator, e.auditor, e.alice, e.bob} {
		_ = e.store.DeleteUser(ctx, u.ID)
	}
	if n, _ := next(); !strings.Contains(n, "Register operators") {
		t.Errorf("no users: next=%q", n)
	}
	_ = e.store.DeleteMachine(ctx, e.machine.ID)
	if n, _ := next(); !strings.Contains(n, "Install orion-belt-agent") {
		t.Errorf("no machines: next=%q", n)
	}
	// A demoted last admin leaves the install without one.
	e.admin.Role, e.admin.IsAdmin = common.RoleOperator, false
	_ = e.store.UpdateUser(ctx, e.admin)
	if n, _ := next(); !strings.Contains(n, "Create an admin") {
		t.Errorf("no admin: next=%q", n)
	}
}

// --- usage dashboard and reports -----------------------------------------------------------

func TestUsageDashboard(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	// Hours back, not "now": TIMESTAMP columns read back shifted by the host's
	// UTC offset, which would push fresh rows past the end of the window.
	sess := common.NewSession(e.alice.ID, e.machine.ID, "root", "/r")
	sess.StartTime = time.Now().Add(-3 * time.Hour)
	_ = e.store.CreateSession(ctx, sess)
	req := common.NewAccessRequest(e.alice.ID, e.machine.ID, []string{"root"}, "x", 60)
	req.RequestedAt = time.Now().Add(-3 * time.Hour)
	_ = e.store.CreateAccessRequest(ctx, req)

	e.expect(e.do("GET", "/api/v1/dashboard/usage", e.aliceKey, nil), 403)
	r := e.do("GET", "/api/v1/dashboard/usage?window_hours=48&top=3", e.auditorKey, nil)
	e.expect(r, 200)
	var d usageDashboardResponse
	r.json(t, &d)
	if d.WindowHours != 48 || d.Volume.SessionsTotal != 1 || d.Volume.RequestsPending != 1 || len(d.TopTargets) != 1 || d.TopTargets[0].MachineName != "web-01" {
		t.Errorf("dashboard = %+v", d)
	}
}

func TestReports_FormatsAndScoping(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	s1 := common.NewSessionWithSource(e.alice.ID, e.machine.ID, "deploy", "/r", "web")
	s2 := common.NewSession(e.bob.ID, e.machine.ID, "root", "/r")
	s2.Status = "completed"
	end := time.Now()
	s2.EndTime = &end
	for _, s := range []*common.Session{s1, s2} {
		_ = e.store.CreateSession(ctx, s)
	}
	_ = e.store.CreateAuditLog(ctx, common.NewAuditLog(e.alice.ID, "auth.login", "user:a", "1.1.1.1", map[string]interface{}{"via": "key"}))
	_ = e.store.CreateAuditLog(ctx, common.NewAuditLog(e.bob.ID, "machine.create", "machine:m", "1.1.1.1", nil))

	e.expect(e.do("GET", "/api/v1/reports/bogus/export", e.adminKey, nil), 400)
	e.expect(e.do("GET", "/api/v1/reports/sessions/export?format=xlsx", e.adminKey, nil), 400)

	for _, report := range []string{"sessions", "audit", "audit-logs"} {
		for _, format := range []string{"json", "csv", "pdf"} {
			r := e.do("GET", "/api/v1/reports/"+report+"/export?format="+format, e.adminKey, nil)
			if r.code != 200 || !strings.Contains(r.hdr.Get("Content-Disposition"), "attachment") || len(r.body) == 0 {
				t.Errorf("%s/%s = %d %q", report, format, r.code, r.hdr.Get("Content-Disposition"))
			}
		}
	}

	count := func(path, key string) int {
		var env reportEnvelope
		e.do("GET", path, key, nil).json(t, &env)
		return env.Count
	}
	cases := []struct {
		path string
		key  string
		want int
	}{
		{"/api/v1/reports/sessions/export", e.adminKey, 2},
		{"/api/v1/reports/sessions/export", e.aliceKey, 1},
		{"/api/v1/reports/sessions/export?status=completed", e.adminKey, 1},
		{"/api/v1/reports/sessions/export?status=active", e.adminKey, 1},
		{"/api/v1/reports/sessions/export?q=deploy", e.adminKey, 1},
		{"/api/v1/reports/sessions/export?q=nothing-matches", e.adminKey, 0},
		{"/api/v1/reports/audit/export?action=auth.login", e.adminKey, 1},
		{"/api/v1/reports/audit/export?actor=bob", e.adminKey, 1},
		{"/api/v1/reports/audit/export?q=key", e.adminKey, 1},
		{"/api/v1/reports/audit/export?q=zzz", e.adminKey, 0},
		{"/api/v1/reports/audit/export", e.aliceKey, 1},
	}
	for _, tc := range cases {
		if got := count(tc.path, tc.key); got != tc.want {
			t.Errorf("%s = %d records, want %d", tc.path, got, tc.want)
		}
	}

	var env reportEnvelope
	e.do("GET", "/api/v1/reports/sessions/export?status=completed&limit=5", e.adminKey, nil).json(t, &env)
	if env.Filters["status"] != "completed" || env.Filters["limit"] != "5" {
		t.Errorf("filters echoed = %v", env.Filters)
	}
}

func TestParseLimit(t *testing.T) {
	cases := map[string]int{"": 10, "abc": 10, "-1": 10, "5": 5, "500": 100}
	for in, want := range cases {
		if got := parseLimit(in, 10, 100); got != want {
			t.Errorf("parseLimit(%q) = %d, want %d", in, got, want)
		}
	}
}
