package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
)

// explainAccess answers "why can (or can't) this user reach this machine as
// this account?". A plain user may only ask about themselves; admins,
// operators and auditors may ask about anyone.
func (s *APIServer) explainAccess(c *gin.Context) {
	ctx := c.Request.Context()
	machineRef := strings.TrimSpace(c.Query("machine"))
	remoteUser := strings.TrimSpace(c.Query("remote_user"))
	if machineRef == "" || remoteUser == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "machine and remote_user are required"})
		return
	}
	accessType := strings.TrimSpace(c.DefaultQuery("access_type", "ssh"))
	if accessType != "ssh" && accessType != "scp" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "access_type must be ssh or scp"})
		return
	}

	userID := c.GetString("user_id")
	if ref := strings.TrimSpace(c.Query("user")); ref != "" {
		u, err := s.store.GetUser(ctx, ref)
		if err != nil {
			u, err = s.store.GetUserByUsername(ctx, ref)
		}
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		if u.ID != userID && !isPrivilegedViewer(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": "only admins, operators and auditors can explain another user's access"})
			return
		}
		userID = u.ID
	}

	machine, err := s.store.GetMachine(ctx, machineRef)
	if err != nil {
		machine, err = s.store.GetMachineByName(ctx, machineRef)
	}
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}

	ex, err := s.authService.ExplainAccess(ctx, userID, machine.ID, accessType, remoteUser)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "user or machine not found"})
			return
		}
		s.logger.Error("Failed to explain access for user %s on machine %s: %v", userID, machine.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to explain access"})
		return
	}
	c.JSON(http.StatusOK, ex)
}

// denialReason is the one-line explanation attached to a refused session,
// or "" when it cannot be worked out.
func (s *APIServer) denialReason(c *gin.Context, userID, machineID, remoteUser string) string {
	ex, err := s.authService.ExplainAccess(c.Request.Context(), userID, machineID, "ssh", remoteUser)
	if err != nil {
		return ""
	}
	return ex.Summary
}
