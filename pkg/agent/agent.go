package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/zrougamed/orion-belt/pkg/ca"
	"github.com/zrougamed/orion-belt/pkg/common"
	"github.com/zrougamed/orion-belt/pkg/tracing"
	"github.com/zrougamed/orion-belt/pkg/version"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	gossh "golang.org/x/crypto/ssh"
)

// Agent represents an Orion-Belt agent
type Agent struct {
	config    *common.Config
	logger    *common.Logger
	sshClient *gossh.Client
	sshConn   gossh.Conn
	machineID string
	privKey   gossh.Signer
	hostCert  *gossh.Certificate
	certPath  string
}

// New creates a new agent
func New(config *common.Config, logger *common.Logger) (*Agent, error) {
	return &Agent{
		config: config,
		logger: logger,
	}, nil
}

// Start starts the agent
func (a *Agent) Start() error {
	a.logger.Info("Starting Orion-Belt agent: %s", a.config.Agent.Name)

	// Connect to server
	if err := a.connectToServer(); err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}

	// Keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := a.maybeRenewHostCert(); err != nil {
				a.logger.Warn("Host cert renewal: %v", err)
			}
			if err := a.sendHeartbeat(); err != nil {
				a.logger.Error("Heartbeat failed: %v", err)
				// Attempt to reconnect
				if err := a.connectToServer(); err != nil {
					a.logger.Error("Failed to reconnect: %v", err)
				}
			}
		}
	}
}

// connectToServer establishes connection to the server
func (a *Agent) connectToServer() error {
	// Load SSH key from file
	keyData, err := os.ReadFile(a.config.Auth.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to read private key file: %w", err)
	}

	key, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}
	a.privKey = key

	// If a Host-CA-signed cert has been provisioned for this agent (see
	// cmd/server/agent.go's CA-aware registration branch), present it
	// instead of the raw key — this is what lets handleAgentCertAuth on
	// the gateway identify the agent by machine rather than through the
	// legacy synthetic-user mechanism. No cert file present -> unchanged
	// raw-key behavior, so already-registered agents keep working as-is.
	authMethod := gossh.PublicKeys(key)
	a.certPath = a.config.Auth.KeyFile + "-cert.pub"
	a.hostCert = nil
	if certBytes, err := os.ReadFile(a.certPath); err == nil {
		if pub, _, _, _, err := gossh.ParseAuthorizedKey(certBytes); err == nil {
			if cert, ok := pub.(*gossh.Certificate); ok {
				if certSigner, err := gossh.NewCertSigner(cert, key); err == nil {
					authMethod = gossh.PublicKeys(certSigner)
					a.hostCert = cert
					a.logger.Info("Using Host-CA-signed certificate for agent identity (expires %s)",
						time.Unix(int64(cert.ValidBefore), 0).Format(time.RFC3339))
				} else {
					a.logger.Warn("Failed to use cached agent certificate, falling back to raw key: %v", err)
				}
			}
		}
	}

	a.logger.Info("Attempting to authenticate as user: %s", a.config.Agent.Name)
	a.logger.Debug("Using key file: %s", a.config.Auth.KeyFile)

	hostKeyCallback, err := common.NewHostKeyCallback(common.HostKeyConfig{
		KnownHosts:            a.config.Auth.KnownHosts,
		StrictHostKeyChecking: a.config.Auth.StrictHostKeyChecking,
		HostCAPublicKey:       a.config.Auth.HostCAPublicKey,
	}, a.logger)
	if err != nil {
		return fmt.Errorf("host key verification setup: %w", err)
	}

	config := &gossh.ClientConfig{
		User:            a.config.Agent.Name,
		Auth:            []gossh.AuthMethod{authMethod},
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	serverAddr := net.JoinHostPort(a.config.Server.Host, strconv.Itoa(a.config.Server.Port))
	a.logger.Info("Connecting to server: %s", serverAddr)

	// Dial with custom connection to handle incoming channels
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("failed to dial server: %w", err)
	}

	// Perform SSH handshake
	sshConn, chans, reqs, err := gossh.NewClientConn(conn, serverAddr, config)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	// Store the connection
	a.sshConn = sshConn
	a.logger.Info("Connected to server: %s", serverAddr)

	a.logger.Debug("Setting up channel handlers...")

	go a.handleChannels(chans)

	a.logger.Debug("Channel handler started")

	// Handle global requests
	go gossh.DiscardRequests(reqs)

	a.logger.Debug("Global request handler started")

	a.logger.Debug("Agent fully initialized and ready")

	return nil
}

