package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"github.com/orion-belt-dev/orion-belt/pkg/api"
	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/authz"
	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/orion-belt-dev/orion-belt/pkg/metrics"
	"github.com/orion-belt-dev/orion-belt/pkg/plugin"
	"github.com/orion-belt-dev/orion-belt/pkg/recording"
	"github.com/orion-belt-dev/orion-belt/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// Server represents the Orion-Belt SSH server
type Server struct {
	config        *common.Config
	store         database.Store
	authService   *auth.AuthService
	recorder      *recording.Recorder
	pluginManager *plugin.Manager
	logger        *common.Logger
	sshConfig     *ssh.ServerConfig
	sshConfigMu   sync.RWMutex
	hostPrivate   ssh.Signer
	hostCert      *ssh.Certificate
	listener      net.Listener
	apiServer     *api.APIServer
	agents        map[string]*AgentConnection
	agentsMu      sync.RWMutex
	shutdown      chan struct{}
	ca            *ca.Authority
}

// AgentConnection represents a connection from an agent
type AgentConnection struct {
	MachineID string
	SSHConn   *ssh.ServerConn
	Channels  <-chan ssh.NewChannel
	Requests  <-chan *ssh.Request
	LastSeen  time.Time
}

// New creates a new Orion-Belt server
func New(config *common.Config, logger *common.Logger) (*Server, error) {
	// Initialize database
	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to create database store: %w", err)
	}

	// Connect to database
	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Run migrations
	if err := store.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Initialize services
	authService := auth.NewAuthService(store, logger)

	if fga, err := authz.NewOpenFGA(config.Auth.OpenFGA, logger); err != nil {
		return nil, fmt.Errorf("openfga config: %w", err)
	} else if fga != nil {
		authService.SetAuthorizer(fga)
		logger.Info("OpenFGA authorizer enabled (%s)", config.Auth.OpenFGA.APIURL)
	}

	// Initialize recorder
	storagePath := resolveDirWithCreate(
		config.Recording.StoragePath,
		"/etc/orion-belt/recordings",
		logger,
	)
	recorder, err := recording.NewRecorder(storagePath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create recorder: %w", err)
	}

	recCrypto, err := recording.NewCrypto(config.Recording.EncryptionKey, logger)
	if err != nil {
		return nil, fmt.Errorf("recording encryption: %w", err)
	}
	recorder.SetCrypto(recCrypto)
	recorder.SetCompression(config.Recording.Compression)
	if recCrypto.Enabled() {
		logger.Info("Session recording encryption enabled")
	}

	var wa *webauthnlib.WebAuthn
	if config.Auth.WebAuthn.Enabled {
		display := config.Auth.WebAuthn.RPDisplay
		if display == "" {
			display = "Orion Belt"
		}
		rpid := config.Auth.WebAuthn.RPID
		if rpid == "" {
			rpid = "localhost"
		}
		origins := config.Auth.WebAuthn.Origins
		if len(origins) == 0 {
			origins = []string{"http://localhost:8080", "https://localhost:8080"}
		}
		wa, err = webauthnlib.New(&webauthnlib.Config{
			RPDisplayName: display,
			RPID:          rpid,
			RPOrigins:     origins,
		})
		if err != nil {
			return nil, fmt.Errorf("webauthn: %w", err)
		}
		logger.Info("WebAuthn/FIDO2 enabled (rp_id=%s)", rpid)
	}

	// Initialize plugin manager and register all bundled plugins — compiled
	// directly into this binary (see plugins_builtin.go), not dynamically
	// loaded .so files.
	pluginManager := plugin.NewManager(logger)
	if err := registerBuiltinPlugins(pluginManager); err != nil {
		logger.Warn("Failed to register built-in plugins: %v", err)
	}

	// Plugin config lives in the database so it can be edited from the UI
	// without a restart. server.yaml's `plugins:` block and the zero-config
	// defaults only seed the DB on a deployment's first boot; after that the
	// DB wins.
	pluginSettings, err := store.ListPluginSettings(ctx)
	if err != nil {
		logger.Warn("Failed to load plugin settings: %v", err)
	}
	settingsByName := make(map[string]*common.PluginSetting, len(pluginSettings))
	for _, s := range pluginSettings {
		settingsByName[s.Name] = s
	}
	for name, cfg := range defaultPluginConfigs {
		if _, seeded := settingsByName[name]; seeded {
			continue
		}
		seed := &common.PluginSetting{Name: name, Enabled: true, Config: cfg, UpdatedAt: time.Now()}
		if err := store.UpsertPluginSetting(ctx, seed); err != nil {
			logger.Warn("Failed to seed default plugin setting %s: %v", name, err)
			continue
		}
		settingsByName[name] = seed
	}
	for name, cfg := range config.Plugins {
		if _, seeded := settingsByName[name]; seeded {
			continue
		}
		seed := &common.PluginSetting{Name: name, Enabled: true, Config: cfg, UpdatedAt: time.Now()}
		if err := store.UpsertPluginSetting(ctx, seed); err != nil {
			logger.Warn("Failed to seed plugin setting %s from server.yaml: %v", name, err)
			continue
		}
		settingsByName[name] = seed
	}
	for name, setting := range settingsByName {
		if err := pluginManager.Configure(ctx, name, setting.Config); err != nil {
			logger.Warn("plugin %s failed to configure: %v", name, err)
		}
		if err := pluginManager.SetEnabled(name, setting.Enabled); err != nil {
			logger.Warn("plugin %s: %v", name, err)
		}
	}

	// SSH Certificate Authority (optional; auto-bootstraps its User/Host CA
	// keypairs on first enable, see pkg/ca).
	caAuthority, err := ca.New(config.SSHCA, store, logger)
	if err != nil {
		return nil, fmt.Errorf("ssh ca: %w", err)
	}
	if caAuthority != nil {
		logger.Info("SSH Certificate Authority enabled")
	}

	// Initialize API server
	apiServer := api.NewAPIServer(store, authService, logger, api.Options{
		JWTSecret:          config.Auth.JWTSecret,
		JWTExpiryHours:     config.Auth.JWTExpiryHours,
		PluginManager:      pluginManager,
		MetricsEnabled:     true,
		MFARequired:        config.Auth.MFARequired,
		RecordingCrypt:     recCrypto,
		Recorder:           recorder,
		WebAuthn:           wa,
		RateLimitPerMinute: config.Auth.RateLimitPerMinute,
		CA:                 caAuthority,
	})

	server := &Server{
		config:        config,
		store:         store,
		authService:   authService,
		recorder:      recorder,
		pluginManager: pluginManager,
		apiServer:     apiServer,
		logger:        logger,
		agents:        make(map[string]*AgentConnection),
		shutdown:      make(chan struct{}),
		ca:            caAuthority,
	}

	apiServer.SetAgentCommander(server)
	apiServer.SetTerminalBridge(server)

	// Configure SSH server
	if err := server.setupSSHConfig(); err != nil {
		return nil, fmt.Errorf("failed to setup SSH config: %w", err)
	}

	return server, nil
}

