package client

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestForward_RelaysLocalConnections(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Forward(ctx, "alice", "win-01", "rdp", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()

	var addr net.Addr
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatalf("Forward returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel never came up")
	}

	// Two connections in turn, as an RDP client reconnecting would make.
	for _, msg := range []string{"first", "second"} {
		conn, err := net.Dial("tcp", addr.String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, msg)
		_ = conn.(*net.TCPConn).CloseWrite()
		got, _ := io.ReadAll(conn)
		conn.Close()
		if string(got) != msg {
			t.Errorf("relayed %q, want %q", got, msg)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Forward after cancel = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forward did not stop on cancel")
	}
	_, cmds, _ := g.snapshot()
	if len(cmds) == 0 || cmds[0] != "tunnel win-01:3389" {
		t.Errorf("gateway saw %v, want the machine and RDP port", cmds)
	}
}

func TestForward_FailsEarlyWithGatewayReason(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	err := c.Forward(context.Background(), "alice", "locked", "vnc", "127.0.0.1:0", func(net.Addr) {
		t.Error("listener opened despite the refusal")
	})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Forward = %v, want the gateway's refusal", err)
	}
	if err := c.Forward(context.Background(), "alice", "win-01", "ssh", "127.0.0.1:0", nil); err == nil {
		t.Error("Forward accepted a non-tunnel protocol")
	}
}

func TestRequestAccessType_TunnelNamesNoRemoteUser(t *testing.T) {
	fastPolling(t, time.Second)
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)
	c, _ := NewSSHClient(cfg, quietLogger)

	withStdio(t, nil, func() {
		if err := c.RequestAccessType("web-01", "rdp", "patching", 600); err != nil {
			t.Errorf("RequestAccessType: %v", err)
		}
	})
	api.mu.Lock()
	body := api.created
	api.mu.Unlock()
	if body["access_type"] != "rdp" {
		t.Errorf("access_type = %v, want rdp", body["access_type"])
	}
	if _, ok := body["remote_users"]; ok {
		t.Errorf("rdp request named remote users: %v", body["remote_users"])
	}
}

func TestForward_SetupFailures(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	// The local address is taken.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	if err := c.Forward(context.Background(), "alice", "win-01", "rdp", busy.Addr().String(), nil); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("busy port: %v", err)
	}

	// The gateway refuses the key.
	g.mu.Lock()
	g.reject = true
	g.mu.Unlock()
	if err := c.Forward(context.Background(), "alice", "win-01", "rdp", "127.0.0.1:0", nil); err == nil {
		t.Error("Forward succeeded with a rejected key")
	}
}

func TestForward_EndsWithGatewayConnection(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Forward(context.Background(), "alice", "win-01", "vnc", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()
	select {
	case <-addrs:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel never came up")
	}
	g.closeAll()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "tunnel closed") {
			t.Errorf("Forward after the gateway went away = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forward kept running without a gateway")
	}
}

func TestForwardKube_RelaysAndRefuses(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	if err := c.ForwardKube(context.Background(), "alice", "locked", "127.0.0.1:0", nil); err == nil || !strings.Contains(err.Error(), "no k8s access") {
		t.Fatalf("refused cluster: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.ForwardKube(ctx, "alice", "prod-eu", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()
	var addr net.Addr
	select {
	case addr = <-addrs:
	case err := <-done:
		t.Fatalf("ForwardKube returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("relay never came up")
	}
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "GET /api HTTP/1.1\r\n\r\n")
	_ = conn.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(conn)
	conn.Close()
	if !strings.HasPrefix(string(got), "GET /api") {
		t.Errorf("relayed %q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ForwardKube after cancel = %v", err)
	}
}

func TestRequestKubeAccess_SendsScopes(t *testing.T) {
	fastPolling(t, time.Second)
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)
	c, _ := NewSSHClient(cfg, quietLogger)

	withStdio(t, nil, func() {
		if err := c.RequestKubeAccess("web-01", []string{"payments/edit", "*/view"}, "INC-42", 600); err != nil {
			t.Errorf("RequestKubeAccess: %v", err)
		}
	})
	api.mu.Lock()
	body := api.created
	api.mu.Unlock()
	scopes, _ := body["remote_users"].([]any)
	if body["access_type"] != "k8s" || len(scopes) != 2 || scopes[0] != "payments/edit" {
		t.Errorf("request body = %v", body)
	}
}
