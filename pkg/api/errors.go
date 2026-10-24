package api

import (
	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/apperr"
)

// fail answers with err's status and a message safe to show: the public
// message of a kinded error, or "internal error" with the cause logged here
// and never sent. The body is {"error": message, "code": kind}. See
// docs/ERROR_HANDLING.md.
func (s *APIServer) fail(c *gin.Context, err error) {
	kind := apperr.KindOf(err)
	if kind == apperr.Internal || kind == apperr.Unavailable {
		s.logger.Error("%s %s: %v", c.Request.Method, c.FullPath(), err)
	}
	c.JSON(kind.HTTPStatus(), gin.H{"error": apperr.Message(err), "code": kind.String()})
}