// handleChannels handles incoming channel requests from the server
func (a *Agent) handleChannels(chans <-chan gossh.NewChannel) {
	a.logger.Info("handleChannels started - waiting for incoming channels...")
	for newChannel := range chans {
		a.logger.Info("Received channel type: %s", newChannel.ChannelType())

		if newChannel.ChannelType() != "session" {
			a.logger.Warn("Rejecting non-session channel: %s", newChannel.ChannelType())
			newChannel.Reject(gossh.UnknownChannelType, "only session channels supported")
			continue
		}

		go a.handleSession(newChannel)
	}
	a.logger.Warn("Channel handler exiting")
}

// handleSession handles a single session request
func (a *Agent) handleSession(newChannel gossh.NewChannel) {
	a.logger.Info("Accepting session channel")

	// Continue the gateway's trace when it sent context in the channel-open
	// extra data. Absent or unparseable data yields a plain context and this
	// session simply roots its own trace, so an older gateway still works.
	ctx := tracing.ExtractChannelData(context.Background(), newChannel.ExtraData())
	linked := tracing.HasRemoteParent(ctx)

	ctx, span := tracing.Start(ctx, "agent.session", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.agent.name", a.config.Agent.Name),
		// Distinguishes a trace stitched to the gateway from one the agent
		// rooted alone — the quickest way to spot a half-configured fleet.
		attribute.Bool("orion.trace.linked_to_gateway", linked),
	)

	channel, requests, err := newChannel.Accept()
	if err != nil {
		a.logger.Error("Failed to accept channel: %v", err)
		tracing.RecordError(span, err)
		return
	}

	a.logger.Info("Session channel accepted, waiting for requests...")

	var ptyReq *ptyRequestMsg
	var execCommand string

	for req := range requests {
		a.logger.Debug("Session request: %s", req.Type)

		switch req.Type {
		case "pty-req":
			ptyReq = &ptyRequestMsg{}
			if err := gossh.Unmarshal(req.Payload, ptyReq); err != nil {
				a.logger.Error("Failed to parse pty-req: %v", err)
				req.Reply(false, nil)
			} else {
				a.logger.Info("PTY requested: %s %dx%d", ptyReq.Term, ptyReq.Columns, ptyReq.Rows)
				req.Reply(true, nil)
			}

		case "shell":
			var payload struct {
				User string
			}
			if err := gossh.Unmarshal(req.Payload, &payload); err != nil {
				a.logger.Error("Failed to parse exec payload: %v", err)
				req.Reply(false, nil)
				continue
			}
			username := payload.User
			a.logger.Info("Shell requested")

			req.Reply(true, nil)

			a.startShell(ctx, channel, username, ptyReq)
			channel.Close()
			return

		case "exec":
			var payload struct {
				Command string
				User    string
			}
			if err := gossh.Unmarshal(req.Payload, &payload); err != nil {
				a.logger.Error("Failed to parse exec payload: %v", err)
				req.Reply(false, nil)
				continue
			}

			execCommand = payload.Command
			a.logger.Info("Exec requested: %s", execCommand)
			req.Reply(true, nil)

			// Server control commands (orion:*) — do not execute as shell
			if strings.HasPrefix(execCommand, "orion:") {
				a.handleControlCommand(ctx, channel, execCommand)
				return
			}

			a.executeCommand(ctx, channel, execCommand, payload.User)
			return

		case "env":
			req.Reply(true, nil)

		default:
			a.logger.Debug("Unsupported request: %s", req.Type)
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}

	a.logger.Info("Session ended without exec or shell request")
	channel.Close()
}

type ptyRequestMsg struct {
	Term     string
	Columns  uint32
	Rows     uint32
	Width    uint32
	Height   uint32
	Modelist string
}

// startShell runs the interactive login shell on the target. The span it opens
// is the final gateway -> agent -> target hop; its duration is how long the
// user stayed connected, not a latency figure.
func (a *Agent) startShell(ctx context.Context, channel gossh.Channel, username string, ptyReq *ptyRequestMsg) {
	_, span := tracing.Start(ctx, "agent.target.shell")
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.target.remote_user", username),
		attribute.Bool("orion.target.pty", ptyReq != nil),
	)

	a.logger.Info("Starting interactive shell")

	if username == "" {
		username = "root"
	}

	cmd, err := a.buildShellCommand(username, ptyReq)
	if err != nil {
		a.logger.Error("Failed to prepare shell for %s: %v", username, err)
		channel.Write([]byte(fmt.Sprintf("Failed to start shell for %s: %v\r\n", username, err)))
		a.sendExitStatus(channel, 1)
		return
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		a.logger.Error("Failed to start shell with PTY: %v", err)
		channel.Write([]byte(fmt.Sprintf("Failed to start shell: %v\r\n", err)))
		a.sendExitStatus(channel, 1)
		return
	}
	defer ptmx.Close()

	if ptyReq != nil {
		pty.Setsize(ptmx, &pty.Winsize{
			Rows: uint16(ptyReq.Rows),
			Cols: uint16(ptyReq.Columns),
		})
	}

	a.logger.Info("Shell started with PTY (PID: %d)", cmd.Process.Pid)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(channel, ptmx)
		a.logger.Debug("PTY->Channel copy finished")
		channel.CloseWrite()
	}()

	go func() {
		defer wg.Done()
		io.Copy(ptmx, channel)
		a.logger.Debug("Channel->PTY copy finished")
	}()

	// Wait for PTY->Channel to finish
	err = cmd.Wait()

	// Send exit status to client
	exitStatus := 0
	if err != nil {
		a.logger.Info("Shell exited with error: %v", err)
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitStatus = exitErr.ExitCode()
		} else {
			exitStatus = 1
		}
	}
	// Send exit-status request
	a.sendExitStatus(channel, exitStatus)

	// Close the channel
	ptmx.Close()
	channel.CloseWrite()
	wg.Wait()

	a.logger.Debug("Closing channel after shell completion")
	channel.Close()
}

