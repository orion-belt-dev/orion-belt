package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func (e *env) setRiskPolicy(policy map[string]any) {
	e.t.Helper()
	e.expect(e.do(http.MethodPut, "/api/v1/admin/risk-policy", e.adminKey, policy), http.StatusOK)
}

func (e *env) taggedMachine(name, sensitivity string) *common.Machine {
	e.t.Helper()
	m := common.NewMachine(name, name+".internal", 22, map[string]string{common.SensitivityTagKey: sensitivity})
	if err := e.store.CreateMachine(context.Background(), m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *env) requestAccess(key, machineID, remoteUser string, duration int) common.AccessRequest {
	e.t.Helper()
	r := e.do(http.MethodPost, "/api/v1/access-requests", key, map[string]any{
		"machine_id": machineID, "remote_users": []string{remoteUser}, "reason": "work", "duration": duration,
	})
	e.expect(r, http.StatusCreated)
	var out common.AccessRequest
	r.json(e.t, &out)
	return out
}

func TestRiskPolicyIsAdminOnlyAndValidated(t *testing.T) {
	e := newEnv(t, nil)
	r := e.do(http.MethodGet, "/api/v1/admin/risk-policy", e.operatorKey, nil)
	e.expect(r, http.StatusOK)
	if r.obj(t)["auto_approve"].(map[string]any)["enabled"] != false {
		t.Errorf("default policy = %s", r.body)
	}
	e.expect(e.do(http.MethodPut, "/api/v1/admin/risk-policy", e.operatorKey, map[string]any{}), http.StatusForbidden)
	e.expect(e.do(http.MethodPut, "/api/v1/admin/risk-policy", e.adminKey, map[string]any{
		"auto_approve": map[string]any{"enabled": true, "max_sensitivity": "high", "max_duration_seconds": 60},
	}), http.StatusBadRequest)
	e.setRiskPolicy(map[string]any{"escalate": map[string]any{"enabled": true, "sensitivities": []string{"high"}}})
	if !e.audited("risk_policy.update") {
		t.Error("policy change not audited")
	}
}

// A request every auto-approve condition holds for is approved on the spot,
// with no reviewer, and the grant works.
func TestLowRiskRequestIsAutoApproved(t *testing.T) {
	e := newEnv(t, nil)
	low := e.taggedMachine("lab-01", common.SensitivityLow)
	e.setRiskPolicy(map[string]any{"auto_approve": map[string]any{
		"enabled": true, "max_sensitivity": "low", "max_duration_seconds": 3600,
		"source_cidrs": []string{"192.0.2.0/24"}, "remote_users": []string{"deploy"},
	}})

	req := e.requestAccess(e.aliceKey, low.ID, "deploy", 1800)
	if req.Status != "approved" || req.ReviewedBy != nil || req.ExpiresAt == nil {
		t.Fatalf("request = %+v", req)
	}
	if req.RiskDecision == nil || req.RiskDecision.Outcome != common.RiskOutcomeAutoApprove {
		t.Fatalf("decision = %+v", req.RiskDecision)
	}
	if err := e.srv.authService.CheckPermissionWithRemoteUser(context.Background(), e.alice.ID, low.ID, "ssh", "deploy"); err != nil {
		t.Errorf("auto-approved grant does not work: %v", err)
	}
	if !e.audited("access.auto_approve") {
		t.Error("auto-approval not audited")
	}
	_, ex := e.explain(e.aliceKey, "machine=lab-01&remote_user=deploy")
	if link := holding(ex); link == nil || link.GrantedBy != "risk policy" || link.Via != "access_request:"+req.ID {
		t.Errorf("explanation of an auto-approved grant = %+v", ex)
	}

	// root is not in the auto-approvable accounts: it waits for review.
	req = e.requestAccess(e.aliceKey, low.ID, "root", 1800)
	if req.Status != "pending" || req.RiskDecision.Outcome != common.RiskOutcomeManual {
		t.Errorf("root request = %+v / %+v", req, req.RiskDecision)
	}
}

// An escalated request can only be approved by an admin.
func TestEscalatedRequestNeedsAnAdmin(t *testing.T) {
	e := newEnv(t, nil)
	high := e.taggedMachine("vault-01", common.SensitivityHigh)
	e.setRiskPolicy(map[string]any{
		"escalate":     map[string]any{"enabled": true, "sensitivities": []string{"high"}},
		"auto_approve": map[string]any{"enabled": true, "max_sensitivity": "medium", "max_duration_seconds": 3600},
	})

	req := e.requestAccess(e.aliceKey, high.ID, "root", 600)
	if req.Status != "pending" || req.RiskDecision.Outcome != common.RiskOutcomeEscalate {
		t.Fatalf("request = %+v / %+v", req, req.RiskDecision)
	}
	if !e.audited("access.escalate") {
		t.Error("escalation not audited")
	}
	e.expect(e.do(http.MethodPost, "/api/v1/admin/access-requests/"+req.ID+"/approve", e.operatorKey, nil), http.StatusForbidden)
	e.expect(e.do(http.MethodPost, "/api/v1/admin/access-requests/"+req.ID+"/approve", e.adminKey, nil), http.StatusOK)
}
