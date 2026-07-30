package client

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// SSHClient represents an Orion-Belt SSH client
type SSHClient struct {
	config *common.Config
	logger *common.Logger
}

// NewSSHClient creates a new SSH client
func NewSSHClient(config *common.Config, logger *common.Logger) (*SSHClient, error) {
	return &SSHClient{
		config: config,
		logger: logger,
	}, nil
}

// Connect connects to a target machine through the Orion-Belt server
func (c *SSHClient) Connect(target string, username string) error {
	// Parse target for user@machine format
	targetMachine := target
	targetUser := username
	targetSSHUser := username

	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		if len(parts) == 2 {
			targetMachine = parts[1]
			targetSSHUser = parts[0]
		}
	}

	c.logger.Info("Connecting to %s through Orion-Belt server", targetMachine)

	// Uses a short-lived SSH cert when the server has SSH CA enabled,
	// transparently falling back to the raw static key otherwise.
	signer, err := LoadSigner(c.config, "", c.logger)
	if err != nil {
		return err
	}

	// Configure SSH client
	// Get username from parameter, environment, or use root as fallback
	if targetUser == "" {
		targetUser = os.Getenv("USER")
		if targetUser == "" {
			targetUser = "root"
		}
	}

	hostKeyCallback, err := common.NewHostKeyCallback(common.HostKeyConfig{
		KnownHosts:            c.config.Auth.KnownHosts,
		StrictHostKeyChecking: c.config.Auth.StrictHostKeyChecking,
		HostCAPublicKey:       c.config.Auth.HostCAPublicKey,
	}, c.logger)
	if err != nil {
		return fmt.Errorf("host key verification setup: %w", err)
	}

	config := &ssh.ClientConfig{
		User: targetUser,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	c.logger.Info("Authenticating as user: %s", targetUser)

	// Connect to Orion-Belt server
	serverAddr := fmt.Sprintf("%s:%d", c.config.Server.Host, c.config.Server.Port)
	client, err := ssh.Dial("tcp", serverAddr, config)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer client.Close()

	// Create session
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Set up terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to set raw mode: %w", err)
		}
		defer term.Restore(fd, oldState)

		// Get terminal size
		width, height, err := term.GetSize(fd)
		if err != nil {
			width, height = 80, 24
		}

		// Request PTY
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}

		if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
			return fmt.Errorf("failed to request PTY: %w", err)
		}
	}

	// Set up I/O
	if term.IsTerminal(fd) {
		// Wrap Stdout and Stderr to handle the "staircase" effect
		session.Stdout = &rawModeWriter{os.Stdout}
		session.Stderr = &rawModeWriter{os.Stderr}
	} else {
		session.Stdout = os.Stdout
		session.Stderr = os.Stderr
	}
	session.Stdin = os.Stdin

	// Start shell with target as argument
	connectionStr := targetSSHUser + "@" + targetMachine
	if err := session.Start(connectionStr); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	err = session.Wait()
	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			os.Exit(exitErr.ExitStatus())
		}
		return fmt.Errorf("session wait error: %w", err)
	}

	return nil
}

type rawModeWriter struct {
	io.Writer
}

func (w *rawModeWriter) Write(p []byte) (n int, err error) {
	s := strings.ReplaceAll(string(p), "\n", "\r\n")
	_, err = w.Writer.Write([]byte(s))
	return len(p), err
}

