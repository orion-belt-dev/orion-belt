package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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

// statusLog collects what a forward tells the user.
type statusLog struct {
	mu   sync.Mutex
	msgs []string
}

func (s *statusLog) add(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
}

func (s *statusLog) has(sub string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func fastRedial(t *testing.T) {
	t.Helper()
	prev := []time.Duration{redialMin, redialMax, keepaliveInterval, keepaliveTimeout}
	redialMin, redialMax = 10*time.Millisecond, 50*time.Millisecond
	keepaliveInterval, keepaliveTimeout = 50*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		redialMin, redialMax, keepaliveInterval, keepaliveTimeout = prev[0], prev[1], prev[2], prev[3]
	})
}

// echoThrough dials the forward's local port and returns what came back.
func echoThrough(t *testing.T, addr net.Addr, msg string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, msg)
	_ = conn.(*net.TCPConn).CloseWrite()
	got, _ := io.ReadAll(conn)
	return string(got)
}

// startForward runs Forward in the background and returns its address.
func startForward(t *testing.T, c *SSHClient) (net.Addr, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.Forward(ctx, "alice", "win-01", "vnc", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()
	select {
	case a := <-addrs:
		// Wait for Forward to exit, so nothing outlives the test's settings.
		exited := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			err := <-done
			close(finished)
			exited <- err
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("Forward did not exit")
			}
		})
		return a, cancel, exited
	case err := <-done:
		t.Fatalf("Forward returned early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel never came up")
	}
	return nil, cancel, done
}

