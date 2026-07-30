package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var (
	agentName     string
	agentKey      string
	agentHostname string
	agentPort     int
	agentTags     []string
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agents",
	Long:  `Register, list, and manage Orion-Belt agents.`,
}

var agentRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new agent",
	Long:  `Register a new agent machine with the Orion-Belt server.`,
	Example: `  orion-belt-server agent register --name web-01 --key "ssh-rsa AAAAB3..." --hostname 192.168.1.100 --port 22
  orion-belt-server agent register --name db-01 --key "ssh-ed25519 AAAAC3..." --tags environment=production,role=database`,
	Run: runAgentRegister,
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered agents",
	Long:  `List all agents registered with the Orion-Belt server.`,
	Run:   runAgentList,
}

var agentDeleteCmd = &cobra.Command{
	Use:   "delete NAME",
	Short: "Delete an agent",
	Long:  `Delete an agent and its associated machine record.`,
	Args:  cobra.ExactArgs(1),
	Run:   runAgentDelete,
}

func init() {
	agentCmd.AddCommand(agentRegisterCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentDeleteCmd)

	agentRegisterCmd.Flags().StringVarP(&agentName, "name", "n", "", "agent name (required)")
	agentRegisterCmd.Flags().StringVarP(&agentKey, "key", "k", "", "agent SSH public key (required)")
	agentRegisterCmd.Flags().StringVarP(&agentHostname, "hostname", "H", "", "agent hostname (defaults to name)")
	agentRegisterCmd.Flags().IntVarP(&agentPort, "port", "p", 22, "SSH port on the agent machine")
	agentRegisterCmd.Flags().StringSliceVarP(&agentTags, "tags", "t", []string{}, "comma-separated key=value tags")

	_ = agentRegisterCmd.MarkFlagRequired("name")
	_ = agentRegisterCmd.MarkFlagRequired("key")
}

func runAgentRegister(cmd *cobra.Command, args []string) {
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	if !strings.HasPrefix(agentKey, "ssh-") {
		logger.Fatal("Invalid SSH public key format. Key must start with ssh-rsa, ssh-ed25519, etc.")
	}

	if agentHostname == "" {
		agentHostname = agentName
	}

	tagMap := make(map[string]string)
	for _, tag := range agentTags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) == 2 {
			tagMap[parts[0]] = parts[1]
		}
	}

	existingMachine, _ := store.GetMachineByName(ctx, agentName)
	if existingMachine != nil {
		logger.Fatal("Agent with name '%s' already exists", agentName)
	}

	authority, err := ca.New(config.SSHCA, store, logger)
	if err != nil {
		logger.Fatal("SSH CA: %v", err)
	}

	machine := common.NewMachine(agentName, agentHostname, agentPort, tagMap)

	if authority != nil {
		// SSH CA path: machine-only identity; Host-CA cert authenticates the agent.
		if err := store.CreateMachine(ctx, machine); err != nil {
			logger.Fatal("Failed to create machine: %v", err)
		}
		pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(agentKey))
		if err != nil {
			_ = store.DeleteMachine(ctx, machine.ID)
			logger.Fatal("Invalid agent public key: %v", err)
		}
		hostCert, err := authority.IssueHostCert(ctx, machine.ID, []string{agentName}, pub, authority.HostCertTTL())
		if err != nil {
			_ = store.DeleteMachine(ctx, machine.ID)
			logger.Fatal("Failed to issue agent host certificate: %v", err)
		}
		_, hostCALine := authority.ExportPublicKeys()

		logger.Info("Agent registered with Host CA certificate:")
		fmt.Printf("  Name:       %s\n", agentName)
		fmt.Printf("  Machine ID: %s\n", machine.ID)
		fmt.Printf("  Hostname:   %s\n", agentHostname)
		fmt.Printf("  Port:       %d\n", agentPort)
		fmt.Printf("\nWrite the agent private key to auth.key_file and place this cert beside it as <key_file>-cert.pub:\n\n")
		fmt.Print(string(ssh.MarshalAuthorizedKey(hostCert)))
		fmt.Printf("\nAlso set auth.host_ca_public_key in agent.yaml to:\n  %s\n", strings.TrimSpace(hostCALine))
		fmt.Printf("\nThen start: orion-belt-agent -c /path/to/agent.yaml\n")
		return
	}

	// Legacy: synthetic agent user + machine
	agentUser := common.NewUser(agentName, fmt.Sprintf("%s@agent.orion-belt", agentName), agentKey, false)
	if err := store.CreateUser(ctx, agentUser); err != nil {
		logger.Fatal("Failed to create agent user: %v", err)
	}

	machine.AgentID = agentUser.ID
	if err := store.CreateMachine(ctx, machine); err != nil {
		_ = store.DeleteUser(ctx, agentUser.ID)
		logger.Fatal("Failed to create machine: %v", err)
	}

	logger.Info("Agent registered successfully:")
	fmt.Printf("  Name:       %s\n", agentName)
	fmt.Printf("  User ID:    %s\n", agentUser.ID)
	fmt.Printf("  Machine ID: %s\n", machine.ID)
	fmt.Printf("  Hostname:   %s\n", agentHostname)
	fmt.Printf("  Port:       %d\n", agentPort)
	if len(tagMap) > 0 {
		fmt.Printf("  Tags:       ")
		first := true
		for k, v := range tagMap {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s=%s", k, v)
			first = false
		}
		fmt.Println()
	}

	fmt.Printf("\nAgent '%s' can now connect to the server using:\n", agentName)
	fmt.Printf("  orion-belt-agent -c /path/to/agent.yaml\n")
}

