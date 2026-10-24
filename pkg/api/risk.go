package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
)

// collectRiskSignals captures who is asking, from where, with what client,
// when, and how sensitive the target is. Lookups are best effort: a missing
// user or machine leaves those signals empty rather than failing the request.
func (s *APIServer) collectRiskSignals(c *gin.Context, userID, machineID string) *common.RiskSignals {
	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		user = nil
	}
	machine, err := s.store.GetMachine(ctx, machineID)
	if err != nil {
		machine = nil
	}
	return common.NewRiskSignals(c.ClientIP(), c.Request.UserAgent(), c.GetString("auth_method"), user, machine, time.Now())
}

// applyRiskPolicy routes a just-created request. It records the decision,
// approves the request without a reviewer when the policy says so, and
// returns the request as it now stands. Policy failures fall back to manual
// review: a broken policy must never approve anything.
func (s *APIServer) applyRiskPolicy(c *gin.Context, req *common.AccessRequest) *common.AccessRequest {
	ctx := c.Request.Context()
	decision := req.RiskDecision
	if decision == nil {
		return req
	}
	switch decision.Outcome {
	case common.RiskOutcomeEscalate:
		s.recordAudit(c, "access.escalate", "access_request:"+req.ID, map[string]interface{}{
			"reasons": decision.Reasons,
		})
	case common.RiskOutcomeAutoApprove:
		if err := s.authService.ApproveAccessRequestWithTTL(ctx, req.ID, "", nil); err != nil {
			s.logger.Error("Risk policy could not auto-approve request %s; leaving it for review: %v", req.ID, err)
			return req
		}
		approved, err := s.store.GetAccessRequest(ctx, req.ID)
		if err != nil {
			s.logger.Error("Auto-approved request %s but could not reload it: %v", req.ID, err)
			return req
		}
		s.recordAudit(c, "access.auto_approve", "access_request:"+req.ID, map[string]interface{}{
			"user_id":      approved.UserID,
			"machine_id":   approved.MachineID,
			"duration":     approved.Duration,
			"access_type":  approved.AccessType,
			"remote_users": approved.RemoteUsers,
			"reasons":      decision.Reasons,
		})
		s.kubeGrantChanged(approved.MachineID, approved.AccessType)
		s.notifyAccessRequestApproved(ctx, approved)
		if s.pluginManager != nil {
			_ = s.pluginManager.TriggerHook(ctx, plugin.HookAccessGranted, &plugin.HookContext{
				UserID:    approved.UserID,
				MachineID: approved.MachineID,
				Data: map[string]interface{}{
					"request_id":  approved.ID,
					"status":      "approved",
					"duration":    approved.Duration,
					"approved_by": "risk_policy",
				},
			})
		}
		return approved
	}
	return req
}

// evaluateRisk decides how a new request is routed. A policy that cannot be
// read leaves the request for manual review.
func (s *APIServer) evaluateRisk(c *gin.Context, req *common.AccessRequest) *common.RiskDecision {
	policy, err := s.store.GetRiskPolicy(c.Request.Context())
	if err != nil {
		s.logger.Error("Failed to read risk policy; routing request to manual review: %v", err)
		policy = nil
	}
	d := common.EvaluateRisk(policy, req.RiskSignals, req.RemoteUsers, req.Duration)
	return &d
}

func (s *APIServer) getRiskPolicy(c *gin.Context) {
	policy, err := s.store.GetRiskPolicy(c.Request.Context())
	if err != nil {
		s.logger.Error("Failed to get risk policy: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get risk policy"})
		return
	}
	if policy == nil {
		policy = &common.RiskPolicy{}
	}
	c.JSON(http.StatusOK, policy)
}

// putRiskPolicy replaces the risk policy. Only admins may change it: it can
// approve access with nobody in the loop.
func (s *APIServer) putRiskPolicy(c *gin.Context) {
	if !callerIsAdmin(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only admins can change the risk policy"})
		return
	}
	var policy common.RiskPolicy
	if err := c.ShouldBindJSON(&policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := policy.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	if err := s.store.UpsertRiskPolicy(ctx, &policy, c.GetString("user_id")); err != nil {
		s.logger.Error("Failed to save risk policy: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save risk policy"})
		return
	}
	s.recordAudit(c, "risk_policy.update", "risk_policy", map[string]interface{}{
		"auto_approve": policy.AutoApprove.Enabled,
		"escalate":     policy.Escalate.Enabled,
	})
	saved, err := s.store.GetRiskPolicy(ctx)
	if err != nil || saved == nil {
		c.JSON(http.StatusOK, policy)
		return
	}
	c.JSON(http.StatusOK, saved)
}
