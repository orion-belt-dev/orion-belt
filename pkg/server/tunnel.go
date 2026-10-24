package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/ssh"
)

// directTCPIPData is the RFC 4254 §7.2 payload of a direct-tcpip channel.
type directTCPIPData struct {
	HostToConnect  string
	PortToConnect  uint32
	OriginatorIP   string
	OriginatorPort uint32
}

// handleClientTunnel relays one TCP connection from a client to a machine's
// RDP or VNC service through its agent. The client names the machine as the
// host and the protocol by its well-known port, which is what plain OpenSSH
// sends for `ssh -L 13389:web-01:3389 alice@gateway`.
func (s *Server) handleClientTunnel(newChannel ssh.NewChannel, remoteAddr net.Addr, userID, username string) {
	var req directTCPIPData
	if err := ssh.Unmarshal(newChannel.ExtraData(), &req); err != nil {
		newChannel.Reject(ssh.ConnectionFailed, "malformed direct-tcpip request")
		return
	}
	protocol := common.TunnelProtocolForPort(req.PortToConnect)
	if protocol == "" {
		newChannel.Reject(ssh.Prohibited, fmt.Sprintf("port %d is not relayed; use 3389 (RDP) or 5900 (VNC)", req.PortToConnect))
		return
	}

	ctx, span := tracing.Start(context.Background(), "gateway.tunnel",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	tracing.SetAttributes(span,
		attribute.String("orion.user", username),
		attribute.String("orion.user_id", userID),
		attribute.String("orion.target.machine", req.HostToConnect),
		attribute.String("orion.tunnel.protocol", protocol),
	)

	ip := ""
	if remoteAddr != nil {
		ip, _, _ = net.SplitHostPort(remoteAddr.String())
	}
	audit := func(action, resource string, metadata map[string]interface{}) {
		metadata["protocol"] = protocol
		metadata["machine"] = req.HostToConnect
		if err := s.store.CreateAuditLog(ctx, common.NewAuditLog(userID, action, resource, ip, metadata)); err != nil {
			s.logger.Warn("Failed to audit %s for %s: %v", action, username, err)
		}
	}

	machine, err := s.store.GetMachineByName(ctx, req.HostToConnect)
	if err != nil {
		tracing.RecordError(span, err)
		newChannel.Reject(ssh.ConnectionFailed, "machine not found: "+req.HostToConnect)
		return
	}
	resource := "machine:" + machine.ID
	tracing.SetAttributes(span, attribute.String("orion.target.machine_id", machine.ID))

	if err := s.authService.CheckTunnelPermission(ctx, userID, machine.ID, protocol); err != nil {
		s.logger.Warn("Permission denied: %s cannot open %s to %s", username, protocol, machine.Name)
		tracing.RecordError(span, err)
		audit("tunnel.denied", resource, map[string]interface{}{})
		newChannel.Reject(ssh.Prohibited, fmt.Sprintf("permission denied: no %s access to %s; request it with access_type %q", protocol, machine.Name, protocol))
		return
	}

	s.agentsMu.RLock()
	agentConn, connected := s.agents[machine.ID]
	s.agentsMu.RUnlock()
	if !connected || !machine.IsActive {
		newChannel.Reject(ssh.ConnectionFailed, "machine is not available")
		return
	}

	agentChannel, agentReqs, err := agentConn.SSHConn.Conn.OpenChannel(common.TunnelChannelType,
		ssh.Marshal(&common.TunnelChannelData{Protocol: protocol}))
	if err != nil {
		tracing.RecordError(span, err)
		msg := fmt.Sprintf("agent could not open %s: %v", protocol, err)
		var openErr *ssh.OpenChannelError
		if errors.As(err, &openErr) && openErr.Reason == ssh.UnknownChannelType {
			msg = fmt.Sprintf("the agent on %s does not support %s; upgrade it", machine.Name, protocol)
		}
		newChannel.Reject(ssh.ConnectionFailed, msg)
		return
	}
	go ssh.DiscardRequests(agentReqs)

	clientChannel, clientReqs, err := newChannel.Accept()
	if err != nil {
		agentChannel.Close()
		return
	}
	go ssh.DiscardRequests(clientReqs)

	s.logger.Info("Tunnel opened: %s %s to %s", username, protocol, machine.Name)
	audit("tunnel.open", resource, map[string]interface{}{})
	started := time.Now()

	sent, received := relayChannels(clientChannel, agentChannel)

	audit("tunnel.close", resource, map[string]interface{}{
		"bytes_sent":       sent,
		"bytes_received":   received,
		"duration_seconds": int(time.Since(started).Seconds()),
	})
	s.logger.Info("Tunnel closed: %s %s to %s (%d bytes up, %d down)", username, protocol, machine.Name, sent, received)
}

// relayChannels copies a and b into each other until both directions end,
// passing each EOF on, then closes both. It returns the bytes copied a->b and
// b->a.
func relayChannels(a, b ssh.Channel) (aToB, bToA int64) {
	var up, down atomic.Int64
	var wg sync.WaitGroup
	copyHalf := func(dst, src ssh.Channel, n *atomic.Int64) {
		defer wg.Done()
		copied, _ := io.Copy(dst, src)
		n.Store(copied)
		dst.CloseWrite()
	}
	wg.Add(2)
	go copyHalf(b, a, &up)
	go copyHalf(a, b, &down)
	wg.Wait()
	a.Close()
	b.Close()
	return up.Load(), down.Load()
}
