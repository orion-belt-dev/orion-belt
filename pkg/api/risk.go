package api

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
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
