package agent

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	gossh "golang.org/x/crypto/ssh"
)

// tunnelDialTimeout bounds how long the agent waits for the local RDP or VNC
// service to accept before refusing the channel.
var tunnelDialTimeout = 10 * time.Second

// handleTunnel relays one gateway tunnel channel to the address this agent's
// config gives its protocol. The gateway has already checked the user's
// grant; the agent only decides where, never whether.
func (a *Agent) handleTunnel(newChannel gossh.NewChannel) {
	var data common.TunnelChannelData
	if err := gossh.Unmarshal(newChannel.ExtraData(), &data); err != nil {
		newChannel.Reject(gossh.ConnectionFailed, "malformed tunnel request")
		return
	}
	addr := a.config.Agent.TunnelAddress(data.Protocol)
	if addr == "" {
		a.logger.Warn("Refusing %q tunnel: not enabled on this agent", data.Protocol)
		newChannel.Reject(gossh.Prohibited, data.Protocol+" is not enabled on this agent")
		return
	}

	conn, err := net.DialTimeout("tcp", addr, tunnelDialTimeout)
	if err != nil {
		a.logger.Warn("Tunnel %s to %s failed: %v", data.Protocol, addr, err)
		newChannel.Reject(gossh.ConnectionFailed, data.Protocol+" service unreachable on the agent host")
		return
	}
	defer conn.Close()

	channel, reqs, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go gossh.DiscardRequests(reqs)

	a.logger.Info("Tunnel %s relaying to %s", data.Protocol, addr)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(conn, channel)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(channel, conn)
		channel.CloseWrite()
	}()
	wg.Wait()
	a.logger.Info("Tunnel %s to %s closed", data.Protocol, addr)
}
