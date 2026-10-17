package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/internal/pgtest"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// writeHostKey writes an OpenSSH private key to dir and returns its path.
func writePrivateKey(t *testing.T, dir, name string) (string, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(priv)
	return path, signer
}

// baseConfig is a complete gateway config on free loopback ports, backed by
// the test database.
func baseConfig(t *testing.T) *common.Config {
	t.Helper()
	dir := t.TempDir()
	hostKey, _ := writePrivateKey(t, dir, "host_key")
	cfg := &common.Config{}
	cfg.Database.Driver = "postgres"
	cfg.Database.ConnectionString = pgtest.DSN(t)
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = freePort(t)
	cfg.Server.APIPort = freePort(t)
	cfg.Server.SSHHostKey = hostKey
	cfg.Recording.StoragePath = filepath.Join(dir, "recordings")
	cfg.Recording.Compression = "none"
	return cfg
}

// resetDB empties the tables the gateway tests write to.
func resetDB(t *testing.T) {
	t.Helper()
	db, err := sql.Open("postgres", pgtest.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The schema may not exist yet on a fresh container; New migrates it.
	_, _ = db.Exec(`TRUNCATE users, machines, ssh_ca_keys, ssh_certificates CASCADE`)
}

// harness is a running gateway with a user, a machine and that machine's
// legacy agent identity.
type harness struct {
	srv      *Server
	cfg      *common.Config
	logs     *syncBuffer
	alice    *common.User
	aliceKey ssh.Signer
	machine  *common.Machine
	agentKey ssh.Signer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func startHarness(t *testing.T, mutate func(*common.Config)) *harness {
	t.Helper()
	return startHarnessWith(t, mutate, nil)
}

// startHarnessWith is startHarness with a hook that runs after New and
// before Start, while no background loop can race with it.
func startHarnessWith(t *testing.T, mutate func(*common.Config), beforeStart func(*Server)) *harness {
	t.Helper()
	resetDB(t)
	cfg := baseConfig(t)
	if mutate != nil {
		mutate(cfg)
	}
	logs := &syncBuffer{}
	srv, err := New(cfg, common.NewLoggerTo(common.DEBUG, logs))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := &harness{srv: srv, cfg: cfg, logs: logs}
	ctx := context.Background()

	_, h.aliceKey = writePrivateKey(t, t.TempDir(), "alice")
	h.alice = common.NewUser("alice", "alice@example.com", string(ssh.MarshalAuthorizedKey(h.aliceKey.PublicKey())), false)
	if err := srv.store.CreateUser(ctx, h.alice); err != nil {
		t.Fatal(err)
	}

	// Legacy agent identity: a user row named after the machine whose ID the
	// machine's agent_id points at.
	_, h.agentKey = writePrivateKey(t, t.TempDir(), "agent")
	agentUser := common.NewUser("web-01", "agent@example.com", string(ssh.MarshalAuthorizedKey(h.agentKey.PublicKey())), false)
	if err := srv.store.CreateUser(ctx, agentUser); err != nil {
		t.Fatal(err)
	}
	h.machine = common.NewMachine("web-01", "10.0.0.1", 22, nil)
	h.machine.AgentID = agentUser.ID
	if err := srv.store.CreateMachine(ctx, h.machine); err != nil {
		t.Fatal(err)
	}

	if beforeStart != nil {
		beforeStart(srv)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	waitListening(t, cfg.Server.Port)
	t.Cleanup(func() {
		_ = srv.Stop(context.Background())
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("Start returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Start did not return after Stop")
		}
	})
	return h
}

func waitListening(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %d", port)
}

func (h *harness) grant(t *testing.T, remoteUser string) {
	t.Helper()
	p := common.NewPermission(h.alice.ID, h.machine.ID, "ssh", []string{remoteUser}, h.alice.ID, nil)
	if err := h.srv.store.CreatePermission(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func (h *harness) dial(t *testing.T, user string, signer ssh.Signer) (*ssh.Client, error) {
	t.Helper()
	c, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", h.cfg.Server.Port), &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err == nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, err
}

// clientResult is what an SSH client saw from one session.
type clientResult struct {
	out    string
	status int
	err    error
}

// runClient opens a session as user and either execs command or, when
// command is empty, requests a PTY and a shell, feeding stdin.
func (h *harness) runClient(t *testing.T, user, command, stdin string) clientResult {
	t.Helper()
	c, err := h.dial(t, user, h.aliceKey)
	if err != nil {
		t.Fatalf("dial %s: %v", user, err)
	}
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// Separate buffers: the session copies stdout and stderr concurrently.
	var out, errOut bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errOut
	sess.Stdin = strings.NewReader(stdin)

	if command == "" {
		if err := sess.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
			t.Fatal(err)
		}
		if err := sess.Shell(); err != nil {
			t.Fatal(err)
		}
		_ = sess.WindowChange(30, 100)
	} else if err := sess.Start(command); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()
	var res clientResult
	select {
	case res.err = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("client session did not finish")
	}
	res.out = out.String() + errOut.String()
	if exitErr, ok := res.err.(*ssh.ExitError); ok {
		res.status = exitErr.ExitStatus()
	}
	return res
}

// stubAgent dials the gateway as the machine's legacy agent and serves the
// session channels the gateway opens to it.
type stubAgent struct {
	conn      ssh.Conn
	refusePTY bool

	mu      sync.Mutex
	execs   []string
	ptys    int
	resizes int
}

func (h *harness) connectAgent(t *testing.T) *stubAgent {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", h.cfg.Server.Port)
	tcp, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(tcp, addr, &ssh.ClientConfig{
		User:            h.machine.Name,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(h.agentKey)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("agent handshake: %v", err)
	}
	a := &stubAgent{conn: conn}
	go ssh.DiscardRequests(reqs)
	go a.serve(chans)
	t.Cleanup(func() { conn.Close() })

	// Registration happens after the handshake; wait for it.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, id := range h.srv.ListConnectedAgents() {
			if id == h.machine.ID {
				return a
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent never registered")
	return nil
}

func (a *stubAgent) serve(chans <-chan ssh.NewChannel) {
	for nc := range chans {
		ch, reqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go a.session(ch, reqs)
	}
}

func exitStatus(n uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return b[:]
}

// session mimics the real agent: output, then exit-status, then EOF. The
// channel itself is left for the gateway to close, as the agent does.
func (a *stubAgent) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			a.mu.Lock()
			a.ptys++
			a.mu.Unlock()
			_ = req.Reply(!a.refusePTY, nil)
		case "window-change":
			a.mu.Lock()
			a.resizes++
			a.mu.Unlock()
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
		case "exec":
			var p struct{ Command, User string }
			_ = ssh.Unmarshal(req.Payload, &p)
			a.mu.Lock()
			a.execs = append(a.execs, p.Command)
			a.mu.Unlock()
			if p.Command == "refuse" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			go a.runExec(ch, p.Command, p.User)
		case "shell":
			var p struct{ User string }
			_ = ssh.Unmarshal(req.Payload, &p)
			if p.User == "nobody" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			go a.runShell(ch, p.User)
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// runExec answers by command: "no-status" never reports an exit status,
// "hang" produces nothing until the gateway closes the channel, "cat" echoes
// stdin, "chatty" sends an extra request first; anything else reports itself.
func (a *stubAgent) runExec(ch ssh.Channel, command, user string) {
	switch command {
	case "no-status":
		fmt.Fprintf(ch, "silent\n")
		_ = ch.CloseWrite()
		return
	case "hang":
		_, _ = io.Copy(io.Discard, ch)
		return
	case "cat":
		_, _ = io.Copy(ch, ch)
	case "chatty":
		_, _ = ch.SendRequest("progress@test", true, nil)
		fmt.Fprintf(ch, "chatty\n")
	default:
		fmt.Fprintf(ch, "ran %q as %s\n", command, user)
	}
	_, _ = ch.SendRequest("exit-status", true, exitStatus(5))
	_ = ch.CloseWrite()
}

// runShell greets, echoes one read, and exits 7. User "mute" never reports an
// exit status and "chatty" sends an extra request first.
func (a *stubAgent) runShell(ch ssh.Channel, user string) {
	if user == "chatty" {
		_, _ = ch.SendRequest("progress@test", true, nil)
	}
	fmt.Fprintf(ch, "shell for %s\n", user)
	buf := make([]byte, 64)
	n, _ := ch.Read(buf)
	fmt.Fprintf(ch, "echo:%s", buf[:n])
	if user != "mute" {
		_, _ = ch.SendRequest("exit-status", true, exitStatus(7))
	}
	_ = ch.CloseWrite()
}

// agentExec sends a command over an agent-initiated session, as the agent's
// heartbeat does, and returns the gateway's reply and output.
func (a *stubAgent) agentExec(t *testing.T, command string) (bool, string) {
	t.Helper()
	ch, reqs, err := a.conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go ssh.DiscardRequests(reqs)
	ok, err := ch.SendRequest("exec", true, ssh.Marshal(&struct{ Command string }{command}))
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	if ok && command == "agent-register" {
		buf := make([]byte, 64)
		n, _ := ch.Read(buf)
		out = buf[:n]
	}
	_ = ch.Close()
	return ok, string(out)
}
