package main

import (
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

func newCACmd() *cobra.Command {
	ca := &cobra.Command{
		Use:   "ca",
		Short: "Manage the SSH Certificate Authority",
		Long:  `Export CA trust material and manage the lifecycle of issued SSH certificates.`,
	}

	ca.AddCommand(&cobra.Command{
		Use:   "export",
		Short: "Export CA public keys for out-of-band trust distribution",
		Long:  `Prints the User CA and Host CA public keys (authorized_keys format) an operator distributes to clients/agents as auth.host_ca_public_key.`,
		Args:  cobra.NoArgs,
		Run:   runCAExport,
	})

	var certType string
	listCerts := &cobra.Command{
		Use:   "list-certs",
		Short: "List issued SSH certificates",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			runCAListCerts(certType)
		},
	}
	listCerts.Flags().StringVar(&certType, "type", "", "filter by certificate type (user|host)")
	ca.AddCommand(listCerts)

	var reason string
	revoke := &cobra.Command{
		Use:   "revoke [serial]",
		Short: "Revoke an issued SSH certificate ahead of its TTL expiry",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runCARevoke(args[0], reason)
		},
	}
	revoke.Flags().StringVar(&reason, "reason", "", "reason for revocation (recorded in the audit log)")
	ca.AddCommand(revoke)

	return ca
}

func runCAExport(cmd *cobra.Command, args []string) {
	ca, err := api().ExportCA(ctx())
	if err != nil {
		cliflags.Fatalf("exporting CA: %v", err)
	}

	if flags.JSON {
		if err := cliflags.PrintJSON(ca); err != nil {
			cliflags.Fatalf("%v", err)
		}
		return
	}

	if !ca.Enabled {
		cliflags.Print("SSH Certificate Authority is not enabled on this server.")
		return
	}

	cliflags.Print("# User CA public key (not needed by clients; informational)")
	cliflags.Print("%s", ca.UserCA)
	cliflags.Print("# Host CA public key — add to client/agent config as auth.host_ca_public_key")
	cliflags.Print("%s", ca.HostCA)
}

func runCAListCerts(certType string) {
	certs, err := api().ListSSHCertificates(ctx(), certType)
	if err != nil {
		cliflags.Fatalf("listing certificates: %v", err)
	}

	if flags.JSON {
		if err := cliflags.PrintJSON(certs); err != nil {
			cliflags.Fatalf("%v", err)
		}
		return
	}

	if len(certs) == 0 {
		cliflags.Print("No issued certificates.")
		return
	}

	table := cliflags.NewTable("SERIAL", "TYPE", "KEY ID", "ISSUED", "EXPIRES", "STATUS")
	for _, cert := range certs {
		status := "active"
		switch {
		case cert.RevokedAt != nil:
			status = "revoked"
		case cert.ExpiresAt.Before(time.Now()):
			status = "expired"
		}
		table.Row(cert.Serial, cert.CertType, cert.KeyID,
			cliflags.FormatTime(cert.IssuedAt), cliflags.FormatTime(cert.ExpiresAt), status)
	}
	table.Flush()
}

func runCARevoke(serial, reason string) {
	if err := api().RevokeSSHCertificate(ctx(), serial, reason); err != nil {
		cliflags.Fatalf("revoking certificate: %v", err)
	}
	cliflags.Print("✓ Certificate %s revoked", serial)
}