// RequestAccess requests temporary access to a machine
func (c *SSHClient) RequestAccess(target, reason string, duration int) error {
	// Parse target for machine name
	targetMachine := target
	var remoteUsers []string

	if strings.Contains(target, "@") {
		parts := strings.SplitN(target, "@", 2)
		if len(parts) == 2 {
			remoteUsers = []string{parts[0]}
			targetMachine = parts[1]
		}
	}

	if len(remoteUsers) == 0 {
		defaultUser := os.Getenv("USER")
		if defaultUser == "" {
			defaultUser = "root"
		}
		remoteUsers = []string{defaultUser}
	}

	c.logger.Info("Requesting access to %s for %d seconds", target, duration)

	// Get the API client
	apiClient, err := c.getAPIClient()
	if err != nil {
		return fmt.Errorf("failed to get API client: %w", err)
	}

	// Get machine ID by name
	machine, err := apiClient.GetMachineByName(targetMachine)
	if err != nil {
		return fmt.Errorf("machine not found: %w", err)
	}

	// Create access request
	request := map[string]interface{}{
		"machine_id": machine.ID,
		"reason":     reason,
		"duration":   duration,
	}
	if len(remoteUsers) > 0 {
		request["remote_users"] = remoteUsers
	}

	accessReq, err := apiClient.CreateAccessRequest(request)
	if err != nil {
		return fmt.Errorf("failed to create access request: %w", err)
	}

	fmt.Printf("Access request submitted for %s\n", target)
	fmt.Printf("Request ID: %s\n", accessReq.ID)
	fmt.Printf("Reason: %s\n", reason)
	fmt.Printf("Duration: %d seconds\n", duration)
	fmt.Println("\nWaiting for admin approval...")

	// Poll for approval
	return c.pollForApproval(apiClient, accessReq.ID)
}

// pollForApproval polls the API for access request approval
func (c *SSHClient) pollForApproval(apiClient *APIClient, requestID string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("approval timeout - request may still be pending")
		case <-ticker.C:
			request, err := apiClient.GetAccessRequest(requestID)
			if err != nil {
				c.logger.Error("Failed to check request status: %v", err)
				continue
			}

			switch request.Status {
			case "approved":
				fmt.Printf("\n✓ Access approved! You can now connect to the machine.\n")
				if request.ExpiresAt != nil {
					fmt.Printf("Access expires at: %s\n", request.ExpiresAt.Format(time.RFC3339))
				}
				return nil
			case "rejected":
				return fmt.Errorf("access request was rejected")
			case "pending":
				fmt.Print(".")
			}
		}
	}
}

// ListMachines lists available machines
func (c *SSHClient) ListMachines() error {
	apiClient, err := c.getAPIClient()
	if err != nil {
		return fmt.Errorf("failed to get API client: %w", err)
	}

	machines, err := apiClient.ListMachines()
	if err != nil {
		return fmt.Errorf("failed to list machines: %w", err)
	}

	if len(machines) == 0 {
		fmt.Println("No machines available.")
		return nil
	}

	fmt.Println("Available machines:")
	fmt.Println()
	fmt.Printf("%-20s %-30s %-10s %-10s\n", "NAME", "HOSTNAME", "STATUS", "LAST SEEN")
	fmt.Println(strings.Repeat("-", 75))

	for _, machine := range machines {
		status := "offline"
		if machine.IsActive {
			status = "online"
		}

		lastSeen := "never"
		if machine.LastSeenAt != nil {
			lastSeen = formatDuration(time.Since(*machine.LastSeenAt))
		}

		fmt.Printf("%-20s %-30s %-10s %-10s\n",
			machine.Name,
			fmt.Sprintf("%s:%d", machine.Hostname, machine.Port),
			status,
			lastSeen)
	}

	return nil
}

