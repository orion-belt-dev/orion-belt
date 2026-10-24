package server

import (
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Timings that decide how fast a broken network is noticed. Variables so
// tests can shrink them.
var (
	// handshakeTimeout bounds the SSH handshake, so a TCP connection that
	// never speaks cannot hold a goroutine forever.
	handshakeTimeout = 30 * time.Second
	// keepaliveInterval and keepaliveTimeout: every interval the gateway asks
	// each agent and client for a reply; one missing for timeout drops the
	// connection. A half-open link (laptop asleep, NAT reset, cable pulled)
	// is then closed in under 30 s rather than when TCP gives up minutes later.
	keepaliveInterval = 15 * time.Second
	keepaliveTimeout  = 10 * time.Second
	// tunnelDrain is how long a relay waits for the other side once one
	// direction has ended. Peers that half-close and never finish would
	// otherwise keep both channels, and the agent's socket, open for good.
	tunnelDrain = 30 * time.Second
	// grantCacheTTL is how long a client connection remembers that a tunnel
	// was allowed. kubectl and RDP clients open short connections in bursts;
	// this spares the database one lookup set per connection, at the cost of
	// a revocation taking up to this long to stop new connections (open ones
	// already outlive their grant until closed).
	grantCacheTTL = 5 * time.Second
)

// timings is the set of the above a Server reads once, at New, so tests can
// change the variables without racing a server that is still running.
type timings struct {
	handshake, keepalive, keepaliveTimeout, drain, grantTTL time.Duration
	maxTunnels                                              int
}

func currentTimings() timings {
	return timings{handshakeTimeout, keepaliveInterval, keepaliveTimeout, tunnelDrain, grantCacheTTL, maxClientTunnels}
}

// maxClientTunnels caps relayed connections open at once on one client SSH
// connection, so one client cannot exhaust an agent's sockets. A variable,
// read at New like the timings, so tests can lower it.
var maxClientTunnels = 128

// keepConnAlive sends keepalive@openssh.com on conn every keepaliveInterval
// and closes conn when a reply does not arrive within keepaliveTimeout. It
// returns when conn closes. interval and timeout are keepaliveInterval and
// keepaliveTimeout as the server read them. Agents and clients of any version answer: an
// unknown global request still gets a (negative) reply.
func keepConnAlive(conn ssh.Conn, interval, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		conn.Wait()
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
			_, _, err := conn.SendRequest("keepalive@openssh.com", true, nil)
			replied <- err
		}()
		select {
		case <-done:
			return
		case err := <-replied:
			if err != nil {
				conn.Close()
				return
			}
		case <-time.After(timeout):
			conn.Close()
			return
		}
	}
}

// clientConn is what the gateway keeps per client SSH connection for the
// tunnels and kubectl relays opened on it.
type clientConn struct {
	remote   net.Addr
	userID   string
	username string
	slots    chan struct{}
	limit    int

	grantTTL time.Duration

	mu     sync.Mutex
	grants map[string]time.Time // machineID|accessType -> allowed until
}

func newClientConn(remote net.Addr, userID, username string, grantTTL time.Duration, limit int) *clientConn {
	return &clientConn{
		remote:   remote,
		userID:   userID,
		username: username,
		slots:    make(chan struct{}, limit),
		limit:    limit,
		grantTTL: grantTTL,
		grants:   map[string]time.Time{},
	}
}

// acquire takes a tunnel slot, or reports that the connection is at its cap.
func (c *clientConn) acquire() bool {
	select {
	case c.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *clientConn) release() { <-c.slots }

// allowed reports whether a grant for key was confirmed within grantCacheTTL.
func (c *clientConn) allowed(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Before(c.grants[key])
}

// remember records that key was just allowed.
func (c *clientConn) remember(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.grants[key] = time.Now().Add(c.grantTTL)
}