// setupSSHConfig sets up the SSH server configuration
func (s *Server) setupSSHConfig() error {
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: s.handlePublicKeyAuth,
		ServerVersion:     "SSH-2.0-OrionBelt",
	}

	// Load host key
	if s.config.Server.SSHHostKey == "" {
		return fmt.Errorf("SSH host key not configured")
	}
	privateBytes, err := os.ReadFile(s.config.Server.SSHHostKey)
	if err != nil {
		return fmt.Errorf("failed to read host key: %w", err)
	}

	private, err := ssh.ParsePrivateKey(privateBytes)
	if err != nil {
		return fmt.Errorf("failed to parse host key: %w", err)
	}
	s.hostPrivate = private
	cfg.AddHostKey(private)

	// Also present a Host-CA-signed cert for this same key, when SSH CA
	// is enabled. Cert-aware clients (proposing a *-cert-v01@openssh.com
	// algorithm during KEX) get the cert and verify it against the Host
	// CA instead of TOFU; clients that don't negotiate a cert algorithm
	// still get the raw key above — algorithm negotiation makes this
	// fully backward compatible without any dual-mode config flag.
	if s.ca != nil {
		if err := s.attachGatewayHostCert(cfg); err != nil {
			s.logger.Warn("failed to issue gateway host certificate: %v", err)
		}
	}

	s.sshConfigMu.Lock()
	s.sshConfig = cfg
	s.sshConfigMu.Unlock()
	return nil
}

// attachGatewayHostCert issues (or re-issues) the gateway Host cert and
// adds it to cfg. Caller must not hold sshConfigMu for writes only when
// swapping the whole config atomically via renewGatewayHostCert.
func (s *Server) attachGatewayHostCert(cfg *ssh.ServerConfig) error {
	if s.ca == nil || s.hostPrivate == nil {
		return nil
	}
	principals := s.config.SSHCA.HostPrincipals
	if len(principals) == 0 {
		principals = []string{s.config.Server.Host}
	}
	hostCert, err := s.ca.IssueHostCert(context.Background(), "", principals, s.hostPrivate.PublicKey(), s.ca.HostCertTTL())
	if err != nil {
		return err
	}
	certSigner, err := ssh.NewCertSigner(hostCert, s.hostPrivate)
	if err != nil {
		return err
	}
	cfg.AddHostKey(certSigner)
	s.hostCert = hostCert
	s.logger.Info("SSH host certificate issued for %v (expires %s)", principals,
		time.Unix(int64(hostCert.ValidBefore), 0).Format(time.RFC3339))
	return nil
}

func (s *Server) currentSSHConfig() *ssh.ServerConfig {
	s.sshConfigMu.RLock()
	defer s.sshConfigMu.RUnlock()
	return s.sshConfig
}

// Start starts the SSH server
func (s *Server) Start() error {
	// Determine API port (default to 8080 if not configured)
	apiPort := s.config.Server.APIPort
	if apiPort == 0 {
		apiPort = 8080
	}

	// Start API server in a goroutine
	apiAddr := fmt.Sprintf("%s:%d", s.config.Server.Host, apiPort)
	go func() {
		if err := s.apiServer.Start(apiAddr); err != nil {
			s.logger.Error("API server error: %v", err)
		}
	}()

	// Start SSH server
	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	s.logger.Info("Starting Orion-Belt SSH server on %s", addr)

	// Periodic recording retention cleanup
	if s.config.Recording.RetentionDays > 0 {
		go s.runRetentionLoop()
	}

	// Periodic expired HTTP session cleanup
	go s.runSessionCleanupLoop()

	if s.ca != nil {
		go s.runCARevocationRefreshLoop()
		go s.runGatewayHostCertRenewalLoop()
	}

	for {
		select {
		case <-s.shutdown:
			return nil
		default:
			tcpConn, err := listener.Accept()
			if err != nil {
				select {
				case <-s.shutdown:
					return nil
				default:
					s.logger.Error("Failed to accept connection: %v", err)
					continue
				}
			}

			go s.handleConnection(tcpConn)
		}
	}
}

