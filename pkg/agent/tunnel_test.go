package agent

import (
	"errors"
	"io"
	"net"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// echoServer accepts connections and echoes each back, standing in for the
// RDP or VNC service on the agent host.
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	return ln.Addr().String()
}

func openTunnel(conn *gossh.ServerConn, protocol string) (gossh.Channel, error) {
	ch, reqs, err := conn.OpenChannel(common.TunnelChannelType, gossh.Marshal(&common.TunnelChannelData{Protocol: protocol}))
	if err != nil {
		return nil, err
	}
	go gossh.DiscardRequests(reqs)
	return ch, nil
}

func TestTunnel_RelaysToConfiguredAddress(t *testing.T) {
	a, conn, _ := connected(t)
	a.config.Agent.Tunnels = map[string]string{common.AccessTypeRDP: echoServer(t)}

	ch, err := openTunnel(conn, common.AccessTypeRDP)
	if err != nil {
		t.Fatalf("open tunnel: %v", err)
	}
	if _, err := ch.Write([]byte("hello rdp")); err != nil {
		t.Fatal(err)
	}
	_ = ch.CloseWrite()
	got, err := io.ReadAll(ch)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello rdp" {
		t.Fatalf("relayed %q, want %q", got, "hello rdp")
	}
}

func TestTunnel_Refusals(t *testing.T) {
	a, conn, _ := connected(t)
	// A port nothing listens on: bind one, then free it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	ln.Close()
	a.config.Agent.Tunnels = map[string]string{
		common.AccessTypeRDP: "off",
		common.AccessTypeVNC: closed,
	}

	cases := []struct {
		protocol string
		reason   gossh.RejectionReason
	}{
		{common.AccessTypeRDP, gossh.Prohibited},       // disabled in config
		{"ssh", gossh.Prohibited},                      // not a tunnel protocol
		{common.AccessTypeVNC, gossh.ConnectionFailed}, // service not running
	}
	for _, tc := range cases {
		_, err := openTunnel(conn, tc.protocol)
		var openErr *gossh.OpenChannelError
		if !errors.As(err, &openErr) || openErr.Reason != tc.reason {
			t.Errorf("%s: got %v, want rejection %v", tc.protocol, err, tc.reason)
		}
	}

	// Garbage extra data is refused rather than crashing the agent.
	if _, _, err := conn.OpenChannel(common.TunnelChannelType, []byte{1}); err == nil {
		t.Error("malformed tunnel request was accepted")
	}
}
