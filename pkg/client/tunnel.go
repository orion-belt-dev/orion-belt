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
	"golang.org/x/crypto/ssh"
)

// Forward listens on listenAddr and relays every connection to protocol (rdp,
// vnc) on machine through the gateway, until ctx is done. ready is called
// with the bound address once the listener is up, so a ":0" port can be shown
// to the user.
func (c *SSHClient) Forward(ctx context.Context, username, machine, protocol, listenAddr string, ready func(net.Addr)) error {
	port := common.TunnelPort(protocol)
	if port == 0 {
		return fmt.Errorf("unknown tunnel protocol %q", protocol)
	}

	client, err := c.dialGateway(username)
	if err != nil {
		return err
	}
	defer client.Close()

	// Fail now, with the gateway's reason, rather than on the first
	// connection the user's RDP or VNC client makes.
	probe, err := client.Dial("tcp", net.JoinHostPort(machine, strconv.Itoa(int(port))))
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
			remote, err := client.Dial("tcp", net.JoinHostPort(machine, strconv.Itoa(int(port))))
			if err != nil {
				c.logger.Warn("%s connection to %s failed: %v", protocol, machine, tunnelError(err))
				return
			}
			defer remote.Close()
			pipe(local, remote)
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
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	half := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go half(a, b)
	go half(b, a)
	wg.Wait()
}
