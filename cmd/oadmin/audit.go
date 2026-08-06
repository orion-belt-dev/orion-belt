package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zrougamed/orion-belt/pkg/cliflags"
)

func newAuditCmd() *cobra.Command {
	audit := &cobra.Command{
		Use:   "audit",
		Short: "Read the audit log",
		Long:  `Inspect the tamper-evident record of who did what through the gateway.`,
	}

	var (
		limit  int
		action string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List audit log entries, newest first",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runAuditList(limit, action)
		},
	}
	list.Flags().IntVar(&limit, "limit", 50, "maximum number of entries to return")
	list.Flags().StringVar(&action, "action", "", "only entries whose action contains this substring")
	audit.AddCommand(list)

	return audit
}

func runAuditList(limit int, action string) {
	client := api()

	entries, err := client.ListAuditLogs(ctx(), limit)
	if err != nil {
		cliflags.Fatalf("listing audit logs: %v", err)
	}

	if action != "" {
		filtered := entries[:0]
		for _, entry := range entries {
			if strings.Contains(entry.Action, action) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}

	if flags.JSON {
		cliflags.MustPrintJSON(entries)
		return
	}
	if len(entries) == 0 {
		cliflags.Print("No audit entries.")
		return
	}

	names := newNameIndex(client, true, false)
	table := cliflags.NewTable("TIMESTAMP", "USER", "ACTION", "RESOURCE", "IP", "METADATA")
	for _, entry := range entries {
		table.Row(cliflags.FormatTime(entry.Timestamp), names.user(entry.UserID),
			entry.Action, cliflags.OrDash(entry.Resource), cliflags.OrDash(entry.IPAddress),
			cliflags.Truncate(formatMetadata(entry.Metadata), 40))
	}
	table.Flush()
}

// formatMetadata renders audit metadata as sorted k=v pairs, keeping repeated
// runs stable and the column narrow. Full values remain available via --json.
func formatMetadata(metadata map[string]interface{}) string {
	if len(metadata) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		value := metadata[key]
		switch v := value.(type) {
		case string:
			pairs = append(pairs, key+"="+v)
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				continue
			}
			pairs = append(pairs, key+"="+string(encoded))
		}
	}
	return strings.Join(pairs, " ")
}
