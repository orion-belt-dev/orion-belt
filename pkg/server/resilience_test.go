package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// setTiming sets one of the resilience timings for this test. Servers read
// them at New, so this must run before startHarness.
func setTiming(t *testing.T, v *time.Duration, d time.Duration) {
	t.Helper()
	prev := *v
	*v = d
	t.Cleanup(func() { *v = prev })
}

// echoOn sends msg through a tunnel on an existing client connection.
func echoOn(c *ssh.Client, addr, msg string) (string, error) {
	conn, err := c.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, msg); err != nil {
		return "", err
	}
	_ = conn.(interface{ CloseWrite() error }).CloseWrite()
	got, err := io.ReadAll(conn)
	return string(got), err
}

func (h *harness) machineActive(t *testing.T) bool {
	t.Helper()
	m, err := h.srv.store.GetMachine(context.Background(), h.machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m.IsActive
}

// An agent that reconnects (a network change) replaces its old connection,
// and the old one closing later must not mark the machine offline.
func TestGateway_ReconnectingAgentStaysActive(t *testing.T) {
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)
	first := h.connectAgent(t)
	second := h.connectAgent(t)

	closed := make(chan struct{})
	go func() {
		first.conn.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced connection was not closed")
	}
	// Give the old connection's cleanup time to (wrongly) unregister.
	time.Sleep(200 * time.Millisecond)
	if !h.machineActive(t) {
		t.Fatal("machine marked inactive after its agent reconnected")
	}
	if got, err := h.tunnelEcho(t, "web-01:3389", "via new"); err != nil || got != "via new" {
		t.Fatalf("tunnel after reconnect = %q, %v", got, err)
	}
	second.mu.Lock()
	n := len(second.tunnels)
	second.mu.Unlock()
	if n != 1 {
		t.Errorf("new connection served %d tunnels, want 1", n)
	}
}

// An agent that stops answering keepalives (half-open link) is dropped and
// its machine marked offline, instead of hanging every session for minutes.
func TestGateway_KeepaliveDropsSilentAgent(t *testing.T) {
	setTiming(t, &keepaliveInterval, 50*time.Millisecond)
	setTiming(t, &keepaliveTimeout, 100*time.Millisecond)
	h := startHarness(t, nil)
	a := h.connectAgent(t)
	a.muted.Store(true)

	gone := make(chan struct{})
	go func() {
		a.conn.Wait()
		close(gone)
	}()
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("silent agent was never dropped")
	}
	eventually(t, "machine to go inactive", func() bool { return !h.machineActive(t) })
}

// A TCP connection that never starts the SSH handshake is closed.
func TestGateway_HandshakeTimeout(t *testing.T) {
	setTiming(t, &handshakeTimeout, 100*time.Millisecond)
	h := startHarness(t, nil)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", h.cfg.Server.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	// The server's banner arrives first; then the connection must close.
	if _, err := io.ReadAll(conn); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatal("silent connection was kept open")
		}
	}
}

// One client connection cannot hold more than maxClientTunnels at once.
func TestGateway_TunnelCap(t *testing.T) {
	prev := maxClientTunnels
	maxClientTunnels = 8
	t.Cleanup(func() { maxClientTunnels = prev })
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)
	h.connectAgent(t)
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	var open []net.Conn
	t.Cleanup(func() {
		for _, conn := range open {
			conn.Close()
		}
		// Each close writes an audit entry; let them land before the next
		// test resets the database.
		eventually(t, "every tunnel's close to be audited", func() bool {
			opened, closed := 0, 0
			for _, a := range h.auditActions(t, "tunnel.") {
				switch a {
				case "tunnel.open":
					opened++
				case "tunnel.close":
					closed++
				}
			}
			return opened > 0 && opened == closed
		})
	})
	for i := range maxClientTunnels {
		conn, err := c.Dial("tcp", "web-01:3389")
		if err != nil {
			t.Fatalf("tunnel %d: %v", i, err)
		}
		open = append(open, conn)
	}
	_, err = c.Dial("tcp", "web-01:3389")
	var oe *ssh.OpenChannelError
	if !errors.As(err, &oe) || oe.Reason != ssh.ResourceShortage {
		t.Fatalf("tunnel over the cap: %v, want ResourceShortage", err)
	}
	// Closing one frees a slot.
	open[0].Close()
	open = open[1:]
	eventually(t, "a slot to free up", func() bool {
		conn, err := c.Dial("tcp", "web-01:3389")
		if err != nil {
			return false
		}
		open = append(open, conn)
		return true
	})
}

// A permission check is reused on one connection for grantCacheTTL, then
// checked again: a revoked grant stops new tunnels once it lapses.
func TestGateway_GrantCacheExpires(t *testing.T) {
	setTiming(t, &grantCacheTTL, 300*time.Millisecond)
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)
	h.connectAgent(t)
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := echoOn(c, "web-01:3389", "one"); err != nil || got != "one" {
		t.Fatalf("first tunnel = %q, %v", got, err)
	}
	perms, _ := h.srv.store.ListMachinePermissions(context.Background(), h.machine.ID)
	for _, p := range perms {
		if err := h.srv.store.DeletePermission(context.Background(), p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := echoOn(c, "web-01:3389", "two"); err != nil {
		t.Fatalf("within the cache window: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := echoOn(c, "web-01:3389", "three"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("after the window: %v, want permission denied", err)
	}
}

// A target that goes quiet after the client finished sending does not hold
// the relay open forever.
func TestGateway_RelayDrainsQuietPeer(t *testing.T) {
	setTiming(t, &tunnelDrain, 100*time.Millisecond)
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)
	a := h.connectAgent(t)
	a.silentTunnels.Store(true)

	done := make(chan error, 1)
	go func() {
		_, err := h.tunnelEcho(t, "web-01:3389", "hello?")
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay stayed open on a quiet peer")
	}
	eventually(t, "tunnel.close audit", func() bool {
		return strings.Contains(strings.Join(h.auditActions(t, "tunnel."), ","), "tunnel.close")
	})
}

// Many short tunnels in a burst all succeed and leave no goroutines behind.
func TestGateway_ShortConnectionBurst(t *testing.T) {
	h := startHarness(t, nil)
	h.grantType(t, common.AccessTypeRDP, nil)
	h.connectAgent(t)
	c, err := h.dial(t, "alice", h.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := echoOn(c, "web-01:3389", "warm"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	const workers, each = 20, 15
	var failed atomic.Int32
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				msg := fmt.Sprintf("w%d-%d", w, i)
				if got, err := echoOn(c, "web-01:3389", msg); err != nil || got != msg {
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := failed.Load(); n != 0 {
		t.Fatalf("%d of %d short tunnels failed", n, workers*each)
	}
	eventually(t, "goroutines to return to baseline", func() bool {
		return runtime.NumGoroutine() <= before+10
	})
}
