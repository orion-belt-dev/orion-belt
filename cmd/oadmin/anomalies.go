package main

import (
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

func newAnomaliesCmd() *cobra.Command {
	anomalies := &cobra.Command{
		Use:   "anomalies",
		Short: "Review sessions flagged as unusual",
		Long: `Sessions are flagged, never blocked, when they look unusual:

  first_time_access  a user's first session on a machine tagged sensitivity: high
                     (or pci: in-scope)
  off_hours          a session starting outside every hour the user has started
                     one before (needs 10 earlier sessions)

Each flag carries the signal that raised it.`,
	}

	var (
		limit int
		user  string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List flagged sessions, newest first",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runAnomaliesList(limit, user)
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "maximum number of flags to return")
	list.Flags().StringVar(&user, "for", "", "only flags for this user (name or ID)")
	anomalies.AddCommand(list)
	return anomalies
}

func runAnomaliesList(limit int, user string) {
	client := api()
	userID := ""
	if user != "" {
		id, err := resolveUserID(client, user)
		if err != nil {
			cliflags.Fatalf("%v", err)
		}
		userID = id
	}
	flagged, err := client.ListAnomalies(ctx(), limit, userID)
	if err != nil {
		cliflags.Fatalf("listing anomalies: %v", err)
	}
	if flags.JSON {
		cliflags.MustPrintJSON(flagged)
		return
	}
	if len(flagged) == 0 {
		cliflags.Print("No flagged sessions.")
		return
	}
	names := newNameIndex(client, true, false)
	table := cliflags.NewTable("FLAGGED", "KIND", "USER", "TARGET", "SESSION", "SIGNAL")
	for _, a := range flagged {
		table.Row(cliflags.FormatTime(a.FlaggedAt), a.Kind, names.user(a.UserID),
			a.RemoteUser+"@"+a.Machine, cliflags.OrDash(a.SessionID), a.Signal)
	}
	table.Flush()
}
