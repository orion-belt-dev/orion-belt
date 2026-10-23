package main

import (
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

func newExplainCmd() *cobra.Command {
	var (
		accessType string
		user       string
	)
	explain := &cobra.Command{
		Use:   "explain [user@]machine",
		Short: "Explain why you can or cannot reach a machine",
		Long: `Shows the access decision for a remote account on a machine and the
relationships behind it: your role, each grant you hold there (and the
access request it came from), and any pending or rejected requests.

The remote account is the user@ part of the target, root when omitted.
Admins, operators and auditors can explain another user's access with --for.`,
		Example: `  osh explain root@web-01
  osh explain web-01 --for alice --access-type scp`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			remote, machine := "root", args[0]
			if i := strings.LastIndexByte(machine, '@'); i >= 0 {
				remote, machine = machine[:i], machine[i+1:]
			}
			runExplain(machine, remote, accessType, user)
		},
	}
	explain.Flags().StringVar(&accessType, "access-type", "ssh", "ssh or scp")
	explain.Flags().StringVar(&user, "for", "", "explain another user's access, by name or ID (admin, operator or auditor only)")
	return explain
}

func runExplain(machine, remote, accessType, user string) {
	ex, err := api().ExplainAccess(ctx(), machine, remote, accessType, user)
	if err != nil {
		cliflags.Fatalf("explaining access: %v", err)
	}
	if flags.JSON {
		cliflags.MustPrintJSON(ex)
		return
	}

	cliflags.Print("%s\n", ex.Summary)
	table := cliflags.NewTable("", "RELATION", "REF", "DETAIL", "REASON")
	for _, link := range ex.Chain {
		mark := " "
		if link.Holds {
			mark = "✓"
		}
		table.Row(mark, link.Relation, cliflags.OrDash(link.Ref), link.Detail, cliflags.OrDash(cliflags.Truncate(link.Reason, 40)))
	}
	table.Flush()
}
