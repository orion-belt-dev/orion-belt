package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/recording"
)

// --- users -----------------------------------------------------------------------------

func TestUsers_DirectoryVisibility(t *testing.T) {
	e := newEnv(t, nil)

	if l := e.do("GET", "/api/v1/users", e.adminKey, nil).list(t); len(l) != 5 {
		t.Errorf("admin sees %d users, want 5", len(l))
	}
	if l := e.do("GET", "/api/v1/users", e.auditorKey, nil).list(t); len(l) != 5 {
		t.Errorf("auditor sees %d users, want 5", len(l))
	}
	l := e.do("GET", "/api/v1/users", e.aliceKey, nil).list(t)
	if len(l) != 1 || l[0]["username"] != "alice" {
		t.Errorf("plain user directory = %v, want only themselves", l)
	}

	e.expect(e.do("GET", "/api/v1/users/"+e.alice.ID, e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/users/"+e.bob.ID, e.aliceKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/users/"+e.bob.ID, e.operatorKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/users/missing", e.adminKey, nil), 404)
}

func TestUsers_CreateUpdateDelete(t *testing.T) {
	e := newEnv(t, nil)

	e.expect(e.do("POST", "/api/v1/admin/users", e.adminKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/admin/users", e.adminKey, map[string]any{"username": "x", "email": "x@x", "role": "god"}), 400)
	e.expect(e.do("POST", "/api/v1/admin/users", e.operatorKey, map[string]any{"username": "x", "email": "x@x", "is_admin": true}), 403)
	e.expect(e.do("POST", "/api/v1/admin/users", e.aliceKey, map[string]any{"username": "x", "email": "x@x"}), 403)

	r := e.do("POST", "/api/v1/admin/users", e.operatorKey, map[string]any{"username": "carol", "email": "c@x", "role": common.RoleAuditor})
	e.expect(r, 201)
	carol := r.obj(t)
	if carol["role"] != common.RoleAuditor || carol["is_admin"] != false {
		t.Errorf("created user = %v", carol)
	}
	e.expect(e.do("POST", "/api/v1/admin/users", e.adminKey, map[string]any{"username": "dave", "email": "d@x", "is_admin": true}), 201)
	e.expect(e.do("POST", "/api/v1/admin/users", e.adminKey, map[string]any{"username": "carol", "email": "c@x"}), 500)
	if !e.audited("user.create") {
		t.Error("user creation not audited")
	}

	id := carol["id"].(string)
	updates := []struct {
		name string
		key  string
		body any
		code int
		role string
	}{
		{"bad json", e.adminKey, `{`, 400, ""},
		{"invalid role", e.adminKey, map[string]any{"role": "god"}, 400, ""},
		{"operator cannot grant admin", e.operatorKey, map[string]any{"role": "admin"}, 403, ""},
		{"operator cannot touch admins", e.operatorKey, map[string]any{"email": "x"}, 0, ""},
		{"email and key", e.operatorKey, map[string]any{"email": "new@x", "public_key": "ssh-ed25519 AAA"}, 200, common.RoleAuditor},
		{"promote by role", e.adminKey, map[string]any{"role": "admin"}, 200, common.RoleAdmin},
		{"demote by flag", e.adminKey, map[string]any{"is_admin": false}, 200, common.RoleUser},
		{"promote by flag", e.adminKey, map[string]any{"is_admin": true}, 200, common.RoleAdmin},
	}
	for _, u := range updates {
		target := id
		if u.code == 0 {
			target, u.code = e.admin.ID, 403
		}
		r := e.do("PUT", "/api/v1/admin/users/"+target, u.key, u.body)
		if r.code != u.code {
			t.Errorf("%s: status %d, want %d: %s", u.name, r.code, u.code, r.body)
			continue
		}
		if u.role != "" && r.obj(t)["role"] != u.role {
			t.Errorf("%s: role %v, want %s", u.name, r.obj(t)["role"], u.role)
		}
	}
	e.expect(e.do("PUT", "/api/v1/admin/users/missing", e.adminKey, map[string]any{}), 404)

	e.expect(e.do("DELETE", "/api/v1/admin/users/"+e.admin.ID, e.operatorKey, nil), 403)
	e.expect(e.do("DELETE", "/api/v1/admin/users/"+e.bob.ID, e.operatorKey, nil), 200)
	if _, err := e.store.GetUser(context.Background(), e.bob.ID); err == nil {
		t.Error("deleted user still exists")
	}
	e.expect(e.do("DELETE", "/api/v1/admin/users/missing", e.adminKey, nil), 500)
	if !e.audited("user.delete") || !e.audited("user.update") {
		t.Errorf("audit trail = %v", e.auditActions())
	}
}

// --- machines --------------------------------------------------------------------------

func TestMachines_CRUD(t *testing.T) {
	e := newEnv(t, nil)

	if l := e.do("GET", "/api/v1/machines", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("machines = %v", l)
	}
	e.expect(e.do("GET", "/api/v1/machines/"+e.machine.ID, e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/machines/missing", e.aliceKey, nil), 404)

	e.expect(e.do("POST", "/api/v1/admin/machines", e.adminKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/admin/machines", e.adminKey, map[string]any{"name": "web-01", "hostname": "h"}), 409)
	r := e.do("POST", "/api/v1/admin/machines", e.operatorKey, map[string]any{"name": "db-01", "hostname": "10.0.0.2", "is_active": true, "tags": map[string]string{"role": "db"}})
	e.expect(r, 201)
	db := r.obj(t)
	if db["port"] != float64(22) || db["is_active"] != true {
		t.Errorf("created machine = %v", db)
	}
	dbID := db["id"].(string)

	e.expect(e.do("PUT", "/api/v1/admin/machines/missing", e.adminKey, map[string]any{}), 404)
	e.expect(e.do("PUT", "/api/v1/admin/machines/"+dbID, e.adminKey, `{`), 400)
	e.expect(e.do("PUT", "/api/v1/admin/machines/"+dbID, e.adminKey, map[string]any{"name": "web-01"}), 409)
	r = e.do("PUT", "/api/v1/admin/machines/"+dbID, e.adminKey, map[string]any{
		"name": "db-02", "hostname": "10.0.0.3", "port": 2200, "tags": map[string]string{"a": "b"}, "agent_id": "x", "is_active": false,
	})
	e.expect(r, 200)
	if m := r.obj(t); m["name"] != "db-02" || m["port"] != float64(2200) || m["agent_id"] != "x" || m["is_active"] != false {
		t.Errorf("updated machine = %v", m)
	}

	e.expect(e.do("DELETE", "/api/v1/admin/machines/missing", e.adminKey, nil), 404)
	r = e.do("DELETE", "/api/v1/admin/machines/"+dbID+"?archive=true", e.adminKey, nil)
	e.expect(r, 200)
	if _, err := e.store.GetMachine(context.Background(), dbID); err != nil {
		t.Error("archiving deleted the machine")
	}
	for _, a := range []string{"machine.create", "machine.update", "machine.archive"} {
		if !e.audited(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

func TestMachines_HardDeleteRemovesAgentUserAndRecordings(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	agentUser := common.NewUser("web-01-agent", "a@x", testPubKey, false)
	_ = e.store.CreateUser(ctx, agentUser)
	e.machine.AgentID = agentUser.ID
	_ = e.store.UpdateMachine(ctx, e.machine)

	dir := t.TempDir()
	cast := writeFile(t, dir, "s.cast", "x")
	writeFile(t, dir, "s.cast.gz", "x")
	writeFile(t, dir, "s.cast.enc", "x")
	sess := common.NewSession(e.alice.ID, e.machine.ID, "root", dir)
	sess.RecordingPath = cast
	_ = e.store.CreateSession(ctx, sess)

	e.expect(e.do("DELETE", "/api/v1/admin/machines/"+e.machine.ID, e.adminKey, nil), 200)
	if _, err := e.store.GetUser(ctx, agentUser.ID); err == nil {
		t.Error("agent user survived machine deletion")
	}
	for _, f := range []string{cast, cast + ".gz", cast + ".enc"} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived machine deletion", filepath.Base(f))
		}
	}
	if len(e.agents.disconnected) != 1 || e.agents.disconnected[0] != e.machine.ID {
		t.Errorf("agent not disconnected: %v", e.agents.disconnected)
	}
}

// --- permissions -----------------------------------------------------------------------

func TestPermissions_GrantUpdateRevoke(t *testing.T) {
	e := newEnv(t, nil)
	base := map[string]any{"user_id": e.alice.ID, "machine_id": e.machine.ID, "access_type": "ssh"}
	with := func(kv ...any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, base), 400) // no remote users
	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, with("remote_users", []string{"root"}, "expires_at", "tomorrow")), 400)
	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, with("remote_users", []string{"root"}, "expires_at", time.Now().Add(-time.Hour).Format(time.RFC3339))), 400)

	r := e.do("POST", "/api/v1/admin/permissions", e.adminKey, with("remote_users", []string{"root"}, "expires_at", time.Now().Add(time.Hour).Format(time.RFC3339)))
	e.expect(r, 201)
	perm := r.obj(t)
	if perm["access_type"] != "ssh" || perm["expires_at"] == nil {
		t.Errorf("granted = %v", perm)
	}
	r = e.do("POST", "/api/v1/admin/permissions", e.adminKey, map[string]any{
		"user_id": e.bob.ID, "machine_id": e.machine.ID, "access_type": "scp", "remote_users": []string{"deploy"}, "duration_seconds": 600,
	})
	e.expect(r, 201)

	// A grant for a missing user cannot be stored.
	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, map[string]any{
		"user_id": "ghost", "machine_id": e.machine.ID, "access_type": "ssh", "remote_users": []string{"root"},
	}), 500)

	if l := e.do("GET", "/api/v1/admin/permissions?limit=1", e.adminKey, nil).list(t); len(l) != 1 {
		t.Errorf("limit=1 returned %d", len(l))
	}
	if l := e.do("GET", "/api/v1/admin/permissions?limit=99999", e.adminKey, nil).list(t); len(l) != 2 {
		t.Errorf("all permissions = %d", len(l))
	}

	// Visibility: users see their own grants only.
	e.expect(e.do("GET", "/api/v1/permissions/user/"+e.bob.ID, e.aliceKey, nil), 403)
	if l := e.do("GET", "/api/v1/permissions/user/"+e.alice.ID, e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("own grants = %v", l)
	}
	if l := e.do("GET", "/api/v1/permissions/machine/"+e.machine.ID, e.aliceKey, nil).list(t); len(l) != 1 || l[0]["user_id"] != e.alice.ID {
		t.Errorf("machine grants for alice = %v", l)
	}
	if l := e.do("GET", "/api/v1/permissions/machine/"+e.machine.ID, e.auditorKey, nil).list(t); len(l) != 2 {
		t.Errorf("machine grants for auditor = %d", len(l))
	}

	id := perm["id"].(string)
	e.expect(e.do("PATCH", "/api/v1/admin/permissions/"+id, e.adminKey, `{`), 400)
	e.expect(e.do("PATCH", "/api/v1/admin/permissions/missing", e.adminKey, map[string]any{}), 404)
	e.expect(e.do("PATCH", "/api/v1/admin/permissions/"+id, e.adminKey, map[string]any{"expires_at": "soon"}), 400)
	steps := []struct {
		body    map[string]any
		expires bool
	}{
		{map[string]any{"access_type": "both", "remote_users": []string{"root", "deploy"}, "duration_seconds": 60}, true},
		{map[string]any{"duration_seconds": 0}, false},
		{map[string]any{"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}, true},
		{map[string]any{"expires_at": ""}, false},
	}
	for i, st := range steps {
		r := e.do("PATCH", "/api/v1/admin/permissions/"+id, e.adminKey, st.body)
		e.expect(r, 200)
		if got := r.obj(t)["expires_at"] != nil; got != st.expires {
			t.Errorf("step %d: has expiry = %v, want %v", i, got, st.expires)
		}
	}

	e.expect(e.do("DELETE", "/api/v1/admin/permissions/"+id, e.adminKey, nil), 200)
	e.expect(e.do("DELETE", "/api/v1/admin/permissions/"+id, e.adminKey, nil), 500)
	for _, a := range []string{"permission.grant", "permission.update", "permission.revoke"} {
		if !e.audited(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

// --- access requests --------------------------------------------------------------------

func TestAccessRequests_Lifecycle(t *testing.T) {
	e := newEnv(t, nil)
	create := func(key string, body any) resp { return e.do("POST", "/api/v1/access-requests", key, body) }

	e.expect(create(e.aliceKey, `{`), 400)
	e.expect(create(e.aliceKey, map[string]any{"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "x", "duration": -1}), 400)
	e.expect(create(e.aliceKey, map[string]any{"machine_id": "ghost", "remote_users": []string{"root"}, "reason": "x"}), 500)

	r := create(e.aliceKey, map[string]any{"machine_id": e.machine.ID, "remote_users": []string{"deploy"}, "reason": "deploy"})
	e.expect(r, 201)
	req1 := r.obj(t)
	if req1["duration"] != float64(DefaultAccessRequestTTLSeconds) || req1["access_type"] != "both" {
		t.Errorf("defaults = %v", req1)
	}
	r = create(e.bobKey, map[string]any{"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "debug", "access_type": "ssh", "duration": 0})
	e.expect(r, 201)
	req2 := r.obj(t)

	id1, id2 := req1["id"].(string), req2["id"].(string)
	e.expect(e.do("GET", "/api/v1/access-requests/"+id1, e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/access-requests/"+id2, e.aliceKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/access-requests/"+id2, e.auditorKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/access-requests/missing", e.adminKey, nil), 404)
	if l := e.do("GET", "/api/v1/access-requests", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("alice's requests = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/access-requests", e.adminKey, nil).list(t); len(l) != 2 {
		t.Errorf("all requests = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/admin/access-requests/pending", e.operatorKey, nil).list(t); len(l) != 2 {
		t.Errorf("pending = %d", len(l))
	}

	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+id1+"/approve", e.adminKey, map[string]any{"reviewer_id": e.operator.ID}), 403)
	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+id1+"/approve", e.adminKey, map[string]any{"duration": -5}), 400)
	r = e.do("POST", "/api/v1/admin/access-requests/"+id1+"/approve", e.adminKey, map[string]any{"duration": 120, "reviewer_id": e.admin.ID})
	e.expect(r, 200)
	if got := r.obj(t); got["status"] != "approved" || got["duration"] != float64(120) {
		t.Errorf("approved = %v", got)
	}
	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+id1+"/approve", e.adminKey, nil), 500) // not pending

	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+id2+"/reject", e.adminKey, map[string]any{"reviewer_id": "someone"}), 403)
	r = e.do("POST", "/api/v1/admin/access-requests/"+id2+"/reject", e.operatorKey, nil)
	e.expect(r, 200)
	if r.obj(t)["status"] != "rejected" {
		t.Errorf("rejected = %s", r.body)
	}
	e.expect(e.do("POST", "/api/v1/admin/access-requests/missing/reject", e.adminKey, nil), 500)

	ctx := context.Background()
	if n, _ := e.store.CountUnreadNotifications(ctx, e.alice.ID); n != 1 {
		t.Errorf("alice notifications = %d, want the approval", n)
	}
	if n, _ := e.store.CountUnreadNotifications(ctx, e.bob.ID); n != 1 {
		t.Errorf("bob notifications = %d, want the rejection", n)
	}
	if ok, _ := e.store.HasPermissionWithRemoteUser(ctx, e.alice.ID, e.machine.ID, "ssh", "deploy"); !ok {
		t.Error("approval did not grant access")
	}
	for _, a := range []string{"access.request", "access.approve", "access.reject"} {
		if !e.audited(a) {
			t.Errorf("%s not audited", a)
		}
	}
}

// --- sessions -----------------------------------------------------------------------------

func TestSessions_ListingAndPlayback(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	dir := t.TempDir()

	mk := func(user *common.User, status, path string) *common.Session {
		s := common.NewSession(user.ID, e.machine.ID, "root", dir)
		s.Status = status
		s.RecordingPath = path
		if err := e.store.CreateSession(ctx, s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	plain := mk(e.alice, "completed", writeFile(t, dir, "plain.cast", "plain cast"))
	var gz bytes.Buffer
	gz.WriteString("OBGZ1\n")
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("gzipped cast"))
	_ = zw.Close()
	zipped := mk(e.alice, "active", writeFile(t, dir, "zipped.cast", gz.String()))
	noRec := mk(e.bob, "active", "")
	missing := mk(e.bob, "completed", filepath.Join(dir, "gone.txt"))
	corrupt := mk(e.bob, "completed", writeFile(t, dir, "bad.cast", "OBGZ1\nnot gzip"))

	if l := e.do("GET", "/api/v1/sessions", e.auditorKey, nil).list(t); len(l) != 5 {
		t.Errorf("auditor sessions = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/sessions", e.aliceKey, nil).list(t); len(l) != 2 {
		t.Errorf("alice sessions = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/sessions?status=completed", e.adminKey, nil).list(t); len(l) != 3 {
		t.Errorf("completed = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/sessions?status=active", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("alice active (status filter) = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/sessions/active", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("alice active = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/sessions/active", e.adminKey, nil).list(t); len(l) != 2 {
		t.Errorf("all active = %d", len(l))
	}

	e.expect(e.do("GET", "/api/v1/sessions/"+plain.ID, e.aliceKey, nil), 200)
	e.expect(e.do("GET", "/api/v1/sessions/"+noRec.ID, e.aliceKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/missing", e.adminKey, nil), 404)

	r := e.do("GET", "/api/v1/sessions/"+plain.ID+"/content", e.aliceKey, nil)
	e.expect(r, 200)
	if string(r.body) != "plain cast" || r.hdr.Get("Content-Type") != "application/x-asciicast" {
		t.Errorf("plain playback = %q %s", r.body, r.hdr.Get("Content-Type"))
	}
	if r := e.do("GET", "/api/v1/sessions/"+zipped.ID+"/content", e.adminKey, nil); string(r.body) != "gzipped cast" {
		t.Errorf("gzip playback = %q", r.body)
	}
	e.expect(e.do("GET", "/api/v1/sessions/"+noRec.ID+"/content", e.aliceKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/missing/content", e.aliceKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/"+noRec.ID+"/content", e.adminKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/"+missing.ID+"/content", e.adminKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/"+corrupt.ID+"/content", e.adminKey, nil), 500)
	if !e.audited("session.playback") {
		t.Error("playback not audited")
	}
}

func TestSessions_EncryptedPlayback(t *testing.T) {
	crypt, _ := recording.NewCrypto("recording-key", nil)
	e := newEnv(t, func(o *Options) { o.RecordingCrypt = crypt })
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.txt")
	if err := crypt.EncryptAndWrite(path, []byte("secret output")); err != nil {
		t.Fatal(err)
	}
	s := common.NewSession(e.alice.ID, e.machine.ID, "root", dir)
	s.RecordingPath = path
	_ = e.store.CreateSession(context.Background(), s)

	r := e.do("GET", "/api/v1/sessions/"+s.ID+"/content", e.adminKey, nil)
	e.expect(r, 200)
	if string(r.body) != "secret output" || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/plain") {
		t.Errorf("decrypted playback = %q", r.body)
	}

	_ = os.WriteFile(path, []byte("OBENC1\ngarbage"), 0o600)
	e.expect(e.do("GET", "/api/v1/sessions/"+s.ID+"/content", e.adminKey, nil), 500)
}

// --- audit logs -------------------------------------------------------------------------

func TestAuditLogs_ScopedAndLimited(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = e.store.CreateAuditLog(ctx, common.NewAuditLog(e.alice.ID, "login", "x", "1.1.1.1", nil))
	}
	_ = e.store.CreateAuditLog(ctx, common.NewAuditLog(e.bob.ID, "login", "x", "1.1.1.1", nil))

	if l := e.do("GET", "/api/v1/audit-logs", e.aliceKey, nil).list(t); len(l) != 3 {
		t.Errorf("alice's audit entries = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/audit-logs?limit=2", e.adminKey, nil).list(t); len(l) != 2 {
		t.Errorf("limit=2 = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/audit-logs?limit=9999", e.adminKey, nil).list(t); len(l) != 4 {
		t.Errorf("clamped limit = %d", len(l))
	}
}

// --- registration and key login -------------------------------------------------------------

func TestRegisterAgent(t *testing.T) {
	e := newEnv(t, nil)
	body := map[string]any{"name": "app-01", "hostname": "10.0.0.5", "port": 22, "public_key": testPubKey}

	e.expect(e.do("POST", "/api/v1/public/register/agent", "", body), 401)
	e.expect(e.do("POST", "/api/v1/public/register/agent", e.aliceKey, body), 403)
	e.expect(e.do("POST", "/api/v1/public/register/agent", e.operatorKey, `{`), 400)
	r := e.do("POST", "/api/v1/public/register/agent", e.operatorKey, body)
	e.expect(r, 201)
	if got := r.obj(t); got["user_id"] == nil || got["machine_id"] == nil {
		t.Errorf("legacy registration = %v", got)
	}
	e.expect(e.do("POST", "/api/v1/public/register/agent", e.operatorKey, body), 409)

	// The synthetic agent user name clashes: the machine is not created.
	_ = e.store.CreateUser(context.Background(), common.NewUser("clash", "c@x", testPubKey, false))
	e.expect(e.do("POST", "/api/v1/public/register/agent", e.adminKey, map[string]any{"name": "clash", "hostname": "h", "port": 22, "public_key": testPubKey}), 500)
}

func TestRegisterAgent_WithSSHCA(t *testing.T) {
	e := newEnv(t, nil)
	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, e.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.ca = authority

	r := e.do("POST", "/api/v1/public/register/agent", e.adminKey, map[string]any{"name": "app-02", "hostname": "h", "port": 22, "public_key": testPubKey})
	e.expect(r, 201)
	got := r.obj(t)
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(got["host_certificate"].(string)))
	if err != nil {
		t.Fatalf("host certificate: %v", err)
	}
	if cert, ok := pub.(*ssh.Certificate); !ok || cert.CertType != ssh.HostCert || got["host_ca_public_key"] == "" {
		t.Errorf("registration = %v", got)
	}

	e.expect(e.do("POST", "/api/v1/public/register/agent", e.adminKey, map[string]any{"name": "app-03", "hostname": "h", "port": 22, "public_key": "garbage"}), 400)
	if _, err := e.store.GetMachineByName(context.Background(), "app-03"); err == nil {
		t.Error("machine kept after a rejected registration")
	}
}

func TestRegisterClient(t *testing.T) {
	e := newEnv(t, nil)
	body := func(name string, admin bool) map[string]any {
		return map[string]any{"username": name, "email": name + "@x", "public_key": testPubKey, "is_admin": admin}
	}

	e.expect(e.do("POST", "/api/v1/public/register/client", "", `{`), 400)
	e.expect(e.do("POST", "/api/v1/public/register/client", "", body("anon", false)), 401)
	e.expect(e.do("POST", "/api/v1/public/register/client", "", body("anon", true)), 401) // already bootstrapped
	e.expect(e.do("POST", "/api/v1/public/register/client", e.aliceKey, body("x", false)), 403)
	e.expect(e.do("POST", "/api/v1/public/register/client", e.operatorKey, body("x", true)), 403)
	e.expect(e.do("POST", "/api/v1/public/register/client", e.operatorKey, body("alice", false)), 409)
	e.expect(e.do("POST", "/api/v1/public/register/client", e.operatorKey, body("eve", false)), 201)

	// First-run bootstrap: with no accounts an anonymous admin may register.
	resetTables(t)
	r := e.do("POST", "/api/v1/public/register/client", "", body("root", true))
	e.expect(r, 201)
	if !strings.Contains(string(r.body), "Bootstrap admin") || !e.audited("user.bootstrap") {
		t.Errorf("bootstrap = %s; audit %v", r.body, e.auditActions())
	}
}

// keyLogin signs a fresh challenge with signer and posts it to /login/key.
func (e *env) keyLogin(username string, signer ssh.Signer, pubLine, totp string) resp {
	e.t.Helper()
	ch := e.do("POST", "/api/v1/public/auth/challenge", "", map[string]any{"username": username}).obj(e.t)["challenge"].(string)
	sig, err := signer.Sign(nil, []byte(ch))
	if err != nil {
		sig = &ssh.Signature{Format: "x", Blob: []byte("x")}
	}
	return e.do("POST", "/api/v1/public/login/key", "", map[string]any{
		"username": username, "public_key": pubLine, "challenge": ch,
		"signature_format": sig.Format, "signature": base64.StdEncoding.EncodeToString(sig.Blob), "totp_code": totp,
	})
}

func TestLoginWithKey(t *testing.T) {
	e := newEnv(t, nil)
	signer := genSigner(t)
	pubLine := string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	carol := common.NewUser("carol", "c@x", pubLine, false)
	_ = e.store.CreateUser(context.Background(), carol)

	e.expect(e.do("POST", "/api/v1/public/login/key", "", `{`), 400)
	e.expect(e.keyLogin("nobody", signer, pubLine, ""), 401)
	e.expect(e.keyLogin("carol", signer, testPubKey, ""), 401)    // key not on file
	e.expect(e.keyLogin("carol", genSigner(t), pubLine, ""), 401) // wrong private key

	r := e.keyLogin("carol", signer, pubLine, "")
	e.expect(r, 200)
	key := r.obj(t)["api_key"].(string)
	if me := e.do("GET", "/api/v1/auth/me", key, nil); me.code != 200 || !strings.Contains(string(me.body), "carol") {
		t.Errorf("issued key does not authenticate: %d %s", me.code, me.body)
	}

	// A stored key that is not a valid authorized_keys line cannot be verified.
	broken := common.NewUser("broken", "b@x", "ssh-ed25519 notbase64", false)
	_ = e.store.CreateUser(context.Background(), broken)
	e.expect(e.keyLogin("broken", signer, "ssh-ed25519 notbase64", ""), 400)
}

// --- authentication middleware ------------------------------------------------------------

func TestAuthentication_Methods(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()

	e.expect(e.do("GET", "/api/v1/auth/me", "", nil), 401)
	e.expect(e.do("GET", "/api/v1/auth/me", "wrong-key", nil), 401)

	_, token, err := e.srv.authService.CreateSession(ctx, e.alice.ID, "1.1.1.1", "ua", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", token), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Cookie", "session_token="+token), 200)
	e.expect(e.do("GET", "/api/v1/auth/me?token="+token, "", nil), 200)

	jwt, _, err := e.srv.jwt.Issue(e.alice.ID, "alice", false)
	if err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Authorization", "Bearer "+jwt), 200)
	e.expect(e.do("GET", "/api/v1/auth/me?access_token="+jwt, "", nil), 200)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Authorization", "Bearer forged.token.here"), 401)

	ghostJWT, _, _ := e.srv.jwt.Issue("ghost", "ghost", false)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "Authorization", "Bearer "+ghostJWT), 401)

	// Two days back: expires_at is a TIMESTAMP without time zone, so a
	// recently expired key reads back shifted by the host's UTC offset.
	past := time.Now().Add(-48 * time.Hour)
	_, rawExpired, _ := e.srv.authService.GenerateAPIKey(ctx, e.alice.ID, "old", &past)
	e.expect(e.do("GET", "/api/v1/auth/me", rawExpired, nil), 401)

	k, rawRevoked, _ := e.srv.authService.GenerateAPIKey(ctx, e.alice.ID, "gone", nil)
	_ = e.store.RevokeAPIKey(ctx, k.ID)
	e.expect(e.do("GET", "/api/v1/auth/me", rawRevoked, nil), 401)

	// Expired session tokens are rejected and cleaned up.
	sess := common.NewHTTPSession(e.alice.ID, hashSessionToken("old-token"), "1.1.1.1", "ua", time.Now().Add(-48*time.Hour))
	_ = e.store.CreateHTTPSession(ctx, sess)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", "old-token"), 401)

	// Credentials for an account that no longer exists stop working.
	_, rawBob, _ := e.srv.authService.GenerateAPIKey(ctx, e.bob.ID, "b", nil)
	_, bobToken, _ := e.srv.authService.CreateSession(ctx, e.bob.ID, "1.1.1.1", "ua", time.Hour)
	_ = e.store.DeleteUser(ctx, e.bob.ID)
	e.expect(e.do("GET", "/api/v1/auth/me", rawBob, nil), 401)
	e.expect(e.do("GET", "/api/v1/auth/me", "", nil, "X-Session-Token", bobToken), 401)

	e.expect(e.do("GET", "/api/v1/admin/permissions", e.aliceKey, nil), 403)
}

func TestPublicEndpoints(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/health", "/api/v1/version", "/api/v1/openapi.yaml", "/api/v1/openapi.json", "/metrics", "/api/v1/gateway-info"} {
		if r := e.do("GET", p, "", nil); r.code != 200 {
			t.Errorf("GET %s = %d", p, r.code)
		}
	}
	info := e.do("GET", "/api/v1/gateway-info", "", nil).obj(t)
	if info["ui_url"] != "https://gw.example.com/ui" {
		t.Errorf("gateway-info = %v", info)
	}
	e.expect(e.do("POST", "/api/v1/public/auth/challenge", "", `{`), 400)
}
