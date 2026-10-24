package api

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// fakeReconciler records the clusters grant changes were pushed for.
type fakeReconciler struct {
	mu      sync.Mutex
	changed []string
	plan    kube.Plan
	err     error
	dryRuns []bool
}

func (f *fakeReconciler) ReconcileKube(_ context.Context, _ string, dryRun bool) (kube.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dryRuns = append(f.dryRuns, dryRun)
	return f.plan, f.err
}

func (f *fakeReconciler) KubeGrantChanged(machineID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changed = append(f.changed, machineID)
}

func (f *fakeReconciler) changes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.changed)
}

func TestKube_GrantScopesValidatedAndPushed(t *testing.T) {
	e := newEnv(t, nil)
	rec := &fakeReconciler{}
	e.srv.SetKubeReconciler(rec)
	grant := func(scopes any) resp {
		return e.do("POST", "/api/v1/admin/permissions", e.adminKey, map[string]any{
			"user_id": e.alice.ID, "machine_id": e.machine.ID, "access_type": "k8s", "remote_users": scopes,
		})
	}
	e.expect(grant(nil), 400)                       // no scope
	e.expect(grant([]string{"root"}), 400)          // a UNIX user is not a scope
	e.expect(grant([]string{"Payments/edit"}), 400) // not a namespace name
	if rec.changes() != 0 {
		t.Fatalf("rejected grants were pushed: %d", rec.changes())
	}

	r := grant([]string{"payments/edit", "*/view"})
	e.expect(r, 201)
	if rec.changes() != 1 {
		t.Errorf("grant pushed %d times, want 1", rec.changes())
	}
	perm := r.obj(t)
	id := perm["id"].(string)

	// Updating to an invalid scope is refused; a valid one is pushed.
	e.expect(e.do("PATCH", "/api/v1/admin/permissions/"+id, e.adminKey, map[string]any{"remote_users": []string{"nope"}}), 400)
	e.expect(e.do("PATCH", "/api/v1/admin/permissions/"+id, e.adminKey, map[string]any{"remote_users": []string{"ops/admin"}}), 200)
	e.expect(e.do("DELETE", "/api/v1/admin/permissions/"+id, e.adminKey, nil), 200)
	if rec.changes() != 3 {
		t.Errorf("grant, update and revoke pushed %d times, want 3", rec.changes())
	}

	// An ssh grant is not a Kubernetes change.
	e.expect(e.do("POST", "/api/v1/admin/permissions", e.adminKey, map[string]any{
		"user_id": e.bob.ID, "machine_id": e.machine.ID, "access_type": "ssh", "remote_users": []string{"root"},
	}), 201)
	if rec.changes() != 3 {
		t.Errorf("ssh grant pushed a Kubernetes reconcile")
	}
}

func TestKube_RequestApprovalPushesAndAudits(t *testing.T) {
	e := newEnv(t, nil)
	rec := &fakeReconciler{}
	e.srv.SetKubeReconciler(rec)

	e.expect(e.do("POST", "/api/v1/access-requests", e.aliceKey, map[string]any{
		"machine_id": e.machine.ID, "reason": "x", "access_type": "k8s", "remote_users": []string{"root"},
	}), 400)
	r := e.do("POST", "/api/v1/access-requests", e.aliceKey, map[string]any{
		"machine_id": e.machine.ID, "reason": "INC-42", "access_type": "k8s", "remote_users": []string{"payments/edit"},
	})
	e.expect(r, 201)
	id := r.obj(t)["id"].(string)
	e.expect(e.do("POST", "/api/v1/admin/access-requests/"+id+"/approve", e.adminKey, nil), 200)
	if rec.changes() != 1 {
		t.Errorf("approval pushed %d times", rec.changes())
	}

	// The lifecycle entries carry the type and scope, as SSH ones carry the
	// remote user.
	logs, err := e.store.ListAuditLogs(context.Background(), 20, 0, map[string]interface{}{"action_prefix": "access."})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, l := range logs {
		if l.Metadata["access_type"] == "k8s" {
			seen[l.Action] = true
		}
	}
	if !seen["access.request"] || !seen["access.approve"] {
		t.Errorf("k8s access audit = %v", seen)
	}
}

func TestKube_ReconcileEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	path := "/api/v1/admin/machines/" + e.machine.ID + "/kubernetes/reconcile"
	e.expect(e.do("GET", path, e.adminKey, nil), 503) // no reconciler wired

	rec := &fakeReconciler{plan: kube.Plan{Delete: []kube.Binding{{Name: "orion-x"}}}}
	e.srv.SetKubeReconciler(rec)
	e.expect(e.do("GET", path, e.aliceKey, nil), 403)

	r := e.do("GET", path, e.adminKey, nil)
	e.expect(r, 200)
	if body := r.obj(t); body["dry_run"] != true || body["in_sync"] != false {
		t.Errorf("dry run = %v", body)
	}
	e.expect(e.do("POST", path, e.adminKey, nil), 200)
	if len(rec.dryRuns) != 2 || !rec.dryRuns[0] || rec.dryRuns[1] {
		t.Errorf("dry runs = %v", rec.dryRuns)
	}
	if !e.audited("k8s.reconcile") {
		t.Error("applying a reconcile was not audited")
	}

	rec.err = kube.ErrNoAgent
	e.expect(e.do("GET", path, e.adminKey, nil), 409)
	rec.err = errors.New("agent timed out")
	e.expect(e.do("POST", path, e.adminKey, nil), 502)
}
