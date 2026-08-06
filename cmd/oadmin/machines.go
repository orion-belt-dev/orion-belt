package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
	"github.com/zrougamed/orion-belt/pkg/sdk"
)

func newMachinesCmd() *cobra.Command {
	machines := &cobra.Command{
		Use:   "machines",
		Short: "Manage target machines",
		Long:  `List, inspect, create, update, and delete the machines agents connect for.`,
	}

	machines.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List machines",
		Args:  cobra.NoArgs,
		Run:   runMachinesList,
	})

	machines.AddCommand(&cobra.Command{
		Use:   "get [name|machine-id]",
		Short: "Show one machine",
		Args:  cobra.ExactArgs(1),
		Run:   runMachinesGet,
	})

	var (
		hostname string
		port     int
		agentID  string
		tags     []string
		active   bool
	)
	create := &cobra.Command{
		Use:   "create [name]",
		Short: "Register a machine",
		Long: `Creates a machine record.

This is the API-side half of onboarding; the host still needs an agent dialing
out to the gateway. "oadmin agents install-script" generates that installer.

A new machine starts inactive — it becomes active once its agent registers, so
a record created here will not be connectable until then. Pass --active to
override that, for example when re-creating a record for a host whose agent is
already running.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			tagMap, err := cliflags.ParseKeyValues(tags)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			req := sdk.CreateMachineRequest{
				Name:     args[0],
				Hostname: hostname,
				Port:     port,
				Tags:     tagMap,
				AgentID:  agentID,
			}
			if active {
				yes := true
				req.IsActive = &yes
			}
			machine, err := api().CreateMachine(ctx(), req)
			if err != nil {
				cliflags.Fatalf("creating machine: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(machine)
				return
			}
			cliflags.Print("✓ Machine %s created (id %s)", machine.Name, machine.ID)
			if !machine.IsActive {
				cliflags.Print("  Inactive until its agent registers. Installer: oadmin agents install-script %s", machine.Name)
			}
		},
	}
	create.Flags().StringVar(&hostname, "hostname", "", "hostname or address of the target (required)")
	create.Flags().IntVar(&port, "port", 22, "SSH port on the target")
	create.Flags().StringVar(&agentID, "agent-id", "", "agent identity that serves this machine")
	create.Flags().StringArrayVar(&tags, "tag", nil, "tag as key=value (repeatable)")
	create.Flags().BoolVar(&active, "active", false, "create the machine already active, instead of waiting for its agent")
	_ = create.MarkFlagRequired("hostname")
	machines.AddCommand(create)

	var (
		newName     string
		newHostname string
		newPort     int
		newAgentID  string
		newTags     []string
		enable      bool
		disable     bool
	)
	update := &cobra.Command{
		Use:   "update [name|machine-id]",
		Short: "Update a machine",
		Long:  `Changes a machine's name, address, port, agent, tags, or active state. Unset flags are left untouched.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			machineID, err := resolveMachineID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}

			var req sdk.UpdateMachineRequest
			if cmd.Flags().Changed("name") {
				req.Name = &newName
			}
			if cmd.Flags().Changed("hostname") {
				req.Hostname = &newHostname
			}
			if cmd.Flags().Changed("port") {
				req.Port = &newPort
			}
			if cmd.Flags().Changed("agent-id") {
				req.AgentID = &newAgentID
			}
			if cmd.Flags().Changed("tag") {
				tagMap, terr := cliflags.ParseKeyValues(newTags)
				if terr != nil {
					cliflags.Fatalf("%v", terr)
				}
				if tagMap == nil {
					tagMap = map[string]string{}
				}
				req.Tags = &tagMap
			}
			switch {
			case enable && disable:
				cliflags.Fatalf("--enable and --disable are mutually exclusive")
			case enable:
				active := true
				req.IsActive = &active
			case disable:
				active := false
				req.IsActive = &active
			}
			if req.Name == nil && req.Hostname == nil && req.Port == nil &&
				req.AgentID == nil && req.Tags == nil && req.IsActive == nil {
				cliflags.Fatalf("nothing to update: pass --name, --hostname, --port, --agent-id, --tag, --enable, or --disable")
			}

			machine, err := client.UpdateMachine(ctx(), machineID, req)
			if err != nil {
				cliflags.Fatalf("updating machine: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(machine)
				return
			}
			cliflags.Print("✓ Machine %s updated", machine.Name)
		},
	}
	update.Flags().StringVar(&newName, "name", "", "new machine name")
	update.Flags().StringVar(&newHostname, "hostname", "", "new hostname or address")
	update.Flags().IntVar(&newPort, "port", 22, "new SSH port")
	update.Flags().StringVar(&newAgentID, "agent-id", "", "new agent identity")
	update.Flags().StringArrayVar(&newTags, "tag", nil, "replacement tag as key=value (repeatable; replaces all tags)")
	update.Flags().BoolVar(&enable, "enable", false, "mark the machine active")
	update.Flags().BoolVar(&disable, "disable", false, "mark the machine inactive")
	machines.AddCommand(update)

	var archive bool
	del := &cobra.Command{
		Use:   "delete [name|machine-id]",
		Short: "Delete a machine",
		Long:  `Removes a machine. With --archive the record is retained and marked inactive, which keeps its sessions and audit trail attributable.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			machineID, err := resolveMachineID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if err := client.DeleteMachine(ctx(), machineID, archive); err != nil {
				cliflags.Fatalf("deleting machine: %v", err)
			}
			if archive {
				cliflags.Print("✓ Machine %s archived", args[0])
				return
			}
			cliflags.Print("✓ Machine %s deleted", args[0])
		},
	}
	del.Flags().BoolVar(&archive, "archive", false, "archive instead of deleting outright")
	machines.AddCommand(del)

	return machines
}

func runMachinesList(cmd *cobra.Command, args []string) {
	machines, err := api().ListMachines(ctx())
	if err != nil {
		cliflags.Fatalf("listing machines: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(machines)
		return
	}
	if len(machines) == 0 {
		cliflags.Print("No machines.")
		return
	}

	table := cliflags.NewTable("ID", "NAME", "HOSTNAME", "PORT", "ACTIVE", "TAGS", "LAST SEEN")
	for _, machine := range machines {
		table.Row(cliflags.Short(machine.ID), machine.Name, machine.Hostname, machine.Port,
			cliflags.YesNo(machine.IsActive), cliflags.FormatTags(machine.Tags), cliflags.FormatTimePtr(machine.LastSeenAt))
	}
	table.Flush()
}

func runMachinesGet(cmd *cobra.Command, args []string) {
	client := api()
	machineID, err := resolveMachineID(client, args[0])
	if err != nil {
		cliflags.Fatalf("%v", err)
	}

	machine, err := client.GetMachine(ctx(), machineID)
	if err != nil {
		cliflags.Fatalf("getting machine: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(machine)
		return
	}

	cliflags.Print("ID:        %s", machine.ID)
	cliflags.Print("Name:      %s", machine.Name)
	cliflags.Print("Hostname:  %s", machine.Hostname)
	cliflags.Print("Port:      %d", machine.Port)
	cliflags.Print("Agent:     %s", cliflags.OrDash(machine.AgentID))
	cliflags.Print("Active:    %s", cliflags.YesNo(machine.IsActive))
	cliflags.Print("Tags:      %s", cliflags.FormatTags(machine.Tags))
	cliflags.Print("Last seen: %s", cliflags.FormatTimePtr(machine.LastSeenAt))
	cliflags.Print("Created:   %s", cliflags.FormatTime(machine.CreatedAt))
}

// resolveMachineID accepts either a machine ID or the machine name operators
// actually type (the same name osh connects to).
func resolveMachineID(client *sdk.Client, ref string) (string, error) {
	if looksLikeID(ref) {
		return ref, nil
	}
	machine, err := client.GetMachineByName(ctx(), ref)
	if err != nil {
		return "", fmt.Errorf("looking up machine %q: %w", ref, err)
	}
	return machine.ID, nil
}