// handleConnection handles a new TCP connection
func (s *Server) handleConnection(tcpConn net.Conn) {
	// Perform SSH handshake
	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, s.currentSSHConfig())
	if err != nil {
		s.logger.Debug("SSH handshake failed: %v", err)
		tcpConn.Close()
		return
	}

	// Host-CA-certified agents are identified by machine_id, set directly in
	// handleAgentCertAuth — no synthetic "agent user" lookup needed for them.
	if sshConn.Permissions.Extensions["cert_type"] == "host" {
		machineID := sshConn.Permissions.Extensions["machine_id"]
		if machine, err := s.store.GetMachine(context.Background(), machineID); err == nil {
			s.logger.Info("Host-cert agent connection for machine: %s", machine.Name)
			s.handleAgentConnection(sshConn, chans, reqs, machine)
			return
		} else {
			s.logger.Warn("Host-cert auth succeeded but machine %s lookup failed: %v", machineID, err)
		}
	}

	// Get authenticated user from connection metadata
	user := sshConn.Permissions.Extensions["user"]
	userID := sshConn.Permissions.Extensions["user_id"]

	s.logger.Info("New SSH connection from user: %s (ID: %s)", user, userID)

	// Check if this is a legacy (non-cert) agent connection: a synthetic
	// "agent user" row whose ID the target machine's agent_id points at.
	ctx := context.Background()
	machine, err := s.store.GetMachineByName(ctx, user)
	if err == nil {
		s.logger.Info("Machine found in database: %s (agent_id: %s, user_id: %s)", machine.Name, machine.AgentID, userID)
		// Check if the user is the agent for this machine
		if machine.AgentID == userID {
			// This is an agent connection
			s.handleAgentConnection(sshConn, chans, reqs, machine)
			return
		} else {
			s.logger.Info("User %s is not the agent for machine %s (expected agent_id: %s, got user_id: %s)", user, machine.Name, machine.AgentID, userID)
		}
	} else {
		s.logger.Info("Machine lookup failed for user '%s': %v", user, err)
	}

	s.logger.Info("Treating connection as client connection for user: %s", user)

	// This is a client connection
	sshUser := sshConn.Permissions.Extensions["ssh_user"]
	if sshUser == "" {
		sshUser = user
	}
	s.handleClientConnection(sshConn, chans, reqs, userID, user, sshUser)
}

// handlePublicKeyAuth dispatches SSH public-key authentication: a
// Host-CA-signed cert identifies an agent, a User-CA-signed cert
// identifies a human via short-lived credentials, and anything else falls
// back to the legacy static-pubkey path (unchanged, so existing
// deployments and not-yet-migrated agents keep working when SSH CA is
// enabled). Host certs authenticating here — rather than via the usual
// client-side HostKeyCallback — is intentionally non-standard SSH usage:
// agent identity is anchored in the Host CA. ssh.CertChecker.Authenticate
// refuses non-UserCert types by design, which is why this dispatches
// manually instead of delegating to it.
func (s *Server) handlePublicKeyAuth(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	if cert, ok := key.(*ssh.Certificate); ok {
		if s.ca == nil {
			return nil, fmt.Errorf("certificate authentication is not enabled on this server")
		}
		switch cert.CertType {
		case ssh.UserCert:
			return s.handleUserCertAuth(conn, cert)
		case ssh.HostCert:
			return s.handleAgentCertAuth(conn, cert)
		default:
			return nil, fmt.Errorf("unsupported certificate type %d", cert.CertType)
		}
	}
	return s.handleLegacyPublicKeyAuth(conn, key)
}

// handleUserCertAuth verifies a User-CA-signed certificate and authenticates
// the human it was issued to.
func (s *Server) handleUserCertAuth(conn ssh.ConnMetadata, cert *ssh.Certificate) (*ssh.Permissions, error) {
	authUser, _, _ := common.ParseGatewaySSHUser(conn.User())

	checker := s.ca.CertChecker()
	if !checker.IsUserAuthority(cert.SignatureKey) {
		return nil, fmt.Errorf("certificate signed by unrecognized user authority")
	}
	if err := checker.CheckCert(authUser, cert); err != nil {
		return nil, fmt.Errorf("certificate invalid: %w", err)
	}

	user, err := s.store.GetUserByUsername(context.Background(), authUser)
	if err != nil {
		s.logger.Warn("User cert auth: unknown user %s", authUser)
		return nil, fmt.Errorf("authentication failed")
	}
	// Defense in depth: the cert's KeyId should always match the principal
	// it was issued to (pkg/ca sets KeyId=username at issuance).
	if user.Username != cert.KeyId {
		s.logger.Warn("User cert auth: cert key_id %q does not match resolved user %q", cert.KeyId, user.Username)
		return nil, fmt.Errorf("authentication failed")
	}
	if s.config.Auth.MFARequired && !user.MFAEnabled {
		s.logger.Warn("SSH denied for %s: MFA enrollment required", authUser)
		return nil, fmt.Errorf("mfa enrollment required")
	}

	perms := &ssh.Permissions{
		Extensions: map[string]string{
			"user":      user.Username,
			"user_id":   user.ID,
			"ssh_user":  conn.User(),
			"cert_type": "user",
		},
	}

	hookCtx := &plugin.HookContext{UserID: user.ID, Data: make(map[string]interface{})}
	s.pluginManager.TriggerHook(context.Background(), plugin.HookPostAuth, hookCtx)

	return perms, nil
}

// handleAgentCertAuth verifies a Host-CA-signed certificate presented by
// an agent dialing in over the reverse tunnel, identifying it by machine
// rather than through the legacy synthetic-user mechanism.
func (s *Server) handleAgentCertAuth(conn ssh.ConnMetadata, cert *ssh.Certificate) (*ssh.Permissions, error) {
	checker := s.ca.CertChecker()
	if !checker.IsHostAuthority(cert.SignatureKey, "") {
		return nil, fmt.Errorf("certificate signed by unrecognized host authority")
	}
	if err := checker.CheckCert(conn.User(), cert); err != nil {
		return nil, fmt.Errorf("certificate invalid: %w", err)
	}

	machine, err := s.store.GetMachineByName(context.Background(), conn.User())
	if err != nil {
		s.logger.Warn("Agent cert auth: unknown machine %s", conn.User())
		return nil, fmt.Errorf("authentication failed")
	}

	return &ssh.Permissions{
		Extensions: map[string]string{
			"cert_type":  "host",
			"machine_id": machine.ID,
			"pubkey_fp":  ssh.FingerprintSHA256(cert.Key),
		},
	}, nil
}