// unixIdentity is the subset of a local user-database entry needed to spawn a
// process as that user: uid/gid/supplementary groups, home directory, and shell.
type unixIdentity struct {
	Username string
	UID      uint32
	GID      uint32
	Groups   []uint32
	Home     string
	Shell    string
}

// resolveUnixIdentity looks up username in the local user database. Shared by
// buildShellCommand (interactive shell) and executeCommand (one-shot exec) so
// both impersonate a target user the same way.
func resolveUnixIdentity(username string) (*unixIdentity, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return nil, fmt.Errorf("unknown user %q: %w", username, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid uid for %q: %w", username, err)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid gid for %q: %w", username, err)
	}
	groups := []uint32{}
	if ids, gErr := u.GroupIds(); gErr == nil {
		for _, g := range ids {
			if n, pErr := strconv.ParseUint(g, 10, 32); pErr == nil {
				groups = append(groups, uint32(n))
			}
		}
	}
	return &unixIdentity{
		Username: username,
		UID:      uint32(uid),
		GID:      uint32(gid),
		Groups:   groups,
		Home:     u.HomeDir,
		Shell:    loginShell(username),
	}, nil
}

// credentialForIdentity returns the syscall.Credential needed to drop privileges
// to id, or nil if the agent is already running as that user (no-op). Errors if
// the agent isn't root and the target differs from its own identity — dropping
// privileges to an arbitrary uid requires CAP_SETUID/CAP_SETGID.
func credentialForIdentity(id *unixIdentity) (*syscall.Credential, error) {
	if id.UID == uint32(os.Geteuid()) {
		return nil, nil
	}
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("agent is not running as root; cannot start a session as %q", id.Username)
	}
	return &syscall.Credential{Uid: id.UID, Gid: id.GID, Groups: id.Groups}, nil
}

