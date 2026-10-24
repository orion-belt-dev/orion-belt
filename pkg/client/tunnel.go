package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"

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
func (c *SSHClient) forward(ctx context.Context, username, listenAddr string, ready func(net.Addr), what string, open func(*ssh.Client) (stream, error)) error {
	client, err := c.dialGateway(username)
	if err != nil {
		return err
	}
	defer client.Close()

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
	// The gateway connection ending ends the tunnel too.
	go func() {
		client.Wait()
		ln.Close()
	}()

	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		local, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tunnel closed: %w", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer local.Close()
			remote, err := open(client)
			if err != nil {
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

// pipe copies a and b into each other, passing each EOF on, until both
// directions end.
func pipe(a, b stream) {
	var wg sync.WaitGroup
	wg.Add(2)
	half := func(dst, src stream) {
		defer wg.Done()
		io.Copy(dst, src)
		dst.CloseWrite()
	}
	go half(a, b)
	go half(b, a)
	wg.Wait()
}
