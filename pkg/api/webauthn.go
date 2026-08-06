package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
)

type webAuthnUser struct {
	user        *common.User
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte          { return []byte(u.user.ID) }
func (u *webAuthnUser) WebAuthnName() string        { return u.user.Username }
func (u *webAuthnUser) WebAuthnDisplayName() string { return u.user.Username }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}
func (u *webAuthnUser) WebAuthnIcon() string { return "" }

var webauthnSessions sync.Map

func (s *APIServer) registerWebAuthnRoutes(protected, public *gin.RouterGroup) {
	if s.webAuthn == nil {
		return
	}
	protected.POST("/webauthn/register/begin", s.webauthnRegisterBegin)
	protected.POST("/webauthn/register/finish", s.webauthnRegisterFinish)
	protected.GET("/webauthn/credentials", s.webauthnList)
	protected.DELETE("/webauthn/credentials/:id", s.webauthnDelete)
	public.POST("/webauthn/login/begin", s.webauthnLoginBegin)
	public.POST("/webauthn/login/finish", s.webauthnLoginFinish)
}

func (s *APIServer) loadWebAuthnUser(ctx context.Context, user *common.User) (*webAuthnUser, error) {
	creds, err := s.store.ListWebAuthnCredentials(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	wa := &webAuthnUser{user: user}
	for _, c := range creds {
		wa.credentials = append(wa.credentials, webauthn.Credential{
			ID:              c.CredentialID,
			PublicKey:       c.PublicKey,
			AttestationType: c.AttestationType,
			Flags: webauthn.CredentialFlags{
				BackupEligible: c.BackupEligible,
				BackupState:    c.BackupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:       c.AAGUID,
				SignCount:    c.SignCount,
				CloneWarning: c.CloneWarning,
			},
		})
	}
	return wa, nil
}

// alignUnknownCredentialFlags copies BE/BS from the assertion onto the matched
// credential when flags were never persisted (pre-v0.8.1 registrations), so
// ValidateLogin does not reject otherwise-valid YubiKey / platform authenticators.
func alignUnknownCredentialFlags(wa *webAuthnUser, stored []*common.WebAuthnCredential, parsed *protocol.ParsedCredentialAssertionData) {
	if wa == nil || parsed == nil {
		return
	}
	be := parsed.Response.AuthenticatorData.Flags.HasBackupEligible()
	bs := parsed.Response.AuthenticatorData.Flags.HasBackupState()
	rawID := parsed.Raw.RawID
	for i := range wa.credentials {
		if len(rawID) > 0 && !bytes.Equal(wa.credentials[i].ID, rawID) {
			continue
		}
		var known bool
		for _, s := range stored {
			if bytes.Equal(s.CredentialID, wa.credentials[i].ID) {
				known = s.FlagsKnown
				break
			}
		}
		if known {
			continue
		}
		wa.credentials[i].Flags.BackupEligible = be
		wa.credentials[i].Flags.BackupState = bs
	}
}

func (s *APIServer) webauthnRegisterBegin(c *gin.Context) {
	userID, _ := c.Get("user_id")
	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	options, session, err := s.webAuthn.BeginRegistration(waUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	webauthnSessions.Store("reg:"+user.ID, session)
	c.JSON(http.StatusOK, options)
}

func (s *APIServer) webauthnRegisterFinish(c *gin.Context) {
	userID, _ := c.Get("user_id")
	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	raw, ok := webauthnSessions.Load("reg:" + user.ID)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no registration in progress"})
		return
	}
	session := raw.(*webauthn.SessionData)
	credential, err := s.webAuthn.FinishRegistration(waUser, *session, c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	webauthnSessions.Delete("reg:" + user.ID)

	cred := &common.WebAuthnCredential{
		ID:              uuid.New().String(),
		UserID:          user.ID,
		Name:            "YubiKey / FIDO2",
		CredentialID:    credential.ID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		FlagsKnown:      true,
		CreatedAt:       time.Now(),
	}
	if err := s.store.CreateWebAuthnCredential(ctx, cred); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store credential"})
		return
	}
	user.WebAuthnEnabled = true
	_ = s.store.UpdateUser(ctx, user)
	c.JSON(http.StatusOK, gin.H{"message": "webauthn registered", "id": cred.ID})
}

func (s *APIServer) webauthnList(c *gin.Context) {
	userID, _ := c.Get("user_id")
	creds, err := s.store.ListWebAuthnCredentials(c.Request.Context(), userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(creds))
	for _, cr := range creds {
		out = append(out, gin.H{
			"id":         cr.ID,
			"name":       cr.Name,
			"created_at": cr.CreatedAt,
			"cred_id":    base64.RawURLEncoding.EncodeToString(cr.CredentialID),
		})
	}
	c.JSON(http.StatusOK, out)
}

func (s *APIServer) webauthnDelete(c *gin.Context) {
	userID, _ := c.Get("user_id")
	ctx := c.Request.Context()
	creds, err := s.store.ListWebAuthnCredentials(ctx, userID.(string))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	found := false
	for _, cr := range creds {
		if cr.ID == id {
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "credential not found"})
		return
	}
	if err := s.store.DeleteWebAuthnCredential(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(creds) <= 1 {
		user, _ := s.store.GetUser(ctx, userID.(string))
		if user != nil {
			user.WebAuthnEnabled = false
			_ = s.store.UpdateUser(ctx, user)
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func (s *APIServer) webauthnLoginBegin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	user, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, user)
	if err != nil || len(waUser.credentials) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no webauthn credentials registered"})
		return
	}
	options, session, err := s.webAuthn.BeginLogin(waUser)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	webauthnSessions.Store("login:"+user.Username, session)
	c.JSON(http.StatusOK, options)
}

func (s *APIServer) webauthnLoginFinish(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}
	var meta struct {
		Username string          `json:"username"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	username := meta.Username
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username required"})
		return
	}
	credBody := meta.Response
	if len(credBody) == 0 {
		credBody = body // client sent raw WebAuthn JSON with username query
	}

	ctx := c.Request.Context()
	user, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	waUser, err := s.loadWebAuthnUser(ctx, user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	raw, ok := webauthnSessions.Load("login:" + username)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no login in progress"})
		return
	}
	session := raw.(*webauthn.SessionData)

	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(credBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("parse: %v", err)})
		return
	}
	storedList, _ := s.store.ListWebAuthnCredentials(ctx, user.ID)
	alignUnknownCredentialFlags(waUser, storedList, parsed)
	cred, err := s.webAuthn.ValidateLogin(waUser, *session, parsed)
	if err != nil {
		metrics.Default.IncAuthFailure()
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	webauthnSessions.Delete("login:" + username)

	stored, err := s.store.GetWebAuthnCredentialByCredID(ctx, cred.ID)
	if err == nil {
		stored.SignCount = cred.Authenticator.SignCount
		stored.CloneWarning = cred.Authenticator.CloneWarning
		stored.BackupEligible = cred.Flags.BackupEligible
		stored.BackupState = cred.Flags.BackupState
		stored.FlagsKnown = true
		_ = s.store.UpdateWebAuthnCredential(ctx, stored)
	}

	httpSession, rawToken, err := s.authService.CreateSession(ctx, user.ID, c.ClientIP(), c.Request.UserAgent(), 60*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "session create failed"})
		return
	}
	resp := gin.H{
		"session_token": rawToken,
		"expires_at":    httpSession.ExpiresAt,
		"user":          enrichLoginUser(user),
	}
	if s.jwt != nil && s.jwt.Enabled() {
		if token, exp, err := s.jwt.Issue(user.ID, user.Username, user.IsAdmin); err == nil {
			resp["access_token"] = token
			resp["expires_at"] = exp
		}
	}
	c.JSON(http.StatusOK, resp)
}