// The gateway connection dropping (a restart, a network change) no longer
// ends the forward: the local port stays open and the next connection goes
// through a fresh gateway connection.
func TestForward_SurvivesGatewayDrop(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	log := &statusLog{}
	c.Status = log.add

	addr, cancel, done := startForward(t, c)
	if got := echoThrough(t, addr, "before"); got != "before" {
		t.Fatalf("before the drop: %q", got)
	}

	g.closeAll()
	if got := echoThrough(t, addr, "after"); got != "after" {
		t.Fatalf("after the drop: %q", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !(log.has("lost") && log.has("Reconnected")) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !log.has("lost") || !log.has("Reconnected") {
		t.Errorf("status messages = %v", log.msgs)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Forward after cancel = %v", err)
	}
}

// A gateway that stops answering (a half-open link) is detected by
// keepalives and replaced, rather than hanging every new connection.
func TestForward_ReplacesSilentGateway(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	log := &statusLog{}
	c.Status = log.add

	addr, _, _ := startForward(t, c)
	g.mu.Lock()
	g.mute = true
	g.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !log.has("lost") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !log.has("lost") {
		t.Fatal("silent gateway never detected")
	}
	g.mu.Lock()
	g.mute = false
	g.mu.Unlock()
	if got := echoThrough(t, addr, "again"); got != "again" {
		t.Errorf("after replacing the link: %q", got)
	}
}

// Losing the grant is final: the forward ends with the gateway's reason
// instead of retrying forever.
func TestForward_StopsWhenAccessIsRefused(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.ForwardKube(ctx, "alice", "prod-eu", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()
	addr := <-addrs
	g.mu.Lock()
	g.lockAll = true
	g.mu.Unlock()
	conn, err := net.Dial("tcp", addr.String())
	if err == nil {
		defer conn.Close()
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no k8s access") {
			t.Errorf("Forward = %v, want the refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Forward kept going after access was refused")
	}
}

// Bursts of very short connections all go through and leave nothing behind.
func TestForward_ManyShortConnections(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	addr, cancel, done := startForward(t, c)

	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("m%d", i)
			conn, err := net.Dial("tcp", addr.String())
			if err != nil {
				failed.Add(1)
				return
			}
			defer conn.Close()
			_, _ = io.WriteString(conn, msg)
			_ = conn.(*net.TCPConn).CloseWrite()
			got, _ := io.ReadAll(conn)
			if string(got) != msg {
				failed.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := failed.Load(); n != 0 {
		t.Errorf("%d of 200 short connections failed", n)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Forward = %v", err)
	}
}

// pipe gives a peer that half-closes and then goes quiet pipeDrain to
// finish, then closes both sides instead of waiting forever.
func TestPipeDrainsHalfClosedPeer(t *testing.T) {
	prev := pipeDrain
	pipeDrain = 50 * time.Millisecond
	t.Cleanup(func() { pipeDrain = prev })

	a1, a2 := tcpPair(t)
	b1, b2 := tcpPair(t)
	done := make(chan struct{})
	go func() {
		pipe(a2.(stream), b1.(stream))
		close(done)
	}()
	_ = a1.(*net.TCPConn).CloseWrite() // one side is finished sending...
	// ...and b2 never answers or closes.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pipe waited forever on a silent peer")
	}
	_ = b2
}

func tcpPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	s := <-accepted
	t.Cleanup(func() { c.Close(); s.Close() })
	return c, s
}

func TestIsPermanent(t *testing.T) {
	cases := map[error]bool{
		&ssh.OpenChannelError{Reason: ssh.Prohibited}:         true,
		&ssh.OpenChannelError{Reason: ssh.UnknownChannelType}: true,
		&ssh.OpenChannelError{Reason: ssh.ConnectionFailed}:   false,
		io.EOF: false,
		&net.OpError{Op: "dial", Err: errors.New("connection refused")}:  false,
		errors.New("ssh: handshake failed: ssh: unable to authenticate"): true,
		errors.New("read private key: no such file"):                     true,
		errors.New("write: broken pipe"):                                 false,
		context.DeadlineExceeded:                                         false,
	}
	for err, want := range cases {
		if got := isPermanent(err); got != want {
			t.Errorf("isPermanent(%v) = %v, want %v", err, got, want)
		}
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

// A cluster whose agent is briefly unavailable is retried, not given up on.
func TestForward_RetriesUnavailableTarget(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	g.busy = 2
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	ctx, cancel := context.WithCancel(context.Background())
	addrs := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.ForwardKube(ctx, "alice", "prod-eu", "127.0.0.1:0", func(a net.Addr) { addrs <- a })
	}()
	addr := <-addrs
	if got := echoThrough(t, addr, "GET /api"); got != "GET /api" {
		t.Errorf("after the target came back: %q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("ForwardKube = %v", err)
	}
	g.mu.Lock()
	opens := g.kubeOpens
	g.mu.Unlock()
	if opens != 4 {
		t.Errorf("channel opens = %d, want probe + 2 refusals + 1 success", opens)
	}
}

// While the gateway refuses connections (restarting), the forward keeps
// redialling and recovers once it is back.
func TestForward_WaitsOutGatewayRestart(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	log := &statusLog{}
	c.Status = log.add
	addr, _, _ := startForward(t, c)

	g.mu.Lock()
	g.dropNew = true
	g.mu.Unlock()
	g.closeAll()
	go func() {
		time.Sleep(300 * time.Millisecond)
		g.mu.Lock()
		g.dropNew = false
		g.mu.Unlock()
	}()
	if got := echoThrough(t, addr, "back"); got != "back" {
		t.Fatalf("after the restart: %q", got)
	}
	if !log.has("lost") {
		t.Errorf("status = %v", log.msgs)
	}
}

// A link that died between the liveness check and the open is dropped and
// replaced, and the connection still goes through.
func TestGatewayLink_ReplacesLinkThatDiedUnderneath(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	link := newGatewayLink(context.Background(), c, "alice")
	defer link.close()

	first, err := link.get(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s, err := link.openStream(context.Background(), func(cl *ssh.Client) (stream, error) {
		calls++
		if calls == 1 {
			return nil, io.EOF // the link failed under us
		}
		conn, err := cl.Dial("tcp", "win-01:5900")
		if err != nil {
			return nil, err
		}
		return conn.(stream), nil
	})
	if err != nil {
		t.Fatalf("openStream = %v", err)
	}
	s.Close()
	if link.current() == first {
		t.Error("the failed link was kept")
	}
}

// A target that stays unavailable gives up after redialWait instead of
// holding the local connection forever.
func TestGatewayLink_GivesUpAfterRedialWait(t *testing.T) {
	fastRedial(t)
	prev := redialWait
	redialWait = 150 * time.Millisecond
	t.Cleanup(func() { redialWait = prev })
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	link := newGatewayLink(context.Background(), c, "alice")
	defer link.close()

	start := time.Now()
	_, err := link.openStream(context.Background(), func(*ssh.Client) (stream, error) {
		return nil, &ssh.OpenChannelError{Reason: ssh.ConnectionFailed, Message: "not available"}
	})
	if err == nil {
		t.Fatal("openStream succeeded on a target that never came back")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("gave up after %s", d)
	}
	if tunnelError(io.EOF) != io.EOF {
		t.Error("tunnelError changed a plain error")
	}
}

// A gateway that no longer accepts the key is a final answer: openStream
// returns at once instead of retrying.
func TestGatewayLink_RejectedKeyIsFinal(t *testing.T) {
	fastRedial(t)
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	g.reject = true
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	link := newGatewayLink(context.Background(), c, "alice")
	defer link.close()

	start := time.Now()
	if _, err := link.openStream(context.Background(), func(*ssh.Client) (stream, error) {
		t.Error("opened a stream without a gateway connection")
		return nil, nil
	}); err == nil || !isPermanent(err) {
		t.Fatalf("openStream = %v, want a permanent error", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s to give up on a rejected key", d)
	}
}

// keepClientAlive keeps a healthy link and returns once it closes.
func TestKeepClientAliveReturnsOnClose(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)
	cl, err := c.dialGateway("alice")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		keepClientAlive(cl, 5*time.Millisecond, time.Second)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // several healthy rounds
	cl.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive loop outlived its connection")
	}
}
