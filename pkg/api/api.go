package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/zrougamed/orion-belt/docs/openapi"
	"github.com/zrougamed/orion-belt/pkg/auth"
	"github.com/zrougamed/orion-belt/pkg/ca"
	"github.com/zrougamed/orion-belt/pkg/common"
	"github.com/zrougamed/orion-belt/pkg/database"
	"github.com/zrougamed/orion-belt/pkg/metrics"
	"github.com/zrougamed/orion-belt/pkg/plugin"
	"github.com/zrougamed/orion-belt/pkg/recording"
	"github.com/zrougamed/orion-belt/pkg/version"
	"github.com/zrougamed/orion-belt/web"
	"golang.org/x/crypto/ssh"
)

// APIServer provides REST API endpoints
type APIServer struct {
	store           database.Store
	authService     *auth.AuthService
	jwt             *auth.JWTManager
	pluginManager   *plugin.Manager
	logger          *common.Logger
	router          *gin.Engine
	rateLimiter     *rateLimiter
	agentCommander  AgentCommander
	mfaRequired     bool
	recordingCrypt  *recording.Crypto
	recorder        *recording.Recorder
	webAuthn        *webauthn.WebAuthn
	terminalBridge  TerminalBridge
	ca              *ca.Authority
	challenges      *challengeStore
	bootstrap       *bootstrapStore
	passwordTickets *passwordTicketStore
}

// AgentCommander sends control commands to connected agents.
type AgentCommander interface {
	SendAgentCommand(machineID, command string) ([]byte, error)
	ListConnectedAgents() []string
	DisconnectAgent(machineID string) error
}

// Options configures optional API server dependencies.
type Options struct {
	JWTSecret          string
	JWTExpiryHours     int
	PluginManager      *plugin.Manager
	AgentCommander     AgentCommander
	MetricsEnabled     bool
	MFARequired        bool
	RecordingCrypt     *recording.Crypto
	Recorder           *recording.Recorder
	WebAuthn           *webauthn.WebAuthn
	TerminalBridge     TerminalBridge
	RateLimitPerMinute int
	CA                 *ca.Authority
}