// buildShellCommand resolves the target OS user from the local user database
// (uid/gid/home/shell) and prepares a login shell to run under that identity by
// dropping privileges via syscall.Credential, the same low-level mechanism
// OpenSSH uses for session spawning. This avoids shelling out to `su`, whose
// behavior (PAM stack, setuid handling, hardcoded fallback shell) varies enough
// across distros/hardened images that it fails outright on some of them.
func (a *Agent) buildShellCommand(username string, ptyReq *ptyRequestMsg) (*exec.Cmd, error) {
	id, err := resolveUnixIdentity(username)
	if err != nil {
		return nil, err
	}
	cred, err := credentialForIdentity(id)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(id.Shell)
	// Leading "-" on argv[0] is the standard Unix convention (used by login, su -,
	// getty) that tells the shell to behave as a login shell.
	cmd.Args = []string{"-" + filepath.Base(id.Shell)}
	cmd.Dir = id.Home
	if _, statErr := os.Stat(cmd.Dir); statErr != nil {
		cmd.Dir = "/"
	}

	term := "xterm-256color"
	if ptyReq != nil && ptyReq.Term != "" {
		term = ptyReq.Term
	}
	cmd.Env = []string{
		"TERM=" + term,
		"HOME=" + id.Home,
		"USER=" + username,
		"LOGNAME=" + username,
		"SHELL=" + id.Shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}

	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	}

	return cmd, nil
}

// loginShell reads the target user's registered shell straight from /etc/passwd,
// falling back to /bin/sh if it's unset or the db can't be read. Never assumes
// /bin/bash exists — minimal distro images frequently don't ship it.
func loginShell(username string) string {
	return loginShellFromPasswd("/etc/passwd", username)
}

func loginShellFromPasswd(passwdPath, username string) string {
	data, err := os.ReadFile(passwdPath)
	if err != nil {
		return "/bin/sh"
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] != username {
			continue
		}
		if shell := strings.TrimSpace(fields[6]); shell != "" {
			return shell
		}
	}
	return "/bin/sh"
}

