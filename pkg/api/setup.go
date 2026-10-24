package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// setupStatus returns a first-run checklist for operators (admin/operator).
func (s *APIServer) setupStatus(c *gin.Context) {
	ctx := c.Request.Context()

	users, err := s.store.ListUsers(ctx, 500, 0)
	if err != nil {
		s.fail(c, err)
		return
	}
	machines, err := s.store.ListMachines(ctx, 500, 0)
	if err != nil {
		s.fail(c, err)
		return
	}

	adminCount := 0
	nonAdminCount := 0
	for _, u := range users {
		if u.IsAdmin || u.Role == "admin" {
			adminCount++
		} else {
			nonAdminCount++
		}
	}

	connected := 0
	if s.agentCommander != nil {
		connected = len(s.agentCommander.ListConnectedAgents())
	}

	permCount := 0
	for _, u := range users {
		perms, perr := s.store.ListUserPermissions(ctx, u.ID)
		if perr == nil {
			permCount += len(perms)
		}
	}

	steps := gin.H{
		"admin_exists":         adminCount > 0,
		"has_machines":         len(machines) > 0,
		"has_connected_agents": connected > 0,
		"has_users":            nonAdminCount > 0,
		"has_permissions":      permCount > 0,
	}

	complete := steps["admin_exists"].(bool) &&
		steps["has_machines"].(bool) &&
		steps["has_connected_agents"].(bool)

	next := "You're set — grant users machine access under Users / Permissions."
	switch {
	case !steps["admin_exists"].(bool):
		next = "Create an admin: orion-belt-server setup  (or user create --admin)"
	case !steps["has_machines"].(bool):
		next = "Install orion-belt-agent on a host and register it (see Setup guide)."
	case !steps["has_connected_agents"].(bool):
		next = "Start the agent service so it connects to the gateway (port 2222)."
	case !steps["has_users"].(bool):
		next = "Register operators/users in the UI or: orion-belt-server user create …"
	case !steps["has_permissions"].(bool):
		next = "Grant users access to machines (admin → Permissions)."
	}

	c.JSON(http.StatusOK, gin.H{
		"complete": complete,
		"steps":    steps,
		"counts": gin.H{
			"admins":            adminCount,
			"users":             nonAdminCount,
			"machines":          len(machines),
			"connected_agents":  connected,
			"permissions":       permCount,
		},
		"next": next,
	})
}
