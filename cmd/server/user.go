package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/spf13/cobra"
)

var (
	userName  string
	userEmail string
	userKey   string
	userAdmin bool
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage users",
	Long:  `Create, list, and manage Orion-Belt users.`,
}

var userCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user",
	Long:  `Create a new user account in the Orion-Belt system.`,
	Example: `  orion-belt-server user create --name alice --email alice@example.com --key "ssh-rsa AAAAB3..."
  orion-belt-server user create --name admin --email admin@example.com --key "ssh-ed25519 AAAAC3..." --admin`,
	Run: runUserCreate,
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all users",
	Long:  `List all users registered in the Orion-Belt system.`,
	Run:   runUserList,
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete USERNAME",
	Short: "Delete a user",
	Long:  `Delete a user account from the Orion-Belt system.`,
	Args:  cobra.ExactArgs(1),
	Run:   runUserDelete,
}

func init() {
	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userDeleteCmd)

	// Create flags
	userCreateCmd.Flags().StringVarP(&userName, "name", "n", "", "username (required)")
	userCreateCmd.Flags().StringVarP(&userEmail, "email", "e", "", "user email (required)")
	userCreateCmd.Flags().StringVarP(&userKey, "key", "k", "", "user SSH public key (required)")
	userCreateCmd.Flags().BoolVarP(&userAdmin, "admin", "a", false, "grant admin privileges")

	userCreateCmd.MarkFlagRequired("name")
	userCreateCmd.MarkFlagRequired("email")
	userCreateCmd.MarkFlagRequired("key")
}

func runUserCreate(cmd *cobra.Command, args []string) {
	logger := getLogger()
	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		logger.Fatal("%v", err)
	}
	defer store.Close()

	// Validate public key format
	if !strings.HasPrefix(userKey, "ssh-") {
		logger.Fatal("Invalid SSH public key format. Key must start with ssh-rsa, ssh-ed25519, etc.")
	}

	// Check if user already exists
	existingUser, _ := store.GetUserByUsername(ctx, userName)
	if existingUser != nil {
		logger.Fatal("User with username '%s' already exists", userName)
	}

	// Create user
	user := common.NewUser(userName, userEmail, userKey, userAdmin)
	if err := store.CreateUser(ctx, user); err != nil {
		logger.Fatal("Failed to create user: %v", err)
	}

	logger.Info("User created successfully:")
	fmt.Printf("  Username: %s\n", userName)
	fmt.Printf("  Email:    %s\n", userEmail)
	fmt.Printf("  User ID:  %s\n", user.ID)
	fmt.Printf("  Admin:    %v\n", userAdmin)

	fmt.Printf("\nUser '%s' can now connect using:\n", userName)
	fmt.Printf("  osh machine-name\n")
}

func runUserList(cmd *cobra.Command, args []string) {
	logger := getLogger()
	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		logger.Fatal("%v", err)
	}
	defer store.Close()

	// List all users
	users, err := store.ListUsers(ctx, 1000, 0)
	if err != nil {
		logger.Fatal("Failed to list users: %v", err)
	}

	if len(users) == 0 {
		fmt.Println("No users registered.")
		return
	}

	// Print table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "USERNAME\tEMAIL\tADMIN\tCREATED")
	fmt.Fprintln(w, "--------\t-----\t-----\t-------")

	for _, user := range users {
		admin := "no"
		if user.IsAdmin {
			admin = "yes"
		}

		created := user.CreatedAt.Format("2006-01-02 15:04:05")

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			user.Username, user.Email, admin, created)
	}
	w.Flush()
}

func runUserDelete(cmd *cobra.Command, args []string) {
	username := args[0]
	logger := getLogger()
	ctx := context.Background()
	store, err := openStore(ctx)
	if err != nil {
		logger.Fatal("%v", err)
	}
	defer store.Close()

	// Get user
	user, err := store.GetUserByUsername(ctx, username)
	if err != nil {
		logger.Fatal("User '%s' not found", username)
	}

	// Delete user
	if err := store.DeleteUser(ctx, user.ID); err != nil {
		logger.Fatal("Failed to delete user: %v", err)
	}

	fmt.Printf("User '%s' deleted successfully.\n", username)
}
