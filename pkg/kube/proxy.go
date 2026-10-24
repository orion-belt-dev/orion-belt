package kube

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"
)

// NewProxy returns a handler that forwards kubectl requests to the API
// server as user. Whatever identity the client claims is dropped: the agent
// authenticates with its service account and impersonates Subject(user), so
// the user gets exactly what their issued bindings allow. Upgrades (exec,
// attach, port-forward) pass through.
func NewProxy(c *Client, user string, onError func(error)) http.Handler {
	subject := Subject(user)
	return &httputil.ReverseProxy{
		Transport: c.Transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(c.Base())
			r.Out.Host = c.Base().Host
			stripIdentity(r.Out.Header)
			if token, err := c.Token(); err == nil {
				r.Out.Header.Set("Authorization", "Bearer "+token)
			}
			r.Out.Header.Set("Impersonate-User", subject)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if onError != nil {
				onError(err)
			}
			http.Error(w, "orion-belt: kubernetes API unreachable from the agent", http.StatusBadGateway)
		},
	}
}

// stripIdentity removes every header that could change who the API server
// thinks is calling.
func stripIdentity(h http.Header) {
	h.Del("Authorization")
	h.Del("Proxy-Authorization")
	for name := range h {
		if strings.HasPrefix(strings.ToLower(name), "impersonate-") {
			h.Del(name)
		}
	}
}

// ServeConn serves HTTP on a single connection until it closes, whether the
// server closes it or an upgraded (hijacked) exchange ends.
func ServeConn(conn net.Conn, h http.Handler) {
	ln := &oneConnListener{done: make(chan struct{})}
	ln.conn = &closeHookConn{Conn: conn, onClose: ln.close}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
	_ = srv.Serve(ln)
}

// ServeStream serves HTTP on a stream with no deadlines, such as an SSH
// channel. net/http cancels its background reads with read deadlines, so the
// stream is bridged through a net.Pipe, which has them.
func ServeStream(rw io.ReadWriteCloser, h http.Handler) {
	server, bridge := net.Pipe()
	go func() {
		_, _ = io.Copy(bridge, rw)
		bridge.Close()
	}()
	go func() {
		_, _ = io.Copy(rw, bridge)
		rw.Close()
	}()
	ServeConn(server, h)
	bridge.Close()
	rw.Close()
}

// closeHookConn runs onClose once the connection is closed.
type closeHookConn struct {
	net.Conn
	onClose func()
}

func (c *closeHookConn) Close() error {
	err := c.Conn.Close()
	c.onClose()
	return err
}

// oneConnListener yields conn once, then blocks until it closes.
type oneConnListener struct {
	conn   net.Conn
	once   sync.Once
	mu     sync.Mutex
	served bool
	done   chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	first := !l.served
	l.served = true
	l.mu.Unlock()
	if first {
		return l.conn, nil
	}
	<-l.done
	return nil, errors.New("connection closed")
}

func (l *oneConnListener) close() { l.once.Do(func() { close(l.done) }) }

func (l *oneConnListener) Close() error { l.close(); return nil }

func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
