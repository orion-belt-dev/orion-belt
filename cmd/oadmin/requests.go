package main

import (
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

func newRequestsCmd() *cobra.Command {
	requests := &cobra.Command{
		Use:   "requests",
		Short: "Manage access requests",
		Long:  `List pending JIT access requests and approve or reject them.`,
	}

	requests.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List pending access requests",
		Long:  `Lists access requests awaiting review. Use "osh requests list" to see your own requests in any state.`,
		Args:  cobra.NoArgs,
		Run:   runListRequests,
	})

	requests.AddCommand(&cobra.Command{
		Use:   "approve [request-id]",
		Short: "Approve an access request",
		Args:  cobra.ExactArgs(1),
		Run:   runApprove,
	})

	requests.AddCommand(&cobra.Command{
		Use:   "reject [request-id]",
		Short: "Reject an access request",
		Args:  cobra.ExactArgs(1),
		Run:   runReject,
	})

	return requests
}

func runListRequests(cmd *cobra.Command, args []string) {
	client := api()

	requests, err := client.ListPendingAccessRequests(ctx())
	if err != nil {
		cliflags.Fatalf("listing requests: %v", err)
	}

	if flags.JSON {
		if err := cliflags.PrintJSON(requests); err != nil {
			cliflags.Fatalf("%v", err)
		}
		return
	}

	if len(requests) == 0 {
		cliflags.Print("No pending access requests.")
		return
	}

	cliflags.Print("Pending Access Requests (%d):\n", len(requests))

	names := newNameIndex(client, true, true)
	table := cliflags.NewTable("REQUEST ID", "USER", "MACHINE", "REMOTE USER", "REASON", "DURATION", "REQUESTED")
	for _, req := range requests {
		remoteUser := "default"
		if len(req.RemoteUsers) > 0 {
			remoteUser = req.RemoteUsers[0]
		}
		table.Row(
			req.ID,
			names.user(req.UserID),
			names.machine(req.MachineID),
			remoteUser,
			cliflags.Truncate(req.Reason, 20),
			cliflags.FormatDuration(time.Duration(req.Duration)*time.Second),
			cliflags.FormatDuration(time.Since(req.RequestedAt)),
		)
	}
	table.Flush()

	cliflags.Print("\nUse 'oadmin requests approve <request-id>' to approve")
	cliflags.Print("Use 'oadmin requests reject <request-id>' to reject")
}

func runApprove(cmd *cobra.Command, args []string) {
	requestID := args[0]

	// An empty reviewer lets the API attribute the decision to the
	// authenticated admin identity.
	if err := api().ApproveAccessRequest(ctx(), requestID, ""); err != nil {
		cliflags.Fatalf("approving request: %v", err)
	}
	cliflags.Print("✓ Access request %s approved", cliflags.Short(requestID))
}

func runReject(cmd *cobra.Command, args []string) {
	requestID := args[0]

	if err := api().RejectAccessRequest(ctx(), requestID, ""); err != nil {
		cliflags.Fatalf("rejecting request: %v", err)
	}
	cliflags.Print("✗ Access request %s rejected", cliflags.Short(requestID))
}
