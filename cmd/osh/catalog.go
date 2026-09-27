package main

import (
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

func newCatalogCmd() *cobra.Command {
	var (
		all    bool
		userID string
	)
	catalog := &cobra.Command{
		Use:   "catalog",
		Short: "Show the access you hold or can request",
		Long: `Lists capabilities: a login on a machine with an access type, whether you
already hold it, and why it is suggested.

By default the catalog covers machines related to you (current grants, past
requests and sessions). Use --all to include every machine. To request one,
run "osh --request-access [user@]machine --reason ...".`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			scope := ""
			if all {
				scope = "all"
			}
			runCatalog(scope, userID)
		},
	}
	catalog.Flags().BoolVar(&all, "all", false, "include every machine, not only those related to you")
	catalog.Flags().StringVar(&userID, "user-id", "", "show another user's catalog (admin, operator or auditor only)")
	return catalog
}

func runCatalog(scope, userID string) {
	result, err := api().ListCapabilities(ctx(), scope, userID)
	if err != nil {
		cliflags.Fatalf("listing capabilities: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(result)
		return
	}
	if len(result.Capabilities) == 0 {
		cliflags.Print("No capabilities.")
		return
	}

	table := cliflags.NewTable("MACHINE", "REMOTE USER", "ACCESS", "STATUS", "EXPIRES", "REASON")
	for _, capability := range result.Capabilities {
		table.Row(capability.MachineName, cliflags.OrDash(capability.RemoteUser), capability.AccessType,
			capability.Status, cliflags.FormatTimePtr(capability.ExpiresAt), cliflags.Truncate(capability.Reason, 40))
	}
	table.Flush()
	if result.Truncated {
		cliflags.Print("\nThe catalog was truncated; narrow it by leaving out --all.")
	}
}
