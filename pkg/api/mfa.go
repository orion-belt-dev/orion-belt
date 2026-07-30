package api

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
)

// pendingEnrollments holds in-progress MFA enrollments (userID -> secret) until confirmed.
var pendingEnrollments sync.Map

func (s *APIServer) registerMFARoutes(protected *gin.RouterGroup) {
	protected.POST("/mfa/enroll", s.mfaEnroll)
	protected.POST("/mfa/confirm", s.mfaConfirm)
	protected.POST("/mfa/disable", s.mfaDisable)
	protected.GET("/mfa/status", s.mfaStatus)
}

// mfaEnroll starts TOTP enrollment and returns otpauth URL + secret + backup codes (preview).
func (s *APIServer) mfaEnroll(c *gin.Context) {
	userID, _ := c.Get("user_id")
	username, _ := c.Get("username")
	ctx := c.Request.Context()

	user, err := s.store.GetUser(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if user.MFAEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "mfa already enabled"})
		return
	}

	key, err := auth.GenerateTOTPSecret("Orion-Belt", username.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate totp secret"})
		return
	}

	codes, hashStore, err := auth.GenerateBackupCodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate backup codes"})
		return
	}

	pendingEnrollments.Store(userID.(string), map[string]string{
		"secret":        key.Secret(),
		"backup_hashes": hashStore,
	})

	c.JSON(http.StatusOK, gin.H{
		"secret":       key.Secret(),
		"otpauth_url":  key.URL(),
		"backup_codes": codes,
		"message":      "scan otpauth_url with your authenticator, then POST /mfa/confirm with a code",
	})
}

func (s *APIServer) mfaConfirm(c *gin.Context) {
	userID, _ := c.Get("user_id")
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	raw, ok := pendingEnrollments.Load(userID.(string))
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no pending enrollment; call /mfa/enroll first"})
		return
	}
	pending := raw.(map[string]string)
	secret := pending["secret"]
	if !auth.ValidateTOTP(secret, req.Code) {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid totp code"})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	user.MFAEnabled = true
	user.TOTPSecret = secret
	user.BackupCodesHash = pending["backup_hashes"]
	if err := s.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enable mfa"})
		return
	}
	pendingEnrollments.Delete(userID.(string))

	c.JSON(http.StatusOK, gin.H{"message": "mfa enabled", "mfa_enabled": true})
}

func (s *APIServer) mfaDisable(c *gin.Context) {
	userID, _ := c.Get("user_id")
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if !user.MFAEnabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mfa not enabled"})
		return
	}

	newHashes, ok := auth.ValidateMFACode(user.TOTPSecret, user.BackupCodesHash, req.Code)
	if !ok {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mfa code"})
		return
	}
	_ = newHashes

	if user.HasPassword() {
		c.JSON(http.StatusConflict, gin.H{
			"error": "cannot disable totp while a password is set; clear the password first",
		})
		return
	}

	user.MFAEnabled = false
	user.TOTPSecret = ""
	user.BackupCodesHash = ""
	if err := s.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to disable mfa"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "mfa disabled", "mfa_enabled": false})
}

func (s *APIServer) mfaStatus(c *gin.Context) {
	userID, _ := c.Get("user_id")
	user, err := s.store.GetUser(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mfa_enabled":  user.MFAEnabled,
		"mfa_required": s.mfaRequired,
	})
}

// enforceMFAAfterPubkey returns true if the request may proceed (MFA satisfied or not required).
// TOTP after SSH-key login is only required when auth.mfa_required is set. Enrolling TOTP for
// password login does not force a second factor on osh login / CLI key auth.
func (s *APIServer) enforceMFAAfterPubkey(c *gin.Context, userID string, totpCode string) bool {
	if !s.mfaRequired {
		return true
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return false
	}

	if !user.MFAEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"error":                   "mfa enrollment required",
			"mfa_enrollment_required": true,
		})
		return false
	}
	if totpCode == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":        "mfa code required",
			"mfa_required": true,
		})
		return false
	}

	newHashes, ok := auth.ValidateMFACode(user.TOTPSecret, user.BackupCodesHash, totpCode)
	if !ok {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid mfa code"})
		return false
	}
	if newHashes != "" {
		user.BackupCodesHash = newHashes
		_ = s.store.UpdateUser(ctx, user)
	}
	return true
}
