package main

import (
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newBulkGrantCmd() *cobra.Command {
	var (
		users, machines, remoteUsers []string
		accessType, expiresAt        string
		duration                     int
	)
	cmd := &cobra.Command{
		Use:   "bulk-grant",
		Short: "Grant access to several users on several machines",
		Long: `Grants one access type to every --user on every --machine (at most 500
pairs). Each grant is applied and audited on its own; pairs that fail are
listed and the rest still apply.`,
		Example: `  oadmin permissions bulk-grant --user alice --user bob --machine web-01 --machine web-02 \
    --access-type ssh --remote-user deploy --duration 3600`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			req := sdk.BulkGrantRequest{AccessType: accessType, RemoteUsers: remoteUsers, ExpiresAt: expiresAt}
			if duration > 0 {
				req.DurationSec = &duration
			}
			for _, u := range users {
				id, err := resolveUserID(client, u)
				if err != nil {
					cliflags.Fatalf("%v", err)
				}
				req.UserIDs = append(req.UserIDs, id)
			}
			for _, m := range machines {
				id, err := resolveMachineID(client, m)
				if err != nil {
					cliflags.Fatalf("%v", err)
				}
				req.MachineIDs = append(req.MachineIDs, id)
			}
			res, err := client.BulkGrantPermissions(ctx(), req)
			if err != nil {
				cliflags.Fatalf("bulk grant: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(res)
				return
			}
			cliflags.Print("✓ Granted %s access %d times", accessType, res.Granted)
			for _, f := range res.Failed {
				cliflags.Print("  ✗ %s on %s: %s", f.UserID, f.MachineID, f.Error)
			}
		},
	}
	cmd.Flags().StringArrayVar(&users, "user", nil, "username or user ID to grant to (repeatable)")
	cmd.Flags().StringArrayVar(&machines, "machine", nil, "machine name or ID to grant on (repeatable)")
	cmd.Flags().StringVar(&accessType, "access-type", "ssh", "access type: ssh | scp | both (ssh+scp) | rdp | vnc | k8s")
	cmd.Flags().StringArrayVar(&remoteUsers, "remote-user", nil, "remote account, or namespace/role scope for k8s (repeatable)")
	cmd.Flags().IntVar(&duration, "duration", 0, "grant lifetime in seconds (0 = permanent)")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "explicit expiry as an RFC3339 timestamp")
	_ = cmd.MarkFlagRequired("user")
	_ = cmd.MarkFlagRequired("machine")
	return cmd
}

func newBulkRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bulk-revoke [permission-id...]",
		Short: "Revoke several permissions at once",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			res, err := api().BulkRevokePermissions(ctx(), args)
			if err != nil {
				cliflags.Fatalf("bulk revoke: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(res)
				return
			}
			cliflags.Print("✓ Revoked %d", res.Revoked)
			for _, f := range res.Failed {
				cliflags.Print("  ✗ %s: %s", f.PermissionID, f.Error)
			}
		},
	}
}

func newEffectiveCmd() *cobra.Command {
	var user, machine string
	cmd := &cobra.Command{
		Use:   "effective",
		Short: "Show what a user can actually do, or who can reach a machine",
		Long: `Resolves roles and grants into effective access: admins and operators reach
every machine with any access type; a "both" grant is ssh and scp. Each line
names its source (role or permission ID) and expiry. Pass --user or
--machine.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if (user == "") == (machine == "") {
				cliflags.Fatalf("pass exactly one of --user or --machine")
			}
			client := api()
			var userID, machineID string
			var err error
			if user != "" {
				userID, err = resolveUserID(client, user)
			} else {
				machineID, err = resolveMachineID(client, machine)
			}
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			rows, err := client.EffectivePermissions(ctx(), userID, machineID)
			if err != nil {
				cliflags.Fatalf("effective permissions: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(rows)
				return
			}
			if len(rows) == 0 {
				cliflags.Print("No access.")
				return
			}
			t := cliflags.NewTable("USER", "MACHINE", "ACCESS", "AS", "VIA", "EXPIRES")
			for _, r := range rows {
				as := strings.Join(r.RemoteUsers, ",")
				if r.AccessType == "*" {
					as = "any"
				} else if as == "" {
					as = "—"
				}
				expires := "never"
				if r.ExpiresAt != nil {
					expires = r.ExpiresAt.Local().Format("2006-01-02 15:04")
				}
				t.Row(r.Username, r.MachineName, r.AccessType, as, r.Source, expires)
			}
			t.Flush()
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "username or user ID")
	cmd.Flags().StringVar(&machine, "machine", "", "machine name or ID")
	return cmd
}