// handleLegacyPublicKeyAuth is the original, pre-CA static-pubkey auth
// path: unchanged so existing deployments and not-yet-migrated agents
// keep working whether or not SSH CA is enabled.
func (s *Server) handleLegacyPublicKeyAuth(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	username := conn.User()

	s.logger.Debug("Authentication attempt for user: %s", username)

	// Convert ssh.PublicKey to the type expected by AuthenticateUser
	user, err := s.authService.AuthenticateUser(context.Background(), username, key)
	if err != nil {
		s.logger.Warn("Authentication failed for user: %s", username)
		return nil, fmt.Errorf("authentication failed")
	}

	if s.config.Auth.MFARequired && !user.MFAEnabled {
		s.logger.Warn("SSH denied for %s: MFA enrollment required", username)
		return nil, fmt.Errorf("mfa enrollment required")
	}

	// Store user info in permissions
	perms := &ssh.Permissions{
		Extensions: map[string]string{
			"user":     user.Username,
			"user_id":  user.ID,
			"ssh_user": conn.User(), // may include +machine for OpenSSH agentless
		},
	}

	// Trigger post-auth hook (auth already succeeded)
	hookCtx := &plugin.HookContext{
		UserID: user.ID,
		Data:   make(map[string]interface{}),
	}
	s.pluginManager.TriggerHook(context.Background(), plugin.HookPostAuth, hookCtx)

	return perms, nil
}

// handleAgentConnection handles an agent SSH connection
func (s *Server) handleAgentConnection(sshConn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, machine *common.Machine) {
	s.logger.Info("Agent connection detected for machine: %s", machine.Name)

	// Register agent
	agentConn := &AgentConnection{
		MachineID: machine.ID,
		SSHConn:   sshConn,
		Channels:  chans,
		Requests:  reqs,
		LastSeen:  time.Now(),
	}

	s.agentsMu.Lock()
	s.agents[machine.ID] = agentConn
	agentCount := len(s.agents)
	s.agentsMu.Unlock()
	metrics.Default.SetAgentsConnected(int64(agentCount))

	// Update machine status
	ctx := context.Background()
	now := time.Now()
	machine.IsActive = true
	machine.LastSeenAt = &now
	s.store.UpdateMachine(ctx, machine)

	s.logger.Info("Agent registered: %s", machine.Name)

	// Handle global requests (keepalive + host-cert renewal)
	go func() {
		for req := range reqs {
			s.logger.Debug("Agent global request: %s", req.Type)
			switch req.Type {
			case ca.AgentCertRenewRequest:
				s.handleAgentCertRenewal(req, machine, sshConn)
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}
	}()

	// Handle agent channels
	go func() {
		for newChannel := range chans {
			if newChannel.ChannelType() != "session" {
				newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
				continue
			}

			channel, requests, err := newChannel.Accept()
			if err != nil {
				s.logger.Error("Failed to accept channel: %v", err)
				continue
			}

			// Handle session requests
			go s.handleAgentSession(channel, requests, machine)
		}
	}()

	// Wait for connection to close
	sshConn.Wait()

	// Unregister agent
	s.agentsMu.Lock()
	delete(s.agents, machine.ID)
	agentCount = len(s.agents)
	s.agentsMu.Unlock()
	metrics.Default.SetAgentsConnected(int64(agentCount))

	// Update machine status
	machine.IsActive = false
	s.store.UpdateMachine(ctx, machine)

	s.logger.Info("Agent disconnected: %s", machine.Name)
}

// handleAgentSession handles agent session requests
func (s *Server) handleAgentSession(channel ssh.Channel, requests <-chan *ssh.Request, machine *common.Machine) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "exec":
			// Parse command
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				req.Reply(false, nil)
				continue
			}

			s.logger.Debug("Agent exec request: %s", payload.Command)

			// Handle agent commands
			switch payload.Command {
			case "agent-register":
				req.Reply(true, nil)
				channel.Write([]byte("Agent registered\n"))
				channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			case "heartbeat":
				req.Reply(true, nil)
				// Update last seen
				s.agentsMu.Lock()
				if agent, exists := s.agents[machine.ID]; exists {
					agent.LastSeen = time.Now()
				}
				s.agentsMu.Unlock()
				channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
				channel.Close()
			default:
				req.Reply(false, nil)
			}

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// handleClientConnection handles a client SSH connection
func (s *Server) handleClientConnection(sshConn *ssh.ServerConn, chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request, userID, username, sshUser string) {
	defer sshConn.Close()

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	// Handle each channel
	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			s.logger.Error("Failed to accept channel: %v", err)
			continue
		}

		go s.handleClientSession(channel, requests, userID, username, sshUser)
	}
}

