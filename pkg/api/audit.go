package api

import (
	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// recordAudit writes a DB audit log entry (best-effort; never fails the request).
func (s *APIServer) recordAudit(c *gin.Context, action, resource string, meta map[string]interface{}) {
	if s.store == nil {
		return
	}
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)
	if meta == nil {
		meta = map[string]interface{}{}
	}
	entry := common.NewAuditLog(uid, action, resource, c.ClientIP(), meta)
	if err := s.store.CreateAuditLog(c.Request.Context(), entry); err != nil && s.logger != nil {
		s.logger.Warn("audit log write failed: %v", err)
	}
}
