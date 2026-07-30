package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/auth"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/spf13/cobra"
)

var (
	permUsername    string
	permMachine     string
	permAccessType  string
	permDuration    int
	permGrantedBy   string
	permRemoteUsers string
)

var permissionCmd = &cobra.Command{
	Use:   "permission",
	Short: "Manage permissions",
	Long:  `Grant and revoke user permissions to access machines.`,
}

var permGrantCmd = &cobra.Command{
	Use:   "grant",
	Short: "Grant permission to a user",
	Long:  `Grant a user permission to access a specific machine.`,
	Example: `  orion-belt-server permission grant --user alice --machine web-01 --type ssh --remote-users "root,user"
  orion-belt-server permission grant --user bob --machine db-01 --type both --duration 3600 --remote-users "postgres"`,
	Run: runPermGrant,
}

var permListCmd = &cobra.Command{
	Use:   "list [USERNAME]",
	Short: "List permissions",
	Long:  `List all permissions or permissions for a specific user.`,
	Args:  cobra.MaximumNArgs(1),
	Run:   runPermList,
}

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage sessions",
	Long:  `View and manage active and historical sessions.`,
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions",
	Long:  `List all active sessions.`,
	Run:   runSessionList,
}

func init() {
	permissionCmd.AddCommand(permGrantCmd)
	permissionCmd.AddCommand(permListCmd)

	sessionCmd.AddCommand(sessionListCmd)

	// Permission grant flags
	permGrantCmd.Flags().StringVarP(&permUsername, "user", "u", "", "username (required)")
	permGrantCmd.Flags().StringVarP(&permMachine, "machine", "m", "", "machine name (required)")
	permGrantCmd.Flags().StringVarP(&permAccessType, "type", "t", "ssh", "access type: ssh, scp, or both")
	permGrantCmd.Flags().IntVarP(&permDuration, "duration", "d", 0, "duration in seconds (0 = permanent)")
	permGrantCmd.Flags().StringVarP(&permGrantedBy, "granted-by", "g", "", "username of grantor (defaults to first admin or first user)")
	permGrantCmd.Flags().StringVarP(&permRemoteUsers, "remote-users", "r", "root", "comma-separated list of allowed remote users (e.g., 'root,user,postgres')")

	permGrantCmd.MarkFlagRequired("user")
	permGrantCmd.MarkFlagRequired("machine")
}

func runPermGrant(cmd *cobra.Command, args []string) {
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Validate access type
	if permAccessType != "ssh" && permAccessType != "scp" && permAccessType != "both" {
		logger.Fatal("Invalid access type '%s'. Must be 'ssh', 'scp', or 'both'", permAccessType)
	}

	// Parse remote users
	remoteUsers := []string{}
	if permRemoteUsers != "" {
		parts := strings.Split(permRemoteUsers, ",")
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				remoteUsers = append(remoteUsers, trimmed)
			}
		}
	}

	if len(remoteUsers) == 0 {
		remoteUsers = []string{"root"} // default
	}

	// Initialize database
	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	// Get user
	user, err := store.GetUserByUsername(ctx, permUsername)
	if err != nil {
		logger.Fatal("User '%s' not found", permUsername)
	}

	// Get machine
	machine, err := store.GetMachineByName(ctx, permMachine)
	if err != nil {
		logger.Fatal("Machine '%s' not found", permMachine)
	}

	// Determine who is granting the permission
	var grantedBy string
	var grantorUsername string

	if permGrantedBy != "" {
		// Use explicitly specified grantor
		grantor, err := store.GetUserByUsername(ctx, permGrantedBy)
		if err != nil {
			logger.Fatal("Grantor user '%s' not found", permGrantedBy)
		}
		grantedBy = grantor.ID
		grantorUsername = grantor.Username
	} else {
		// Auto-select: first admin user, or first user, or self
		users, err := store.ListUsers(ctx, 100, 0)
		if err != nil {
			logger.Fatal("Failed to list users: %v", err)
		}

		// Look for an admin user
		for _, u := range users {
			if u.IsAdmin {
				grantedBy = u.ID
				grantorUsername = u.Username
				break
			}
		}

		// If no admin found, use the first user
		if grantedBy == "" {
			if len(users) > 0 {
				grantedBy = users[0].ID
				grantorUsername = users[0].Username
				logger.Warn("No admin user found, using first user (%s) as grantor", grantorUsername)
			} else {
				logger.Fatal("No users in database to use as permission grantor")
			}
		}
	}

	// Create auth service
	authService := auth.NewAuthService(store, logger)

	// Calculate expiration
	var duration *time.Duration
	if permDuration > 0 {
		d := time.Duration(permDuration) * time.Second
		duration = &d
	}

	// Grant permission with remote users
	if err := authService.GrantPermission(ctx, user.ID, machine.ID, permAccessType, remoteUsers, grantedBy, duration); err != nil {
		logger.Fatal("Failed to grant permission: %v", err)
	}

	logger.Info("Permission granted successfully:")
	fmt.Printf("  User:         %s\n", permUsername)
	fmt.Printf("  Machine:      %s\n", permMachine)
	fmt.Printf("  Access Type:  %s\n", permAccessType)
	fmt.Printf("  Remote Users: %v\n", remoteUsers)
	fmt.Printf("  Granted By:   %s\n", grantorUsername)
	if permDuration > 0 {
		fmt.Printf("  Duration:     %d seconds\n", permDuration)
		expiry := time.Now().Add(time.Duration(permDuration) * time.Second)
		fmt.Printf("  Expires:      %s\n", expiry.Format("2006-01-02 15:04:05"))
	} else {
		fmt.Printf("  Duration:     permanent\n")
	}
}

