package main

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

func newRequestsCmd() *cobra.Command {
	requests := &cobra.Command{
		Use:   "requests",
		Short: "Track your access requests",
		Long: `List and inspect the JIT access requests you have raised.

Create one with "osh --request-access [user@]machine --reason ...". Reviewing
other people's requests is an admin task ("oadmin requests").`,
	}

	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "List your access requests",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runRequestsList(status)
		},
	}
	list.Flags().StringVar(&status, "status", "", "filter by status (pending | approved | rejected | expired)")
	requests.AddCommand(list)

	requests.AddCommand(&cobra.Command{
		Use:   "get [request-id]",
		Short: "Show one access request",
		Args:  cobra.ExactArgs(1),
		Run:   runRequestsGet,
	})

	return requests
}

func runRequestsList(status string) {
	client := api()

	requests, err := client.ListAccessRequests(ctx(), status)
	if err != nil {
		cliflags.Fatalf("listing requests: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(requests)
		return
	}
	if len(requests) == 0 {
		cliflags.Print("No access requests.")
		return
	}

	machines := machineNames(client)
	table := cliflags.NewTable("ID", "MACHINE", "REMOTE USERS", "STATUS", "REASON", "DURATION", "REQUESTED", "EXPIRES")
	for _, request := range requests {
		remoteUsers := "default"
		if len(request.RemoteUsers) > 0 {
			remoteUsers = strings.Join(request.RemoteUsers, ",")
		}
		table.Row(request.ID, machines.name(request.MachineID), remoteUsers, request.Status,
			cliflags.Truncate(request.Reason, 24),
			cliflags.FormatDuration(time.Duration(request.Duration)*time.Second),
			cliflags.FormatTime(request.RequestedAt), cliflags.FormatTimePtr(request.ExpiresAt))
	}
	table.Flush()
}

// machineIndex maps machine IDs back to the names osh connects to. Access
// requests reference machines by UUID, which is unreadable in a listing.
// Lookups are best-effort: the listing is still worth showing without them.
type machineIndex map[string]string

func machineNames(client *sdk.Client) machineIndex {
	index := machineIndex{}
	if machines, err := client.ListMachines(ctx()); err == nil {
		for _, machine := range machines {
			index[machine.ID] = machine.Name
		}
	}
	return index
}

func (m machineIndex) name(machineID string) string {
	if name, ok := m[machineID]; ok {
		return name
	}
	return cliflags.Short(machineID)
}

func runRequestsGet(cmd *cobra.Command, args []string) {
	client := api()

	request, err := client.GetAccessRequest(ctx(), args[0])
	if err != nil {
		cliflags.Fatalf("getting request: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(request)
		return
	}

	cliflags.Print("ID:           %s", request.ID)
	cliflags.Print("Machine:      %s", request.MachineID)
	cliflags.Print("Access type:  %s", cliflags.OrDash(request.AccessType))
	cliflags.Print("Remote users: %s", cliflags.OrDash(strings.Join(request.RemoteUsers, ",")))
	cliflags.Print("Status:       %s", request.Status)
	cliflags.Print("Reason:       %s", cliflags.OrDash(request.Reason))
	cliflags.Print("Duration:     %s", cliflags.FormatDuration(time.Duration(request.Duration)*time.Second))
	cliflags.Print("Requested:    %s", cliflags.FormatTime(request.RequestedAt))
	cliflags.Print("Reviewed:     %s", cliflags.FormatTimePtr(request.ReviewedAt))
	cliflags.Print("Expires:      %s", cliflags.FormatTimePtr(request.ExpiresAt))
}