// NewAPIServer creates a new API server
func NewAPIServer(store database.Store, authService *auth.AuthService, logger *common.Logger, opts ...Options) *APIServer {
	gin.SetMode(gin.ReleaseMode)

	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	ttl := time.Duration(opt.JWTExpiryHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	rateLimit := opt.RateLimitPerMinute
	if rateLimit <= 0 {
		rateLimit = 600 // SPA-friendly default (was 60; too low for /ui)
	}

	api := &APIServer{
		store:           store,
		authService:     authService,
		jwt:             auth.NewJWTManager(opt.JWTSecret, ttl),
		pluginManager:   opt.PluginManager,
		agentCommander:  opt.AgentCommander,
		logger:          logger,
		router:          gin.New(),
		rateLimiter:     newRateLimiter(rateLimit, time.Minute),
		mfaRequired:     opt.MFARequired,
		recordingCrypt:  opt.RecordingCrypt,
		recorder:        opt.Recorder,
		webAuthn:        opt.WebAuthn,
		terminalBridge:  opt.TerminalBridge,
		ca:              opt.CA,
		challenges:      newChallengeStore(),
		bootstrap:       newBootstrapStore(),
		passwordTickets: newPasswordTicketStore(),
	}

	api.router.Use(gin.Recovery())
	api.router.Use(api.loggingMiddleware())
	api.router.Use(api.metricsMiddleware())
	api.setupRoutes(opt.MetricsEnabled)

	return api
}

// SetTerminalBridge wires web terminal / file browser after construction.
func (s *APIServer) SetTerminalBridge(b TerminalBridge) {
	s.terminalBridge = b
}

// SetAgentCommander wires remote agent control after the server is constructed.
func (s *APIServer) SetAgentCommander(c AgentCommander) {
	s.agentCommander = c
}

// setupRoutes configures all API routes
func (s *APIServer) setupRoutes(metricsEnabled bool) {
	// Health check endpoint
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "healthy",
			"service": "orion-belt-api",
			"version": version.Info(),
		})
	})

	s.router.GET("/api/v1/version", func(c *gin.Context) {
		c.JSON(200, version.Info())
	})

	s.router.GET("/api/v1/openapi.yaml", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=300")
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", openapi.Spec)
	})
	s.router.GET("/api/v1/openapi.json", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "Use /api/v1/openapi.yaml for the full OpenAPI 3.0 specification",
			"yaml":    "/api/v1/openapi.yaml",
			"docs":    "https://github.com/zrougamed/orion-belt/blob/master/docs/openapi/openapi.yaml",
		})
	})

	if metricsEnabled {
		s.router.GET("/metrics", gin.WrapH(metrics.Default.Handler()))
	}

	web.Register(s.router)

	v1 := s.router.Group("/api/v1")

	// Public endpoints
	public := v1.Group("/public")
	{
		public.POST("/register/agent", s.registerAgent)
		public.POST("/register/client", s.registerClient)
		public.POST("/auth/challenge", s.issueChallenge)
		public.POST("/auth/browser-bootstrap/redeem", s.redeemBrowserBootstrap)
		public.POST("/login", s.login)
		public.POST("/login/key", s.loginWithKey)
		public.POST("/login/token", s.loginJWT)

		// Inbound plugin webhooks (e.g. chat-platform interaction callbacks).
		// Unauthenticated at this layer; each plugin verifies its own caller.
		public.Any("/plugins/:name/*proxyPath", s.pluginWebhook)
	}

	// Protected endpoints
	protected := v1.Group("/")
	protected.Use(s.authMiddleware())
	protected.Use(s.rateLimitMiddleware())
	{
		// Authentication & current user
		protected.POST("/logout", s.logout)
		protected.GET("/auth/me", s.getCurrentUser)

		// API Key management
		protected.POST("/api-keys", s.createAPIKey)
		protected.GET("/api-keys", s.listAPIKeys)
		protected.POST("/api-keys/:id/revoke", s.revokeAPIKey)
		protected.DELETE("/api-keys/:id", s.deleteAPIKey)

		// User management
		protected.GET("/users", s.listUsers)
		protected.GET("/users/:id", s.getUser)

		// Machine management
		protected.GET("/machines", s.listMachines)
		protected.GET("/machines/:id", s.getMachine)

		// Permission management
		protected.GET("/permissions/user/:id", s.getUserPermissions)
		protected.GET("/permissions/machine/:id", s.getMachinePermissions)

		// Access request management
		protected.GET("/access-requests", s.listAccessRequests)
		protected.POST("/access-requests", s.createAccessRequest)
		protected.GET("/access-requests/:id", s.getAccessRequest)

		// Session management
		protected.GET("/sessions", s.listSessions)
		protected.GET("/sessions/active", s.listActiveSessions)
		protected.GET("/sessions/:id", s.getSession)
		protected.GET("/sessions/:id/content", s.getSessionContent)

		// Audit logs
		protected.GET("/audit-logs", s.listAuditLogs)
		protected.GET("/reports/:name/export", s.exportReport)
		protected.GET("/dashboard/usage", s.usageDashboard)

		// Notifications (in-app, scoped to the authenticated user)
		protected.GET("/notifications", s.listNotifications)
		protected.GET("/notifications/unread-count", s.unreadNotificationCount)
		protected.POST("/notifications/:id/read", s.markNotificationRead)
		protected.POST("/notifications/read-all", s.markAllNotificationsRead)
		protected.GET("/notifications/prefs", s.getNotificationPrefs)
		protected.PUT("/notifications/prefs", s.putNotificationPrefs)

		// First-run / operator setup checklist
		protected.GET("/setup/status", s.setupStatus)

		// Browser session bootstrap (see `osh login`)
		protected.POST("/auth/browser-bootstrap", s.issueBrowserBootstrap)

		// MFA + password+TOTP login
		s.registerMFARoutes(protected)
		s.registerPasswordRoutes(protected, public)

		// WebAuthn / FIDO2, terminal, files, SSH keys
		s.registerWebAuthnRoutes(protected, public)
		s.registerTerminalRoutes(protected)
	}

	// Admin-only endpoints
	admin := v1.Group("/admin")
	admin.Use(s.authMiddleware())
	admin.Use(s.adminMiddleware())
	{
		// User management
		admin.POST("/users", s.createUser)
		admin.PUT("/users/:id", s.updateUser)
		admin.DELETE("/users/:id", s.deleteUser)

		// Machine management
		admin.POST("/machines", s.createMachine)
		admin.PUT("/machines/:id", s.updateMachine)
		admin.DELETE("/machines/:id", s.deleteMachine)

		// Notification policy (bounds for per-user preferences)
		admin.GET("/notifications/policy", s.getNotificationPolicy)
		admin.PUT("/notifications/policy", s.putNotificationPolicy)

		// Permission management
		admin.GET("/permissions", s.listAllPermissions)
		admin.POST("/permissions", s.grantPermission)
		admin.PATCH("/permissions/:id", s.updatePermission)
		admin.DELETE("/permissions/:id", s.revokePermission)

		// Access request management
		admin.GET("/access-requests/pending", s.listPendingAccessRequests)
		admin.POST("/access-requests/:id/approve", s.approveAccessRequest)
		admin.POST("/access-requests/:id/reject", s.rejectAccessRequest)

		// Plugin management
		admin.GET("/plugins", s.listPlugins)
		admin.PUT("/plugins/:name/config", s.updatePluginConfig)
		admin.POST("/plugins/:name/enable", s.enablePlugin)
		admin.POST("/plugins/:name/disable", s.disablePlugin)

		// Agent remote management
		admin.GET("/agents/connected", s.listConnectedAgents)
		admin.POST("/agents/:machine_id/command", s.sendAgentCommand)
		admin.POST("/agents/:machine_id/disconnect", s.disconnectAgent)
		admin.POST("/agents/install-script", s.generateAgentInstallScript)
	}

	// SSH Certificate Authority (cert issuance/trust lookup on `protected`,
	// lifecycle management on `admin`)
	s.registerSSHCARoutes(protected, admin)
}