// executeCommand runs a single non-interactive command via /bin/sh -c. When
// username is non-empty, it resolves that user from the local user database and
// drops privileges to it (same mechanism as buildShellCommand) before running the
// command, so file-browser and CLI exec requests honor the remote user that was
// actually granted access — instead of always running as the agent's own uid.
// An empty username preserves the previous behavior (run as the agent's identity)
// for callers that don't carry a remote user, e.g. admin agent control commands.
// executeCommand runs a one-shot command on the target — the final
// gateway -> agent -> target hop for non-interactive sessions (including scp).
func (a *Agent) executeCommand(ctx context.Context, channel gossh.Channel, command string, username string) {
	_, span := tracing.Start(ctx, "agent.target.exec")
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.target.remote_user", username),
		// Program name only — a full command line can carry secrets, and
		// spans leave the box for a collector.
		attribute.String("orion.command", tracing.CommandName(command)),
	)

	a.logger.Info("Executing command as %q: %s", username, command)

	cmd := exec.Command("/bin/sh", "-c", command)

	if username == "" {
		cmd.Env = os.Environ()
	} else {
		id, err := resolveUnixIdentity(username)
		if err != nil {
			a.logger.Error("Failed to resolve user %s for exec: %v", username, err)
			channel.Write([]byte(fmt.Sprintf("Failed to execute command as %s: %v\n", username, err)))
			a.sendExitStatus(channel, 1)
			return
		}
		cred, err := credentialForIdentity(id)
		if err != nil {
			a.logger.Error("Failed to prepare credential for %s: %v", username, err)
			channel.Write([]byte(fmt.Sprintf("Failed to execute command as %s: %v\n", username, err)))
			a.sendExitStatus(channel, 1)
			return
		}
		if cred != nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
		}
		cmd.Dir = id.Home
		if _, statErr := os.Stat(cmd.Dir); statErr != nil {
			cmd.Dir = "/"
		}
		cmd.Env = []string{
			"HOME=" + id.Home,
			"USER=" + username,
			"LOGNAME=" + username,
			"SHELL=" + id.Shell,
			"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.logger.Error("Failed to get stdin pipe: %v", err)
		channel.Write([]byte(fmt.Sprintf("Failed to execute command: %v\n", err)))
		a.sendExitStatus(channel, 1)
		return
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.logger.Error("Failed to get stdout pipe: %v", err)
		channel.Write([]byte(fmt.Sprintf("Failed to execute command: %v\n", err)))
		a.sendExitStatus(channel, 1)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.logger.Error("Failed to get stderr pipe: %v", err)
		channel.Write([]byte(fmt.Sprintf("Failed to execute command: %v\n", err)))
		a.sendExitStatus(channel, 1)
		return
	}

	if err := cmd.Start(); err != nil {
		a.logger.Error("Failed to start command: %v", err)
		channel.Write([]byte(fmt.Sprintf("Failed to execute command: %v\n", err)))
		a.sendExitStatus(channel, 1)
		return
	}

	a.logger.Info("Command started (PID: %d)", cmd.Process.Pid)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		io.Copy(stdin, channel)
		stdin.Close()
		a.logger.Debug("Channel->stdin copy finished")
	}()
	go func() {
		defer wg.Done()
		io.Copy(channel, stdout)
		a.logger.Debug("stdout->Channel copy finished")
	}()
	go func() {
		defer wg.Done()
		io.Copy(channel.Stderr(), stderr)
		a.logger.Debug("stderr->Channel copy finished")
	}()

	cmdErr := cmd.Wait()
	a.logger.Debug("Command process finished, waiting for output I/O to complete")

	wg.Wait()
	a.logger.Debug("All output I/O finished")

	exitStatus := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			exitStatus = exitErr.ExitCode()
		} else {
			exitStatus = 1
		}
		a.logger.Info("Command exited with error: %v (status: %d)", cmdErr, exitStatus)
	} else {
		a.logger.Info("Command exited successfully")
	}

	a.sendExitStatus(channel, exitStatus)
	a.logger.Debug("Closing write side of channel after command completion")
	channel.CloseWrite()
}

func (a *Agent) sendExitStatus(channel gossh.Channel, status int) {
	statusMsg := make([]byte, 4)
	statusMsg[0] = byte(status >> 24)
	statusMsg[1] = byte(status >> 16)
	statusMsg[2] = byte(status >> 8)
	statusMsg[3] = byte(status)

	ok, err := channel.SendRequest("exit-status", true, statusMsg)
	if err != nil || !ok {
		a.logger.Warn("Failed to send exit status (ack: %v, err: %v)", ok, err)
	} else {
		a.logger.Debug("Sent exit status: %d and received ACK", status)
	}
}

// handleControlCommand processes server→agent management commands.
// Supported: orion:status, orion:health, orion:info, orion:ping, orion:restart
func (a *Agent) handleControlCommand(ctx context.Context, channel gossh.Channel, command string) {
	_, span := tracing.Start(ctx, "agent.control")
	defer span.End()
	// Control verbs (orion:*) are a fixed internal vocabulary with no user
	// data in them, so the whole command is safe to record.
	tracing.SetAttributes(span, attribute.String("orion.control.command", command))

	defer channel.Close()

	cmd := strings.TrimSpace(command)
	var result map[string]interface{}
	exitStatus := 0

	switch cmd {
	case "orion:ping":
		result = map[string]interface{}{
			"ok":   true,
			"pong": true,
		}
	case "orion:health", "orion:status":
		result = map[string]interface{}{
			"ok":          true,
			"status":      "healthy",
			"agent":       a.config.Agent.Name,
			"machine_id":  a.machineID,
			"connected":   a.sshConn != nil,
			"hostname":    hostnameOrEmpty(),
			"goos":        runtime.GOOS,
			"goarch":      runtime.GOARCH,
			"pid":         os.Getpid(),
			"uptime_hint": "connected",
		}
	case "orion:info":
		result = map[string]interface{}{
			"ok":         true,
			"agent":      a.config.Agent.Name,
			"tags":       a.config.Agent.Tags,
			"machine_id": a.machineID,
			"version":    version.String(),
			"goos":       runtime.GOOS,
			"goarch":     runtime.GOARCH,
			"num_cpu":    runtime.NumCPU(),
			"hostname":   hostnameOrEmpty(),
		}
	case "orion:restart":
		result = map[string]interface{}{
			"ok":      true,
			"message": "restart scheduled",
		}
		data, _ := json.Marshal(result)
		channel.Write(append(data, '\n'))
		a.sendExitStatus(channel, 0)
		channel.CloseWrite()
		go func() {
			time.Sleep(500 * time.Millisecond)
			cmd := exec.Command("systemctl", "restart", "orion-belt-agent")
			if err := cmd.Start(); err != nil {
				a.logger.Warn("systemctl restart failed (%v); exiting for process supervisor", err)
				os.Exit(0)
			}
		}()
		return
	default:
		result = map[string]interface{}{
			"ok":      false,
			"error":   "unknown control command",
			"command": cmd,
			"supported": []string{
				"orion:ping", "orion:health", "orion:status", "orion:info", "orion:restart",
			},
		}
		exitStatus = 1
	}

	data, _ := json.Marshal(result)
	channel.Write(append(data, '\n'))
	a.sendExitStatus(channel, exitStatus)
	channel.CloseWrite()
}

