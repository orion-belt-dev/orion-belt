package main

import (
	"os"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

// defaultPackageBaseURL is where generated installers fetch agent packages
// from. It matches the console's Add-agent default (the public GitHub Pages
// mirror); air-gapped installs point --package-base-url at their own mirror.
const defaultPackageBaseURL = "https://orion-belt-dev.github.io/packages"

func newAgentsCmd() *cobra.Command {
	agents := &cobra.Command{
		Use:   "agents",
		Short: "Manage connected agents",
		Long:  `Inspect the agents currently dialed in, send them control commands, and generate installers for new hosts.`,
	}

	agents.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List connected agents",
		Args:  cobra.NoArgs,
		Run:   runAgentsList,
	})

	agents.AddCommand(&cobra.Command{
		Use:   "command [name|machine-id] [command]",
		Short: "Send a control command to a connected agent",
		Long:  `Sends a control command over the agent's existing reverse tunnel and prints its reply.`,
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			machineID, err := resolveMachineID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			output, err := client.SendAgentCommand(ctx(), machineID, args[1])
			if err != nil {
				cliflags.Fatalf("sending command: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(map[string]string{"machine_id": machineID, "command": args[1], "output": output})
				return
			}
			cliflags.Print("%s", output)
		},
	})

	agents.AddCommand(&cobra.Command{
		Use:   "disconnect [name|machine-id]",
		Short: "Drop an agent's connection",
		Long:  `Closes the agent's reverse tunnel. A healthy agent dials back on its retry interval.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			machineID, err := resolveMachineID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			if err := client.DisconnectAgent(ctx(), machineID); err != nil {
				cliflags.Fatalf("disconnecting agent: %v", err)
			}
			cliflags.Print("✓ Agent %s disconnected", args[0])
		},
	})

	var (
		hostname       string
		port           int
		tags           []string
		targetOS       string
		gatewayHost    string
		gatewayPort    int
		packageBaseURL string
		agentVersion   string
		output         string
	)
	installScript := &cobra.Command{
		Use:   "install-script [name]",
		Short: "Generate an agent install script for a new host",
		Long: `Generates the one-shot installer that registers a machine and starts its agent.

The script embeds a freshly minted agent identity, so treat the output as a
credential: write it to a file with --output and copy it to the host over a
channel you trust.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			tagMap, err := cliflags.ParseKeyValues(tags)
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			resp, err := api().GenerateAgentInstallScript(ctx(), sdk.AgentInstallScriptRequest{
				Name:           args[0],
				Hostname:       hostname,
				Port:           port,
				Tags:           tagMap,
				OS:             targetOS,
				GatewayHost:    gatewayHost,
				GatewayPort:    gatewayPort,
				PackageBaseURL: packageBaseURL,
				Version:        agentVersion,
			})
			if err != nil {
				cliflags.Fatalf("generating install script: %v", err)
			}

			if flags.JSON {
				cliflags.MustPrintJSON(resp)
				return
			}
			if output == "" {
				cliflags.Print("%s", resp.Script)
				return
			}
			// 0700: the script carries the agent's private identity.
			if err := os.WriteFile(output, []byte(resp.Script), 0o700); err != nil {
				cliflags.Fatalf("writing %s: %v", output, err)
			}
			cliflags.Print("✓ Wrote installer for %s to %s (machine %s)", resp.AgentName, output, resp.MachineID)
		},
	}
	installScript.Flags().StringVar(&hostname, "hostname", "", "hostname or address of the target host")
	installScript.Flags().IntVar(&port, "port", 22, "SSH port on the target host")
	installScript.Flags().StringArrayVar(&tags, "tag", nil, "tag as key=value (repeatable)")
	installScript.Flags().StringVar(&targetOS, "os", "linux", "target operating system")
	installScript.Flags().StringVar(&gatewayHost, "gateway-host", "", "gateway host the agent dials out to (required)")
	installScript.Flags().IntVar(&gatewayPort, "gateway-port", 0, "gateway SSH port the agent dials out to")
	installScript.Flags().StringVar(&packageBaseURL, "package-base-url", defaultPackageBaseURL,
		"base URL the installer downloads agent packages from")
	installScript.Flags().StringVar(&agentVersion, "agent-version", "", "agent version to install (default: server's choice)")
	installScript.Flags().StringVarP(&output, "output", "o", "", "write the script to this file instead of stdout")
	_ = installScript.MarkFlagRequired("gateway-host")
	agents.AddCommand(installScript)

	return agents
}

func runAgentsList(cmd *cobra.Command, args []string) {
	client := api()

	connected, err := client.ListConnectedAgents(ctx())
	if err != nil {
		cliflags.Fatalf("listing agents: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(connected)
		return
	}
	if len(connected) == 0 {
		cliflags.Print("No agents connected.")
		return
	}

	// The endpoint returns machine IDs; resolve names so the output matches
	// what operators type elsewhere. A lookup failure is not fatal — the
	// connection list is still the useful answer.
	names := map[string]string{}
	if machines, merr := client.ListMachines(ctx()); merr == nil {
		for _, machine := range machines {
			names[machine.ID] = machine.Name
		}
	}

	table := cliflags.NewTable("MACHINE ID", "NAME")
	for _, machineID := range connected {
		table.Row(machineID, cliflags.OrDash(names[machineID]))
	}
	table.Flush()
}
