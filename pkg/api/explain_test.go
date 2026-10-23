package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func (e *env) explain(key, query string) (resp, common.AccessExplanation) {
	e.t.Helper()
	r := e.do(http.MethodGet, "/api/v1/access/explain?"+query, key, nil)
	var ex common.AccessExplanation
	if r.code == http.StatusOK {
		r.json(e.t, &ex)
	}
	return r, ex
}

func holding(ex common.AccessExplanation) *common.AccessLink {
	for i := range ex.Chain {
		if ex.Chain[i].Holds {
			return &ex.Chain[i]
		}
	}
	return nil
}

func TestExplainAccessWithoutAnyRelationship(t *testing.T) {
	e := newEnv(t, nil)
	r, ex := e.explain(e.aliceKey, "machine=web-01&remote_user=root")
	e.expect(r, http.StatusOK)
	if ex.Allowed || len(ex.Chain) != 1 || ex.Chain[0].Relation != common.LinkNone {
		t.Fatalf("explanation = %+v", ex)
	}
	if !strings.Contains(ex.Summary, "no grant on web-01") {
		t.Errorf("summary = %q", ex.Summary)
	}
}

// A grant for another account explains the denial; the same grant explains
// the allowed case, along with the request it came from and its reason.
func TestExplainAccessThroughApprovedRequest(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do(http.MethodPost, "/api/v1/access-requests", e.aliceKey, map[string]any{
		"machine_id": e.machine.ID, "remote_users": []string{"deploy"}, "reason": "ship 1.4", "duration": 3600,
	})
	e.expect(r, http.StatusCreated)
	reqID, _ := r.obj(t)["id"].(string)
	e.expect(e.do(http.MethodPost, "/api/v1/admin/access-requests/"+reqID+"/approve", e.adminKey, nil), http.StatusOK)

	_, ex := e.explain(e.aliceKey, "machine="+e.machine.ID+"&remote_user=deploy")
	link := holding(ex)
	if !ex.Allowed || link == nil || link.Relation != common.LinkGrant {
		t.Fatalf("explanation = %+v", ex)
	}
	if link.Via != "access_request:"+reqID || link.Reason != "ship 1.4" || link.GrantedBy != "admin" || link.ExpiresAt == nil {
		t.Errorf("grant link = %+v", link)
	}

	_, ex = e.explain(e.aliceKey, "machine=web-01&remote_user=root")
	if ex.Allowed || holding(ex) != nil {
		t.Fatalf("root must be denied: %+v", ex)
	}
	if !strings.Contains(ex.Summary, "grant allows deploy, not root") {
		t.Errorf("summary = %q", ex.Summary)
	}
}

func TestExplainAccessPendingRequestAndRole(t *testing.T) {
	e := newEnv(t, nil)
	e.expect(e.do(http.MethodPost, "/api/v1/access-requests", e.bobKey, map[string]any{
		"machine_id": e.machine.ID, "remote_users": []string{"root"}, "reason": "debug",
	}), http.StatusCreated)
	_, ex := e.explain(e.bobKey, "machine=web-01&remote_user=root")
	if ex.Allowed || !strings.Contains(ex.Summary, "waiting for approval") {
		t.Errorf("pending request explanation = %+v", ex)
	}

	_, ex = e.explain(e.operatorKey, "machine=web-01&remote_user=root")
	if link := holding(ex); !ex.Allowed || link == nil || link.Ref != "role:operator" {
		t.Errorf("operator explanation = %+v", ex)
	}
}

func TestExplainAccessScopesOtherUsers(t *testing.T) {
	e := newEnv(t, nil)
	r, _ := e.explain(e.aliceKey, "machine=web-01&remote_user=root&user=bob")
	e.expect(r, http.StatusForbidden)
	r, ex := e.explain(e.auditorKey, "machine=web-01&remote_user=root&user=bob")
	e.expect(r, http.StatusOK)
	if ex.Username != "bob" {
		t.Errorf("explained %q, want bob", ex.Username)
	}
	r, _ = e.explain(e.aliceKey, "machine=web-01")
	e.expect(r, http.StatusBadRequest)
	r, _ = e.explain(e.aliceKey, "machine=nope&remote_user=root")
	e.expect(r, http.StatusNotFound)
}

// The console terminal says why it refused, not just that it did.
func TestTerminalDenialCarriesReason(t *testing.T) {
	e := newEnv(t, nil)
	e.bridge.machines = map[string]*common.Machine{"web-01": e.machine}
	r := e.do(http.MethodGet, "/api/v1/terminal/ws?machine=web-01&user=root", e.aliceKey, nil)
	e.expect(r, http.StatusForbidden)
	if reason, _ := r.obj(t)["reason"].(string); !strings.Contains(reason, "no grant on web-01") {
		t.Errorf("reason = %q", reason)
	}
}