// handleClientSession handles a client session (osh or vanilla OpenSSH).
func (s *Server) handleClientSession(channel ssh.Channel, requests <-chan *ssh.Request, userID, username, sshUser string) {
	defer channel.Close()

	var (
		ptyReq    *ptyRequest
		wantShell bool
		target    string
	)

	_, remoteUser, machineFromUser := common.ParseGatewaySSHUser(sshUser)
	if machineFromUser != "" {
		target = common.FormatTarget(remoteUser, machineFromUser)
	}

	for req := range requests {
		switch req.Type {
		case "exec":
			var payload struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
				req.Reply(false, nil)
				return
			}
			s.logger.Info("Client exec command: '%s'", payload.Command)
			req.Reply(true, nil)
			s.proxyToMachine(channel, payload.Command, userID, username, ptyReq, nil)
			return

		case "shell":
			wantShell = true
			req.Reply(true, nil)

		case "pty-req":
			ptyReq = &ptyRequest{}
			_ = ssh.Unmarshal(req.Payload, ptyReq)
			req.Reply(true, nil)

		case "env":
			req.Reply(true, nil)

		case "subsystem":
			var payload struct{ Name string }
			if err := ssh.Unmarshal(req.Payload, &payload); err == nil && payload.Name == "sftp" {
				req.Reply(false, nil)
				channel.Write([]byte("SFTP subsystem not available; use ocp or the web file browser.\n"))
				return
			}
			if req.WantReply {
				req.Reply(false, nil)
			}

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}

		if wantShell {
			if target == "" {
				msg := "Orion-Belt Gateway (OpenSSH compatible)\n\n" +
					"Usage:\n" +
					"  ssh alice+web-01@gateway              # interactive shell as root@web-01\n" +
					"  ssh alice+bob%web-01@gateway          # interactive shell as bob@web-01\n" +
					"  ssh alice@gateway 'bob@web-01'        # exec form (also works)\n" +
					"  ssh alice@gateway 'bob@web-01 uptime' # remote command\n\n" +
					"See docs/openssh-clients.md for ssh_config snippets.\n"
				channel.Write([]byte(msg))
				return
			}
			s.proxyToMachine(channel, target, userID, username, ptyReq, requests)
			return
		}
	}
}

type ptyRequest struct {
	Term     string
	Columns  uint32
	Rows     uint32
	Width    uint32
	Height   uint32
	Modelist string
}

