package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/orion-belt-dev/orion-belt/pkg/client"
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
	"github.com/spf13/cobra"
)

func newKubeCmd() *cobra.Command {
	var (
		listen     string
		kubeconfig string
		namespace  string
		request    bool
		scopes     []string
		reason     string
		duration   int
	)
	cmd := &cobra.Command{
		Use:   "kube cluster",
		Short: "Open kubectl access to a Kubernetes cluster",
		Long: `Relays kubectl to a cluster's API server through the gateway and the
cluster's in-cluster agent, and writes a kubeconfig pointing at the relay.
Run kubectl with KUBECONFIG set to the printed path while osh kube runs;
Ctrl-C closes it.

kubectl acts as orion:<your gateway username>. What it may do is what your
k8s grants issued: each namespace/role scope is a RoleBinding to that
ClusterRole in the namespace, or a ClusterRoleBinding for */role, removed
when the grant ends.

With --request-access, osh first asks for the --scope values (repeatable)
and waits for approval.`,
		Example: `  osh kube prod-eu
  osh kube prod-eu -r --scope payments/edit --reason "INC-42" -d 1800
  osh kube prod-eu -r --scope '*/view' --reason "audit"
  KUBECONFIG=~/.orion-belt/kube/prod-eu.yaml kubectl get pods`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster := args[0]
			if request {
				if reason == "" {
					return fmt.Errorf("access reason is required (use --reason)")
				}
				if err := kube.ValidateScopes(scopes); err != nil {
					return err
				}
			}
			if kubeconfig == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				kubeconfig = filepath.Join(home, ".orion-belt", "kube", cluster+".yaml")
			}
			if namespace == "" && len(scopes) > 0 {
				if s, err := kube.ParseScope(scopes[0]); err == nil {
					namespace = s.Namespace
				}
			}
			return runKube(cluster, listen, kubeconfig, namespace, request, scopes, reason, duration)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:0", "local address for the relay (port 0 picks a free one)")
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig file to write (default ~/.orion-belt/kube/<cluster>.yaml)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "default namespace in the kubeconfig (default: the first scope's)")
	cmd.Flags().BoolVarP(&request, "request-access", "r", false, "request k8s access for --scope first and wait for approval")
	cmd.Flags().StringArrayVar(&scopes, "scope", nil, "namespace/role to request, or */role cluster-wide (repeatable)")
	cmd.Flags().StringVar(&reason, "reason", "", "reason for the access request")
	cmd.Flags().IntVarP(&duration, "duration", "d", 3600, "requested access duration in seconds")
	return cmd
}

func runKube(cluster, listen, kubeconfig, namespace string, request bool, scopes []string, reason string, duration int) error {
	logger := flags.Logger()
	config, err := flags.LoadConfig()
	if err != nil {
		return err
	}
	user, err := flags.Username(config)
	if err != nil {
		return err
	}
	sshClient, err := client.NewSSHClient(config, logger)
	if err != nil {
		return err
	}
	if request {
		if err := sshClient.RequestKubeAccess(cluster, scopes, reason, duration); err != nil {
			return fmt.Errorf("request access: %w", err)
		}
	}

	// Network drops are survived, not fatal: say so while it happens.
	sshClient.Status = func(msg string) { cliflags.Print("%s", msg) }
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var writeErr error
	err = sshClient.ForwardKube(ctx, user, cluster, listen, func(addr net.Addr) {
		if writeErr = writeKubeconfig(kubeconfig, cluster, "http://"+addr.String(), namespace); writeErr != nil {
			stop()
			return
		}
		cliflags.Print("Kubernetes API of %s relayed on %s", cluster, addr)
		cliflags.Print("export KUBECONFIG=%s", kubeconfig)
		cliflags.Print("Ctrl-C to close.")
	})
	if writeErr != nil {
		return writeErr
	}
	return err
}

// writeKubeconfig writes a kubeconfig for the relay. It holds no
// credentials: the relay listens on loopback and the gateway authenticates
// the SSH connection behind it.
func writeKubeconfig(path, cluster, server, namespace string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	name := "orion-" + cluster
	ns := ""
	if namespace != "" {
		ns = fmt.Sprintf("\n    namespace: %q", namespace)
	}
	body := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: %[1]q
  cluster:
    server: %[2]q
users:
- name: orion
  user: {}
contexts:
- name: %[1]q
  context:
    cluster: %[1]q
    user: orion%[3]s
current-context: %[1]q
`, name, server, ns)
	return os.WriteFile(path, []byte(body), 0o600)
}
