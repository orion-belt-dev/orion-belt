package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
	"github.com/zrougamed/orion-belt/pkg/version"
)

var flags cliflags.Common

var rootCmd = &cobra.Command{
	Use:   "oadmin",
	Short: "Orion-Belt Admin CLI",
	Long: `oadmin is the Orion-Belt admin tool for operating the gateway from a terminal.

It covers the same ground as the admin areas of the web console: access
requests, users, machines, permissions, sessions and recordings, audit logs,
plugins, connected agents, notification policy, and the SSH CA.`,
	Version: version.String(),
}

func init() {
	flags.BindPersistent(rootCmd)

	rootCmd.AddCommand(newRequestsCmd())
	rootCmd.AddCommand(newUsersCmd())
	rootCmd.AddCommand(newMachinesCmd())
	rootCmd.AddCommand(newPermissionsCmd())
	rootCmd.AddCommand(newSessionsCmd())
	rootCmd.AddCommand(newAuditCmd())
	rootCmd.AddCommand(newPluginsCmd())
	rootCmd.AddCommand(newAgentsCmd())
	rootCmd.AddCommand(newNotificationsCmd())
	rootCmd.AddCommand(newCACmd())
	rootCmd.AddCommand(newReportsCmd())
	rootCmd.AddCommand(newUsageCmd())
	rootCmd.AddCommand(newSetupCmd())
	rootCmd.AddCommand(newVersionCmd())
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
