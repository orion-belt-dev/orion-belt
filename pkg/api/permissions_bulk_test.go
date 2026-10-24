package api

import (
	"context"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func TestPermissions_BulkGrantAndRevoke(t *testing.T) {
	e := newEnv(t, nil)
	db := common.NewMachine("db-01", "db.internal", 22, nil)
	if err := e.store.CreateMachine(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	rec := &fakeReconciler{}
	e.srv.SetKubeReconciler(rec)

	grant := func(body map[string]any) resp {
		return e.do("POST", "/api/v1/admin/permissions/bulk", e.adminKey, body)
	}
	e.expect(grant(map[string]any{"user_ids": []string{}, "machine_ids": []string{db.ID}, "access_type": "ssh"}), 400)
	e.expect(grant(map[string]any{"user_ids": []string{e.alice.ID}, "machine_ids": []string{db.ID}, "access_type": "ssh"}), 400) // no remote user
	e.expect(grant(map[string]any{"user_ids": []string{e.alice.ID}, "machine_ids": []string{db.ID}, "access_type": "ssh", "remote_users": []string{"root"}, "expires_at": "soon"}), 400)
	many := make([]string, 30)
	for i := range many {
		many[i] = e.alice.ID
	}
	e.expect(grant(map[string]any{"user_ids": many, "machine_ids": many, "access_type": "rdp"}), 400) // 900 pairs
	e.expect(e.do("POST", "/api/v1/admin/permissions/bulk", e.aliceKey, map[string]any{}), 403)

	r := grant(map[string]any{
		"user_ids":    []string{e.alice.ID, e.bob.ID, "ghost"},
		"machine_ids": []string{e.machine.ID, db.ID},
		"access_type": "both", "remote_users": []string{"deploy"}, "duration_seconds": 3600,
	})
	e.expect(r, 200)
	out := r.obj(t)
	if out["granted"] != float64(4) || len(out["failed"].([]any)) != 2 {
		t.Fatalf("bulk grant = %s", r.body)
	}
	perms, _ := e.store.ListMachinePermissions(context.Background(), db.ID)
	if len(perms) != 2 || perms[0].ExpiresAt == nil {
		t.Fatalf("db-01 grants = %+v", perms)
	}
	audits := 0
	for _, a := range e.auditActions() {
		if a == "permission.grant" {
			audits++
		}
	}
	if audits != 4 {
		t.Errorf("audited %d grants, want 4", audits)
	}

	// k8s bulk grants push a reconcile per machine.
	e.expect(grant(map[string]any{"user_ids": []string{e.alice.ID}, "machine_ids": []string{db.ID}, "access_type": "k8s", "remote_users": []string{"payments/view"}}), 200)
	if rec.changes() != 1 {
		t.Errorf("k8s bulk grant pushed %d reconciles", rec.changes())
	}

	// Revoke two real grants and one that does not exist.
	r = e.do("POST", "/api/v1/admin/permissions/bulk-revoke", e.adminKey, map[string]any{"ids": []string{perms[0].ID, perms[1].ID, "missing"}})
	e.expect(r, 200)
	out = r.obj(t)
	failed := out["failed"].([]any)
	if out["revoked"] != float64(2) || len(failed) != 1 || !strings.Contains(failed[0].(map[string]any)["error"].(string), "no such grant") {
		t.Fatalf("bulk revoke = %s", r.body)
	}
	e.expect(e.do("POST", "/api/v1/admin/permissions/bulk-revoke", e.adminKey, map[string]any{"ids": []string{}}), 400)
	e.expect(e.do("POST", "/api/v1/admin/permissions/bulk-revoke", e.adminKey, `{`), 400)
}

func TestPermissions_EffectiveAccess(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for _, p := range []*common.Permission{
		common.NewPermission(e.alice.ID, e.machine.ID, "both", []string{"deploy"}, e.admin.ID, nil),
		common.NewPermission(e.alice.ID, e.machine.ID, "k8s", []string{"payments/edit"}, e.admin.ID, nil),
	} {
		if err := e.store.CreatePermission(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	r := e.do("GET", "/api/v1/admin/permissions/effective?user_id="+e.alice.ID, e.adminKey, nil)
	e.expect(r, 200)
	types := map[string][]any{}
	for _, row := range r.list(t) {
		if row["machine_name"] != e.machine.Name || !strings.HasPrefix(row["source"].(string), "permission:") {
			t.Errorf("row = %v", row)
		}
		types[row["access_type"].(string)] = row["remote_users"].([]any)
	}
	if len(types) != 3 || types["ssh"][0] != "deploy" || types["scp"][0] != "deploy" || types["k8s"][0] != "payments/edit" {
		t.Errorf("alice's effective access = %v (both should expand to ssh and scp)", types)
	}

	// The admin reaches every machine through their role.
	r = e.do("GET", "/api/v1/admin/permissions/effective?user_id="+e.admin.ID, e.adminKey, nil)
	e.expect(r, 200)
	if rows := r.list(t); len(rows) != 1 || rows[0]["access_type"] != "*" || rows[0]["source"] != "role:admin" {
		t.Errorf("admin effective = %v", rows)
	}

	// From the machine's side: admin and operator by role, alice by grants.
	r = e.do("GET", "/api/v1/admin/permissions/effective?machine_id="+e.machine.ID, e.adminKey, nil)
	e.expect(r, 200)
	sources := map[string]bool{}
	for _, row := range r.list(t) {
		sources[row["username"].(string)+" "+row["source"].(string)[:5]] = true
	}
	for _, want := range []string{"admin role:", "operator role:", "alice permi"} {
		if !sources[want] {
			t.Errorf("machine view lacks %q: %v", want, sources)
		}
	}

	e.expect(e.do("GET", "/api/v1/admin/permissions/effective", e.adminKey, nil), 400)
	e.expect(e.do("GET", "/api/v1/admin/permissions/effective?user_id=a&machine_id=b", e.adminKey, nil), 400)
	e.expect(e.do("GET", "/api/v1/admin/permissions/effective?user_id=ghost", e.adminKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/admin/permissions/effective?machine_id=ghost", e.adminKey, nil), 404)
}

func TestPermissions_ListSearchesServerSide(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	for _, p := range []*common.Permission{
		common.NewPermission(e.alice.ID, e.machine.ID, "ssh", []string{"deploy"}, e.admin.ID, nil),
		common.NewPermission(e.bob.ID, e.machine.ID, "rdp", nil, e.admin.ID, nil),
	} {
		if err := e.store.CreatePermission(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	r := e.do("GET", "/api/v1/admin/permissions?q=alice", e.adminKey, nil)
	e.expect(r, 200)
	if rows := r.list(t); len(rows) != 1 || r.hdr.Get("X-Total-Count") != "1" {
		t.Errorf("q=alice: %d rows, total %q", len(rows), r.hdr.Get("X-Total-Count"))
	}
	r = e.do("GET", "/api/v1/admin/permissions?access_type=rdp&limit=1&offset=0", e.adminKey, nil)
	if rows := r.list(t); len(rows) != 1 || rows[0]["access_type"] != "rdp" {
		t.Errorf("access_type=rdp: %v", rows)
	}
	r = e.do("GET", "/api/v1/admin/permissions?limit=1&offset=1", e.adminKey, nil)
	if rows := r.list(t); len(rows) != 1 || r.hdr.Get("X-Total-Count") != "2" {
		t.Errorf("second page: %d rows, total %q", len(rows), r.hdr.Get("X-Total-Count"))
	}
}
