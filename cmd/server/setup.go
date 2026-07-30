package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive first-run wizard (admin, agents, users)",
	Long: `Guided setup after installing the orion-belt package.

Walks through:
  1) Verify config + database
  2) Create the first admin (if missing)
  3) Print agent install / register steps
  4) Print how to add users and grant access

Non-interactive: set ORION_SETUP_ADMIN_NAME, ORION_SETUP_ADMIN_EMAIL,
ORION_SETUP_ADMIN_KEY (or ORION_SETUP_ADMIN_KEY_FILE).`,
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

	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Config: %v\n  Edit /etc/orion-belt/server.yaml (database + jwt_secret) then re-run setup.", err)
	}
	fmt.Println("✓ Config loaded")

	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Database: %v\n  Ensure Postgres is up and connection_string is correct.", err)
	}
	defer store.Close()
	fmt.Println("✓ Database reachable")

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
			key = prompt(in, "Admin SSH public key (paste one line)", "")
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
		fmt.Println("  Sign in at the UI with that username + matching public key.")
	}

	fmt.Println()
	fmt.Println("── Next: add agents ──")
	fmt.Println(`  On each target host:
    1. Install the agent package (deb/rpm/apk) from your Orion package repo
       or: copy orion-belt-agent binary to /usr/bin/
    2. Edit /etc/orion-belt/agent.yaml — server host/port (gateway :2222)
    3. systemctl enable --now orion-belt-agent
    4. Register the agent (UI Agents page, or public register API)

  Lab helper: make lab-qemu-connect-agents`)

	fmt.Println()
	fmt.Println("── Next: users & access ──")
	fmt.Println(`  • UI → Users: register operators/auditors/users
  • UI → grant machine permissions (remote_users e.g. root)
  • CLI:
      orion-belt-server user create --name alice --email a@x --key "$(cat alice.pub)"
      orion-belt-server permission grant --user alice --machine agent-alpine --type both --remote-users root

  OpenSSH through the gateway:
      ssh -i alice_key -p 2222 alice+machine-name@gateway-host`)

	fmt.Println()
	fmt.Println("── Services ──")
	fmt.Println("  systemctl enable --now orion-belt-server")
	fmt.Println("  UI: http://<host>:8080/ui")
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
