package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// gateway is a stub Orion Belt gateway the agent dials into. Once the agent
// is connected the test drives it by opening session channels over conn.
type gateway struct {
	addr *net.TCPAddr

	mu           sync.Mutex
	reject       bool
	heartbeatOK  bool
	heartbeats   int
	renewOK      bool
	renewPayload []byte
	certKeyIDs   []string
	kubeAnnounce int

	conns chan *gossh.ServerConn
}

func startGateway(t *testing.T) *gateway {
	t.Helper()
	g := &gateway{heartbeatOK: true, conns: make(chan *gossh.ServerConn, 4)}
	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if cert, ok := key.(*gossh.Certificate); ok {
				g.certKeyIDs = append(g.certKeyIDs, cert.KeyId)
			}
			if g.reject {
				return nil, errors.New("not registered")
			}
			return &gossh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(newSigner(t))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	g.addr = ln.Addr().(*net.TCPAddr)

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				conn, chans, reqs, err := gossh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go g.globalRequests(reqs)
				go g.agentChannels(chans)
				g.conns <- conn
			}()
		}
	}()
	return g
}

func (g *gateway) globalRequests(reqs <-chan *gossh.Request) {
	for req := range reqs {
		if req.Type == kube.CapabilityRequest {
			g.mu.Lock()
			g.kubeAnnounce++
			g.mu.Unlock()
			continue
		}
		if req.Type == ca.AgentCertRenewRequest {
			g.mu.Lock()
			ok, payload := g.renewOK, g.renewPayload
			g.mu.Unlock()
			_ = req.Reply(ok, payload)
			continue
		}
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

// agentChannels answers the heartbeat sessions the agent opens.
func (g *gateway) agentChannels(chans <-chan gossh.NewChannel) {
	for nc := range chans {
		ch, reqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range reqs {
				g.mu.Lock()
				g.heartbeats++
				ok := g.heartbeatOK
				g.mu.Unlock()
				_ = req.Reply(ok, nil)
			}
		}()
	}
}

func (g *gateway) waitConn(t *testing.T) *gossh.ServerConn {
	t.Helper()
	select {
	case c := <-g.conns:
		t.Cleanup(func() { c.Close() })
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("agent never connected")
		return nil
	}
}

func newSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// agentFixture writes an agent key and config pointing at g.
func agentFixture(t *testing.T, g *gateway) (*Agent, gossh.Signer) {
	t.Helper()
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := gossh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "agent_key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, _ := gossh.NewSignerFromKey(priv)

	cfg := &common.Config{}
	cfg.Agent.Name = "agent-1"
	cfg.Agent.Tags = map[string]string{"env": "test"}
	cfg.Auth.KeyFile = keyPath
	cfg.Auth.KnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Server.Host = "127.0.0.1"
	if g != nil {
		cfg.Server.Port = g.addr.Port
	}
	a, err := New(cfg, common.NewLoggerTo(common.ERROR, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	return a, signer
}

// result is what the gateway side observed from one session.
type result struct {
	stdout, stderr string
	status         int
	gotStatus      bool
}

// session opens a session channel to the agent, sends reqs in order, writes
// stdin, and collects output until the agent closes the channel.
func session(t *testing.T, conn *gossh.ServerConn, stdin string, reqs ...request) result {
	t.Helper()
	ch, in, err := conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	var res result
	done := make(chan struct{})
	statusSeen := make(chan struct{})
	go func() {
		defer close(done)
		for r := range in {
			if r.Type == "exit-status" && len(r.Payload) == 4 {
				res.status = int(binary.BigEndian.Uint32(r.Payload))
				res.gotStatus = true
				close(statusSeen)
			}
			if r.WantReply {
				_ = r.Reply(true, nil)
			}
		}
	}()

	for _, r := range reqs {
		ok, err := ch.SendRequest(r.typ, r.wantReply, r.payload)
		if err != nil {
			t.Fatalf("send %s: %v", r.typ, err)
		}
		if r.wantReply && ok != r.expectOK {
			t.Errorf("%s reply = %v, want %v", r.typ, ok, r.expectOK)
		}
	}
	if stdin != "" {
		_, _ = io.WriteString(ch, stdin)
	}
	_ = ch.CloseWrite()

	var out, errOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&out, ch) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errOut, ch.Stderr()) }()
	wg.Wait()
	// SSH allows exit-status after EOF, and the agent's shell path sends it
	// that way. Wait for it the way the gateway does before closing.
	select {
	case <-statusSeen:
	case <-time.After(5 * time.Second):
	}
	_ = ch.Close()
	<-done
	res.stdout, res.stderr = out.String(), errOut.String()
	return res
}

type request struct {
	typ       string
	payload   []byte
	wantReply bool
	expectOK  bool
}

func execReq(command, username string) request {
	return request{"exec", gossh.Marshal(&struct{ Command, User string }{command, username}), true, true}
}

func connected(t *testing.T) (*Agent, *gossh.ServerConn, *gateway) {
	t.Helper()
	g := startGateway(t)
	a, _ := agentFixture(t, g)
	if err := a.connectToServer(); err != nil {
		t.Fatalf("connectToServer: %v", err)
	}
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	return a, g.waitConn(t), g
}
