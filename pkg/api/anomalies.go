package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/anomaly"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// listAnomalies returns flagged sessions, newest first. Flags are advisory
// and separate from denials, so reviewers see them as their own list.
func (s *APIServer) listAnomalies(c *gin.Context) {
	if !isPrivilegedViewer(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "anomalies are visible to admins, operators and auditors"})
		return
	}
	limit := parseLimit(c.Query("limit"), 100, 1000)
	filters := map[string]interface{}{"action_prefix": anomaly.ActionPrefix}
	if uid := strings.TrimSpace(c.Query("user_id")); uid != "" {
		filters["user_id"] = uid
	}
	logs, err := s.store.ListAuditLogs(c.Request.Context(), limit, 0, filters)
	if err != nil {
		s.logger.Error("Failed to list anomalies: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list anomalies"})
		return
	}
	out := make([]common.AccessAnomaly, 0, len(logs))
	for _, l := range logs {
		meta := func(key string) string { v, _ := l.Metadata[key].(string); return v }
		out = append(out, common.AccessAnomaly{
			ID:         l.ID,
			Kind:       strings.TrimPrefix(l.Action, anomaly.ActionPrefix),
			Signal:     meta("signal"),
			UserID:     l.UserID,
			MachineID:  meta("machine_id"),
			Machine:    meta("machine"),
			RemoteUser: meta("remote_user"),
			SessionID:  meta("session_id"),
			IPAddress:  l.IPAddress,
			FlaggedAt:  dbInstant(l.Timestamp),
		})
	}
	c.JSON(http.StatusOK, out)
}