func hostnameOrEmpty() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func (a *Agent) sendHeartbeat() error {
	if a.sshConn == nil {
		return fmt.Errorf("not connected to server")
	}

	// Open a session channel for the heartbeat
	channel, reqs, err := a.sshConn.OpenChannel("session", nil)
	if err != nil {
		return fmt.Errorf("failed to open session channel: %w", err)
	}
	defer channel.Close()

	go gossh.DiscardRequests(reqs)

	// Send exec request
	ok, err := channel.SendRequest("exec", true, gossh.Marshal(&struct{ Command string }{"heartbeat"}))
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}
	if !ok {
		return fmt.Errorf("heartbeat request rejected")
	}

	return nil
}

// maybeRenewHostCert asks the gateway for a fresh Host-CA cert when the
// cached one is inside the renewal window. The new cert is written atomically
// next to the private key and used on the next reconnect.
func (a *Agent) maybeRenewHostCert() error {
	if a.sshConn == nil || a.privKey == nil || a.hostCert == nil || a.certPath == "" {
		return nil
	}
	ttl := time.Duration(a.hostCert.ValidBefore-a.hostCert.ValidAfter) * time.Second
	expiresAt := time.Unix(int64(a.hostCert.ValidBefore), 0)
	if !ca.NeedsRenewal(expiresAt, ttl) {
		return nil
	}

	pubLine := gossh.MarshalAuthorizedKey(a.privKey.PublicKey())
	ok, payload, err := a.sshConn.SendRequest(ca.AgentCertRenewRequest, true, pubLine)
	if err != nil {
		return fmt.Errorf("renew request: %w", err)
	}
	if !ok {
		msg := strings.TrimSpace(string(payload))
		if msg == "" {
			msg = "rejected"
		}
		return fmt.Errorf("renew rejected: %s", msg)
	}

	pub, _, _, _, err := gossh.ParseAuthorizedKey(payload)
	if err != nil {
		return fmt.Errorf("parse renewed cert: %w", err)
	}
	cert, okCert := pub.(*gossh.Certificate)
	if !okCert {
		return fmt.Errorf("renewed payload is not an SSH certificate")
	}

	tmp := a.certPath + ".tmp"
	if err := os.WriteFile(tmp, payload, 0644); err != nil {
		return fmt.Errorf("write renewed cert: %w", err)
	}
	if err := os.Rename(tmp, a.certPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install renewed cert: %w", err)
	}
	a.hostCert = cert
	a.logger.Info("Renewed Host-CA identity cert (expires %s); reconnect to present it",
		time.Unix(int64(cert.ValidBefore), 0).Format(time.RFC3339))
	return nil
}

// Stop stops the agent
func (a *Agent) Stop(ctx context.Context) error {
	a.logger.Info("Stopping agent...")

	if a.sshConn != nil {
		a.sshConn.Close()
	}

	return nil
}