// RegisterAgentRequest represents an agent registration request
type RegisterAgentRequest struct {
	Name      string            `json:"name" binding:"required"`
	Hostname  string            `json:"hostname" binding:"required"`
	Port      int               `json:"port" binding:"required"`
	PublicKey string            `json:"public_key" binding:"required"`
	Tags      map[string]string `json:"tags"`
}

// RegisterAgentResponse represents an agent registration response
type RegisterAgentResponse struct {
	UserID          string `json:"user_id,omitempty"`
	MachineID       string `json:"machine_id"`
	Message         string `json:"message"`
	HostCertificate string `json:"host_certificate,omitempty"`
	HostCAPublicKey string `json:"host_ca_public_key,omitempty"`
}

// LoginWithKeyRequest represents an API key login request
type LoginWithKeyRequest struct {
	Username        string `json:"username" binding:"required"`
	PublicKey       string `json:"public_key" binding:"required"`
	Challenge       string `json:"challenge" binding:"required"`
	SignatureFormat string `json:"signature_format" binding:"required"`
	Signature       string `json:"signature" binding:"required"`
	TOTPCode        string `json:"totp_code,omitempty"`
}

// LoginWithKeyResponse represents an API key login response
type LoginWithKeyResponse struct {
	APIKey    string     `json:"api_key"`
	ExpiresAt *time.Time `json:"expires_at"`
	User      struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
		IsAdmin  bool   `json:"is_admin"`
	} `json:"user"`
}

func normalizeKey(k string) string {
	parts := strings.Fields(strings.TrimSpace(k))
	if len(parts) >= 2 {
		// Returns "ssh-rsa <key-data>"
		return parts[0] + " " + parts[1]
	}
	return k
}