// formatDuration formats a duration in human-readable form
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	} else if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	} else {
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// getAPIClient creates an API client for making REST requests
func (c *SSHClient) getAPIClient() (*APIClient, error) {
	return LoadAPIClient(c.config, "", c.logger)
}

// SCPClient represents an Orion-Belt SCP client
type SCPClient struct {
	config *common.Config
	logger *common.Logger
}

// NewSCPClient creates a new SCP client
func NewSCPClient(config *common.Config, logger *common.Logger) (*SCPClient, error) {
	return &SCPClient{
		config: config,
		logger: logger,
	}, nil
}

// Copy copies a file through the Orion-Belt server
func (c *SCPClient) Copy(username, source, destination string, isUpload bool) error {
	var machine, remotePath, localPath string

	if isUpload {
		localPath = source
		parts := splitMachinePath(destination)
		if len(parts) != 2 {
			return fmt.Errorf("invalid destination format, expected machine:path, got: %s", destination)
		}
		machine = parts[0]
		remotePath = parts[1]
	} else {
		localPath = destination
		parts := splitMachinePath(source)
		if len(parts) != 2 {
			return fmt.Errorf("invalid source format, expected machine:path, got: %s", source)
		}
		machine = parts[0]
		remotePath = parts[1]
	}

	localPath = expandLocalPath(localPath)

	signer, err := LoadSigner(c.config, "", c.logger)
	if err != nil {
		return err
	}

	hostKeyCallback, err := common.NewHostKeyCallback(common.HostKeyConfig{
		KnownHosts:            c.config.Auth.KnownHosts,
		StrictHostKeyChecking: c.config.Auth.StrictHostKeyChecking,
		HostCAPublicKey:       c.config.Auth.HostCAPublicKey,
	}, c.logger)
	if err != nil {
		return fmt.Errorf("host key verification setup: %w", err)
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	// Connect to Orion-Belt server
	serverAddr := fmt.Sprintf("%s:%d", c.config.Server.Host, c.config.Server.Port)
	c.logger.Debug("Connecting to server %s", serverAddr)
	client, err := ssh.Dial("tcp", serverAddr, config)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer client.Close()

	// Create session
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	if isUpload {
		return c.uploadFile(session, localPath, machine, remotePath)
	}
	return c.downloadFile(session, localPath, machine, remotePath)
}

func expandLocalPath(path string) string {
	if path == "." || path == "./" {
		wd, _ := os.Getwd()
		return wd
	}
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return home + path[1:]
	}
	return path
}

// uploadFile uploads a file to the remote machine
func (c *SCPClient) uploadFile(session *ssh.Session, source, machine, remotePath string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Set up stdin for session
	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin: %w", err)
	}

	// Start SCP command on remote
	go func() {
		defer stdin.Close()

		// Send file header
		fmt.Fprintf(stdin, "C0644 %d %s\n", stat.Size(), stat.Name())

		// Send file content
		io.Copy(stdin, file)

		// Send terminator
		fmt.Fprint(stdin, "\x00")
	}()

	// Run SCP command
	scpCmd := fmt.Sprintf("%s scp -t %s", machine, remotePath)
	if err := session.Run(scpCmd); err != nil {
		return fmt.Errorf("scp upload failed: %w", err)
	}

	c.logger.Info("File uploaded successfully")
	return nil
}

// downloadFile downloads a file from the remote machine
func (c *SCPClient) downloadFile(session *ssh.Session, destination, machine, remotePath string) error {
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	scpCmd := fmt.Sprintf("%s scp -f %s", machine, remotePath)
	if err := session.Start(scpCmd); err != nil {
		return err
	}

	fmt.Fprint(stdin, "\x00")
	var header string
	buf := make([]byte, 1)
	for {
		_, err := stdout.Read(buf)
		if err != nil || buf[0] == '\n' {
			break
		}
		header += string(buf)
	}

	if len(header) == 0 {
		return fmt.Errorf("failed to read SCP header (empty response)")
	}
	var perms string
	var size int64
	var filename string
	_, err = fmt.Sscanf(header, "C%s %d %s", &perms, &size, &filename)
	if err != nil {
		return fmt.Errorf("failed to parse SCP header '%s': %w", header, err)
	}

	finalLocalPath := destination
	info, err := os.Stat(destination)
	if err == nil && info.IsDir() {
		finalLocalPath = filepath.Join(destination, filename)
	}

	file, err := os.Create(finalLocalPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	fmt.Fprint(stdin, "\x00")
	_, err = io.Copy(file, io.LimitReader(stdout, size))
	if err != nil {
		return fmt.Errorf("failed during data transfer: %w", err)
	}

	finalBuf := make([]byte, 1)
	_, _ = stdout.Read(finalBuf)

	fmt.Fprint(stdin, "\x00")

	stdin.Close()
	return session.Wait()
}

// splitMachinePath splits user@machine:path or machine:path
func splitMachinePath(spec string) []string {
	// Handle user@machine:path format
	atIndex := strings.Index(spec, "@")
	colonIndex := strings.Index(spec, ":")

	if colonIndex == -1 {
		return []string{spec}
	}

	// If @ comes before :, it's part of the machine spec
	if atIndex != -1 && atIndex < colonIndex {
		// user@machine:path format
		return []string{spec[:colonIndex], spec[colonIndex+1:]}
	}

	// machine:path format
	return []string{spec[:colonIndex], spec[colonIndex+1:]}
}