// proxyToMachine proxies a client session to a target machine through an agent
func (s *Server) proxyToMachine(clientChannel ssh.Channel, commandLine, userID, username string, ptyReq *ptyRequest, remainingReqs <-chan *ssh.Request) {
	// Root span for the whole gateway -> agent -> target hop. Its context is
	// what carries trace identity down to the agent, so it has to wrap every
	// step below rather than just the proxy loop.
	ctx, span := tracing.Start(context.Background(), "gateway.ssh.session",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	s.logger.Info("proxyToMachine called: commandLine='%s', user='%s', userID='%s'", commandLine, username, userID)

	var remoteUser, machineName, remoteCommand string
	parts := strings.SplitN(commandLine, " ", 2)
	target := parts[0]
	if len(parts) > 1 {
		remoteCommand = parts[1]
	}

	// Extract remoteUser@machine
	if strings.Contains(target, "@") {
		userMachine := strings.SplitN(target, "@", 2)
		remoteUser = userMachine[0]
		machineName = userMachine[1]
	} else {
		machineName = target
		remoteUser = "root" // default
	}
	s.logger.Info("Gateway user '%s' connecting to '%s@%s'", username, remoteUser, machineName)

	// Identity of the hop, attached before anything can fail so a rejected
	// session is still attributable in the trace.
	tracing.SetAttributes(span,
		attribute.String("orion.user", username),
		attribute.String("orion.user_id", userID),
		attribute.String("orion.target.machine", machineName),
		attribute.String("orion.target.remote_user", remoteUser),
		attribute.Bool("orion.session.interactive", remoteCommand == ""),
	)

	authCtx, authSpan := tracing.Start(ctx, "gateway.authorize")
	machine, err := s.store.GetMachineByName(authCtx, machineName)
	if err != nil {
		s.logger.Error("Machine not found in database: %s (error: %v)", machineName, err)
		tracing.RecordError(authSpan, err)
		authSpan.End()
		tracing.RecordError(span, err)
		clientChannel.Write([]byte(fmt.Sprintf("Machine not found: %s\n", machineName)))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}

	s.logger.Info("Machine found: %s (ID: %s, active: %v)", machine.Name, machine.ID, machine.IsActive)
	tracing.SetAttributes(span, attribute.String("orion.target.machine_id", machine.ID))

	if err := s.authService.CheckPermissionWithRemoteUser(authCtx, userID, machine.ID, "ssh", remoteUser); err != nil {
		s.logger.Warn("Permission denied: %s cannot access %s@%s", username, remoteUser, machineName)
		tracing.RecordError(authSpan, err)
		authSpan.End()
		tracing.RecordError(span, err)

		errorMsg := "\n> Permission denied\n"
		errorMsg += fmt.Sprintf("   Gateway user: %s\n", username)
		errorMsg += fmt.Sprintf("   Target: %s@%s\n\n", remoteUser, machineName)
		errorMsg += fmt.Sprintf("You don't have permission to access '%s' on %s\n", remoteUser, machineName)
		errorMsg += "Contact your administrator or request access\n\n"

		clientChannel.Write([]byte(errorMsg))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}

	authSpan.End()

	// Get agent connection
	s.agentsMu.RLock()
	agentConn, exists := s.agents[machine.ID]
	s.agentsMu.RUnlock()

	if !exists || !machine.IsActive {
		// "Agent not connected" and "machine deactivated" look identical to
		// the user but mean different things to an operator reading a trace.
		tracing.SetAttributes(span,
			attribute.Bool("orion.agent.connected", exists),
			attribute.Bool("orion.target.active", machine.IsActive),
		)
		span.SetStatus(codes.Error, "machine unavailable")
		clientChannel.Write([]byte("Machine is not available\n"))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}

	// Create session record
	recordingPath := s.recorder.GetRecordingStoragePath()
	session := common.NewSession(userID, machine.ID, remoteUser, recordingPath)

	if err := s.store.CreateSession(ctx, session); err != nil {
		s.logger.Error("Failed to create session record: %v", err)
	}

	// Start recording
	sessionRecorder, err := s.recorder.StartRecording(session.ID)
	if err != nil {
		s.logger.Error("Failed to start recording: %v", err)
	}

	// Trigger session start hook
	hookCtx := &plugin.HookContext{
		UserID:    userID,
		MachineID: machine.ID,
		SessionID: session.ID,
		Data:      make(map[string]interface{}),
	}
	s.pluginManager.TriggerHook(ctx, plugin.HookSessionStart, hookCtx)
	metrics.Default.SessionStarted()

	tracing.SetAttributes(span, attribute.String("orion.session.id", session.ID))

	s.logger.Info("Opening session channel to agent")

	// This is the gateway -> agent hop. The span is a client span and its
	// context is injected into the channel-open extra data, which is what
	// makes the agent's spans children of this trace rather than a separate
	// one. Injecting from openCtx (not ctx) means the agent's parent is this
	// span, so the hop itself shows up as a measurable segment.
	openCtx, openSpan := tracing.Start(ctx, "gateway.agent.open_channel",
		trace.WithSpanKind(trace.SpanKindClient))
	agentChannel, agentReqs, err := agentConn.SSHConn.Conn.OpenChannel("session", tracing.InjectChannelData(openCtx))
	if err != nil {
		s.logger.Error("Failed to open session channel: %v", err)
		tracing.RecordError(openSpan, err)
		openSpan.End()
		tracing.RecordError(span, err)
		clientChannel.Write([]byte(fmt.Sprintf("Failed to connect to machine: %v\n", err)))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}
	openSpan.End()

	s.logger.Info("Successfully opened session channel to agent")

	// Forward PTY request to agent when present (OpenSSH interactive)
	if ptyReq != nil && remoteCommand == "" {
		ok, err := agentChannel.SendRequest("pty-req", true, ssh.Marshal(ptyReq))
		if err != nil || !ok {
			s.logger.Warn("Failed to forward pty-req to agent: ok=%v err=%v", ok, err)
		}
	}

	// Forward window-change from client while session is live
	if remainingReqs != nil {
		go func() {
			for req := range remainingReqs {
				switch req.Type {
				case "window-change":
					agentChannel.SendRequest("window-change", false, req.Payload)
					if req.WantReply {
						req.Reply(true, nil)
					}
				default:
					if req.WantReply {
						req.Reply(false, nil)
					}
				}
			}
		}()
	}

	// The proxy span covers the session body: for an interactive shell that is
	// the whole time the user is connected, so its duration is session length
	// rather than a latency measure. The setup spans above are where
	// connect-time latency shows up.
	if remoteCommand != "" {
		s.logger.Info("Executing command on agent: %s", remoteCommand)
		if strings.Contains(remoteCommand, "scp") {
			s.logger.Debug("Directing SCP command to agent: %s", remoteCommand)
		}
		_, execSpan := tracing.Start(ctx, "gateway.proxy.exec")
		// The command line can carry credentials in arguments, so record only
		// its shape — the first word — never the full string.
		tracing.SetAttributes(execSpan, attribute.String("orion.command", tracing.CommandName(remoteCommand)))
		s.executeCommand(clientChannel, agentChannel, agentReqs, remoteCommand, remoteUser, sessionRecorder)
		execSpan.End()
	} else {
		s.logger.Info("Starting interactive shell on agent")
		_, shellSpan := tracing.Start(ctx, "gateway.proxy.shell")
		s.startInteractiveShell(clientChannel, agentChannel, agentReqs, sessionRecorder, remoteUser)
		shellSpan.End()
	}

	endTime := time.Now()
	s.store.EndSession(ctx, session.ID, endTime)
	s.recorder.StopRecording(session.ID)

	s.pluginManager.TriggerHook(ctx, plugin.HookSessionEnd, hookCtx)
	metrics.Default.SessionEnded()

	s.logger.Info("Session ended for user: %s", username)
}

func splitFirst(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func (s *Server) executeCommand(clientChannel, agentChannel ssh.Channel, agentReqs <-chan *ssh.Request, command, remoteUser string, recorder *recording.SessionRecorder) {
	exitStatusReceived := make(chan struct{})

	go func() {
		for req := range agentReqs {
			if req.Type == "exit-status" {
				clientChannel.SendRequest("exit-status", false, req.Payload)
				s.logger.Debug("Forwarded exit-status to client")

				if req.WantReply {
					req.Reply(true, nil)
				}
				close(exitStatusReceived)
				continue
			}
			if req.WantReply {
				req.Reply(true, nil)
			}
		}
	}()

	ok, err := agentChannel.SendRequest("exec", true, ssh.Marshal(&struct{ Command, User string }{command, remoteUser}))
	if err != nil {
		s.logger.Error("Failed to send exec request: %v", err)
		clientChannel.Write([]byte(fmt.Sprintf("Failed to execute command: %v\n", err)))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}
	if !ok {
		s.logger.Error("Exec request rejected by agent")
		clientChannel.Write([]byte("Command execution rejected\n"))
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		return
	}

	s.logger.Info("Command execution started on agent")

	s.proxyConnection(clientChannel, agentChannel, recorder, true)

	select {
	case <-exitStatusReceived:
		s.logger.Info("Exit status received and forwarded successfully")
	case <-time.After(5 * time.Second):
		s.logger.Warn("Timeout waiting for exit status")
		clientChannel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
	}

	clientChannel.CloseWrite()
}

func (s *Server) startInteractiveShell(clientChannel, agentChannel ssh.Channel, agentReqs <-chan *ssh.Request, recorder *recording.SessionRecorder, username string) {
	statusDone := make(chan struct{})
	var once sync.Once
	go func() {
		for req := range agentReqs {
			switch req.Type {
			case "exit-status":
				once.Do(func() {
					clientChannel.SendRequest("exit-status", false, req.Payload)
					s.logger.Debug("Forwarded exit-status to client")
					close(statusDone)
				})
			default:
				if req.WantReply {
					req.Reply(true, nil)
				}
			}
		}
	}()

	type shellPayload struct {
		User string
	}

	payload := shellPayload{User: username}
	encodedPayload := ssh.Marshal(payload)
	ok, err := agentChannel.SendRequest("shell", true, encodedPayload)
	if err != nil || !ok {
		s.logger.Error("Shell request failed")
		return
	}

	s.logger.Info("Interactive shell started on agent")

	s.proxyConnection(clientChannel, agentChannel, recorder, false)

	select {
	case <-statusDone:
		s.logger.Debug("Exit status confirmed")
	case <-time.After(1 * time.Second):
		s.logger.Warn("Timed out waiting for exit status")
	}

	agentChannel.Close()
	clientChannel.Close()
}

// proxyConnection proxies data between client and agent with output-only recording.
func (s *Server) proxyConnection(client, agent ssh.Channel, recorder *recording.SessionRecorder, waitBoth bool) {
	var wg sync.WaitGroup
	wg.Add(2)

	done := make(chan struct{}, 2)

	// Client -> Agent (keystrokes are not recorded; PTY echo covers input in the cast)
	go func() {
		defer wg.Done()
		io.Copy(agent, client)

		if cw, ok := agent.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		s.logger.Debug("Client -> Agent I/O closed")
		done <- struct{}{}
	}()

	// Agent -> Client (PTY output → cast)
	go func() {
		defer wg.Done()
		var out io.Writer = client
		if recorder != nil {
			out = recording.NewRecordingWriter(client, recorder)
		}
		io.Copy(out, agent)

		client.CloseWrite()
		s.logger.Debug("Agent -> Client I/O closed")
		done <- struct{}{}
	}()

	if waitBoth {
		wg.Wait()
	} else {
		<-done
		s.logger.Debug("First I/O direction finished, stopping proxy for shell")
	}
}

// Stop stops the SSH server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Shutting down server...")

	close(s.shutdown)

	if s.listener != nil {
		s.listener.Close()
	}

	if err := s.pluginManager.ShutdownAll(ctx); err != nil {
		s.logger.Error("Failed to shutdown plugins: %v", err)
	}

	if err := s.store.Close(); err != nil {
		s.logger.Error("Failed to close database: %v", err)
	}

	return nil
}

// GetPluginManager returns the plugin manager
func (s *Server) GetPluginManager() *plugin.Manager {
	return s.pluginManager
}

// GetStore returns the database store
func (s *Server) GetStore() database.Store {
	return s.store
}

// GetAuthService returns the auth service
func (s *Server) GetAuthService() *auth.AuthService {
	return s.authService
}

// handleAgentCertRenewal re-issues a Host-CA cert for an already-authenticated
// agent. Request payload is the agent's public key in authorized_keys form
// (must match the key that authenticated this connection).
func (s *Server) handleAgentCertRenewal(req *ssh.Request, machine *common.Machine, sshConn *ssh.ServerConn) {
	if !req.WantReply {
		return
	}
	if s.ca == nil {
		req.Reply(false, []byte("ssh ca disabled"))
		return
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(req.Payload)
	if err != nil {
		req.Reply(false, []byte("invalid public key"))
		return
	}
	if fp := sshConn.Permissions.Extensions["pubkey_fp"]; fp != "" && ssh.FingerprintSHA256(pub) != fp {
		req.Reply(false, []byte("public key mismatch"))
		return
	}
	cert, err := s.ca.IssueHostCert(context.Background(), machine.ID, []string{machine.Name}, pub, s.ca.HostCertTTL())
	if err != nil {
		s.logger.Warn("agent cert renewal for %s failed: %v", machine.Name, err)
		req.Reply(false, []byte(err.Error()))
		return
	}
	s.logger.Info("Renewed Host cert for agent %s (expires %s)", machine.Name,
		time.Unix(int64(cert.ValidBefore), 0).Format(time.RFC3339))
	req.Reply(true, ssh.MarshalAuthorizedKey(cert))
}

// runCARevocationRefreshLoop reloads revoked serials so multi-instance or
// out-of-band DB revocations take effect without restart.
func (s *Server) runCARevocationRefreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			if err := s.ca.RefreshRevocationCache(context.Background()); err != nil {
				s.logger.Warn("CA revocation cache refresh: %v", err)
			}
		}
	}
}

