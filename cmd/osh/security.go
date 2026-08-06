package main

import (
	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

func newMFACmd() *cobra.Command {
	mfa := &cobra.Command{
		Use:   "mfa",
		Short: "Manage TOTP multi-factor authentication",
		Long:  `Check, enroll, and disable the TOTP second factor on your account.`,
	}

	mfa.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show whether MFA is enabled and whether it is required",
		Args:  cobra.NoArgs,
		Run:   runMFAStatus,
	})

	mfa.AddCommand(&cobra.Command{
		Use:   "enroll",
		Short: "Enroll TOTP",
		Long: `Starts TOTP enrollment and confirms it in one step.

The secret and its otpauth:// URL are printed for you to add to an
authenticator app, along with single-use backup codes — store those somewhere
safe, they are shown once. You then type the first generated code to confirm;
enrollment is not active until that succeeds.`,
		Args: cobra.NoArgs,
		Run:  runMFAEnroll,
	})

	mfa.AddCommand(&cobra.Command{
		Use:   "disable",
		Short: "Disable TOTP",
		Long:  `Turns off the TOTP second factor. Requires a current code, and fails if your organization sets auth.mfa_required.`,
		Args:  cobra.NoArgs,
		Run:   runMFADisable,
	})

	return mfa
}

func runMFAStatus(cmd *cobra.Command, args []string) {
	status, err := api().MFAStatus(ctx())
	if err != nil {
		cliflags.Fatalf("getting MFA status: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(status)
		return
	}
	cliflags.Print("MFA enabled:  %s", cliflags.YesNo(status.MFAEnabled))
	cliflags.Print("MFA required: %s", cliflags.YesNo(status.MFARequired))
}

func runMFAEnroll(cmd *cobra.Command, args []string) {
	client := api()

	enrollment, err := client.MFAEnroll(ctx())
	if err != nil {
		cliflags.Fatalf("starting MFA enrollment: %v", err)
	}

	cliflags.Print("Secret:      %s", enrollment.Secret)
	cliflags.Print("Setup URL:   %s", enrollment.OTPAuthURL)
	if len(enrollment.BackupCodes) > 0 {
		cliflags.Print("\nBackup codes (shown once — store them now):")
		for _, code := range enrollment.BackupCodes {
			cliflags.Print("  %s", code)
		}
	}

	code, err := cliflags.PromptSecret("\nEnter the current code from your authenticator to confirm: ")
	if err != nil {
		cliflags.Fatalf("reading code: %v", err)
	}
	if code == "" {
		cliflags.Fatalf("a code is required to finish enrollment; enrollment was not activated")
	}
	if err := client.MFAConfirm(ctx(), code); err != nil {
		cliflags.Fatalf("confirming enrollment: %v", err)
	}
	cliflags.Print("✓ MFA enabled")
}

func runMFADisable(cmd *cobra.Command, args []string) {
	code, err := cliflags.PromptSecret("Current TOTP code: ")
	if err != nil {
		cliflags.Fatalf("reading code: %v", err)
	}
	if code == "" {
		cliflags.Fatalf("a current TOTP code is required")
	}
	if err := api().MFADisable(ctx(), code); err != nil {
		cliflags.Fatalf("disabling MFA: %v", err)
	}
	cliflags.Print("✓ MFA disabled")
}

func newPasswordCmd() *cobra.Command {
	password := &cobra.Command{
		Use:   "password",
		Short: "Manage your console password",
		Long: `Set or clear the password used for console sign-in.

Password login always pairs with TOTP, so both commands ask for a current code;
if MFA is not enabled yet, "osh mfa enroll" comes first.`,
	}

	password.AddCommand(&cobra.Command{
		Use:   "set",
		Short: "Set or change your password",
		Args:  cobra.NoArgs,
		Run:   runPasswordSet,
	})

	password.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Remove your password, leaving key and WebAuthn login",
		Args:  cobra.NoArgs,
		Run:   runPasswordClear,
	})

	return password
}

