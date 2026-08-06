package main

import (
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newPermissionsCmd() *cobra.Command {
	permissions := &cobra.Command{
		Use:     "permissions",
		Aliases: []string{"perms"},
		Short:   "Manage machine access permissions",
		Long:    `List, grant, update, and revoke standing access to machines.`,
	}

	var (
		user    string
		machine string
		limit   int
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List permissions",
		Long: `Lists permissions across the deployment.

Narrow the list with --user or --machine, which read the per-subject views
rather than the full grant table.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runPermissionsList(user, machine, limit)
		},
	}
	list.Flags().StringVar(&user, "user", "", "only permissions granted to this username or user ID")
	list.Flags().StringVar(&machine, "machine", "", "only permissions granted on this machine name or ID")
	list.Flags().IntVar(&limit, "limit", 100, "maximum number of permissions to return")
	permissions.AddCommand(list)

	var (
		grantUser    string
		grantMachine string
		accessType   string
		remoteUsers  []string
		duration     int
		expiresAt    string
	)
	grant := &cobra.Command{
		Use:   "grant",
		Short: "Grant access to a machine",
		Long: `Grants a user access to a machine.

Without --duration or --expires-at the grant is permanent. Temporary access is
usually better requested through the JIT flow ("osh --request-access"), which
records a reason and leaves an approval trail.`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			userID, err := resolveUserID(client, grantUser)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			machineID, err := resolveMachineID(client, grantMachine)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if duration > 0 && expiresAt != "" {
				cliflags.Fatalf("--duration and --expires-at are mutually exclusive")
			}

			req := sdk.GrantPermissionRequest{
				UserID:      userID,
				MachineID:   machineID,
				AccessType:  accessType,
				RemoteUsers: remoteUsers,
				ExpiresAt:   expiresAt,
			}
			if duration > 0 {
				req.DurationSec = &duration
			}

			permission, err := client.GrantPermission(ctx(), req)
			if err != nil {
				cliflags.Fatalf("granting permission: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(permission)
				return
			}
			cliflags.Print("✓ Granted %s access on %s to %s (id %s)",
				permission.AccessType, grantMachine, grantUser, permission.ID)
		},
	}
	grant.Flags().StringVar(&grantUser, "user", "", "username or user ID to grant access to (required)")
	grant.Flags().StringVar(&grantMachine, "machine", "", "machine name or ID to grant access on (required)")
	grant.Flags().StringVar(&accessType, "access-type", "ssh", "access type: ssh | scp | both")
	grant.Flags().StringArrayVar(&remoteUsers, "remote-user", nil, "UNIX user on the target the grant allows (repeatable)")
	grant.Flags().IntVar(&duration, "duration", 0, "grant lifetime in seconds (0 = permanent)")
	grant.Flags().StringVar(&expiresAt, "expires-at", "", "explicit expiry as an RFC3339 timestamp")
	_ = grant.MarkFlagRequired("user")
	_ = grant.MarkFlagRequired("machine")
	permissions.AddCommand(grant)

	var (
		newAccessType  string
		newRemoteUsers []string
		newDuration    int
		newExpiresAt   string
	)
	update := &cobra.Command{
		Use:   "update [permission-id]",
		Short: "Update a permission",
		Long:  `Changes the access type, allowed remote users, or expiry of an existing grant. Unset flags are left untouched.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var req sdk.UpdatePermissionRequest
			if cmd.Flags().Changed("access-type") {
				req.AccessType = &newAccessType
			}
			if cmd.Flags().Changed("remote-user") {
				req.RemoteUsers = newRemoteUsers
			}
			if cmd.Flags().Changed("expires-at") {
				req.ExpiresAt = &newExpiresAt
			}
			if cmd.Flags().Changed("duration") {
				req.DurationSec = &newDuration
			}
			if req.AccessType == nil && req.RemoteUsers == nil && req.ExpiresAt == nil && req.DurationSec == nil {
				cliflags.Fatalf("nothing to update: pass --access-type, --remote-user, --duration, or --expires-at")
			}

			permission, err := api().UpdatePermission(ctx(), args[0], req)
			if err != nil {
				cliflags.Fatalf("updating permission: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(permission)
				return
			}
			cliflags.Print("✓ Permission %s updated", permission.ID)
		},
	}
	update.Flags().StringVar(&newAccessType, "access-type", "ssh", "access type: ssh | scp | both")
	update.Flags().StringArrayVar(&newRemoteUsers, "remote-user", nil, "replacement remote user (repeatable)")
	update.Flags().IntVar(&newDuration, "duration", 0, "new lifetime in seconds from now")
	update.Flags().StringVar(&newExpiresAt, "expires-at", "", "new expiry as an RFC3339 timestamp")
	permissions.AddCommand(update)

	permissions.AddCommand(&cobra.Command{
		Use:   "revoke [permission-id]",
		Short: "Revoke a permission",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := api().RevokePermission(ctx(), args[0]); err != nil {
				cliflags.Fatalf("revoking permission: %v", err)
			}
			cliflags.Print("✓ Permission %s revoked", args[0])
		},
	})

	return permissions
}

func runPermissionsList(user, machine string, limit int) {
	if user != "" && machine != "" {
		cliflags.Fatalf("--user and --machine are mutually exclusive")
	}

	client := api()

	var (
		permissions []sdk.Permission
		err         error
	)
	switch {
	case user != "":
		var userID string
		if userID, err = resolveUserID(client, user); err == nil {
			permissions, err = client.GetUserPermissions(ctx(), userID)
		}
	case machine != "":
		var machineID string
		if machineID, err = resolveMachineID(client, machine); err == nil {
			permissions, err = client.GetMachinePermissions(ctx(), machineID)
		}
	default:
		permissions, err = client.ListAllPermissions(ctx(), limit)
	}
	if err != nil {
		cliflags.Fatalf("listing permissions: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(permissions)
		return
	}
	if len(permissions) == 0 {
		cliflags.Print("No permissions.")
		return
	}

	// IDs are printed in full: "permissions update" and "revoke" take one.
	names := newNameIndex(client, true, true)
	table := cliflags.NewTable("ID", "USER", "MACHINE", "ACCESS", "REMOTE USERS", "GRANTED", "EXPIRES")
	for _, permission := range permissions {
		remoteUsers := "-"
		if len(permission.RemoteUsers) > 0 {
			remoteUsers = strings.Join(permission.RemoteUsers, ",")
		}
		table.Row(permission.ID, names.user(permission.UserID), names.machine(permission.MachineID),
			permission.AccessType, remoteUsers,
			cliflags.FormatTime(permission.GrantedAt), cliflags.FormatTimePtr(permission.ExpiresAt))
	}
	table.Flush()
}
