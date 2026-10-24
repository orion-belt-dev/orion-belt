package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// maxBulkGrants bounds one bulk request (users × machines, or ids), so a
// mistaken selection cannot write thousands of grants at once.
const maxBulkGrants = 500

// grantDuration reads a grant's lifetime from duration_seconds or an RFC3339
// expires_at. Nil means no expiry. Errors are the caller's to fix.
func grantDuration(durationSec *int, expiresAt string) (*time.Duration, error) {
	if durationSec != nil && *durationSec > 0 {
		d := time.Duration(*durationSec) * time.Second
		return &d, nil
	}
	if expiresAt == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return nil, errors.New("expires_at must be RFC3339")
	}
	d := time.Until(t)
	if d <= 0 {
		return nil, errors.New("expires_at must be in the future")
	}
	return &d, nil
}

// bulkFailure is one pair or id a bulk request could not apply.
type bulkFailure struct {
	UserID       string `json:"user_id,omitempty"`
	MachineID    string `json:"machine_id,omitempty"`
	PermissionID string `json:"permission_id,omitempty"`
	Error        string `json:"error"`
}

// bulkGrantPermissions grants one access type to every user × machine pair.
// Each grant is applied and audited on its own, so one failure (a deleted
// user) does not undo the rest; failures are listed in the answer.
func (s *APIServer) bulkGrantPermissions(c *gin.Context) {
	var req struct {
		UserIDs     []string `json:"user_ids" binding:"required"`
		MachineIDs  []string `json:"machine_ids" binding:"required"`
		AccessType  string   `json:"access_type" binding:"required"`
		RemoteUsers []string `json:"remote_users"`
		ExpiresAt   string   `json:"expires_at"`
		DurationSec *int     `json:"duration_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	pairs := len(req.UserIDs) * len(req.MachineIDs)
	if pairs == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_ids and machine_ids each need at least one entry"})
		return
	}
	if pairs > maxBulkGrants {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%d grants requested; at most %d per request", pairs, maxBulkGrants)})
		return
	}
	if err := validateGrantTargets(req.AccessType, req.RemoteUsers); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	duration, err := grantDuration(req.DurationSec, req.ExpiresAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	adminID, _ := c.Get("user_id")
	grantedBy, _ := adminID.(string)
	ctx := c.Request.Context()
	granted := 0
	failures := []bulkFailure{}
	changed := map[string]bool{}
	for _, userID := range req.UserIDs {
		for _, machineID := range req.MachineIDs {
			if err := s.authService.GrantPermission(ctx, userID, machineID, req.AccessType, req.RemoteUsers, grantedBy, duration); err != nil {
				s.logger.Warn("Bulk grant to %s on %s failed: %v", userID, machineID, err)
				failures = append(failures, bulkFailure{UserID: userID, MachineID: machineID, Error: "could not grant (unknown user or machine?)"})
				continue
			}
			granted++
			changed[machineID] = true
			s.recordAudit(c, "permission.grant", "machine:"+machineID, map[string]interface{}{
				"user_id":      userID,
				"access_type":  req.AccessType,
				"remote_users": req.RemoteUsers,
				"bulk":         true,
			})
		}
	}
	for machineID := range changed {
		s.kubeGrantChanged(machineID, req.AccessType)
	}
	c.JSON(http.StatusOK, gin.H{"granted": granted, "failed": failures})
}

// bulkRevokePermissions revokes each listed grant, auditing each.
func (s *APIServer) bulkRevokePermissions(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxBulkGrants {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("ids needs between 1 and %d entries", maxBulkGrants)})
		return
	}
	ctx := c.Request.Context()
	revoked := 0
	failures := []bulkFailure{}
	for _, id := range req.IDs {
		perm, _ := s.store.GetPermission(ctx, id)
		if err := s.authService.RevokePermission(ctx, id); err != nil {
			msg := "could not revoke"
			if errors.Is(err, database.ErrNotFound) {
				msg = "no such grant"
			}
			failures = append(failures, bulkFailure{PermissionID: id, Error: msg})
			continue
		}
		revoked++
		s.recordAudit(c, "permission.revoke", "permission:"+id, map[string]interface{}{"bulk": true})
		if perm != nil {
			s.kubeGrantChanged(perm.MachineID, perm.AccessType)
		}
	}
	c.JSON(http.StatusOK, gin.H{"revoked": revoked, "failed": failures})
}

// effectivePermissions resolves what a user can do on every machine
// (?user_id=) or who can do what on a machine (?machine_id=): roles, grants
// with "both" expanded, scopes and expiry, each with its source.
func (s *APIServer) effectivePermissions(c *gin.Context) {
	userID, machineID := c.Query("user_id"), c.Query("machine_id")
	if (userID == "") == (machineID == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pass exactly one of user_id or machine_id"})
		return
	}
	var (
		rows interface{}
		err  error
	)
	if userID != "" {
		rows, err = s.authService.EffectiveForUser(c.Request.Context(), userID)
	} else {
		rows, err = s.authService.EffectiveForMachine(c.Request.Context(), machineID)
	}
	if errors.Is(err, database.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "user or machine not found"})
		return
	}
	if err != nil {
		s.logger.Error("Effective permissions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve permissions"})
		return
	}
	c.JSON(http.StatusOK, rows)
}

// permissionFilter reads the list endpoint's search parameters.
func permissionFilter(c *gin.Context, defaultLimit int) database.PermissionFilter {
	f := database.PermissionFilter{
		Query:      c.Query("q"),
		AccessType: c.Query("access_type"),
		UserID:     c.Query("user_id"),
		MachineID:  c.Query("machine_id"),
		Limit:      defaultLimit,
	}
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		f.Limit = n
	}
	if n, err := strconv.Atoi(c.Query("offset")); err == nil && n > 0 {
		f.Offset = n
	}
	return f
}
