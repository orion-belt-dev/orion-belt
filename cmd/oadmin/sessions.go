package main

import (
	"os"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newSessionsCmd() *cobra.Command {
	sessions := &cobra.Command{
		Use:   "sessions",
		Short: "Inspect SSH and web-terminal sessions",
		Long:  `List sessions, show session detail, and download recordings for playback.`,
	}

	var (
		active bool
		status string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runSessionsList(active, status)
		},
	}
	list.Flags().BoolVar(&active, "active", false, "only sessions that are currently open")
	list.Flags().StringVar(&status, "status", "", "filter by status (active | completed | terminated)")
	sessions.AddCommand(list)

	sessions.AddCommand(&cobra.Command{
		Use:   "get [session-id]",
		Short: "Show one session",
		Args:  cobra.ExactArgs(1),
		Run:   runSessionsGet,
	})

	var output string
	content := &cobra.Command{
		Use:   "content [session-id]",
		Short: "Download a session recording",
		Long: `Writes the raw recording of a session to stdout, or to a file with --output.

This is the same material the console player replays; live watching stays a
console feature.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runSessionContent(args[0], output)
		},
	}
	content.Flags().StringVarP(&output, "output", "o", "", "write the recording to this file instead of stdout")
	sessions.AddCommand(content)

	return sessions
}

func runSessionsList(active bool, status string) {
	if active && status != "" {
		cliflags.Fatalf("--active and --status are mutually exclusive")
	}

	client := api()

	var (
		sessions []sdk.Session
		err      error
	)
	if active {
		sessions, err = client.ListActiveSessions(ctx())
	} else {
		sessions, err = client.ListSessions(ctx(), status)
	}
	if err != nil {
		cliflags.Fatalf("listing sessions: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(sessions)
		return
	}
	if len(sessions) == 0 {
		cliflags.Print("No sessions.")
		return
	}

	// IDs are printed in full: "sessions get" and "sessions content" take one.
	names := newNameIndex(client, true, true)
	table := cliflags.NewTable("ID", "USER", "MACHINE", "REMOTE USER", "SOURCE", "STATUS", "STARTED", "ENDED")
	for _, session := range sessions {
		table.Row(session.ID, names.user(session.UserID), names.machine(session.MachineID),
			cliflags.OrDash(session.RemoteUser), cliflags.OrDash(session.Source),
			session.Status, cliflags.FormatTime(session.StartTime), cliflags.FormatTimePtr(session.EndTime))
	}
	table.Flush()
}

func runSessionsGet(cmd *cobra.Command, args []string) {
	client := api()

	session, err := client.GetSession(ctx(), args[0])
	if err != nil {
		cliflags.Fatalf("getting session: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(session)
		return
	}

	names := newNameIndex(client, true, true)
	cliflags.Print("ID:          %s", session.ID)
	cliflags.Print("User:        %s (%s)", names.user(session.UserID), session.UserID)
	cliflags.Print("Machine:     %s (%s)", names.machine(session.MachineID), session.MachineID)
	cliflags.Print("Remote user: %s", cliflags.OrDash(session.RemoteUser))
	cliflags.Print("Source:      %s", cliflags.OrDash(session.Source))
	cliflags.Print("Status:      %s", session.Status)
	cliflags.Print("Started:     %s", cliflags.FormatTime(session.StartTime))
	cliflags.Print("Ended:       %s", cliflags.FormatTimePtr(session.EndTime))
	cliflags.Print("Recording:   %s", cliflags.OrDash(session.RecordingPath))
}

func runSessionContent(sessionID, output string) {
	content, err := api().GetSessionContent(ctx(), sessionID)
	if err != nil {
		cliflags.Fatalf("downloading recording: %v", err)
	}

	if output == "" {
		if _, err := os.Stdout.Write(content); err != nil {
			cliflags.Fatalf("writing recording: %v", err)
		}
		return
	}

	// 0600: recordings replay whatever the operator typed, including any
	// secret they pasted into the session.
	if err := os.WriteFile(output, content, 0o600); err != nil {
		cliflags.Fatalf("writing %s: %v", output, err)
	}
	cliflags.Print("✓ Wrote %d bytes to %s", len(content), output)
}