// loginWithKey handles API key-based authentication for client tools
// This is specifically for osh/ocp/oadmin tools, not web sessions
func (s *APIServer) loginWithKey(c *gin.Context) {
	var req LoginWithKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Get user by username
	user, err := s.store.GetUserByUsername(ctx, req.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	// Verify the public key matches what's in the database
	// The public key should be in OpenSSH format
	if normalizeKey(user.PublicKey) != normalizeKey(req.PublicKey) {
		s.logger.Warn("Public key mismatch for user: %s", req.Username)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	presented, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key"})
		return
	}
	if err := s.verifyPossession(req.Username, req.Challenge, req.SignatureFormat, req.Signature, presented); err != nil {
		s.logger.Warn("Proof-of-possession failed for user %s: %v", req.Username, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !s.enforceMFAAfterPubkey(c, user.ID, req.TOTPCode) {
		return
	}

	// Generate API key with 24 hour expiration for CLI tools
	expiresAt := time.Now().Add(24 * time.Hour)
	apiKey, rawKey, err := s.authService.GenerateAPIKey(
		ctx,
		user.ID,
		"CLI Authentication",
		&expiresAt,
	)
	if err != nil {
		s.logger.Error("Failed to create API key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create API key"})
		return
	}

	s.logger.Info("API key created for user: %s (ID: %s)", user.Username, user.ID)

	response := LoginWithKeyResponse{
		APIKey:    rawKey, // Return the raw key, not the hash
		ExpiresAt: apiKey.ExpiresAt,
	}
	response.User.ID = user.ID
	response.User.Username = user.Username
	response.User.Email = user.Email
	response.User.IsAdmin = user.IsAdmin

	c.JSON(http.StatusOK, response)
}

// registerAgent handles agent registration
func (s *APIServer) registerAgent(c *gin.Context) {
	var req RegisterAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()

	existingMachine, _ := s.store.GetMachineByName(ctx, req.Name)
	if existingMachine != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "agent already registered"})
		return
	}

	machine := common.NewMachine(req.Name, req.Hostname, req.Port, req.Tags)

	if s.ca != nil {
		if err := s.store.CreateMachine(ctx, machine); err != nil {
			s.logger.Error("Failed to create machine: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
		if err != nil {
			_ = s.store.DeleteMachine(ctx, machine.ID)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid public_key"})
			return
		}
		hostCert, err := s.ca.IssueHostCert(ctx, machine.ID, []string{req.Name}, pub, s.ca.HostCertTTL())
		if err != nil {
			_ = s.store.DeleteMachine(ctx, machine.ID)
			s.logger.Error("Failed to issue agent host certificate: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
			return
		}
		_, hostCA := s.ca.ExportPublicKeys()
		s.logger.Info("Agent registered with Host CA: %s (machine_id=%s)", req.Name, machine.ID)
		c.JSON(http.StatusCreated, RegisterAgentResponse{
			MachineID:       machine.ID,
			HostCertificate: string(ssh.MarshalAuthorizedKey(hostCert)),
			HostCAPublicKey: hostCA,
			Message:         "Agent registered successfully (host certificate issued)",
		})
		return
	}

	// Legacy: synthetic agent user + machine
	agentUser := common.NewUser(req.Name, fmt.Sprintf("%s@agent.orion-belt", req.Name), req.PublicKey, false)
	if err := s.store.CreateUser(ctx, agentUser); err != nil {
		s.logger.Error("Failed to create agent user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
		return
	}

	machine.AgentID = agentUser.ID
	if err := s.store.CreateMachine(ctx, machine); err != nil {
		_ = s.store.DeleteUser(ctx, agentUser.ID)
		s.logger.Error("Failed to create machine: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register agent"})
		return
	}

	s.logger.Info("Agent registered: %s (user_id=%s, machine_id=%s)", req.Name, agentUser.ID, machine.ID)

	c.JSON(http.StatusCreated, RegisterAgentResponse{
		UserID:    agentUser.ID,
		MachineID: machine.ID,
		Message:   "Agent registered successfully",
	})
}

// RegisterClientRequest represents a client registration request
type RegisterClientRequest struct {
	Username  string `json:"username" binding:"required"`
	Email     string `json:"email" binding:"required"`
	PublicKey string `json:"public_key" binding:"required"`
	IsAdmin   bool   `json:"is_admin"`
}

// RegisterClientResponse represents a client registration response
type RegisterClientResponse struct {
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

// registerClient handles client registration
func (s *APIServer) registerClient(c *gin.Context) {
	var req RegisterClientRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()

	// Check if user already exists
	existingUser, _ := s.store.GetUserByUsername(ctx, req.Username)
	if existingUser != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		return
	}

	// Create user account
	user := common.NewUser(req.Username, req.Email, req.PublicKey, req.IsAdmin)
	if err := s.store.CreateUser(ctx, user); err != nil {
		s.logger.Error("Failed to create user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register client"})
		return
	}

	s.logger.Info("Client registered: %s (user_id=%s, is_admin=%v)", req.Username, user.ID, req.IsAdmin)

	c.JSON(http.StatusCreated, RegisterClientResponse{
		UserID:  user.ID,
		Message: "Client registered successfully",
	})
}

// DefaultAccessRequestTTLSeconds is the JIT access TTL used when a request
// doesn't specify a duration: 30 minutes. A duration of 0 means unlimited.
const DefaultAccessRequestTTLSeconds = 30 * 60

// CreateAccessRequestRequest represents an access request creation
type CreateAccessRequestRequest struct {
	MachineID   string   `json:"machine_id" binding:"required"`
	RemoteUsers []string `json:"remote_users" binding:"required"`
	Reason      string   `json:"reason" binding:"required"`
	AccessType  string   `json:"access_type"` // ssh, scp, both (default both)
	// Duration in seconds. Omitted -> DefaultAccessRequestTTLSeconds (30m). 0 -> unlimited.
	Duration *int `json:"duration"`
}

// createAccessRequest handles access request creation
func (s *APIServer) createAccessRequest(c *gin.Context) {
	var req CreateAccessRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	duration := DefaultAccessRequestTTLSeconds
	if req.Duration != nil {
		if *req.Duration < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "duration must be >= 0 (0 = unlimited)"})
			return
		}
		duration = *req.Duration
	}

	// Get user ID from context (set by auth middleware)
	userID, _ := c.Get("user_id")

	ctx := context.Background()
	accessReq := common.NewAccessRequest(
		userID.(string),
		req.MachineID,
		req.RemoteUsers,
		req.Reason,
		duration,
	)
	if req.AccessType != "" {
		accessReq.AccessType = req.AccessType
	}

	if err := s.store.CreateAccessRequest(ctx, accessReq); err != nil {
		s.logger.Error("Failed to create access request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create access request"})
		return
	}

	metrics.Default.IncAccessRequest()

	if s.pluginManager != nil {
		_ = s.pluginManager.TriggerHook(ctx, plugin.HookAccessRequest, &plugin.HookContext{
			UserID:    userID.(string),
			MachineID: req.MachineID,
			Data: map[string]interface{}{
				"reason":     req.Reason,
				"duration":   duration,
				"request_id": accessReq.ID,
			},
		})
	}

	s.logger.Info("Access request created: request_id=%s, user_id=%s, machine_id=%s",
		accessReq.ID, userID, req.MachineID)
	s.recordAudit(c, "access.request", "access_request:"+accessReq.ID, map[string]interface{}{
		"machine_id": req.MachineID,
		"duration":   duration,
	})

	c.JSON(http.StatusCreated, accessReq)
}