func runPasswordSet(cmd *cobra.Command, args []string) {
	newPassword, err := cliflags.PromptSecret("New password: ")
	if err != nil {
		cliflags.Fatalf("reading password: %v", err)
	}
	confirm, err := cliflags.PromptSecret("Confirm password: ")
	if err != nil {
		cliflags.Fatalf("reading password: %v", err)
	}
	if newPassword == "" {
		cliflags.Fatalf("password cannot be empty")
	}
	if newPassword != confirm {
		cliflags.Fatalf("passwords do not match")
	}
	code, err := cliflags.PromptSecret("TOTP code: ")
	if err != nil {
		cliflags.Fatalf("reading code: %v", err)
	}

	if err := api().SetPassword(ctx(), newPassword, code); err != nil {
		cliflags.Fatalf("setting password: %v", err)
	}
	cliflags.Print("✓ Password set")
}

func runPasswordClear(cmd *cobra.Command, args []string) {
	code, err := cliflags.PromptSecret("TOTP code: ")
	if err != nil {
		cliflags.Fatalf("reading code: %v", err)
	}
	if err := api().ClearPassword(ctx(), code); err != nil {
		cliflags.Fatalf("clearing password: %v", err)
	}
	cliflags.Print("✓ Password removed")
}

func newAPIKeysCmd() *cobra.Command {
	apiKeys := &cobra.Command{
		Use:     "api-keys",
		Aliases: []string{"apikeys"},
		Short:   "Manage your API keys",
		Long: `List, create, revoke, and delete the API keys that authenticate automation as you.

Revoking stops a key working but keeps the record, which is what an audit
trail needs; delete removes the record entirely.`,
	}

	apiKeys.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List your API keys",
		Args:  cobra.NoArgs,
		Run:   runAPIKeysList,
	})

	var expiresInDays int
	create := &cobra.Command{
		Use:   "create [name]",
		Short: "Create an API key",
		Long: `Creates an API key and prints it once.

The raw key is never retrievable again, so capture it as you create it. Without
--expires-in-days the key does not expire; give it a lifetime unless something
else will rotate it.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			req := sdk.CreateAPIKeyRequest{Name: args[0]}
			if cmd.Flags().Changed("expires-in-days") {
				req.ExpiresIn = &expiresInDays
			}
			key, err := api().CreateAPIKey(ctx(), req)
			if err != nil {
				cliflags.Fatalf("creating API key: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(key)
				return
			}
			cliflags.Print("✓ Created API key %s", key.Name)
			cliflags.Print("  Key:     %s", key.APIKey)
			cliflags.Print("  Expires: %s", cliflags.FormatTimePtr(key.ExpiresAt))
			cliflags.Print("\nThis is the only time the key is shown.")
		},
	}
	create.Flags().IntVar(&expiresInDays, "expires-in-days", 0, "lifetime in days (omit for a key that never expires)")
	apiKeys.AddCommand(create)

	apiKeys.AddCommand(&cobra.Command{
		Use:   "revoke [key-id]",
		Short: "Revoke an API key without deleting its record",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().RevokeAPIKey(ctx(), args[0]); err != nil {
				cliflags.Fatalf("revoking API key: %v", err)
			}
			cliflags.Print("✓ API key %s revoked", args[0])
		},
	})

	apiKeys.AddCommand(&cobra.Command{
		Use:   "delete [key-id]",
		Short: "Delete an API key record",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().DeleteAPIKey(ctx(), args[0]); err != nil {
				cliflags.Fatalf("deleting API key: %v", err)
			}
			cliflags.Print("✓ API key %s deleted", args[0])
		},
	})

	return apiKeys
}

func runAPIKeysList(cmd *cobra.Command, args []string) {
	keys, err := api().ListAPIKeys(ctx())
	if err != nil {
		cliflags.Fatalf("listing API keys: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(keys)
		return
	}
	if len(keys) == 0 {
		cliflags.Print("No API keys.")
		return
	}

	table := cliflags.NewTable("ID", "NAME", "PREFIX", "STATUS", "LAST USED", "EXPIRES")
	for _, key := range keys {
		status := "active"
		if key.RevokedAt != nil {
			status = "revoked"
		}
		table.Row(key.ID, key.Name, cliflags.OrDash(key.KeyPrefix), status,
			cliflags.FormatTimePtr(key.LastUsedAt), cliflags.FormatTimePtr(key.ExpiresAt))
	}
	table.Flush()
}
