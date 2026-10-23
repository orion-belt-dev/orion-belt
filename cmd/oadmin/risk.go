package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newRiskPolicyCmd() *cobra.Command {
	risk := &cobra.Command{
		Use:   "risk-policy",
		Short: "Route access requests by risk",
		Long: `The risk policy routes each new access request using the signals recorded
with it (source IP, MFA, time, target sensitivity):

  escalate      any matching condition sends the request to an admin; an
                operator can no longer approve it
  auto_approve  when every condition holds the request is approved at once,
                with no reviewer; high-sensitivity targets never qualify

Escalation is checked first. Everything else waits for the usual review.`,
	}

	risk.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the risk policy",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			policy, err := api().GetRiskPolicy(ctx())
			if err != nil {
				cliflags.Fatalf("getting risk policy: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(policy)
				return
			}
			printRiskPolicy(policy)
		},
	})

	var file string
	set := &cobra.Command{
		Use:   "set",
		Short: "Replace the risk policy from a JSON file (admin only)",
		Long: `Replaces the whole policy with the JSON in --file ("-" reads stdin). Start
from "oadmin risk-policy show --json".`,
		Example: `  oadmin risk-policy set --file - <<'JSON'
  {
    "auto_approve": {"enabled": true, "max_sensitivity": "low", "max_duration_seconds": 3600,
                     "hours_utc": {"start": 8, "end": 18}, "source_cidrs": ["10.0.0.0/8"],
                     "require_mfa": true, "remote_users": ["deploy"]},
    "escalate": {"enabled": true, "sensitivities": ["high"], "without_mfa": true}
  }
  JSON`,
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			var data []byte
			var err error
			if file == "-" {
				data, err = io.ReadAll(os.Stdin)
			} else {
				data, err = os.ReadFile(file)
			}
			if err != nil {
				cliflags.Fatalf("reading policy: %v", err)
			}
			var policy sdk.RiskPolicy
			if err := json.Unmarshal(data, &policy); err != nil {
				cliflags.Fatalf("parsing policy: %v", err)
			}
			saved, err := api().PutRiskPolicy(ctx(), policy)
			if err != nil {
				cliflags.Fatalf("saving risk policy: %v", err)
			}
			cliflags.Print("✓ Risk policy saved\n")
			printRiskPolicy(saved)
		},
	}
	set.Flags().StringVarP(&file, "file", "f", "", "policy JSON file, or - for stdin")
	_ = set.MarkFlagRequired("file")
	risk.AddCommand(set)
	return risk
}

func printRiskPolicy(p *sdk.RiskPolicy) {
	a, e := p.AutoApprove, p.Escalate
	if !e.Enabled {
		cliflags.Print("Escalate:      off")
	} else {
		var when []string
		if len(e.Sensitivities) > 0 {
			when = append(when, "sensitivity "+strings.Join(e.Sensitivities, "/"))
		}
		if e.OutsideHoursUTC != nil {
			when = append(when, fmt.Sprintf("outside %02d-%02d UTC", e.OutsideHoursUTC.Start, e.OutsideHoursUTC.End))
		}
		if e.WithoutMFA {
			when = append(when, "no MFA")
		}
		if len(e.SourceOutsideCIDRs) > 0 {
			when = append(when, "source outside "+strings.Join(e.SourceOutsideCIDRs, ", "))
		}
		cliflags.Print("Escalate:      when %s", cliflags.OrDash(strings.Join(when, ", or ")))
	}
	if !a.Enabled {
		cliflags.Print("Auto-approve:  off")
	} else {
		cond := []string{"sensitivity up to " + a.MaxSensitivity, fmt.Sprintf("at most %ds", a.MaxDurationSeconds)}
		if a.HoursUTC != nil {
			cond = append(cond, fmt.Sprintf("within %02d-%02d UTC", a.HoursUTC.Start, a.HoursUTC.End))
		}
		if len(a.SourceCIDRs) > 0 {
			cond = append(cond, "from "+strings.Join(a.SourceCIDRs, ", "))
		}
		if a.RequireMFA {
			cond = append(cond, "MFA enrolled")
		}
		if len(a.RemoteUsers) > 0 {
			cond = append(cond, "accounts "+strings.Join(a.RemoteUsers, ", "))
		}
		cliflags.Print("Auto-approve:  when %s", strings.Join(cond, ", and "))
	}
	if !p.UpdatedAt.IsZero() {
		cliflags.Print("Updated:       %s", cliflags.FormatTime(p.UpdatedAt))
	}
}
