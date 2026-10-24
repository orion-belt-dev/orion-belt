package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
	"golang.org/x/crypto/ssh"
)

// stream is one relayed connection through the gateway: a net.Conn from
// direct-tcpip, or an ssh.Channel.
type stream interface {
	io.ReadWriteCloser
	CloseWrite() error
}

// Forward listens on listenAddr and relays every connection to protocol (rdp,
// vnc) on machine through the gateway, until ctx is done. ready is called
// with the bound address once the listener is up, so a ":0" port can be shown
// to the user.
func (c *SSHClient) Forward(ctx context.Context, username, machine, protocol, listenAddr string, ready func(net.Addr)) error {
	port := common.TunnelPort(protocol)
	if port == 0 {
		return fmt.Errorf("unknown tunnel protocol %q", protocol)
	}
	addr := net.JoinHostPort(machine, strconv.Itoa(int(port)))
	return c.forward(ctx, username, listenAddr, ready, protocol+" to "+machine, func(client *ssh.Client) (stream, error) {
		conn, err := client.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return conn.(stream), nil
	})
}

// ForwardKube listens on listenAddr and relays every connection to cluster's
// Kubernetes API through the gateway, until ctx is done. kubectl pointed at
// the listener acts as the gateway user, with what their grants issued.
func (c *SSHClient) ForwardKube(ctx context.Context, username, cluster, listenAddr string, ready func(net.Addr)) error {
	data := ssh.Marshal(&kube.ClientChannelData{Cluster: cluster})
	return c.forward(ctx, username, listenAddr, ready, "kubernetes on "+cluster, func(client *ssh.Client) (stream, error) {
		ch, reqs, err := client.OpenChannel(kube.ClientChannelType, data)
		if err != nil {
			return nil, err
		}
		go ssh.DiscardRequests(reqs)
		return ch, nil
	})
}

// forward runs a local listener whose connections each get a stream from
// open. The first stream is opened, and closed, before listening, so a
// refusal fails at once with the gateway's reason.
//
// The listener outlives the gateway connection: when the network drops, the
// connection is redialled in the background and later local connections use
// the new one, so an RDP client's own reconnect, or the next kubectl command,
// just works. Refusals that retrying cannot fix (permission denied, a key the
// gateway no longer accepts) end the forward with the reason.
func (c *SSHClient) forward(ctx context.Context, username, listenAddr string, ready func(net.Addr), what string, open func(*ssh.Client) (stream, error)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	link := newGatewayLink(ctx, c, username)
	defer link.close()

	client, err := link.get(ctx, false)
	if err != nil {
		return err
	}
	probe, err := open(client)
	if err != nil {
		return tunnelError(err)
	}
	probe.Close()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listenAddr, err)
	}
	defer ln.Close()
	if ready != nil {
		ready(ln.Addr())
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		local, err := ln.Accept()
		if err != nil {
			if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return cause
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tunnel closed: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer local.Close()
			remote, err := link.openStream(ctx, open)
			if err != nil {
				if isPermanent(err) {
					cancel(tunnelError(err))
					return
				}
				c.logger.Warn("%s failed: %v", what, tunnelError(err))
				return
			}
			defer remote.Close()
			pipe(local.(stream), remote)
		}()
	}
}

// tunnelError unwraps the gateway's rejection reason from a failed dial.
func tunnelError(err error) error {
	var openErr *ssh.OpenChannelError
	if errors.As(err, &openErr) {
		return errors.New(openErr.Message)
	}
	return err
}

// pipeDrain is how long pipe waits for the other direction once one has
// ended, so a peer that half-closes and never closes cannot hold the local
// socket and the gateway channel open forever.
var pipeDrain = 30 * time.Second

// pipe copies a and b into each other, passing each EOF on, until both
// directions end or one has ended pipeDrain ago.
func pipe(a, b stream) {
	done := make(chan struct{}, 2)
	half := func(dst, src stream) {
		io.Copy(dst, src)
		dst.CloseWrite()
		done <- struct{}{}
	}
	go half(a, b)
	go half(b, a)
	<-done
	select {
	case <-done:
	case <-time.After(pipeDrain):
	}
	a.Close()
	b.Close()
}