func runPermList(cmd *cobra.Command, args []string) {
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Initialize database
	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	var permissions []*common.Permission

	if len(args) > 0 {
		// List permissions for specific user
		username := args[0]
		user, err := store.GetUserByUsername(ctx, username)
		if err != nil {
			logger.Fatal("User '%s' not found", username)
		}

		permissions, err = store.ListUserPermissions(ctx, user.ID)
		if err != nil {
			logger.Fatal("Failed to list permissions: %v", err)
		}

		fmt.Printf("Permissions for user '%s':\n\n", username)
	} else {
		// List all permissions
		logger.Fatal("Listing all permissions not yet implemented. Please specify a username.")
	}

	if len(permissions) == 0 {
		fmt.Println("No permissions found.")
		return
	}

	// Get machine and user names for display
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MACHINE\tACCESS TYPE\tREMOTE USERS\tGRANTED\tEXPIRES")
	fmt.Fprintln(w, "-------\t-----------\t------------\t-------\t-------")

	for _, perm := range permissions {
		machine, _ := store.GetMachine(ctx, perm.MachineID)
		machineName := perm.MachineID
		if machine != nil {
			machineName = machine.Name
		}

		granted := perm.GrantedAt.Format("2006-01-02")

		expires := "never"
		if perm.ExpiresAt != nil {
			expires = perm.ExpiresAt.Format("2006-01-02 15:04:05")
		}

		remoteUsersStr := strings.Join(perm.RemoteUsers, ",")

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			machineName, perm.AccessType, remoteUsersStr, granted, expires)
	}
	w.Flush()
}

func runSessionList(cmd *cobra.Command, args []string) {
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	// Initialize database
	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	// List active sessions
	sessions, err := store.ListActiveSessions(ctx)
	if err != nil {
		logger.Fatal("Failed to list sessions: %v", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USER\tMACHINE\tREMOTE USER\tSTART TIME\tDURATION")
	fmt.Fprintln(w, "----\t-------\t-----------\t----------\t--------")

	for _, session := range sessions {
		user, _ := store.GetUser(ctx, session.UserID)
		machine, _ := store.GetMachine(ctx, session.MachineID)

		username := session.UserID
		if user != nil {
			username = user.Username
		}

		machineName := session.MachineID
		if machine != nil {
			machineName = machine.Name
		}

		startTime := session.StartTime.Format("2006-01-02 15:04:05")
		duration := time.Since(session.StartTime).Round(time.Second)

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			username, machineName, session.RemoteUser, startTime, duration)
	}
	w.Flush()
}