// getAccessRequest gets a specific access request by ID
func (s *APIServer) getAccessRequest(c *gin.Context) {
	requestID := c.Param("id")
	ctx := context.Background()

	request, err := s.store.GetAccessRequest(ctx, requestID)
	if err != nil {
		if err == database.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "access request not found"})
			return
		}
		s.logger.Error("Failed to get access request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get access request"})
		return
	}
	if !isPrivilegedViewer(c) {
		userID, _ := c.Get("user_id")
		if uid, _ := userID.(string); request.UserID != uid {
			c.JSON(http.StatusNotFound, gin.H{"error": "access request not found"})
			return
		}
	}

	c.JSON(http.StatusOK, request)
}

// ApproveAccessRequestRequest represents an approval request
type ApproveAccessRequestRequest struct {
	ReviewerID string `json:"reviewer_id"` // optional; defaults to authenticated user
	// Duration lets the approver override the requester's TTL (seconds).
	// 0 = unlimited. Omitted = keep the originally requested duration.
	Duration *int `json:"duration"`
}

// approveAccessRequest handles access request approval
func (s *APIServer) approveAccessRequest(c *gin.Context) {
	requestID := c.Param("id")

	var req ApproveAccessRequestRequest
	_ = c.ShouldBindJSON(&req)

	reviewerID := req.ReviewerID
	if reviewerID == "" {
		if uid, ok := c.Get("user_id"); ok {
			reviewerID, _ = uid.(string)
		}
	}
	if reviewerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reviewer_id required"})
		return
	}
	if req.Duration != nil && *req.Duration < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration must be >= 0 (0 = unlimited)"})
		return
	}

	ctx := c.Request.Context()

	if err := s.authService.ApproveAccessRequestWithTTL(ctx, requestID, reviewerID, req.Duration); err != nil {
		s.logger.Error("Failed to approve access request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	accessReq, err := s.store.GetAccessRequest(ctx, requestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request approved but failed to fetch"})
		return
	}

	s.logger.Info("Access request approved: request_id=%s, reviewer_id=%s", requestID, reviewerID)
	s.recordAudit(c, "access.approve", "access_request:"+requestID, map[string]interface{}{
		"user_id":    accessReq.UserID,
		"machine_id": accessReq.MachineID,
		"duration":   accessReq.Duration,
	})

	s.notifyAccessRequestApproved(ctx, accessReq)

	if s.pluginManager != nil {
		_ = s.pluginManager.TriggerHook(ctx, plugin.HookAccessGranted, &plugin.HookContext{
			UserID:    accessReq.UserID,
			MachineID: accessReq.MachineID,
			Data: map[string]interface{}{
				"request_id":  requestID,
				"reviewer_id": reviewerID,
				"status":      "approved",
				"duration":    accessReq.Duration,
			},
		})
	}

	c.JSON(http.StatusOK, accessReq)
}

// RejectAccessRequestRequest represents a rejection request
type RejectAccessRequestRequest struct {
	ReviewerID string `json:"reviewer_id"`
}

// rejectAccessRequest handles access request rejection
func (s *APIServer) rejectAccessRequest(c *gin.Context) {
	requestID := c.Param("id")

	var req RejectAccessRequestRequest
	_ = c.ShouldBindJSON(&req)

	reviewerID := req.ReviewerID
	if reviewerID == "" {
		if uid, ok := c.Get("user_id"); ok {
			reviewerID, _ = uid.(string)
		}
	}
	if reviewerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reviewer_id required"})
		return
	}

	ctx := c.Request.Context()

	if err := s.authService.RejectAccessRequest(ctx, requestID, reviewerID); err != nil {
		s.logger.Error("Failed to reject access request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	accessReq, err := s.store.GetAccessRequest(ctx, requestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request rejected but failed to fetch"})
		return
	}

	s.logger.Info("Access request rejected: request_id=%s, reviewer_id=%s", requestID, reviewerID)
	s.recordAudit(c, "access.reject", "access_request:"+requestID, map[string]interface{}{
		"user_id":    accessReq.UserID,
		"machine_id": accessReq.MachineID,
	})

	s.notifyAccessRequestRejected(ctx, accessReq)

	c.JSON(http.StatusOK, accessReq)
}

// listPendingAccessRequests lists all pending access requests
func (s *APIServer) listPendingAccessRequests(c *gin.Context) {
	ctx := context.Background()

	requests, err := s.store.ListPendingAccessRequests(ctx)
	if err != nil {
		s.logger.Error("Failed to list pending access requests: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list requests"})
		return
	}

	c.JSON(http.StatusOK, requests)
}

