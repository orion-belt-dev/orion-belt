package main

import (
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/sdk"
	"github.com/spf13/cobra"
)

func newKubeReconcileCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "kube-reconcile [name|machine-id]",
		Short: "Check or fix a cluster's Kubernetes bindings",
		Long: `Compares the RoleBindings and ClusterRoleBindings Orion Belt issued in a
cluster with the cluster's active k8s grants, through its in-cluster agent.

Without --apply it only reports: bindings to create, to update, and to delete.
A binding listed for deletion is expired, revoked, or orphaned (no grant at
all). With --apply the changes are made and audited; the gateway also does
this on its own every 30 seconds.`,
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			client := api()
			id, err := resolveMachineID(client, args[0])
			if err != nil {
				cliflags.Fatalf("%v", err)
			}
			res, err := client.KubeReconcile(ctx(), id, !apply)
			if err != nil {
				cliflags.Fatalf("reconcile: %v", err)
			}
			if flags.JSON {
				cliflags.MustPrintJSON(res)
				return
			}
			if res.InSync {
				cliflags.Print("✓ %s: bindings match the k8s grants", args[0])
				return
			}
			verb := map[bool]string{true: "would ", false: ""}[res.DryRun]
			t := cliflags.NewTable("ACTION", "KIND", "NAMESPACE", "NAME", "ROLE", "USER", "EXPIRES")
			for _, row := range []struct {
				action string
				list   []kubeRow
			}{
				{verb + "create", toRows(res.Plan.Create)},
				{verb + "update", toRows(res.Plan.Update)},
				{verb + "delete", toRows(res.Plan.Delete)},
			} {
				for _, b := range row.list {
					t.Row(row.action, b.kind, b.namespace, b.name, b.role, b.user, b.expires)
				}
			}
			t.Flush()
			if res.DryRun {
				cliflags.Print("Run with --apply to make these changes.")
			}
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "make the changes instead of only reporting them")
	return cmd
}

type kubeRow struct{ kind, namespace, name, role, user, expires string }

func toRows(bs []sdk.KubeBinding) []kubeRow {
	rows := make([]kubeRow, 0, len(bs))
	for _, b := range bs {
		r := kubeRow{kind: "RoleBinding", namespace: b.Namespace, name: b.Name, role: b.ClusterRole, user: b.User, expires: "never"}
		if b.Namespace == "" {
			r.kind, r.namespace = "ClusterRoleBinding", "*"
		}
		if b.ExpiresAt != nil {
			r.expires = b.ExpiresAt.Local().Format("2006-01-02 15:04")
		}
		rows = append(rows, r)
	}
	return rows
}
