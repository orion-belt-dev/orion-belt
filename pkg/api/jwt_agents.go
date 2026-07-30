package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
	"golang.org/x/crypto/ssh"
)

// metricsMiddleware increments API request counters.
func (s *APIServer) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		metrics.Default.IncAPIRequest()
		c.Next()
	}
}

// rateLimiter is a simple per-key token bucket.
type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:    limit,
		window:   window,
		requests: make(map[string][]time.Time),
	}
}

func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)
	times := r.requests[key]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= r.limit {
		r.requests[key] = filtered
		return false
	}
	r.requests[key] = append(filtered, now)
	return true
}

func (s *APIServer) rateLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()
		if userID, ok := c.Get("user_id"); ok {
			key = "user:" + userID.(string)
		}
		if !s.rateLimiter.allow(key) {
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
				"hint":  "raise auth.rate_limit_per_minute in server config (default 600/min)",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// loginJWT issues a JWT after verifying the user's SSH public key.
func (s *APIServer) loginJWT(c *gin.Context) {
	var req struct {
		Username        string `json:"username" binding:"required"`
		PublicKey       string `json:"public_key" binding:"required"`
		Challenge       string `json:"challenge" binding:"required"`
		SignatureFormat string `json:"signature_format" binding:"required"`
		Signature       string `json:"signature" binding:"required"`
		TOTPCode        string `json:"totp_code,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if s.jwt == nil || !s.jwt.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "JWT authentication is not configured (set auth.jwt_secret)"})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	storedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(user.PublicKey))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	presented, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key"})
		return
	}
	if string(storedKey.Marshal()) != string(presented.Marshal()) {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err := s.verifyPossession(req.Username, req.Challenge, req.SignatureFormat, req.Signature, presented); err != nil {
		metrics.Default.IncAuthFailure()
		s.logger.Warn("Proof-of-possession failed for user %s: %v", req.Username, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !s.enforceMFAAfterPubkey(c, user.ID, req.TOTPCode) {
		return
	}

	token, exp, err := s.jwt.Issue(user.ID, user.Username, user.IsAdmin)
	if err != nil {
		s.logger.Error("Failed to issue JWT: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_at":   exp,
		"user": gin.H{
			"id":          user.ID,
			"username":    user.Username,
			"email":       user.Email,
			"is_admin":    user.IsAdmin,
			"mfa_enabled": user.MFAEnabled,
		},
	})
}

func (s *APIServer) listConnectedAgents(c *gin.Context) {
	if s.agentCommander == nil {
		c.JSON(http.StatusOK, gin.H{"agents": []string{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": s.agentCommander.ListConnectedAgents()})
}

type agentCommandRequest struct {
	Command string `json:"command" binding:"required"`
}

func (s *APIServer) sendAgentCommand(c *gin.Context) {
	if s.agentCommander == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent commander not available"})
		return
	}

	var req agentCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	machineID := c.Param("machine_id")
	out, err := s.agentCommander.SendAgentCommand(machineID, req.Command)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	s.recordAudit(c, "agent.command", "machine:"+machineID, map[string]interface{}{
		"command": req.Command,
	})
	c.JSON(http.StatusOK, gin.H{
		"machine_id": machineID,
		"command":    req.Command,
		"output":     string(out),
	})
}

func (s *APIServer) disconnectAgent(c *gin.Context) {
	if s.agentCommander == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "agent commander not available"})
		return
	}
	machineID := c.Param("machine_id")
	if err := s.agentCommander.DisconnectAgent(machineID); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	s.recordAudit(c, "agent.disconnect", "machine:"+machineID, nil)
	c.JSON(http.StatusOK, gin.H{"message": "agent disconnected", "machine_id": machineID})
}