// listAccessRequests lists access requests with pagination. Privileged
// roles (admin/operator/auditor) see every request; a plain user only sees
// their own request history (all statuses).
func (s *APIServer) listAccessRequests(c *gin.Context) {
	ctx := context.Background()
	var (
		requests []*common.AccessRequest
		err      error
	)
	if isPrivilegedViewer(c) {
		requests, err = s.store.ListAllAccessRequests(ctx, 200, 0)
	} else {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(string)
		requests, err = s.store.ListUserAccessRequests(ctx, uid, 200, 0)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requests == nil {
		requests = []*common.AccessRequest{}
	}
	c.JSON(http.StatusOK, requests)
}

// Placeholder section marker kept for readability of legacy handlers.
func (s *APIServer) listUsers(c *gin.Context) {
	ctx := context.Background()
	users, err := s.store.ListUsers(ctx, 100, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
}

func (s *APIServer) getUser(c *gin.Context) {
	ctx := context.Background()
	user, err := s.store.GetUser(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (s *APIServer) updateUser(c *gin.Context) {
	ctx := c.Request.Context()
	user, err := s.store.GetUser(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var req struct {
		Email     *string `json:"email"`
		PublicKey *string `json:"public_key"`
		IsAdmin   *bool   `json:"is_admin"`
		Role      *string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Email != nil {
		user.Email = *req.Email
	}
	if req.PublicKey != nil {
		user.PublicKey = *req.PublicKey
	}
	if req.Role != nil {
		switch *req.Role {
		case common.RoleAdmin, common.RoleOperator, common.RoleAuditor, common.RoleUser:
			user.Role = *req.Role
			user.IsAdmin = (*req.Role == common.RoleAdmin)
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role (admin|operator|auditor|user)"})
			return
		}
	}
	if req.IsAdmin != nil {
		user.IsAdmin = *req.IsAdmin
		if *req.IsAdmin {
			user.Role = common.RoleAdmin
		} else if user.Role == common.RoleAdmin || user.Role == "" {
			user.Role = common.RoleUser
		}
	}

	if err := s.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.recordAudit(c, "user.update", "user:"+user.ID, map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	})
	c.JSON(http.StatusOK, user)
}

func (s *APIServer) deleteUser(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	user, _ := s.store.GetUser(ctx, id)
	if err := s.store.DeleteUser(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	meta := map[string]interface{}{}
	if user != nil {
		meta["username"] = user.Username
	}
	s.recordAudit(c, "user.delete", "user:"+id, meta)
	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

func (s *APIServer) listMachines(c *gin.Context) {
	ctx := context.Background()
	machines, err := s.store.ListMachines(ctx, 100, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, machines)
}

func (s *APIServer) getMachine(c *gin.Context) {
	ctx := context.Background()
	machine, err := s.store.GetMachine(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}
	c.JSON(http.StatusOK, machine)
}

func (s *APIServer) getUserPermissions(c *gin.Context) {
	ctx := c.Request.Context()
	permissions, err := s.store.ListUserPermissions(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, permissions)
}

func (s *APIServer) getMachinePermissions(c *gin.Context) {
	ctx := c.Request.Context()
	permissions, err := s.store.ListMachinePermissions(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, permissions)
}

func (s *APIServer) createUser(c *gin.Context) {
	var req struct {
		Username  string `json:"username" binding:"required"`
		Email     string `json:"email" binding:"required"`
		PublicKey string `json:"public_key"`
		Role      string `json:"role"`
		IsAdmin   bool   `json:"is_admin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	role := req.Role
	if role == "" {
		if req.IsAdmin {
			role = common.RoleAdmin
		} else {
			role = common.RoleUser
		}
	}
	switch role {
	case common.RoleAdmin, common.RoleOperator, common.RoleAuditor, common.RoleUser:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role (admin|operator|auditor|user)"})
		return
	}

	user := common.NewUser(req.Username, req.Email, req.PublicKey, role == common.RoleAdmin)
	user.Role = role
	user.IsAdmin = role == common.RoleAdmin

	ctx := c.Request.Context()
	if err := s.store.CreateUser(ctx, user); err != nil {
		s.logger.Error("Failed to create user: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	s.recordAudit(c, "user.create", "user:"+user.ID, map[string]interface{}{
		"username": user.Username,
		"role":     user.Role,
	})
	c.JSON(http.StatusCreated, user)
}

func (s *APIServer) createMachine(c *gin.Context) {
	var req struct {
		Name     string            `json:"name" binding:"required"`
		Hostname string            `json:"hostname" binding:"required"`
		Port     int               `json:"port"`
		Tags     map[string]string `json:"tags"`
		AgentID  string            `json:"agent_id"`
		IsActive *bool             `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Port <= 0 {
		req.Port = 22
	}

	ctx := c.Request.Context()
	if existing, err := s.store.GetMachineByName(ctx, req.Name); err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "machine name already exists"})
		return
	}

	machine := common.NewMachine(req.Name, req.Hostname, req.Port, req.Tags)
	machine.AgentID = req.AgentID
	if req.IsActive != nil {
		machine.IsActive = *req.IsActive
	}

	if err := s.store.CreateMachine(ctx, machine); err != nil {
		s.logger.Error("Failed to create machine: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create machine"})
		return
	}

	s.recordAudit(c, "machine.create", "machine:"+machine.ID, map[string]interface{}{
		"name": machine.Name,
	})
	c.JSON(http.StatusCreated, machine)
}

func (s *APIServer) updateMachine(c *gin.Context) {
	ctx := c.Request.Context()
	machine, err := s.store.GetMachine(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}

	var req struct {
		Name     *string            `json:"name"`
		Hostname *string            `json:"hostname"`
		Port     *int               `json:"port"`
		Tags     *map[string]string `json:"tags"`
		AgentID  *string            `json:"agent_id"`
		IsActive *bool              `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != nil && *req.Name != "" && *req.Name != machine.Name {
		if existing, err := s.store.GetMachineByName(ctx, *req.Name); err == nil && existing != nil && existing.ID != machine.ID {
			c.JSON(http.StatusConflict, gin.H{"error": "machine name already exists"})
			return
		}
		machine.Name = *req.Name
	}
	if req.Hostname != nil {
		machine.Hostname = *req.Hostname
	}
	if req.Port != nil && *req.Port > 0 {
		machine.Port = *req.Port
	}
	if req.Tags != nil {
		machine.Tags = *req.Tags
	}
	if req.AgentID != nil {
		machine.AgentID = *req.AgentID
	}
	if req.IsActive != nil {
		machine.IsActive = *req.IsActive
	}

	if err := s.store.UpdateMachine(ctx, machine); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	s.recordAudit(c, "machine.update", "machine:"+machine.ID, map[string]interface{}{
		"name": machine.Name,
	})
	c.JSON(http.StatusOK, machine)
}

func (s *APIServer) deleteMachine(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	machine, err := s.store.GetMachine(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "machine not found"})
		return
	}

	archive := c.Query("archive") == "true" || c.Query("soft") == "true"
	if archive {
		machine.IsActive = false
		if err := s.store.UpdateMachine(ctx, machine); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		s.recordAudit(c, "machine.archive", "machine:"+id, map[string]interface{}{"name": machine.Name})
		c.JSON(http.StatusOK, gin.H{"message": "machine archived", "machine": machine})
		return
	}

	if err := s.store.DeleteMachine(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.recordAudit(c, "machine.delete", "machine:"+id, map[string]interface{}{"name": machine.Name})
	c.JSON(http.StatusOK, gin.H{"message": "machine deleted"})
}

func (s *APIServer) grantPermission(c *gin.Context) {
	var req struct {
		UserID      string   `json:"user_id" binding:"required"`
		MachineID   string   `json:"machine_id" binding:"required"`
		AccessType  string   `json:"access_type" binding:"required"` // ssh, scp, or both
		RemoteUsers []string `json:"remote_users"`
		ExpiresAt   string   `json:"expires_at"`
		DurationSec *int     `json:"duration_seconds"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	adminID, _ := c.Get("user_id")
	grantedBy, _ := adminID.(string)

	var duration *time.Duration
	if req.DurationSec != nil && *req.DurationSec > 0 {
		d := time.Duration(*req.DurationSec) * time.Second
		duration = &d
	} else if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at must be RFC3339"})
			return
		}
		d := time.Until(t)
		if d <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at must be in the future"})
			return
		}
		duration = &d
	}

	ctx := c.Request.Context()
	if err := s.authService.GrantPermission(ctx, req.UserID, req.MachineID, req.AccessType, req.RemoteUsers, grantedBy, duration); err != nil {
		s.logger.Error("Failed to grant permission: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to grant permission"})
		return
	}

	s.recordAudit(c, "permission.grant", "machine:"+req.MachineID, map[string]interface{}{
		"user_id":     req.UserID,
		"access_type": req.AccessType,
	})

	// Return the latest permission for this pair (best-effort).
	perms, err := s.store.ListUserPermissions(ctx, req.UserID)
	if err == nil {
		for _, p := range perms {
			if p.MachineID == req.MachineID && p.AccessType == req.AccessType {
				c.JSON(http.StatusCreated, p)
				return
			}
		}
	}
	c.JSON(http.StatusCreated, gin.H{"message": "permission granted"})
}

func (s *APIServer) listAllPermissions(c *gin.Context) {
	limit := 500
	if v := c.Query("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil && limit > 0 {
			if limit > 2000 {
				limit = 2000
			}
		}
	}
	perms, err := s.store.ListAllPermissions(c.Request.Context(), limit, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if perms == nil {
		perms = []*common.Permission{}
	}
	c.JSON(http.StatusOK, perms)
}

func (s *APIServer) updatePermission(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		AccessType  *string  `json:"access_type"`
		RemoteUsers []string `json:"remote_users"`
		ExpiresAt   *string  `json:"expires_at"` // RFC3339 or "" to clear (permanent)
		DurationSec *int     `json:"duration_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	perm, err := s.store.GetPermission(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "permission not found"})
		return
	}
	if req.AccessType != nil && *req.AccessType != "" {
		perm.AccessType = *req.AccessType
	}
	if req.RemoteUsers != nil {
		perm.RemoteUsers = req.RemoteUsers
	}
	if req.DurationSec != nil {
		if *req.DurationSec <= 0 {
			perm.ExpiresAt = nil
		} else {
			t := time.Now().Add(time.Duration(*req.DurationSec) * time.Second)
			perm.ExpiresAt = &t
		}
	} else if req.ExpiresAt != nil {
		if *req.ExpiresAt == "" {
			perm.ExpiresAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "expires_at must be RFC3339"})
				return
			}
			perm.ExpiresAt = &t
		}
	}
	adminID, _ := c.Get("user_id")
	if g, ok := adminID.(string); ok && g != "" {
		perm.GrantedBy = g
		perm.GrantedAt = time.Now()
	}
	if err := s.store.UpdatePermission(ctx, perm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update permission"})
		return
	}
	s.recordAudit(c, "permission.update", "permission:"+id, map[string]interface{}{
		"access_type": perm.AccessType,
	})
	c.JSON(http.StatusOK, perm)
}

func (s *APIServer) revokePermission(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	if err := s.authService.RevokePermission(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.recordAudit(c, "permission.revoke", "permission:"+id, nil)
	c.JSON(http.StatusOK, gin.H{"message": "permission revoked"})
}

// filterSessionsByUser keeps only sessions owned by userID (used to scope
// non-privileged callers to their own session playback/history).
func filterSessionsByUser(sessions []*common.Session, userID string) []*common.Session {
	out := sessions[:0]
	for _, sess := range sessions {
		if sess.UserID == userID {
			out = append(out, sess)
		}
	}
	return out
}

func (s *APIServer) listSessions(c *gin.Context) {
	ctx := c.Request.Context()
	status := strings.TrimSpace(c.Query("status"))
	privileged := isPrivilegedViewer(c)
	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	var (
		sessions []*common.Session
		err      error
	)
	switch status {
	case "active":
		sessions, err = s.store.ListActiveSessions(ctx)
	default:
		if privileged {
			sessions, err = s.store.ListSessions(ctx, 200, 0)
		} else {
			sessions, err = s.store.ListUserSessions(ctx, uid, 200, 0)
		}
		if err == nil && status != "" {
			filtered := sessions[:0]
			for _, sess := range sessions {
				if sess.Status == status {
					filtered = append(filtered, sess)
				}
			}
			sessions = filtered
		}
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !privileged {
		sessions = filterSessionsByUser(sessions, uid)
	}
	if sessions == nil {
		sessions = []*common.Session{}
	}
	c.JSON(http.StatusOK, sessions)
}

func (s *APIServer) listActiveSessions(c *gin.Context) {
	ctx := c.Request.Context()
	sessions, err := s.store.ListActiveSessions(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !isPrivilegedViewer(c) {
		userID, _ := c.Get("user_id")
		uid, _ := userID.(string)
		sessions = filterSessionsByUser(sessions, uid)
	}
	if sessions == nil {
		sessions = []*common.Session{}
	}
	c.JSON(http.StatusOK, sessions)
}

func (s *APIServer) getSession(c *gin.Context) {
	ctx := c.Request.Context()
	session, err := s.store.GetSession(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if !isPrivilegedViewer(c) {
		userID, _ := c.Get("user_id")
		if uid, _ := userID.(string); session.UserID != uid {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
	}
	c.JSON(http.StatusOK, session)
}

func (s *APIServer) getSessionContent(c *gin.Context) {
	ctx := c.Request.Context()
	session, err := s.store.GetSession(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if !isPrivilegedViewer(c) {
		userID, _ := c.Get("user_id")
		if uid, _ := userID.(string); session.UserID != uid {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
	}

	if session.RecordingPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no recording for this session"})
		return
	}
	if _, err := os.Stat(session.RecordingPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "recording file missing on server"})
		return
	}

	s.recordAudit(c, "session.playback", "session:"+session.ID, map[string]interface{}{
		"machine_id": session.MachineID,
		"user_id":    session.UserID,
	})

	contentType := "text/plain; charset=utf-8"
	if strings.HasSuffix(session.RecordingPath, ".cast") {
		contentType = "application/x-asciicast"
	}

	var plain []byte
	var errRead error
	if s.recordingCrypt != nil && s.recordingCrypt.Enabled() {
		plain, errRead = s.recordingCrypt.DecryptFile(session.RecordingPath)
	} else {
		plain, errRead = os.ReadFile(session.RecordingPath)
	}
	if errRead != nil {
		s.logger.Error("Failed to read recording: %v", errRead)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read recording"})
		return
	}
	plain, errRead = recording.MaybeDecompress(plain)
	if errRead != nil {
		s.logger.Error("Failed to decompress recording: %v", errRead)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decompress recording"})
		return
	}
	c.Data(http.StatusOK, contentType, plain)
}

func (s *APIServer) listAuditLogs(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 100
	if v := c.Query("limit"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &limit); n == 1 && err == nil && limit > 0 {
			if limit > 500 {
				limit = 500
			}
		}
	}
	var filters map[string]interface{}
	if !isPrivilegedViewer(c) {
		userID, _ := c.Get("user_id")
		filters = map[string]interface{}{"user_id": userID}
	}
	logs, err := s.store.ListAuditLogs(ctx, limit, 0, filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logs)
}
