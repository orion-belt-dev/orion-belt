package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

// runAgentControl sends an orion:* control command to one agent, prints its
// reply, and fails when the agent reports ok: false.
func runAgentControl(ref, command string) map[string]any {
	client := api()
	machineID, err := resolveMachineID(client, ref)
	if err != nil {
		cliflags.Fatalf("%v", err)
	}
	out, err := client.SendAgentCommand(ctx(), machineID, command)
	if err != nil {
		cliflags.Fatalf("sending %s: %v", strings.Fields(command)[0], err)
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &reply); err != nil {
		cliflags.Fatalf("agent replied %q", out)
	}
	if flags.JSON {
		cliflags.MustPrintJSON(reply)
	}
	if reply["ok"] != true {
		cliflags.Fatalf("%s: %v", ref, reply["error"])
	}
	return reply
}

func joinList(v any) string {
	items, _ := v.([]any)
	parts := make([]string, 0, len(items))
	for _, i := range items {
		parts = append(parts, fmt.Sprint(i))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func newAgentManageCmds() []*cobra.Command {
	reload := &cobra.Command{
		Use:   "reload [name|machine-id]",
		Short: "Re-read an agent's config file without restarting it",
		Long: `Applies tags, tunnel addresses, Kubernetes settings and remote_update from the
agent's config file while it stays connected. Changes to the gateway
address, credentials, agent name or enabling Kubernetes need a restart and
are listed instead. An invalid file is refused and the running config kept.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			r := runAgentControl(args[0], "orion:reload")
			if !flags.JSON {
				cliflags.Print("✓ %s reloaded. Applied: %s. Needs a restart: %s.", args[0], joinList(r["applied"]), joinList(r["restart_required"]))
			}
		},
	}

	var timeout int
	drain := &cobra.Command{
		Use:   "drain [name|machine-id]",
		Short: "Stop new sessions on an agent and let running ones finish",
		Long: `New shells, commands, tunnels and kubectl sessions are refused; running ones
continue until they end or --timeout passes, when they are closed. Control
commands and Kubernetes binding management keep working. End with undrain.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			r := runAgentControl(args[0], "orion:drain "+strconv.Itoa(timeout))
			if !flags.JSON {
				cliflags.Print("✓ %s draining: %v sessions running, closed after %ds if still open.", args[0], r["active"], timeout)
			}
		},
	}
	drain.Flags().IntVar(&timeout, "timeout", 600, "seconds to let running sessions finish before closing them")

	undrain := &cobra.Command{
		Use:   "undrain [name|machine-id]",
		Short: "Let a drained agent take new sessions again",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runAgentControl(args[0], "orion:undrain")
			if !flags.JSON {
				cliflags.Print("✓ %s takes new sessions again", args[0])
			}
		},
	}

	var url, sum string
	var updateTimeout int
	update := &cobra.Command{
		Use:   "update [name|machine-id]",
		Short: "Install a new agent binary and restart the agent",
		Long: `The agent downloads --url (https; a binary or a release .tar.gz), checks it
against --sha256 before changing anything, then drains, swaps its binary
(keeping the old one as <binary>.old) and restarts. Take the checksum from the
release's checksums file. Agents with agent.remote_update: false refuse.`,
		Example: `  oadmin agents update web-01 \
    --url https://github.com/orion-belt-dev/orion-belt/releases/download/v1.5.0/orion-belt-agent_1.5.0_linux_amd64.tar.gz \
    --sha256 <from checksums.txt>`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			r := runAgentControl(args[0], fmt.Sprintf("orion:update %s %s %d", url, sum, updateTimeout))
			if !flags.JSON {
				cliflags.Print("✓ %s: %v", args[0], r["message"])
			}
		},
	}
	update.Flags().StringVar(&url, "url", "", "https URL of the agent binary or release archive")
	update.Flags().StringVar(&sum, "sha256", "", "expected SHA-256 of the download")
	update.Flags().IntVar(&updateTimeout, "drain-timeout", 600, "seconds to let running sessions finish before restarting")
	_ = update.MarkFlagRequired("url")
	_ = update.MarkFlagRequired("sha256")

	return []*cobra.Command{reload, drain, undrain, update}
}
