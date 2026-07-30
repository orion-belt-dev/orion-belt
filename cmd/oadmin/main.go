package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/client"
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/version"
	"github.com/spf13/cobra"
)

var (
	flags           cliflags.Common
	caListCertsType string
	caRevokeReason  string
)

var rootCmd = &cobra.Command{
	Use:     "oadmin",
	Short:   "Orion-Belt Admin CLI",
	Long:    `oadmin is the Orion-Belt admin tool for managing access requests and system operations.`,
	Version: version.String(),
}

var requestsCmd = &cobra.Command{
	Use:   "requests",
	Short: "Manage access requests",
	Long:  `List, approve, and reject access requests.`,
}

var listRequestsCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending access requests",
	Run:   runListRequests,
}

var approveCmd = &cobra.Command{
	Use:   "approve [request-id]",
	Short: "Approve an access request",
	Args:  cobra.ExactArgs(1),
	Run:   runApprove,
}

var rejectCmd = &cobra.Command{
	Use:   "reject [request-id]",
	Short: "Reject an access request",
	Args:  cobra.ExactArgs(1),
	Run:   runReject,
}

var caCmd = &cobra.Command{
	Use:   "ca",
	Short: "Manage the SSH Certificate Authority",
	Long:  `Export CA trust material and manage the lifecycle of issued SSH certificates.`,
}

var caExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export CA public keys for out-of-band trust distribution",
	Long:  `Prints the User CA and Host CA public keys (authorized_keys format) an operator distributes to clients/agents as auth.host_ca_public_key.`,
	Run:   runCAExport,
}

var caListCertsCmd = &cobra.Command{
	Use:   "list-certs",
	Short: "List issued SSH certificates",
	Run:   runCAListCerts,
}

var caRevokeCmd = &cobra.Command{
	Use:   "revoke [serial]",
	Short: "Revoke an issued SSH certificate ahead of its TTL expiry",
	Args:  cobra.ExactArgs(1),
	Run:   runCARevoke,
}

func init() {
	flags.BindPersistent(rootCmd)

	requestsCmd.AddCommand(listRequestsCmd)
	requestsCmd.AddCommand(approveCmd)
	requestsCmd.AddCommand(rejectCmd)
	rootCmd.AddCommand(requestsCmd)

	caListCertsCmd.Flags().StringVar(&caListCertsType, "type", "", "filter by certificate type (user|host)")
	caRevokeCmd.Flags().StringVar(&caRevokeReason, "reason", "", "reason for revocation (recorded in the audit log)")
	caCmd.AddCommand(caExportCmd)
	caCmd.AddCommand(caListCertsCmd)
	caCmd.AddCommand(caRevokeCmd)
	rootCmd.AddCommand(caCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getAPIClient() (*client.APIClient, error) {
	logger := flags.Logger()
	config, err := flags.LoadConfig()
	if err != nil {
		return nil, err
	}
	user, err := flags.Username(config)
	if err != nil {
		return nil, err
	}
	return client.LoadAPIClient(config, user, logger)
}

func runListRequests(cmd *cobra.Command, args []string) {
	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	requests, err := apiClient.ListPendingAccessRequests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing requests: %v\n", err)
		os.Exit(1)
	}

	if len(requests) == 0 {
		fmt.Println("No pending access requests.")
		return
	}

	fmt.Printf("Pending Access Requests (%d):\n\n", len(requests))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REQUEST ID\tUSER\tMACHINE\tREMOTE USER\tREASON\tDURATION\tREQUESTED")
	fmt.Fprintln(w, "----------\t----\t-------\t-----------\t------\t--------\t---------")

	for _, req := range requests {
		duration := formatDuration(time.Duration(req.Duration) * time.Second)
		requestedAgo := formatDuration(time.Since(req.RequestedAt))
		remoteUser := "default"
		if len(req.RemoteUsers) > 0 {
			remoteUser = req.RemoteUsers[0]
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			req.ID,
			req.UserID[:8]+"...",
			req.MachineID[:8]+"...",
			remoteUser,
			truncate(req.Reason, 20),
			duration,
			requestedAgo,
		)
	}

	w.Flush()

	fmt.Println("\nUse 'oadmin requests approve <request-id>' to approve")
	fmt.Println("Use 'oadmin requests reject <request-id>' to reject")
}

func runApprove(cmd *cobra.Command, args []string) {
	requestID := args[0]

	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Authenticated admin identity is preferred by the API when reviewer_id is empty.
	reviewerID := ""
	if err := apiClient.ApproveAccessRequest(requestID, reviewerID); err != nil {
		fmt.Fprintf(os.Stderr, "Error approving request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Access request %s approved\n", requestID[:8]+"...")
}

func runReject(cmd *cobra.Command, args []string) {
	requestID := args[0]

	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	reviewerID := ""
	if err := apiClient.RejectAccessRequest(requestID, reviewerID); err != nil {
		fmt.Fprintf(os.Stderr, "Error rejecting request: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✗ Access request %s rejected\n", requestID[:8]+"...")
}

func runCAExport(cmd *cobra.Command, args []string) {
	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ca, err := apiClient.ExportCA()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error exporting CA: %v\n", err)
		os.Exit(1)
	}
	if !ca.Enabled {
		fmt.Println("SSH Certificate Authority is not enabled on this server.")
		return
	}

	fmt.Println("# User CA public key (not needed by clients; informational)")
	fmt.Print(ca.UserCA)
	fmt.Println("\n# Host CA public key — add to client/agent config as auth.host_ca_public_key")
	fmt.Print(ca.HostCA)
}

func runCAListCerts(cmd *cobra.Command, args []string) {
	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	certs, err := apiClient.ListSSHCertificates(caListCertsType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing certificates: %v\n", err)
		os.Exit(1)
	}

	if len(certs) == 0 {
		fmt.Println("No issued certificates.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERIAL\tTYPE\tKEY ID\tISSUED\tEXPIRES\tSTATUS")
	fmt.Fprintln(w, "------\t----\t------\t------\t-------\t------")
	for _, c := range certs {
		status := "active"
		if c.RevokedAt != nil {
			status = "revoked"
		} else if c.ExpiresAt.Before(time.Now()) {
			status = "expired"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Serial, c.CertType, c.KeyID,
			c.IssuedAt.Format(time.RFC3339), c.ExpiresAt.Format(time.RFC3339), status)
	}
	w.Flush()
}

func runCARevoke(cmd *cobra.Command, args []string) {
	serial := args[0]

	apiClient, err := getAPIClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := apiClient.RevokeSSHCertificate(serial, caRevokeReason); err != nil {
		fmt.Fprintf(os.Stderr, "Error revoking certificate: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Certificate %s revoked\n", serial)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	} else if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	} else {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
