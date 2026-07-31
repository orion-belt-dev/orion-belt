package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive first-run wizard (admin, agents, users)",
	Long: `Guided setup after installing the orion-belt package.

Walks through:
  1) Verify config + database
  2) Set the public URL (what browsers and agents dial)
  3) Create the first admin (if missing)
  4) Print agent install / register steps
  5) Print how to add users and grant access

Non-interactive: set ORION_SETUP_ADMIN_NAME, ORION_SETUP_ADMIN_EMAIL,
ORION_SETUP_ADMIN_KEY (or ORION_SETUP_ADMIN_KEY_FILE), and optionally
ORION_SETUP_PUBLIC_URL / ORION_SETUP_PUBLIC_SSH_HOST / ORION_SETUP_PUBLIC_SSH_PORT.`,
	Run: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

func runSetup(cmd *cobra.Command, args []string) {
	logger := getLogger()
	in := bufio.NewReader(os.Stdin)

	fmt.Println("╔══════════════════════════════════════════════════════╗")
	fmt.Println("║  Orion Belt — setup wizard                           ║")
	fmt.Println("╚══════════════════════════════════════════════════════╝")
	fmt.Println()

	configPath, err := findConfigPath()
	if err != nil {
		// loadConfig still searches; path is only needed if we persist public_url.
		configPath = ""
	}
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Config: %v\n  Edit /etc/orion-belt/server.yaml (database + jwt_secret) then re-run setup.", err)
	}
	fmt.Println("✓ Config loaded")
	if configPath != "" {
		fmt.Printf("  %s\n", configPath)
	}

	store, err := openStore(cmd.Context())
	if err != nil {
		logger.Fatal("Database: %v\n  Ensure Postgres is up and connection_string is correct.", err)
	}
	defer store.Close()
	fmt.Println("✓ Database reachable (schema up to date)")

	fmt.Println()
	fmt.Println("── Public address ──")
	fmt.Println("  This is the URL browsers open and the host agents dial — not 0.0.0.0.")
	publicURL := strings.TrimSpace(os.Getenv("ORION_SETUP_PUBLIC_URL"))
	if publicURL == "" {
		def := config.Server.AdvertisedURL()
		if def == "" {
			def = fmt.Sprintf("http://localhost:%d", config.Server.EffectiveAPIPort())
		}
		publicURL = envOrPrompt(in, "ORION_SETUP_PUBLIC_URL", "Public URL (UI/API origin)", def)
	}
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicURL != "" {
		if _, err := url.ParseRequestURI(publicURL); err != nil {
			logger.Fatal("Invalid public URL %q: %v", publicURL, err)
		}
	}
	sshHost := strings.TrimSpace(os.Getenv("ORION_SETUP_PUBLIC_SSH_HOST"))
	if sshHost == "" {
		defHost := config.Server.PublicSSHHost
		if defHost == "" && publicURL != "" {
			if u, err := url.Parse(publicURL); err == nil {
				defHost = u.Hostname()
			}
		}
		if defHost == "" {
			defHost = config.Server.AdvertisedSSHHost()
		}
		sshHost = envOrPrompt(in, "ORION_SETUP_PUBLIC_SSH_HOST", "Public SSH host (agents dial this)", defHost)
	}
	sshPortStr := strings.TrimSpace(os.Getenv("ORION_SETUP_PUBLIC_SSH_PORT"))
	sshPort := config.Server.AdvertisedSSHPort()
	if sshPortStr != "" {
		if _, err := fmt.Sscanf(sshPortStr, "%d", &sshPort); err != nil || sshPort <= 0 {
			logger.Fatal("Invalid ORION_SETUP_PUBLIC_SSH_PORT %q", sshPortStr)
		}
	} else if isInteractive() {
		portDef := fmt.Sprintf("%d", sshPort)
		got := prompt(in, "Public SSH port", portDef)
		if got != "" {
			if _, err := fmt.Sscanf(got, "%d", &sshPort); err != nil || sshPort <= 0 {
				logger.Fatal("Invalid SSH port %q", got)
			}
		}
	}

	changed := config.Server.PublicURL != publicURL ||
		config.Server.PublicSSHHost != sshHost ||
		config.Server.PublicSSHPort != sshPort
	config.Server.PublicURL = publicURL
	config.Server.PublicSSHHost = sshHost
	config.Server.PublicSSHPort = sshPort
	config.Server.WebAuthnDefaultsFromPublicURL(&config.Auth.WebAuthn)

	if changed && configPath != "" {
		// Full rewrite drops comments — only do it when the installer/env is
		// driving setup, or the operator confirms.
		write := strings.TrimSpace(os.Getenv("ORION_SETUP_PUBLIC_URL")) != "" ||
			os.Getenv("ORION_SETUP_WRITE_CONFIG") == "1"
		if !write && isInteractive() {
			fmt.Printf("  Write public address to %s? (rewrites the file) [y/N] ", configPath)
			reply, _ := in.ReadString('\n')
			write = strings.HasPrefix(strings.ToLower(strings.TrimSpace(reply)), "y")
		}
		if write {
			if err := common.SaveConfig(configPath, config); err != nil {
				logger.Warn("Could not write public address to %s: %v", configPath, err)
				fmt.Println("  Set server.public_url / public_ssh_host / public_ssh_port manually, then restart.")
			} else {
				fmt.Printf("✓ Public address saved to %s\n", configPath)
				fmt.Println("  Restart the server for WebAuthn / gateway-info to pick this up.")
			}
		} else {
			fmt.Println("  Not writing config. Set these, then restart:")
			fmt.Printf("    server.public_url: %q\n", publicURL)
			fmt.Printf("    server.public_ssh_host: %q\n", sshHost)
			fmt.Printf("    server.public_ssh_port: %d\n", sshPort)
		}
	} else if publicURL != "" {
		fmt.Printf("✓ Public URL: %s\n", publicURL)
		fmt.Printf("  Agents dial: %s:%d\n", sshHost, sshPort)
	}

	users, err := store.ListUsers(cmd.Context(), 50, 0)
	if err != nil {
		logger.Fatal("List users: %v", err)
	}
	hasAdmin := false
	for _, u := range users {
		if u.IsAdmin || u.Role == "admin" {
			hasAdmin = true
			break
		}
	}

	if hasAdmin {
		fmt.Println("✓ Admin user already exists")
	} else {
		fmt.Println()
		fmt.Println("── Create first admin ──")
		name := envOrPrompt(in, "ORION_SETUP_ADMIN_NAME", "Admin username", "admin")
		email := envOrPrompt(in, "ORION_SETUP_ADMIN_EMAIL", "Admin email", "admin@localhost")
		key := strings.TrimSpace(os.Getenv("ORION_SETUP_ADMIN_KEY"))
		if key == "" {
			if kf := os.Getenv("ORION_SETUP_ADMIN_KEY_FILE"); kf != "" {
				b, err := os.ReadFile(kf)
				if err != nil {
					logger.Fatal("read key file: %v", err)
				}
				key = strings.TrimSpace(string(b))
			}
		}
		if key == "" {
			key = prompt(in, "Admin SSH public key (paste one line, or path to .pub / YubiKey sk-*.pub)", "")
			if key != "" && !strings.Contains(key, " ") && (strings.HasSuffix(key, ".pub") || strings.HasPrefix(key, "/") || strings.HasPrefix(key, "~")) {
				path := expandHome(key)
				b, err := os.ReadFile(path)
				if err != nil {
					logger.Fatal("read key file %s: %v", path, err)
				}
				key = strings.TrimSpace(string(b))
			}
		}
		if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
			logger.Fatal("Invalid public key: %v", err)
		}
		user := common.NewUser(name, email, key, true)
		user.Role = "admin"
		if err := store.CreateUser(cmd.Context(), user); err != nil {
			logger.Fatal("Create admin: %v", err)
		}
		fmt.Printf("✓ Admin %q created (id %s)\n", name, user.ID)
		fmt.Println("  Sign in at the UI with that username + matching private key (via osh login).")
	}

	ui := config.Server.UIURL()
	sshAdvertise := fmt.Sprintf("%s:%d", config.Server.AdvertisedSSHHost(), config.Server.AdvertisedSSHPort())

	fmt.Println()
	fmt.Println("── Next: add agents ──")
	fmt.Printf(`  On each target host:
    1. Install the agent package (deb/rpm/apk) from your Orion package repo
       or: copy orion-belt-agent binary to /usr/bin/
    2. Edit /etc/orion-belt/agent.yaml — server host/port = %s
    3. Enable the agent service (systemd or OpenRC)
    4. Or use the console **Add agent** page (defaults to this public address)

  Lab helper: make lab-qemu-connect-agents
`, sshAdvertise)

	fmt.Println()
	fmt.Println("── Next: users & access ──")
	fmt.Printf(`  • UI → Users: register operators/auditors/users
  • UI → grant machine permissions (remote_users e.g. root)
  • CLI:
      orion-belt-server user create --name alice --email a@x --key "$(cat alice.pub)"
      orion-belt-server permission grant --user alice --machine agent-alpine --type both --remote-users root

  OpenSSH through the gateway:
      ssh -i alice_key -p %d alice+machine-name@%s
`, config.Server.AdvertisedSSHPort(), config.Server.AdvertisedSSHHost())

	fmt.Println()
	fmt.Println("── Services ──")
	fmt.Println("  systemctl enable --now orion-belt-server   # or: rc-service orion-belt-server start")
	fmt.Printf("  UI: %s\n", ui)
	fmt.Println()
	fmt.Println("Docs: docs/SETUP.md  ·  Package repos: docs/PACKAGING.md")
	fmt.Println("Setup wizard finished.")
}

func prompt(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func envOrPrompt(in *bufio.Reader, env, label, def string) string {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
	return prompt(in, label, def)
}

func isInteractive() bool {
	if strings.TrimSpace(os.Getenv("ORION_SETUP_ADMIN_NAME")) != "" ||
		strings.TrimSpace(os.Getenv("ORION_SETUP_PUBLIC_URL")) != "" {
		// Caller is driving setup via env; don't block on optional prompts.
		if _, err := os.Stdin.Stat(); err == nil {
			fi, _ := os.Stdin.Stat()
			if fi != nil && (fi.Mode()&os.ModeCharDevice) == 0 {
				return false
			}
		}
	}
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[2:])
	}
	return p
}
