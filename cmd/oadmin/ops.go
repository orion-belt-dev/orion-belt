package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

func newReportsCmd() *cobra.Command {
	reports := &cobra.Command{
		Use:   "reports",
		Short: "Export compliance reports",
		Long:  `Export the same reports the console offers, for archiving or for an auditor.`,
	}

	var (
		format string
		output string
		from   string
		to     string
		asOf   string
	)
	export := &cobra.Command{
		Use:   "export [report-name]",
		Short: "Export a report",
		Long: `Exports a named report: "sessions", "audit", or one of the compliance reports
(admin, operator, or auditor only):

  soc2-access              sessions and access requests in --from/--to, with
                           reason and approver
  iso27001-access-review   grants in force --as-of a point in time, standing
                           vs time-boxed
  pci-admin-activity       sessions in --from/--to on machines tagged
                           pci: in-scope

Dates are RFC3339 or YYYY-MM-DD; the range defaults to the last 30 days. The
server decides which reports exist; an unknown name comes back as an error
listing what is available.`,
		Example: `  oadmin reports export soc2-access --from 2026-07-01 --to 2026-09-30 -o q3.csv
  oadmin reports export iso27001-access-review --as-of 2026-09-30 --format pdf -o review.pdf`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			params := url.Values{}
			for key, v := range map[string]string{"from": from, "to": to, "as_of": asOf} {
				if v != "" {
					params.Set(key, v)
				}
			}
			data, err := api().ExportReportWithParams(ctx(), args[0], format, params)
			if err != nil {
				cliflags.Fatalf("exporting report: %v", err)
			}
			if output == "" {
				if _, err := os.Stdout.Write(data); err != nil {
					cliflags.Fatalf("writing report: %v", err)
				}
				return
			}
			if err := os.WriteFile(output, data, 0o600); err != nil {
				cliflags.Fatalf("writing %s: %v", output, err)
			}
			cliflags.Print("✓ Wrote %d bytes to %s", len(data), output)
		},
	}
	export.Flags().StringVar(&format, "format", "csv", "export format (csv | pdf | json)")
	export.Flags().StringVar(&from, "from", "", "start of the range (RFC3339 or YYYY-MM-DD)")
	export.Flags().StringVar(&to, "to", "", "end of the range (RFC3339 or YYYY-MM-DD, a bare date covers the whole day)")
	export.Flags().StringVar(&asOf, "as-of", "", "point in time for iso27001-access-review (RFC3339 or YYYY-MM-DD)")
	export.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")
	reports.AddCommand(export)

	return reports
}

func newUsageCmd() *cobra.Command {
	var (
		windowHours int
		top         int
	)
	usage := &cobra.Command{
		Use:   "usage",
		Short: "Show access volume and approval latency",
		Long:  `Prints the usage dashboard: session and request volume, approval latency, and the most-visited machines.`,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runUsage(windowHours, top)
		},
	}
	usage.Flags().IntVar(&windowHours, "window", 24, "reporting window in hours")
	usage.Flags().IntVar(&top, "top", 5, "number of top target machines to list")
	return usage
}

func runUsage(windowHours, top int) {
	dashboard, err := api().GetUsageDashboard(ctx(), windowHours, top)
	if err != nil {
		cliflags.Fatalf("getting usage: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(dashboard)
		return
	}

	cliflags.Print("Window: %dh (%s → %s)\n", dashboard.WindowHours,
		cliflags.FormatTime(dashboard.From), cliflags.FormatTime(dashboard.To))

	cliflags.Print("Sessions:  %d total, %d active",
		dashboard.AccessVolume.SessionsTotal, dashboard.AccessVolume.SessionsActive)
	cliflags.Print("Requests:  %d total — %d pending, %d approved, %d rejected",
		dashboard.AccessVolume.RequestsTotal, dashboard.AccessVolume.RequestsPending,
		dashboard.AccessVolume.RequestsApproved, dashboard.AccessVolume.RequestsRejected)

	if dashboard.ApprovalLatency.SampleSize > 0 {
		cliflags.Print("Approval latency (n=%d): avg %s, p50 %s, p95 %s",
			dashboard.ApprovalLatency.SampleSize,
			cliflags.FormatDuration(time.Duration(dashboard.ApprovalLatency.AverageSeconds)*time.Second),
			cliflags.FormatDuration(time.Duration(dashboard.ApprovalLatency.P50Seconds)*time.Second),
			cliflags.FormatDuration(time.Duration(dashboard.ApprovalLatency.P95Seconds)*time.Second))
	}

	if len(dashboard.TopTargets) > 0 {
		cliflags.Print("\nTop targets:")
		table := cliflags.NewTable("MACHINE", "SESSIONS")
		for _, target := range dashboard.TopTargets {
			name := target.MachineName
			if name == "" {
				name = cliflags.Short(target.MachineID)
			}
			table.Row(name, target.SessionCount)
		}
		table.Flush()
	}
}

func newSetupCmd() *cobra.Command {
	setup := &cobra.Command{
		Use:   "setup",
		Short: "Check first-run setup progress",
		Long:  `Report which onboarding steps a fresh deployment still has outstanding.`,
	}

	setup.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show the setup checklist",
		Args:  cobra.NoArgs,
		Run:   runSetupStatus,
	})

	return setup
}

