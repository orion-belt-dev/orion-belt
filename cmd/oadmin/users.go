package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newUsersCmd() *cobra.Command {
	users := &cobra.Command{
		Use:   "users",
		Short: "Manage user accounts",
		Long:  `List, inspect, create, update, and delete gateway users.`,
	}

	users.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List users",
		Args:  cobra.NoArgs,
		Run:   runUsersList,
	})

	users.AddCommand(&cobra.Command{
		Use:   "get [username|user-id]",
		Short: "Show one user",
		Args:  cobra.ExactArgs(1),
		Run:   runUsersGet,
	})

	var (
		email     string
		keyFile   string
		publicKey string
		role      string
		admin     bool
	)
	create := &cobra.Command{
		Use:   "create [username]",
		Short: "Create a user",
		Long: `Creates a user account.

The public key may be given inline with --public-key or read from a file with
--key-file; a user without a key can still sign in with password + TOTP once
they set one in the console.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			key, err := readPublicKey(publicKey, keyFile)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			user, err := api().CreateUser(ctx(), sdk.CreateUserRequest{
				Username:  args[0],
				Email:     email,
				PublicKey: key,
				Role:      role,
				IsAdmin:   admin,
			})
			if err != nil {
				cliflags.Fatalf("creating user: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(user)
				return
			}
			cliflags.Print("✓ User %s created (id %s)", user.Username, user.ID)
		},
	}
	create.Flags().StringVar(&email, "email", "", "email address")
	create.Flags().StringVar(&publicKey, "public-key", "", "SSH public key in authorized_keys format")
	create.Flags().StringVar(&keyFile, "key-file", "", "read the SSH public key from this file")
	create.Flags().StringVar(&role, "role", "", "role: admin | operator | auditor | user")
	create.Flags().BoolVar(&admin, "admin", false, "grant admin rights (equivalent to --role admin)")
	users.AddCommand(create)

	var (
		newEmail  string
		newKey    string
		newFile   string
		newRole   string
		setAdmin  bool
		unsetAdmn bool
	)
	update := &cobra.Command{
		Use:   "update [username|user-id]",
		Short: "Update a user",
		Long:  `Changes a user's email, SSH public key, role, or admin flag. Unset flags are left untouched.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			userID, err := resolveUserID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}

			var req sdk.UpdateUserRequest
			if cmd.Flags().Changed("email") {
				req.Email = &newEmail
			}
			if cmd.Flags().Changed("role") {
				req.Role = &newRole
			}
			if cmd.Flags().Changed("public-key") || cmd.Flags().Changed("key-file") {
				key, kerr := readPublicKey(newKey, newFile)
				if kerr != nil {
					cliflags.Fatalf("%v", kerr)
				}
				req.PublicKey = &key
			}
			switch {
			case setAdmin && unsetAdmn:
				cliflags.Fatalf("--admin and --no-admin are mutually exclusive")
			case setAdmin:
				yes := true
				req.IsAdmin = &yes
			case unsetAdmn:
				no := false
				req.IsAdmin = &no
			}
			if req.Email == nil && req.Role == nil && req.PublicKey == nil && req.IsAdmin == nil {
				cliflags.Fatalf("nothing to update: pass --email, --public-key, --key-file, --role, --admin, or --no-admin")
			}

			user, err := client.UpdateUser(ctx(), userID, req)
			if err != nil {
				cliflags.Fatalf("updating user: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(user)
				return
			}
			cliflags.Print("✓ User %s updated", user.Username)
		},
	}
	update.Flags().StringVar(&newEmail, "email", "", "new email address")
	update.Flags().StringVar(&newKey, "public-key", "", "replacement SSH public key")
	update.Flags().StringVar(&newFile, "key-file", "", "read the replacement SSH public key from this file")
	update.Flags().StringVar(&newRole, "role", "", "role: admin | operator | auditor | user")
	update.Flags().BoolVar(&setAdmin, "admin", false, "grant admin rights")
	update.Flags().BoolVar(&unsetAdmn, "no-admin", false, "revoke admin rights")
	users.AddCommand(update)

	users.AddCommand(&cobra.Command{
		Use:   "delete [username|user-id]",
		Short: "Delete a user",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			userID, err := resolveUserID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if err := client.DeleteUser(ctx(), userID); err != nil {
				cliflags.Fatalf("deleting user: %v", err)
			}
			cliflags.Print("✓ User %s deleted", args[0])
		},
	})

	return users
}

func runUsersList(cmd *cobra.Command, args []string) {
	users, err := api().ListUsers(ctx())
	if err != nil {
		cliflags.Fatalf("listing users: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(users)
		return
	}
	if len(users) == 0 {
		cliflags.Print("No users.")
		return
	}

	table := cliflags.NewTable("ID", "USERNAME", "EMAIL", "ROLE", "MFA", "CREATED")
	for _, user := range users {
		table.Row(cliflags.Short(user.ID), user.Username, cliflags.OrDash(user.Email),
			user.EffectiveRole(), cliflags.YesNo(user.MFAEnabled), cliflags.FormatTime(user.CreatedAt))
	}
	table.Flush()
}

func runUsersGet(cmd *cobra.Command, args []string) {
	client := api()
	userID, err := resolveUserID(client, args[0])
	if err != nil {
		cliflags.Fatalf("%v", err)
	}

	user, err := client.GetUser(ctx(), userID)
	if err != nil {
		cliflags.Fatalf("getting user: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(user)
		return
	}

	cliflags.Print("ID:        %s", user.ID)
	cliflags.Print("Username:  %s", user.Username)
	cliflags.Print("Email:     %s", cliflags.OrDash(user.Email))
	cliflags.Print("Role:      %s", user.EffectiveRole())
	cliflags.Print("MFA:       %s", cliflags.YesNo(user.MFAEnabled))
	cliflags.Print("WebAuthn:  %s", cliflags.YesNo(user.WebAuthnEnabled))
	cliflags.Print("Created:   %s", cliflags.FormatTime(user.CreatedAt))
	cliflags.Print("Updated:   %s", cliflags.FormatTime(user.UpdatedAt))
	if user.PublicKey != "" {
		cliflags.Print("Key:       %s", strings.TrimSpace(user.PublicKey))
	}
}

// resolveUserID accepts either a user ID or a username, so operators do not
// have to look up UUIDs to run a command.
func resolveUserID(client *sdk.Client, ref string) (string, error) {
	if looksLikeID(ref) {
		return ref, nil
	}
	users, err := client.ListUsers(ctx())
	if err != nil {
		return "", fmt.Errorf("looking up user %q: %w", ref, err)
	}
	for _, user := range users {
		if user.Username == ref {
			return user.ID, nil
		}
	}
	return "", fmt.Errorf("no user named %q", ref)
}

// looksLikeID reports whether ref is a UUID as issued by the server. Usernames
// are free-form, so anything else is treated as a name to look up.
func looksLikeID(ref string) bool {
	if len(ref) != 36 {
		return false
	}
	for i, c := range ref {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// readPublicKey returns the inline key, or the contents of keyFile, rejecting
// the combination of both so a typo cannot silently pick the wrong key.
func readPublicKey(inline, keyFile string) (string, error) {
	if inline != "" && keyFile != "" {
		return "", fmt.Errorf("--public-key and --key-file are mutually exclusive")
	}
	if keyFile == "" {
		return strings.TrimSpace(inline), nil
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", keyFile, err)
	}
	return strings.TrimSpace(string(data)), nil
}
