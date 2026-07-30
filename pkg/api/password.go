package api

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
)

const passwordLoginTicketTTL = 2 * time.Minute

type passwordLoginTicket struct {
	userID    string
	username  string
	expiresAt time.Time
}

type passwordTicketStore struct {
	mu      sync.Mutex
	tickets map[string]passwordLoginTicket
}

func newPasswordTicketStore() *passwordTicketStore {
	return &passwordTicketStore{tickets: make(map[string]passwordLoginTicket)}
}

func (s *passwordTicketStore) issue(userID, username string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	s.tickets[id] = passwordLoginTicket{
		userID:    userID,
		username:  username,
		expiresAt: time.Now().Add(passwordLoginTicketTTL),
	}
	return id, nil
}

func (s *passwordTicketStore) consume(id, username string) (userID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	t, exists := s.tickets[id]
	if !exists || t.username != username || time.Now().After(t.expiresAt) {
		return "", false
	}
	delete(s.tickets, id)
	return t.userID, true
}

func (s *passwordTicketStore) gcLocked() {
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.expiresAt) {
			delete(s.tickets, k)
		}
	}
}

func (s *APIServer) registerPasswordRoutes(protected, public *gin.RouterGroup) {
	if s.passwordTickets == nil {
		s.passwordTickets = newPasswordTicketStore()
	}
	public.POST("/login/password", s.loginWithPassword)
	protected.POST("/auth/password", s.setPassword)
	protected.DELETE("/auth/password", s.clearPassword)
}

// setPassword sets or changes the account password. A TOTP code is always
// required: if MFA is not yet enabled, the caller must have an in-progress
// /mfa/enroll and this call confirms it atomically with the password set.
func (s *APIServer) setPassword(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		Password string `json:"password" binding:"required"`
		TOTPCode string `json:"totp_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if user.MFAEnabled {
		newHashes, ok := auth.ValidateMFACode(user.TOTPSecret, user.BackupCodesHash, req.TOTPCode)
		if !ok {
			metrics.Default.IncAuthFailure()
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid totp code", "mfa_required": true})
			return
		}
		if newHashes != "" {
			user.BackupCodesHash = newHashes
		}
	} else {
		raw, ok := pendingEnrollments.Load(userID)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":               "enroll authenticator first via POST /mfa/enroll, then confirm with totp_code",
				"mfa_enroll_required": true,
			})
			return
		}
		pending := raw.(map[string]string)
		secret := pending["secret"]
		if !auth.ValidateTOTP(secret, req.TOTPCode) {
			metrics.Default.IncAuthFailure()
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid totp code"})
			return
		}
		user.MFAEnabled = true
		user.TOTPSecret = secret
		user.BackupCodesHash = pending["backup_hashes"]
		pendingEnrollments.Delete(userID)
	}

	user.PasswordHash = hash
	if err := s.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save password"})
		return
	}

	_ = s.store.CreateAuditLog(ctx, common.NewAuditLog(user.ID, "auth.password.set", "user:"+user.ID, c.ClientIP(), nil))
	c.JSON(http.StatusOK, gin.H{
		"message":           "password set",
		"password_set":      true,
		"mfa_enabled":       true,
		"must_set_password": false,
	})
}

// clearPassword removes password login. MFA may remain for other login paths.
func (s *APIServer) clearPassword(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		TOTPCode string `json:"totp_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if !user.HasPassword() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no password set"})
		return
	}
	if !user.MFAEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mfa required to clear password"})
		return
	}
	newHashes, ok := auth.ValidateMFACode(user.TOTPSecret, user.BackupCodesHash, req.TOTPCode)
	if !ok {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid totp code"})
		return
	}
	if newHashes != "" {
		user.BackupCodesHash = newHashes
	}
	user.PasswordHash = ""
	if err := s.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear password"})
		return
	}
	_ = s.store.CreateAuditLog(ctx, common.NewAuditLog(user.ID, "auth.password.clear", "user:"+user.ID, c.ClientIP(), nil))
	c.JSON(http.StatusOK, gin.H{"message": "password cleared", "password_set": false, "must_set_password": true})
}

// loginWithPassword is a two-step password+TOTP login.
// Step 1: username+password → ticket + need_totp.
// Step 2: username+password+totp_code+ticket (or username+password+totp without ticket for CLI one-shot).
func (s *APIServer) loginWithPassword(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		TOTPCode string `json:"totp_code,omitempty"`
		Ticket   string `json:"ticket,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil || !user.HasPassword() {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if !user.MFAEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"error":                   "password login requires totp; complete password setup in the console first",
			"mfa_enrollment_required": true,
		})
		return
	}

	// Step 1: password OK, no TOTP yet → issue ticket
	if req.TOTPCode == "" {
		ticket, err := s.passwordTickets.issue(user.ID, user.Username)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue login ticket"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"need_totp": true,
			"ticket":    ticket,
			"message":   "password accepted; submit totp_code with ticket to finish",
		})
		return
	}

	// Step 2: verify ticket when provided (UI); CLI may omit ticket with password+totp together
	if req.Ticket != "" {
		if _, ok := s.passwordTickets.consume(req.Ticket, req.Username); !ok {
			metrics.Default.IncAuthFailure()
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired login ticket"})
			return
		}
	}

	newHashes, ok := auth.ValidateMFACode(user.TOTPSecret, user.BackupCodesHash, req.TOTPCode)
	if !ok {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid totp code", "mfa_required": true})
		return
	}
	if newHashes != "" && newHashes != user.BackupCodesHash {
		user.BackupCodesHash = newHashes
		_ = s.store.UpdateUser(ctx, user)
	}

	session, rawSession, err := s.authService.CreateSession(ctx, user.ID, c.ClientIP(), c.Request.UserAgent(), 60*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	resp := LoginResponse{SessionToken: rawSession, ExpiresAt: session.ExpiresAt}
	resp.User.ID = user.ID
	resp.User.Username = user.Username
	resp.User.Email = user.Email
	resp.User.IsAdmin = user.IsAdmin || user.EffectiveRole() == common.RoleAdmin
	resp.User.Role = user.EffectiveRole()
	resp.User.MFAEnabled = user.MFAEnabled

	if s.jwt != nil && s.jwt.Enabled() {
		if token, exp, err := s.jwt.Issue(user.ID, user.Username, resp.User.IsAdmin); err == nil {
			resp.AccessToken = token
			resp.ExpiresAt = exp
		}
	}

	_ = s.store.CreateAuditLog(ctx, common.NewAuditLog(user.ID, "auth.login.password", "user:"+user.ID, c.ClientIP(), nil))
	c.JSON(http.StatusOK, gin.H{
		"session_token":     resp.SessionToken,
		"access_token":      resp.AccessToken,
		"expires_at":        resp.ExpiresAt,
		"user":              enrichLoginUser(user),
		"password_set":      true,
		"must_set_password": false,
	})
}

func enrichLoginUser(user *common.User) gin.H {
	return gin.H{
		"id":                user.ID,
		"username":          user.Username,
		"email":             user.Email,
		"is_admin":          user.IsAdmin || user.EffectiveRole() == common.RoleAdmin,
		"role":              user.EffectiveRole(),
		"mfa_enabled":       user.MFAEnabled,
		"webauthn_enabled":  user.WebAuthnEnabled,
		"password_set":      user.HasPassword(),
		"must_set_password": !user.HasPassword(),
	}
}
