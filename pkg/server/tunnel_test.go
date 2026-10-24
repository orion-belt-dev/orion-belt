package server

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func (h *harness) grantType(t *testing.T, accessType string, remoteUsers []string) {
	t.Helper()
	p := common.NewPermission(h.alice.ID, h.machine.ID, accessType, remoteUsers, h.alice.ID, nil)
	if err := h.srv.store.CreatePermission(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

// tunnelEcho dials addr through the gateway as alice, as `ssh -L` and osh rdp
// do, and returns what came back for msg.
func (h *harness) tunnelEcho(t *testing.T, addr, msg string) (string, error) {
	t.Helper()
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := c.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, msg); err != nil {
		return "", err
	}
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	got, err := io.ReadAll(conn)
	return string(got), err
}

func (h *harness) auditActions(t *testing.T, prefix string) []string {
	t.Helper()
	logs, err := h.srv.store.ListAuditLogs(context.Background(), 1000, 0, map[string]interface{}{"action_prefix": prefix})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, l := range logs {
		actions = append(actions, l.Action)
	}
	return actions
}

func TestGateway_Tunnel(t *testing.T) {
	h := startHarness(t, nil)
	agent := h.connectAgent(t)

	// No grant, then an ssh+scp grant: neither opens RDP.
	if _, err := h.tunnelEcho(t, "web-01:3389", "x"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("without a grant: %v", err)
	}
	h.grantType(t, common.AccessTypeBoth, []string{"root"})
	if _, err := h.tunnelEcho(t, "web-01:3389", "x"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("with only a 'both' grant: %v", err)
	}

	h.grantType(t, common.AccessTypeRDP, nil)
	got, err := h.tunnelEcho(t, "web-01:3389", "rdp bytes")
	if err != nil || got != "rdp bytes" {
		t.Fatalf("rdp tunnel: got %q, %v", got, err)
	}
	// The rdp grant does not extend to vnc.
	if _, err := h.tunnelEcho(t, "web-01:5900", "x"); err == nil || !strings.Contains(err.Error(), "no vnc access") {
		t.Fatalf("vnc with an rdp grant: %v", err)
	}

	agent.mu.Lock()
	tunnels := agent.tunnels
	agent.mu.Unlock()
	if len(tunnels) != 1 || tunnels[0] != common.AccessTypeRDP {
		t.Fatalf("agent saw tunnels %v, want [rdp]", tunnels)
	}

	// The close is audited after the relay ends, a moment after the client
	// has its bytes.
	deadline := time.Now().Add(5 * time.Second)
	var actions []string
	for time.Now().Before(deadline) {
		actions = h.auditActions(t, "tunnel.")
		if strings.Contains(strings.Join(actions, ","), "tunnel.close") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	joined := strings.Join(actions, ",")
	for _, want := range []string{"tunnel.denied", "tunnel.open", "tunnel.close"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit log %v has no %s", actions, want)
		}
	}
}

func TestGateway_TunnelRejections(t *testing.T) {
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)

	// Agent not connected yet.
	if _, err := h.tunnelEcho(t, "web-01:3389", "x"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("agent offline: %v", err)
	}

	agent := h.connectAgent(t)
	cases := map[string]string{
		"web-01:22":   "not relayed", // only the tunnel ports
		"ghost:3389":  "machine not found",
		"web-01:8080": "not relayed",
	}
	for addr, want := range cases {
		if _, err := h.tunnelEcho(t, addr, "x"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", addr, err, want)
		}
	}

	agent.oldVersion = true
	if _, err := h.tunnelEcho(t, "web-01:3389", "x"); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("old agent: %v", err)
	}
}

func TestGateway_TunnelMalformedRequest(t *testing.T) {
	h := startHarness(t, nil)
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.OpenChannel("direct-tcpip", []byte{1, 2})
	if oe, ok := err.(*ssh.OpenChannelError); !ok || oe.Reason != ssh.ConnectionFailed {
		t.Fatalf("malformed direct-tcpip: %v", err)
	}
}
