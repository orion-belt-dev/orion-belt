package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/client"
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

var (
	flags          cliflags.Common
	requestAccess  bool
	accessDuration int
	accessReason   string
	listMachines   bool
	remoteUser     string
	printCodeOnly  bool
	loginPassword  bool
)

var rootCmd = &cobra.Command{
	Use:   "osh [user@]machine",
	Short: "Orion-Belt SSH Client",
	Long: `osh is the Orion-Belt SSH client for connecting to machines through the Orion-Belt server.

Beyond connecting, it manages your own account from the terminal: sign-in
credentials (SSH keys, API keys, password, MFA, WebAuthn), your access
requests, and your notification preferences. Administrative operations live in
oadmin.`,
	Version: version.String(),
	Args:    cobra.MaximumNArgs(1),
	Run:     runSSH,
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to the web console",
	Long: `Authenticates, then opens the browser to finish signing in.

Default auth is your SSH private key. Use --password to authenticate with
password + TOTP instead (useful when the key is unavailable). Use --code to
print the one-time code instead of opening a browser.`,
	Run: runLogin,
}

func init() {
	flags.BindPersistent(rootCmd)
	flags.BindSSHTrust(rootCmd)
	rootCmd.Flags().BoolVarP(&requestAccess, "request-access", "r", false, "request temporary access")
	rootCmd.Flags().IntVarP(&accessDuration, "duration", "d", 3600, "access duration in seconds")
	rootCmd.Flags().StringVar(&accessReason, "reason", "", "reason for access request")
	rootCmd.Flags().BoolVarP(&listMachines, "list", "l", false, "list available machines")
	rootCmd.Flags().StringVar(&remoteUser, "remote-user", "", "UNIX user on the target host (optional; can also use user@machine)")
	loginCmd.Flags().BoolVar(&printCodeOnly, "code", false, "print the sign-in code (and URL) instead of opening a browser")
	loginCmd.Flags().BoolVar(&loginPassword, "password", false, "authenticate with password + TOTP instead of SSH key")

	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(newWhoamiCmd())
	rootCmd.AddCommand(newRequestsCmd())
	rootCmd.AddCommand(newKeysCmd())
	rootCmd.AddCommand(newAPIKeysCmd())
	rootCmd.AddCommand(newMFACmd())
	rootCmd.AddCommand(newPasswordCmd())
	rootCmd.AddCommand(newWebAuthnCmd())
	rootCmd.AddCommand(newNotificationsCmd())
}

// api returns an authenticated SDK client, exiting with a readable message
// when the config, key, or login fails.
func api() *sdk.Client {
	client, err := flags.APIClient()
	if err != nil {
		cliflags.Fatalf("%v", err)
	}
	return client
}

func ctx() context.Context { return context.Background() }

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runLogin(cmd *cobra.Command, args []string) {
	logger := flags.Logger()
	config, err := flags.LoadConfig()
	if err != nil {
		logger.Fatal("%v", err)
	}

	user, err := flags.Username(config)
	if err != nil {
		logger.Fatal("%v", err)
	}

	var apiClient *client.APIClient
	if loginPassword {
		apiClient, err = loginWithPassword(config, user, logger)
	} else {
		apiClient, err = client.LoadAPIClient(config, user, logger)
	}
	if err != nil {
		logger.Fatal("Login failed: %v", err)
	}

	code, err := apiClient.RequestBrowserBootstrap()
	if err != nil {
		logger.Fatal("Failed to obtain a browser sign-in code: %v", err)
	}

	consoleURL := consoleOrigin(config)
	url := fmt.Sprintf("%s/ui/bootstrap?code=%s", consoleURL, code.Code)

	if printCodeOnly {
		fmt.Printf("Code: %s\n", code.Code)
		fmt.Printf("URL:  %s\n", url)
		fmt.Printf("Expires %s\n", code.ExpiresAt.Format("15:04:05 MST"))
		return
	}

	fmt.Printf("Opening browser…\n  %s\n", url)
	if err := cliflags.OpenBrowser(url); err != nil {
		fmt.Fprintf(os.Stderr, "Could not open a browser (%v).\nPaste this URL manually:\n  %s\n\nOr re-run with --code\n", err, url)
		os.Exit(1)
	}
	fmt.Printf("If nothing opens, run: osh login --code\n(expires %s)\n", code.ExpiresAt.Format("15:04:05 MST"))
}

func loginWithPassword(config *common.Config, user string, logger *common.Logger) (*client.APIClient, error) {
	apiEndpoint := config.Server.APIEndpoint
	if apiEndpoint == "" {
		apiEndpoint = fmt.Sprintf("http://%s:8080", config.Server.Host)
	}
	password, err := cliflags.PromptSecret("Password: ")
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}
	totp, err := cliflags.PromptSecret("TOTP code: ")
	if err != nil {
		return nil, fmt.Errorf("read totp: %w", err)
	}
	if password == "" || totp == "" {
		return nil, fmt.Errorf("password and TOTP code are required")
	}
	c := client.NewUnauthenticatedAPIClient(apiEndpoint, logger)
	if _, err := c.LoginWithPassword(user, password, totp); err != nil {
		return nil, err
	}
	return c, nil
}

func consoleOrigin(config *common.Config) string {
	endpoint := config.Server.APIEndpoint
	if endpoint == "" {
		return fmt.Sprintf("http://%s:8080", config.Server.Host)
	}
	return strings.TrimSuffix(strings.TrimSuffix(endpoint, "/"), "/api")
}

func runSSH(cmd *cobra.Command, args []string) {
	logger := flags.Logger()
	config, err := flags.LoadConfig()
	if err != nil {
		logger.Fatal("%v", err)
	}

	sshClient, err := client.NewSSHClient(config, logger)
	if err != nil {
		logger.Fatal("Failed to create SSH client: %v", err)
	}

	if listMachines {
		if err := sshClient.ListMachines(); err != nil {
			logger.Fatal("Failed to list machines: %v", err)
		}
		return
	}

	if len(args) == 0 {
		fmt.Println("Usage: osh [user@]machine")
		fmt.Println("       osh --list")
		fmt.Println("       osh login")
		fmt.Println("       osh --request-access [user@]machine --reason \"reason\" --duration 3600")
		os.Exit(1)
	}

	target := args[0]
	if requestAccess {
		if accessReason == "" {
			logger.Fatal("Access reason is required (use --reason)")
		}
		if err := sshClient.RequestAccess(target, accessReason, accessDuration); err != nil {
			logger.Fatal("Failed to request access: %v", err)
		}
		return
	}

	user, err := flags.Username(config)
	if err != nil {
		logger.Fatal("%v", err)
	}

	if remoteUser != "" && !strings.Contains(target, "@") {
		target = remoteUser + "@" + target
	}

	if err := sshClient.Connect(target, user); err != nil {
		logger.Fatal("Connection failed: %v", err)
	}
}