// runGatewayHostCertRenewalLoop re-issues the gateway's own Host cert before
// expiry so clients using Host-CA trust keep verifying without downtime.
func (s *Server) runGatewayHostCertRenewalLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			s.renewGatewayHostCertIfNeeded()
		}
	}
}

func (s *Server) renewGatewayHostCertIfNeeded() {
	if s.ca == nil || s.hostPrivate == nil || s.hostCert == nil {
		return
	}
	ttl := time.Duration(s.hostCert.ValidBefore-s.hostCert.ValidAfter) * time.Second
	expiresAt := time.Unix(int64(s.hostCert.ValidBefore), 0)
	if !ca.NeedsRenewal(expiresAt, ttl) {
		return
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: s.handlePublicKeyAuth,
		ServerVersion:     "SSH-2.0-OrionBelt",
	}
	cfg.AddHostKey(s.hostPrivate)
	if err := s.attachGatewayHostCert(cfg); err != nil {
		s.logger.Warn("gateway host cert renewal failed: %v", err)
		return
	}
	s.sshConfigMu.Lock()
	s.sshConfig = cfg
	s.sshConfigMu.Unlock()
	s.logger.Info("Gateway host certificate renewed (expires %s)",
		time.Unix(int64(s.hostCert.ValidBefore), 0).Format(time.RFC3339))
}

