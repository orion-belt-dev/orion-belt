package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
)

// AuthContext holds the authenticated user context
type AuthContext struct {
	UserID     string
	Username   string
	IsAdmin    bool
	AuthMethod string // "api_key", "session", "bearer"
}

// loggingMiddleware logs all HTTP requests as structured JSON and increments API metrics.
func (s *APIServer) loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = fmt.Sprintf("%d", start.UnixNano())
		}
		c.Set("request_id", reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()
		metrics.Default.IncAPIRequest()
		s.logger.WithField("request_id", reqID).Info("%s %s %d %v", c.Request.Method, c.Request.URL.Path, status, duration)
	}
}

// authMiddleware enforces authentication for protected endpoints
func (s *APIServer) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Try API Key authentication
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			if user, err := s.validateAPIKey(ctx, apiKey); err == nil {
				s.setAuthContext(c, user.ID, user.Username, user.IsAdmin, "api_key")
				c.Next()
				return
			}
		}

		// Session: header, cookie, or ?token= (WebSocket cannot set custom headers)
		sessionToken := c.GetHeader("X-Session-Token")
		if sessionToken == "" {
			if cookie, err := c.Cookie("session_token"); err == nil {
				sessionToken = cookie
			}
		}
		if sessionToken == "" {
			sessionToken = c.Query("token")
		}
		if sessionToken != "" {
			if user, err := s.validateSession(ctx, sessionToken); err == nil {
				s.setAuthContext(c, user.ID, user.Username, user.IsAdmin, "session")
				c.Next()
				return
			}
		}

		// JWT: Authorization Bearer, or ?access_token= for WebSocket/SSE
		bearerToken := ""
		if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			bearerToken = strings.TrimPrefix(authHeader, "Bearer ")
		}
		if bearerToken == "" {
			bearerToken = c.Query("access_token")
		}
		if bearerToken != "" {
			if user, err := s.validateBearerToken(ctx, bearerToken); err == nil {
				s.setAuthContext(c, user.ID, user.Username, user.IsAdmin, "bearer")
				c.Next()
				return
			}
		}

		// No valid authentication found
		metrics.Default.IncAuthFailure()
		s.logger.Warn("Authentication failed for %s %s from %s", c.Request.Method, c.Request.URL.Path, c.ClientIP())
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "authentication required",
		})
		c.Abort()
	}
}

// adminMiddleware requires admin or operator role
func (s *APIServer) adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		isAdmin, _ := c.Get("is_admin")
		r, _ := role.(string)
		admin, _ := isAdmin.(bool)
		if admin || r == common.RoleAdmin || r == common.RoleOperator {
			c.Next()
			return
		}
		userID, _ := c.Get("user_id")
		s.logger.Warn("Admin access denied for user: %v role=%v", userID, role)
		c.JSON(http.StatusForbidden, gin.H{"error": "admin or operator privileges required"})
		c.Abort()
	}
}

// isPrivilegedViewer reports whether the authenticated caller can see
// other users' sessions/audit logs/access requests (admin, operator,
// auditor). A plain "user" is scoped to their own records.
func isPrivilegedViewer(c *gin.Context) bool {
	role, _ := c.Get("role")
	r, _ := role.(string)
	switch r {
	case common.RoleAdmin, common.RoleOperator, common.RoleAuditor:
		return true
	}
	isAdmin, _ := c.Get("is_admin")
	admin, _ := isAdmin.(bool)
	return admin
}

// setAuthContext sets authentication context in the Gin context
func (s *APIServer) setAuthContext(c *gin.Context, userID, username string, isAdmin bool, authMethod string) {
	c.Set("user_id", userID)
	c.Set("username", username)
	c.Set("is_admin", isAdmin)
	c.Set("auth_method", authMethod)
	c.Set("authenticated", true)
	role := common.RoleUser
	if isAdmin {
		role = common.RoleAdmin
	}
	if user, err := s.store.GetUser(c.Request.Context(), userID); err == nil {
		role = user.EffectiveRole()
		c.Set("is_admin", user.IsAdmin || role == common.RoleAdmin)
	}
	c.Set("role", role)
}

// validateAPIKey validates an API key and returns the associated user
func (s *APIServer) validateAPIKey(ctx context.Context, apiKey string) (*common.User, error) {
	keyHash := hashAPIKey(apiKey)

	key, err := s.store.GetAPIKeyByHash(ctx, keyHash)
	if err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	if key.RevokedAt != nil {
		return nil, fmt.Errorf("API key has been revoked")
	}

	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, fmt.Errorf("API key has expired")
	}

	go func() {
		_ = s.store.UpdateAPIKeyLastUsed(ctx, key.ID, time.Now())
	}()

	user, err := s.store.GetUser(ctx, key.UserID)
	if err != nil {
		return nil, fmt.Errorf("user not found for API key")
	}

	s.logger.Debug("API key authenticated: user=%s key=%s", user.Username, key.Name)
	return user, nil
}

// validateSession validates a session token and returns the associated user
func (s *APIServer) validateSession(ctx context.Context, sessionToken string) (*common.User, error) {
	tokenHash := hashSessionToken(sessionToken)

	session, err := s.store.GetHTTPSessionByToken(ctx, tokenHash)
	if err != nil {
		return nil, fmt.Errorf("invalid session token")
	}

	if time.Now().After(session.ExpiresAt) {
		_ = s.store.DeleteHTTPSession(ctx, session.ID)
		return nil, fmt.Errorf("session has expired")
	}

	go func() {
		_ = s.store.UpdateHTTPSessionLastSeen(ctx, session.ID, time.Now())
	}()

	user, err := s.store.GetUser(ctx, session.UserID)
	if err != nil {
		return nil, fmt.Errorf("user not found for session")
	}

	s.logger.Debug("Session authenticated: user=%s", user.Username)
	return user, nil
}

// validateBearerToken validates a JWT bearer token
func (s *APIServer) validateBearerToken(ctx context.Context, token string) (*common.User, error) {
	if s.jwt == nil || !s.jwt.Enabled() {
		return nil, fmt.Errorf("JWT authentication not configured")
	}

	claims, err := s.jwt.Validate(token)
	if err != nil {
		return nil, err
	}

	user, err := s.store.GetUser(ctx, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("user not found for token")
	}

	s.logger.Debug("JWT authenticated: user=%s", user.Username)
	return user, nil
}

// hashAPIKey creates a SHA256 hash of an API key
func hashAPIKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// hashSessionToken creates a SHA256 hash of a session token
func hashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// Start starts the API server
func (s *APIServer) Start(addr string) error {
	s.logger.Info("Starting API server on %s", addr)
	return s.router.Run(addr)
}

// Router returns the Gin router
func (s *APIServer) Router() *gin.Engine {
	return s.router
}