func runAgentList(cmd *cobra.Command, args []string) {
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	machines, err := store.ListMachines(ctx, 1000, 0)
	if err != nil {
		logger.Fatal("Failed to list machines: %v", err)
	}

	if len(machines) == 0 {
		fmt.Println("No agents registered.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tHOSTNAME\tPORT\tSTATUS\tLAST SEEN\tTAGS")
	fmt.Fprintln(w, "----\t--------\t----\t------\t---------\t----")

	for _, machine := range machines {
		status := "offline"
		if machine.IsActive {
			status = "online"
		}

		lastSeen := "never"
		if machine.LastSeenAt != nil {
			lastSeen = machine.LastSeenAt.Format("2006-01-02 15:04:05")
		}

		tags := ""
		if len(machine.Tags) > 0 {
			tagPairs := []string{}
			for k, v := range machine.Tags {
				tagPairs = append(tagPairs, fmt.Sprintf("%s=%s", k, v))
			}
			tags = strings.Join(tagPairs, ", ")
		}

		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
			machine.Name, machine.Hostname, machine.Port, status, lastSeen, tags)
	}
	w.Flush()
}

func runAgentDelete(cmd *cobra.Command, args []string) {
	agentName := args[0]
	logger := getLogger()
	config, err := loadConfig()
	if err != nil {
		logger.Fatal("Failed to load config: %v", err)
	}

	store, err := database.NewStore(config.Database.Driver, config.Database.ConnectionString)
	if err != nil {
		logger.Fatal("Failed to create database store: %v", err)
	}

	ctx := context.Background()
	if err := store.Connect(ctx); err != nil {
		logger.Fatal("Failed to connect to database: %v", err)
	}
	defer store.Close()

	machine, err := store.GetMachineByName(ctx, agentName)
	if err != nil {
		logger.Fatal("Agent '%s' not found", agentName)
	}

	if err := store.DeleteMachine(ctx, machine.ID); err != nil {
		logger.Fatal("Failed to delete machine: %v", err)
	}

	if machine.AgentID != "" {
		if err := store.DeleteUser(ctx, machine.AgentID); err != nil {
			logger.Warn("Failed to delete agent user: %v", err)
		}
	}

	fmt.Printf("Agent '%s' deleted successfully.\n", agentName)
}