// runSessionCleanupLoop periodically deletes expired HTTP sessions. Expiry is
// already enforced at validation time (a token past its expiry is rejected
// regardless of whether the row still exists), so this only reclaims storage —
// without it, http_sessions grows unbounded.
func (s *Server) runSessionCleanupLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	cleanup := func() {
		if err := s.authService.CleanupExpiredSessions(context.Background()); err != nil {
			s.logger.Warn("session cleanup: %v", err)
		}
	}
	cleanup()
	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

// runRetentionLoop periodically deletes expired recordings.
func (s *Server) runRetentionLoop() {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	path := s.recorder.GetRecordingStoragePath()
	days := s.config.Recording.RetentionDays
	if _, err := recording.EnforceRetention(path, days, s.logger); err != nil {
		s.logger.Warn("retention cleanup: %v", err)
	}
	if n, err := s.store.ExpireStalePendingAccessRequests(context.Background(), 7*24*time.Hour); err != nil {
		s.logger.Warn("expire access requests: %v", err)
	} else if n > 0 {
		s.logger.Info("Expired %d stale pending access requests", n)
	}
	for {
		select {
		case <-s.shutdown:
			return
		case <-ticker.C:
			if _, err := recording.EnforceRetention(path, days, s.logger); err != nil {
				s.logger.Warn("retention cleanup: %v", err)
			}
			if n, err := s.store.ExpireStalePendingAccessRequests(context.Background(), 7*24*time.Hour); err != nil {
				s.logger.Warn("expire access requests: %v", err)
			} else if n > 0 {
				s.logger.Info("Expired %d stale pending access requests", n)
			}
		}
	}
}

// ResolveMachine looks up a machine by name for the web terminal bridge.
func (s *Server) ResolveMachine(name string) (*common.Machine, error) {
	return s.store.GetMachineByName(context.Background(), name)
}

// OpenAgentSession opens a session channel to a connected agent.
//
// This is the entry point used by the web terminal and by agent control
// commands, rather than the OpenSSH proxy path in proxyToMachine. It is
// instrumented separately so those sessions reach the agent with trace context
// too; the resulting span is a root unless the caller supplies one via
// OpenAgentSessionContext.
func (s *Server) OpenAgentSession(machineID, remoteUser string) (ssh.Channel, <-chan *ssh.Request, error) {
	return s.OpenAgentSessionContext(context.Background(), machineID, remoteUser)
}

// OpenAgentSessionContext is OpenAgentSession with an explicit context, so a
// caller that already has a span (an HTTP handler, say) keeps the agent hop in
// the same trace instead of starting a disconnected one.
func (s *Server) OpenAgentSessionContext(ctx context.Context, machineID, remoteUser string) (ssh.Channel, <-chan *ssh.Request, error) {
	s.agentsMu.RLock()
	agentConn, exists := s.agents[machineID]
	s.agentsMu.RUnlock()
	if !exists {
		return nil, nil, fmt.Errorf("agent not connected")
	}

	openCtx, span := tracing.Start(ctx, "gateway.agent.open_channel",
		trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.target.machine_id", machineID),
		attribute.String("orion.target.remote_user", remoteUser),
	)

	channel, reqs, err := agentConn.SSHConn.Conn.OpenChannel("session", tracing.InjectChannelData(openCtx))
	tracing.RecordError(span, err)
	return channel, reqs, err
}

// ListConnectedAgents returns machine IDs of connected agents.
func (s *Server) ListConnectedAgents() []string {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	ids := make([]string, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	return ids
}

// DisconnectAgent closes the reverse tunnel for a connected agent.
func (s *Server) DisconnectAgent(machineID string) error {
	s.agentsMu.Lock()
	agentConn, exists := s.agents[machineID]
	if exists {
		delete(s.agents, machineID)
	}
	agentCount := len(s.agents)
	s.agentsMu.Unlock()
	if !exists {
		return fmt.Errorf("agent not connected: %s", machineID)
	}
	metrics.Default.SetAgentsConnected(int64(agentCount))
	if agentConn.SSHConn != nil {
		_ = agentConn.SSHConn.Close()
	}
	s.logger.Info("Agent disconnected by admin: %s", machineID)
	return nil
}

// SendAgentCommand opens a session to a connected agent and runs a control/exec command.
func (s *Server) SendAgentCommand(machineID, command string) ([]byte, error) {
	s.agentsMu.RLock()
	agentConn, exists := s.agents[machineID]
	s.agentsMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("agent not connected: %s", machineID)
	}

	channel, reqs, err := agentConn.SSHConn.Conn.OpenChannel("session", nil)
	if err != nil {
		return nil, fmt.Errorf("open agent channel: %w", err)
	}
	defer channel.Close()

	go ssh.DiscardRequests(reqs)

	// No remote-user impersonation for admin agent commands (orion:* control
	// commands are intercepted agent-side before reaching executeCommand; any
	// other command here runs as the agent's own identity, same as before).
	ok, err := channel.SendRequest("exec", true, ssh.Marshal(&struct{ Command, User string }{command, ""}))
	if err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("command rejected by agent")
	}

	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, channel)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("agent command timed out")
	}

	return []byte(buf.String()), nil
}

func resolveDirWithCreate(path, fallback string, logger *common.Logger) string {
	if path == "" {
		path = fallback
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		logger.Warn("failed to create directory: %s", err.Error())
		return fallback
	}

	return path
}
