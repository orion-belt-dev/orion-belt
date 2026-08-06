package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the account you are signed in as",
		Long:  `Authenticates with your SSH key and prints the identity, role, and auth method the gateway resolved.`,
		Args:  cobra.NoArgs,
		Run:   runWhoami,
	}
}

func runWhoami(cmd *cobra.Command, args []string) {
	user, err := api().GetCurrentUser(ctx())
	if err != nil {
		cliflags.Fatalf("%v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(user)
		return
	}

	cliflags.Print("Username: %s", user.Username)
	cliflags.Print("ID:       %s", user.ID)
	cliflags.Print("Email:    %s", cliflags.OrDash(user.Email))
	cliflags.Print("Role:     %s", roleOf(user.Role, user.IsAdmin))
	cliflags.Print("MFA:      %s", cliflags.YesNo(user.MFAEnabled))
	cliflags.Print("WebAuthn: %s", cliflags.YesNo(user.WebAuthnEnabled))
	cliflags.Print("Password: %s", cliflags.YesNo(user.PasswordSet))
}

func roleOf(role string, isAdmin bool) string {
	if role != "" {
		return role
	}
	if isAdmin {
		return "admin"
	}
	return "user"
}

func newKeysCmd() *cobra.Command {
	keys := &cobra.Command{
		Use:   "keys",
		Short: "Manage your SSH public keys",
		Long: `List, add, and remove the SSH public keys that can sign in as you.

These are the keys the gateway accepts for osh/ocp/oadmin authentication and
for the login challenge; removing your last key leaves password + TOTP as the
only way in.`,
	}

	keys.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List your SSH public keys",
		Args:  cobra.NoArgs,
		Run:   runKeysList,
	})

	var keyFile string
	add := &cobra.Command{
		Use:   "add [name]",
		Short: "Add an SSH public key",
		Long:  `Registers an additional public key under a name you choose, read from --key-file (or stdin with "-").`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			publicKey, err := readKeyMaterial(keyFile)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			key, err := api().AddSSHKey(ctx(), args[0], publicKey)
			if err != nil {
				cliflags.Fatalf("adding key: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(key)
				return
			}
			cliflags.Print("✓ Added key %s (%s)", key.Name, key.KeyType)
		},
	}
	add.Flags().StringVar(&keyFile, "key-file", "", "public key file to read (\"-\" for stdin) (required)")
	_ = add.MarkFlagRequired("key-file")
	keys.AddCommand(add)

	keys.AddCommand(&cobra.Command{
		Use:   "remove [key-id]",
		Short: "Remove an SSH public key",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().DeleteSSHKey(ctx(), args[0]); err != nil {
				cliflags.Fatalf("removing key: %v", err)
			}
			cliflags.Print("✓ Removed key %s", args[0])
		},
	})

	return keys
}

func runKeysList(cmd *cobra.Command, args []string) {
	keys, err := api().ListSSHKeys(ctx())
	if err != nil {
		cliflags.Fatalf("listing keys: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(keys)
		return
	}
	if len(keys) == 0 {
		cliflags.Print("No SSH keys registered.")
		return
	}

	table := cliflags.NewTable("ID", "NAME", "TYPE", "ADDED")
	for _, key := range keys {
		table.Row(key.ID, key.Name, cliflags.OrDash(key.KeyType), cliflags.FormatTime(key.CreatedAt))
	}
	table.Flush()
}

func newWebAuthnCmd() *cobra.Command {
	webauthn := &cobra.Command{
		Use:   "webauthn",
		Short: "Manage your hardware security keys",
		Long: `List and remove the WebAuthn/FIDO2 credentials registered to your account.

Registering one needs a browser ceremony, so enroll in the console under
Security; the CLI covers reviewing and removing what is enrolled.`,
	}

	webauthn.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List registered WebAuthn credentials",
		Args:  cobra.NoArgs,
		Run:   runWebAuthnList,
	})

	webauthn.AddCommand(&cobra.Command{
		Use:   "remove [credential-id]",
		Short: "Remove a WebAuthn credential",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().WebAuthnDeleteCredential(ctx(), args[0]); err != nil {
				cliflags.Fatalf("removing credential: %v", webAuthnError(err))
			}
			cliflags.Print("✓ Removed credential %s", args[0])
		},
	})

	return webauthn
}

// webAuthnError explains the 404 a server without WebAuthn configured returns.
// The routes are only registered when the feature is set up, so the bare "not
// found" would otherwise read as a broken client.
func webAuthnError(err error) error {
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("WebAuthn is not enabled on this server")
	}
	return err
}

func runWebAuthnList(cmd *cobra.Command, args []string) {
	credentials, err := api().WebAuthnCredentials(ctx())
	if err != nil {
		cliflags.Fatalf("listing credentials: %v", webAuthnError(err))
	}

	if flags.JSON {
		cliflags.MustPrintJSON(credentials)
		return
	}
	if len(credentials) == 0 {
		cliflags.Print("No WebAuthn credentials registered.")
		return
	}

	table := cliflags.NewTable("ID", "NAME", "REGISTERED")
	for _, credential := range credentials {
		table.Row(credential.ID, cliflags.OrDash(credential.Name), cliflags.FormatTime(credential.CreatedAt))
	}
	table.Flush()
}

// readKeyMaterial reads a public key from a file, or from stdin when the path
// is "-".
func readKeyMaterial(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read public key from stdin: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if strings.Contains(key, "PRIVATE KEY") {
		return "", fmt.Errorf("%s looks like a private key; pass the matching .pub file", path)
	}
	return key, nil
}
