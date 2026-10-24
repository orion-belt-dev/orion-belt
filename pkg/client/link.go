package client

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Timings for a forward's gateway connection. Variables so tests can shrink
// them.
var (
	// keepaliveInterval and keepaliveTimeout: a reply missing for timeout
	// means the link is dead (laptop slept, Wi-Fi changed, NAT dropped the
	// flow), so it is closed and redialled instead of hanging.
	keepaliveInterval = 15 * time.Second
	keepaliveTimeout  = 10 * time.Second
	redialMin         = time.Second
	redialMax         = 30 * time.Second
	// redialWait bounds how long a new local connection waits for the
	// gateway to come back before it is closed.
	redialWait = time.Minute
)

// gatewayLink keeps one SSH connection to the gateway for a forward,
// redialling it with backoff whenever it drops.
type gatewayLink struct {
	c        *SSHClient
	username string
	life     context.Context // ends with the forward; background redials stop with it
	stop     context.CancelFunc
	wg       sync.WaitGroup // keepalives, drop watchers and background redials

	dialMu sync.Mutex // one dialler at a time
	mu     sync.Mutex
	client *ssh.Client
	closed bool
	wasUp  bool
}

func newGatewayLink(ctx context.Context, c *SSHClient, username string) *gatewayLink {
	life, stop := context.WithCancel(ctx)
	return &gatewayLink{c: c, username: username, life: life, stop: stop}
}

// current returns the live connection, or nil.
func (l *gatewayLink) current() *ssh.Client {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.client
}

// get returns the live connection, dialling one if needed. With retry it
// keeps trying, with backoff, until ctx ends or an error retrying cannot fix.
func (l *gatewayLink) get(ctx context.Context, retry bool) (*ssh.Client, error) {
	if cl := l.current(); cl != nil {
		return cl, nil
	}
	l.dialMu.Lock()
	defer l.dialMu.Unlock()
	if cl := l.current(); cl != nil {
		return cl, nil // another caller reconnected meanwhile
	}
	wait := redialMin
	for {
		cl, err := l.c.dialGateway(l.username)
		if err == nil {
			l.adopt(cl)
			return cl, nil
		}
		if !retry || isPermanent(err) {
			return nil, err
		}
		l.c.logger.Warn("Gateway unreachable: %v (retrying in %s)", err, wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait/2 + time.Duration(rand.Int64N(int64(wait/2)+1))):
		}
		wait = min(wait*2, redialMax)
	}
}

// adopt makes cl the live connection, keeps it alive, and redials in the
// background when it drops.
func (l *gatewayLink) adopt(cl *ssh.Client) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		cl.Close()
		return
	}
	l.client = cl
	reconnected := l.wasUp
	l.wasUp = true
	l.mu.Unlock()
	if reconnected {
		l.c.status("Reconnected to the gateway.")
	}

	interval, timeout := keepaliveInterval, keepaliveTimeout
	l.wg.Add(2)
	go func() {
		defer l.wg.Done()
		keepClientAlive(cl, interval, timeout)
	}()
	go func() {
		defer l.wg.Done()
		cl.Wait()
		l.lost(cl)
	}()
}

// lost retires cl as the live connection, if it still is, tells the user,
// and redials in the background. Whichever notices first (the connection's
// watcher or a failed open) reports it; the other finds nothing to do.
func (l *gatewayLink) lost(cl *ssh.Client) {
	l.mu.Lock()
	current := l.client == cl && !l.closed
	if current {
		l.client = nil
	}
	l.mu.Unlock()
	cl.Close()
	if !current || l.life.Err() != nil {
		return
	}
	l.c.status("Connection to the gateway lost; reconnecting. The local port stays open.")
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		l.get(l.life, true)
	}()
}

// openStream opens a stream for one local connection. While the gateway is
// unreachable, or the target's agent is itself reconnecting, it keeps
// trying with backoff for up to redialWait before giving up on this
// connection.
func (l *gatewayLink) openStream(ctx context.Context, open func(*ssh.Client) (stream, error)) (stream, error) {
	ctx, cancel := context.WithTimeout(ctx, redialWait)
	defer cancel()
	wait := redialMin / 2
	for {
		cl, err := l.get(ctx, true)
		if err != nil {
			return nil, err
		}
		s, err := open(cl)
		if err == nil {
			return s, nil
		}
		var openErr *ssh.OpenChannelError
		if errors.As(err, &openErr) {
			// The gateway answered: a refusal is final, anything else (the
			// target's agent reconnecting) is worth another try.
			if isPermanent(err) {
				return nil, err
			}
		} else {
			// No answer at all means the link failed under us, whatever
			// the error says; retire it so get redials.
			l.lost(cl)
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Second)
	}
}

// close ends the link and waits for its background goroutines.
func (l *gatewayLink) close() {
	l.stop()
	l.mu.Lock()
	l.closed = true
	cl := l.client
	l.client = nil
	l.mu.Unlock()
	if cl != nil {
		cl.Close()
	}
	l.wg.Wait()
}

// keepClientAlive closes cl when the gateway stops answering keepalives.
func keepClientAlive(cl *ssh.Client, interval, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		cl.Wait()
		close(done)
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}
		replied := make(chan error, 1)
		go func() {
			_, _, err := cl.SendRequest("keepalive@openssh.com", true, nil)
			replied <- err
		}()
		// A closed connection fails the request at once, so there is no
		// separate case for it: no reply in time and a failed request are
		// both the end of this connection.
		var err error
		select {
		case err = <-replied:
		case <-time.After(timeout):
			err = errNoKeepaliveReply
		}
		if err != nil {
			cl.Close()
			return
		}
	}
}

var errNoKeepaliveReply = errors.New("no keepalive reply")

// isPermanent reports whether retrying err cannot help. Only network
// failures are worth retrying: a dropped or refused TCP connection, a
// timeout, or a connection that closed mid-handshake. The gateway refusing a
// channel as prohibited (no grant) is final; a machine that is briefly
// unavailable (its agent reconnecting) is not.
func isPermanent(err error) bool {
	var openErr *ssh.OpenChannelError
	if errors.As(err, &openErr) {
		return openErr.Reason == ssh.Prohibited || openErr.Reason == ssh.UnknownChannelType
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, net.ErrClosed) {
		return false
	}
	msg := err.Error()
	for _, transient := range []string{"connection reset", "broken pipe", "connection refused", "no route to host", "network is unreachable"} {
		if strings.Contains(msg, transient) {
			return false
		}
	}
	return true
}
