package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
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
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
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
