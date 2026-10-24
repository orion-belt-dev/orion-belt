package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// gatewayStub is an in-process SSH server standing in for the Orion Belt
// gateway: it accepts public-key auth, runs each exec request through
// handle, and reports the returned exit status.
type gatewayStub struct {
	addr   *net.TCPAddr
	handle func(cmd string, ch ssh.Channel) uint32
	reject bool // refuse every public key

	mu        sync.Mutex
	users     []string
	commands  []string
	ptys      int
	resizes   int
	resizedCh chan struct{}
	conns     []net.Conn
}

func startGateway(t *testing.T, handle func(cmd string, ch ssh.Channel) uint32) *gatewayStub {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	g := &gatewayStub{handle: handle, resizedCh: make(chan struct{}, 8)}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			g.mu.Lock()
			g.users = append(g.users, meta.User())
			g.mu.Unlock()
			if g.reject {
				return nil, errors.New("key not authorized")
			}
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	g.addr = ln.Addr().(*net.TCPAddr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go g.serve(conn, cfg)
		}
	}()
	return g
}

func (g *gatewayStub) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	g.mu.Lock()
	g.conns = append(g.conns, conn)
	g.mu.Unlock()
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() == "direct-tcpip" {
			g.tunnel(nc)
			continue
		}
		if nc.ChannelType() == kube.ClientChannelType {
			g.kube(nc)
			continue
		}
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			return
		}
		go g.session(ch, chReqs)
	}
}

func (g *gatewayStub) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			g.mu.Lock()
			g.ptys++
			g.mu.Unlock()
			_ = req.Reply(true, nil)
		case "window-change":
			g.mu.Lock()
			g.resizes++
			g.mu.Unlock()
			g.resizedCh <- struct{}{}
		case "exec":
			var payload struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &payload)
			_ = req.Reply(true, nil)
			g.mu.Lock()
			g.commands = append(g.commands, payload.Command)
			g.mu.Unlock()
			// Keep consuming requests (window-change) while the command runs.
			go func() {
				for r := range reqs {
					if r.Type == "window-change" {
						g.mu.Lock()
						g.resizes++
						g.mu.Unlock()
						g.resizedCh <- struct{}{}
					}
					if r.WantReply {
						_ = r.Reply(false, nil)
					}
				}
			}()
			status := g.handle(payload.Command, ch)
			var buf [4]byte
			binary.BigEndian.PutUint32(buf[:], status)
			_, _ = ch.SendRequest("exit-status", false, buf[:])
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

func (g *gatewayStub) snapshot() (users, commands []string, ptys int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.users...), append([]string(nil), g.commands...), g.ptys
}

// tunnel echoes what a direct-tcpip channel sends, as an RDP or VNC service
// behind the gateway would answer. Host "locked" is refused as the gateway
// refuses a user without a grant.
func (g *gatewayStub) tunnel(nc ssh.NewChannel) {
	var req struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	_ = ssh.Unmarshal(nc.ExtraData(), &req)
	g.mu.Lock()
	g.commands = append(g.commands, fmt.Sprintf("tunnel %s:%d", req.Host, req.Port))
	g.mu.Unlock()
	if req.Host == "locked" {
		_ = nc.Reject(ssh.Prohibited, "permission denied: no rdp access to locked")
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer ch.Close()
		_, _ = io.Copy(ch, ch)
		_ = ch.CloseWrite()
	}()
}

// closeAll drops every client connection, as a gateway restart would.
func (g *gatewayStub) closeAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.conns {
		c.Close()
	}
}

// kube echoes a kubectl relay channel, as the cluster's agent would answer.
// Cluster "locked" is refused as for a user without a k8s grant.
func (g *gatewayStub) kube(nc ssh.NewChannel) {
	var req kube.ClientChannelData
	_ = ssh.Unmarshal(nc.ExtraData(), &req)
	g.mu.Lock()
	g.commands = append(g.commands, "kube "+req.Cluster)
	g.mu.Unlock()
	if req.Cluster == "locked" {
		_ = nc.Reject(ssh.Prohibited, "permission denied: no k8s access to locked")
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer ch.Close()
		_, _ = io.Copy(ch, ch)
		_ = ch.CloseWrite()
	}()
}
