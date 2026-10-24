package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/orion-belt-dev/orion-belt/pkg/client"
	"github.com/orion-belt-dev/orion-belt/pkg/cliflags"
	"github.com/spf13/cobra"
)

// newTunnelCmd returns `osh rdp` or `osh vnc`: a local port relayed to that
// service on a machine through the gateway.
func newTunnelCmd(protocol, client string) *cobra.Command {
	var (
		listen   string
		request  bool
		reason   string
		duration int
	)
	name := strings.ToUpper(protocol)
	cmd := &cobra.Command{
		Use:   protocol + " machine",
		Short: "Open a local port to " + name + " on a machine",
		Long: fmt.Sprintf(`Listens on a local port and relays each connection to %[1]s on the machine
through the gateway, with no inbound port open on the target. Point your %[1]s
client at the address it prints; Ctrl-C closes the tunnel.

Needs a grant with access type %[2]q on the machine; with --request-access
osh asks for one, waits for approval, then opens the tunnel. The login itself
happens in your %[1]s client, against the target.`, name, protocol),
		Example: fmt.Sprintf(`  osh %[1]s win-01                 relay on a free local port
  osh %[1]s win-01 --listen 127.0.0.1:1%[2]d
  osh %[1]s win-01 -r --reason "patching" -d 1800
  ssh -N -L 1%[2]d:win-01:%[2]d alice@gateway   the same with plain OpenSSH`, protocol, tunnelPorts[protocol]),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if request && reason == "" {
				return fmt.Errorf("access reason is required (use --reason)")
			}
			return runTunnel(protocol, client, args[0], listen, request, reason, duration)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:0", "local address to listen on (port 0 picks a free one)")
	cmd.Flags().BoolVarP(&request, "request-access", "r", false, "request "+protocol+" access first and wait for approval")
	cmd.Flags().StringVar(&reason, "reason", "", "reason for the access request")
	cmd.Flags().IntVarP(&duration, "duration", "d", 3600, "requested access duration in seconds")
	return cmd
}

var tunnelPorts = map[string]int{"rdp": 3389, "vnc": 5900}

func runTunnel(protocol, viewer, machine, listen string, request bool, reason string, duration int) error {
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
		if err := sshClient.RequestAccessType(machine, protocol, reason, duration); err != nil {
			return fmt.Errorf("request access: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sshClient.Forward(ctx, user, machine, protocol, listen, func(addr net.Addr) {
		cliflags.Print("%s to %s on %s", strings.ToUpper(protocol), machine, addr)
		cliflags.Print("Connect your %s to %s. Ctrl-C to close.", viewer, addr)
	})
}