func runSetupStatus(cmd *cobra.Command, args []string) {
	status, err := api().GetSetupStatus(ctx())
	if err != nil {
		cliflags.Fatalf("getting setup status: %v", err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(status)
		return
	}

	if status.Complete {
		cliflags.Print("Setup complete.")
	} else {
		cliflags.Print("Setup incomplete.")
	}

	table := cliflags.NewTable("STEP", "DONE", "COUNT")
	table.Row("admin account", cliflags.YesNo(status.Steps.AdminExists), status.Counts.Admins)
	table.Row("users", cliflags.YesNo(status.Steps.HasUsers), status.Counts.Users)
	table.Row("machines", cliflags.YesNo(status.Steps.HasMachines), status.Counts.Machines)
	table.Row("connected agents", cliflags.YesNo(status.Steps.HasConnectedAgents), status.Counts.ConnectedAgents)
	table.Row("permissions", cliflags.YesNo(status.Steps.HasPermissions), status.Counts.Permissions)
	table.Flush()

	if status.Next != "" {
		cliflags.Print("\nNext: %s", status.Next)
	}
}

func newGatewayInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gateway-info",
		Short: "Show the addresses this gateway advertises",
		Long: `Prints the URLs and ports the gateway hands out to clients and agents.

These are the reachable addresses, not the bind addresses — they are what to
put in a client config, and what "agents install-script --gateway-host" should
point at. It needs no authentication, so it also works before you have
credentials.`,
		Args: cobra.NoArgs,
		Run:  runGatewayInfo,
	}
}

func runGatewayInfo(cmd *cobra.Command, args []string) {
	cfg, err := flags.LoadConfig()
	if err != nil {
		cliflags.Fatalf("%v", err)
	}
	endpoint := cliflags.APIEndpointFor(cfg)

	client, err := sdk.NewClient(endpoint, sdk.WithTimeout(flags.Timeout))
	if err != nil {
		cliflags.Fatalf("%v", err)
	}

	info, err := client.GetGatewayInfo(context.Background())
	if err != nil {
		cliflags.Fatalf("reading gateway info from %s: %v", endpoint, err)
	}

	if flags.JSON {
		cliflags.MustPrintJSON(info)
		return
	}

	cliflags.Print("Public URL: %s", cliflags.OrDash(info.PublicURL))
	cliflags.Print("Console:    %s", cliflags.OrDash(info.UIURL))
	cliflags.Print("SSH:        %s:%d", cliflags.OrDash(info.SSHHost), info.SSHPort)
	cliflags.Print("API port:   %d", info.APIPort)
	if info.PublicSSHHost != "" || info.PublicSSHPort != 0 {
		cliflags.Print("Configured public SSH override: %s:%d",
			cliflags.OrDash(info.PublicSSHHost), info.PublicSSHPort)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show CLI and gateway versions",
		Long: `Prints this binary's version and the version the configured gateway reports.

The server version is read without authenticating, so this also works as a
reachability check for a new config.`,
		Args: cobra.NoArgs,
		Run:  runVersion,
	}
}

func runVersion(cmd *cobra.Command, args []string) {
	info := map[string]string{"cli": version.String()}

	cfg, err := flags.LoadConfig()
	if err != nil {
		cliflags.Fatalf("%v", err)
	}
	endpoint := cliflags.APIEndpointFor(cfg)

	client, err := sdk.NewClient(endpoint, sdk.WithTimeout(flags.Timeout))
	if err != nil {
		cliflags.Fatalf("%v", err)
	}

	var serverVersion struct {
		Version string `json:"version"`
		Commit  string `json:"commit,omitempty"`
	}
	serverErr := client.DoPublic(context.Background(), http.MethodGet, "/api/v1/version", nil, &serverVersion)
	if serverErr == nil {
		info["server"] = serverVersion.Version
		if serverVersion.Commit != "" {
			info["server_commit"] = serverVersion.Commit
		}
	}
	info["endpoint"] = endpoint

	if flags.JSON {
		cliflags.MustPrintJSON(info)
		if serverErr != nil {
			cliflags.Fatalf("reading server version from %s: %v", endpoint, serverErr)
		}
		return
	}

	cliflags.Print("oadmin:  %s", info["cli"])
	cliflags.Print("gateway: %s", endpoint)
	if serverErr != nil {
		cliflags.Fatalf("reading server version: %v", serverErr)
	}
	cliflags.Print("server:  %s", info["server"])
	if commit := info["server_commit"]; commit != "" {
		cliflags.Print("commit:  %s", commit)
	}
}
