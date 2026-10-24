package client

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

var quietLogger = common.NewLoggerTo(common.ERROR, io.Discard)

// writeKey writes a fresh ed25519 private key in OpenSSH format and returns
// its path and signer.
func writeKey(t *testing.T, dir string) (string, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, signer
}

// --- API stub ---------------------------------------------------------------------

// apiStub answers the Orion Belt REST routes the client uses.
type apiStub struct {
	*httptest.Server
	mu        sync.Mutex
	statuses  []string // access-request statuses returned in order, then the last repeats
	mfa       bool     // demand a TOTP code on login
	caEnabled bool
	certLine  string
	fail      map[string]int // path -> status
	paths     []string
	totpSeen  string
	lastBody  map[string]any
	created   map[string]any // body of the last access request created
}

func newAPIStub(t *testing.T) *apiStub {
	t.Helper()
	a := &apiStub{statuses: []string{"approved"}, fail: map[string]int{}}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		a.mu.Lock()
		a.paths = append(a.paths, r.Method+" "+r.URL.RequestURI())
		a.lastBody = body
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/access-requests" {
			a.created = body
		}
		code := a.fail[r.URL.Path]
		a.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"error":"stub failure"}`)
			return
		}

		switch r.URL.Path {
		case "/api/v1/public/auth/challenge":
			writeJSON(w, map[string]string{"challenge": "nonce"})
		case "/api/v1/public/login/key":
			if err := checkSignature(body); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if totp, _ := body["totp_code"].(string); a.mfa && totp == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"mfa code required","mfa_required":true}`)
				return
			}
			a.mu.Lock()
			a.totpSeen, _ = body["totp_code"].(string)
			a.mu.Unlock()
			writeJSON(w, map[string]any{"api_key": "issued", "expires_at": time.Now().Add(time.Hour)})
		case "/api/v1/public/login/password":
			writeJSON(w, map[string]any{"session_token": "sess", "expires_at": time.Now()})
		case "/api/v1/machines":
			writeJSON(w, []Machine{
				{ID: "m1", Name: "web-01", Hostname: "10.0.0.1", Port: 22, IsActive: true, LastSeenAt: ptrTime(time.Now().Add(-3 * time.Hour))},
				{ID: "m2", Name: "db-01", Hostname: "10.0.0.2", Port: 22},
			})
		case "/api/v1/access-requests":
			writeJSON(w, AccessRequest{ID: "req-1", Status: "pending"})
		case "/api/v1/access-requests/req-1":
			a.mu.Lock()
			status := a.statuses[0]
			if len(a.statuses) > 1 {
				a.statuses = a.statuses[1:]
			}
			a.mu.Unlock()
			if status == "error" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeJSON(w, AccessRequest{ID: "req-1", Status: status, ExpiresAt: ptrTime(time.Now().Add(time.Hour))})
		case "/api/v1/ssh-cert/ca":
			writeJSON(w, TrustedCA{Enabled: a.caEnabled, UserCA: "ssh-ed25519 AAA"})
		case "/api/v1/ssh-cert":
			writeJSON(w, IssuedCert{Certificate: a.certLine, Serial: "1"})
		case "/api/v1/admin/ca/export":
			writeJSON(w, TrustedCA{Enabled: true, HostCA: "ssh-ed25519 HOST"})
		case "/api/v1/admin/ssh-certificates":
			writeJSON(w, map[string]any{"certificates": []IssuedSSHCertificate{{Serial: "7"}}})
		case "/api/v1/admin/access-requests/pending":
			writeJSON(w, []AccessRequest{{ID: "req-1"}})
		case "/api/v1/auth/browser-bootstrap":
			writeJSON(w, BootstrapCode{Code: "abc"})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *apiStub) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.paths...)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func ptrTime(t time.Time) *time.Time { return &t }

func checkSignature(body map[string]any) error {
	pubLine, _ := body["public_key"].(string)
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubLine))
	if err != nil {
		return err
	}
	sigB64, _ := body["signature"].(string)
	blob, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	format, _ := body["signature_format"].(string)
	return pub.Verify([]byte("nonce"), &ssh.Signature{Format: format, Blob: blob})
}

// clientConfig points a client at the gateway stub and API stub.
func clientConfig(t *testing.T, g *gatewayStub, api *apiStub) (*common.Config, ssh.Signer) {
	t.Helper()
	dir := t.TempDir()
	keyPath, signer := writeKey(t, dir)
	cfg := &common.Config{}
	cfg.Auth.KeyFile = keyPath
	cfg.Auth.User = "alice"
	cfg.Auth.KnownHosts = filepath.Join(dir, "known_hosts")
	cfg.Auth.StrictHostKeyChecking = "ask"
	if api != nil {
		cfg.Server.APIEndpoint = api.URL
	} else {
		cfg.Server.APIEndpoint = "http://127.0.0.1:1"
	}
	if g != nil {
		cfg.Server.Host = "127.0.0.1"
		cfg.Server.Port = g.addr.Port
	}
	return cfg, signer
}

// withStdio swaps the process stdio for the duration of a call and returns
// what was written to stdout and stderr.
func withStdio(t *testing.T, stdin io.Reader, fn func()) (string, string) {
	t.Helper()
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	prevIn, prevOut, prevErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	go func() {
		if stdin != nil {
			_, _ = io.Copy(inW, stdin)
		}
		inW.Close()
	}()
	var out, errOut bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&out, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errOut, errR) }()

	fn()

	os.Stdin, os.Stdout, os.Stderr = prevIn, prevOut, prevErr
	outW.Close()
	errW.Close()
	wg.Wait()
	inR.Close()
	return out.String(), errOut.String()
}

// --- SSH sessions -------------------------------------------------------------------

func TestConnect_RunsTargetThroughGateway(t *testing.T) {
	g := startGateway(t, func(cmd string, ch ssh.Channel) uint32 {
		fmt.Fprintf(ch, "hello from %s\n", cmd)
		return 0
	})
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	var err error
	out, _ := withStdio(t, nil, func() { err = c.Connect("deploy@web-01", "alice") })
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	users, cmds, ptys := g.snapshot()
	if len(users) == 0 || users[len(users)-1] != "alice" || len(cmds) != 1 || cmds[0] != "deploy@web-01" {
		t.Errorf("gateway saw users=%v commands=%v", users, cmds)
	}
	if ptys != 0 {
		t.Errorf("requested a PTY without a terminal (%d)", ptys)
	}
	if !strings.Contains(out, "hello from deploy@web-01") {
		t.Errorf("stdout = %q", out)
	}
	if data, _ := os.ReadFile(cfg.Auth.KnownHosts); len(data) == 0 {
		t.Error("TOFU did not record the gateway host key")
	}
}

func TestConnect_DefaultUserAndExitStatus(t *testing.T) {
	cases := []struct {
		name, target, output string
		status               uint32
		wantExit             int
		wantHint             bool
	}{
		{"non-zero exit", "web-01", "boom\n", 3, 3, false},
		{"unknown default user gets a hint", "web-01", `unknown user "alice"` + "\n", 1, 0, true},
		{"explicit user gets the raw status", "alice@web-01", `unknown user "alice"` + "\n", 1, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, func(_ string, ch ssh.Channel) uint32 {
				_, _ = io.WriteString(ch.Stderr(), tc.output)
				return tc.status
			})
			cfg, _ := clientConfig(t, g, nil)
			c, _ := NewSSHClient(cfg, quietLogger)

			var err error
			withStdio(t, nil, func() { err = c.Connect(tc.target, "alice") })

			var exitErr *ExitStatusError
			switch {
			case tc.wantHint:
				if err == nil || !strings.Contains(err.Error(), "osh root@web-01") {
					t.Errorf("Connect = %v, want the choose-a-user hint", err)
				}
			case !errors.As(err, &exitErr) || exitErr.Status != tc.wantExit:
				t.Errorf("Connect = %v, want exit status %d", err, tc.wantExit)
			}
			if exitErr != nil && exitErr.Error() != fmt.Sprintf("remote exited with status %d", tc.wantExit) {
				t.Errorf("ExitStatusError.Error() = %q", exitErr.Error())
			}
		})
	}
}

func TestConnect_UsernameFallbacks(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	t.Setenv("USER", "envuser")
	withStdio(t, nil, func() { _ = c.Connect("web-01", "") })
	t.Setenv("USER", "")
	withStdio(t, nil, func() { _ = c.Connect("web-01", "") })

	users, cmds, _ := g.snapshot()
	if len(users) < 2 || users[len(users)-2] != "envuser" || users[len(users)-1] != "root" {
		t.Errorf("auth users = %v, want envuser then root", users)
	}
	if cmds[len(cmds)-1] != "@web-01" {
		t.Errorf("command with no user = %q", cmds[len(cmds)-1])
	}
}

// With a real terminal on stdin the client requests a PTY, forwards resizes
// and translates newlines for raw mode.
func TestConnect_InteractiveTerminal(t *testing.T) {
	g := startGateway(t, func(_ string, ch ssh.Channel) uint32 {
		_, _ = io.WriteString(ch, "line1\nline2\n")
		return 0
	})
	cfg, _ := clientConfig(t, g, nil)
	c, _ := NewSSHClient(cfg, quietLogger)

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 100})

	outR, outW, _ := os.Pipe()
	prevIn, prevOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = tty, outW
	err = c.Connect("root@web-01", "alice")
	os.Stdin, os.Stdout = prevIn, prevOut
	outW.Close()
	out, _ := io.ReadAll(outR)

	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if _, _, ptys := g.snapshot(); ptys != 1 {
		t.Errorf("PTY requests = %d, want 1", ptys)
	}
	if !strings.Contains(string(out), "line1\r\nline2\r\n") {
		t.Errorf("raw-mode output = %q, want CRLF line endings", out)
	}
}

func TestForwardWindowChanges_SendsResize(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	// Hold the session open until the test has seen the resize.
	g := startGateway(t, func(string, ssh.Channel) uint32 { <-release; return 0 })
	_, signer := writeKey(t, t.TempDir())
	conn, err := ssh.Dial("tcp", g.addr.String(), &ssh.ClientConfig{
		User: "alice", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	session, _ := conn.NewSession()
	defer session.Close()
	if err := session.Start("x"); err != nil {
		t.Fatal(err)
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})

	stop := forwardWindowChanges(session, int(tty.Fd()))
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	select {
	case <-g.resizedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("no window-change request reached the gateway")
	}
}

func TestConnect_SetupFailures(t *testing.T) {
	g := startGateway(t, func(string, ssh.Channel) uint32 { return 0 })
	cases := map[string]func(cfg *common.Config){
		"missing key":      func(cfg *common.Config) { cfg.Auth.KeyFile = filepath.Join(t.TempDir(), "absent") },
		"bad host CA":      func(cfg *common.Config) { cfg.Auth.HostCAPublicKey = "garbage" },
		"gateway down":     func(cfg *common.Config) { cfg.Server.Port = 1 },
		"key not accepted": func(*common.Config) { g.reject = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			g.reject = false
			cfg, _ := clientConfig(t, g, nil)
			mutate(cfg)
			c, _ := NewSSHClient(cfg, quietLogger)
			var err error
			withStdio(t, nil, func() { err = c.Connect("web-01", "alice") })
			if err == nil {
				t.Error("Connect succeeded")
			}
			s, _ := NewSCPClient(cfg, quietLogger)
			if err := s.Copy("alice", "/tmp/x", "web-01:/tmp/x", true); err == nil {
				t.Error("Copy succeeded")
			}
		})
	}
	g.reject = false
}

// --- SCP ------------------------------------------------------------------------------

func TestCopy_UploadSendsSCPStream(t *testing.T) {
	var got bytes.Buffer
	g := startGateway(t, func(cmd string, ch ssh.Channel) uint32 {
		_, _ = io.Copy(&got, ch)
		return 0
	})
	cfg, _ := clientConfig(t, g, nil)
	src := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, _ := NewSCPClient(cfg, quietLogger)
	if err := s.Copy("alice", src, "deploy@web-01:/srv/notes.txt", true); err != nil {
		t.Fatalf("upload: %v", err)
	}
	_, cmds, _ := g.snapshot()
	if len(cmds) != 1 || cmds[0] != "deploy@web-01 scp -t /srv/notes.txt" {
		t.Errorf("commands = %v", cmds)
	}
	if got.String() != "C0644 5 notes.txt\nhello\x00" {
		t.Errorf("SCP stream = %q", got.String())
	}
}

func TestCopy_UploadFailures(t *testing.T) {
	g := startGateway(t, func(_ string, ch ssh.Channel) uint32 { _, _ = io.Copy(io.Discard, ch); return 1 })
	cfg, _ := clientConfig(t, g, nil)
	s, _ := NewSCPClient(cfg, quietLogger)

	if err := s.Copy("alice", "/tmp/x", "no-colon", true); err == nil || !strings.Contains(err.Error(), "invalid destination") {
		t.Errorf("bad destination = %v", err)
	}
	if err := s.Copy("alice", filepath.Join(t.TempDir(), "absent"), "web-01:/x", true); err == nil || !strings.Contains(err.Error(), "open source file") {
		t.Errorf("missing source = %v", err)
	}
	src := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(src, []byte("x"), 0o600)
	if err := s.Copy("alice", src, "web-01:/x", true); err == nil || !strings.Contains(err.Error(), "scp upload failed") {
		t.Errorf("remote failure = %v", err)
	}
}

// scpSource plays the remote side of `scp -f`.
func scpSource(name, content string) func(string, ssh.Channel) uint32 {
	return func(_ string, ch ssh.Channel) uint32 {
		r := bufio.NewReader(ch)
		_, _ = r.ReadByte() // client ready
		fmt.Fprintf(ch, "C0644 %d %s\n", len(content), name)
		_, _ = r.ReadByte() // header acknowledged
		_, _ = io.WriteString(ch, content)
		_, _ = ch.Write([]byte{0})
		_, _ = r.ReadByte() // transfer acknowledged
		return 0
	}
}

func TestCopy_DownloadWritesFile(t *testing.T) {
	g := startGateway(t, scpSource("report.csv", "a,b\n1,2\n"))
	cfg, _ := clientConfig(t, g, nil)
	s, _ := NewSCPClient(cfg, quietLogger)

	dir := t.TempDir()
	if err := s.Copy("alice", "web-01:/var/report.csv", dir, false); err != nil {
		t.Fatalf("download into dir: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "report.csv")); string(data) != "a,b\n1,2\n" {
		t.Errorf("downloaded = %q", data)
	}
	_, cmds, _ := g.snapshot()
	if cmds[0] != "web-01 scp -f /var/report.csv" {
		t.Errorf("command = %q", cmds[0])
	}

	named := filepath.Join(dir, "renamed.csv")
	if err := s.Copy("alice", "web-01:/var/report.csv", named, false); err != nil {
		t.Fatalf("download to file: %v", err)
	}
	if _, err := os.Stat(named); err != nil {
		t.Errorf("explicit destination not written: %v", err)
	}
}

func TestCopy_DownloadFailures(t *testing.T) {
	cases := []struct {
		name   string
		handle func(string, ssh.Channel) uint32
		dest   func(t *testing.T) string
		want   string
	}{
		{"empty header", func(_ string, ch ssh.Channel) uint32 { _, _ = ch.Read(make([]byte, 1)); return 1 },
			func(t *testing.T) string { return t.TempDir() }, "empty response"},
		{"bad header", func(_ string, ch ssh.Channel) uint32 {
			_, _ = ch.Read(make([]byte, 1))
			_, _ = io.WriteString(ch, "E\n")
			return 1
		}, func(t *testing.T) string { return t.TempDir() }, "parse SCP header"},
		{"unwritable destination", scpSource("f", "x"),
			func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "f") }, "create local file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := startGateway(t, tc.handle)
			cfg, _ := clientConfig(t, g, nil)
			s, _ := NewSCPClient(cfg, quietLogger)
			if err := s.Copy("alice", "web-01:/f", tc.dest(t), false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Copy = %v, want %q", err, tc.want)
			}
		})
	}

	s, _ := NewSCPClient(&common.Config{}, quietLogger)
	if err := s.Copy("alice", "no-colon", "/tmp", false); err == nil || !strings.Contains(err.Error(), "invalid source") {
		t.Errorf("bad source = %v", err)
	}
}

func TestSplitMachinePathAndExpandLocalPath(t *testing.T) {
	cases := map[string][]string{
		"web:/etc/hosts":      {"web", "/etc/hosts"},
		"root@web:/etc/hosts": {"root@web", "/etc/hosts"},
		"plain":               {"plain"},
		"a:b:c":               {"a", "b:c"},
	}
	for in, want := range cases {
		if got := splitMachinePath(in); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("splitMachinePath(%q) = %v, want %v", in, got, want)
		}
	}

	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{".": wd, "./": wd, "~/x": home + "/x", "/abs": "/abs"} {
		if got := expandLocalPath(in); got != want {
			t.Errorf("expandLocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- REST flows -----------------------------------------------------------------------

func TestListMachines_PrintsTable(t *testing.T) {
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)
	c, _ := NewSSHClient(cfg, quietLogger)

	var err error
	out, _ := withStdio(t, nil, func() { err = c.ListMachines() })
	if err != nil {
		t.Fatalf("ListMachines: %v", err)
	}
	for _, want := range []string{"web-01", "10.0.0.1:22", "online", "3h ago", "db-01", "offline", "never"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestListMachines_EmptyAndErrors(t *testing.T) {
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)
	c, _ := NewSSHClient(cfg, quietLogger)

	api.fail["/api/v1/machines"] = http.StatusForbidden
	if err := c.ListMachines(); err == nil || !strings.Contains(err.Error(), "failed to list machines") {
		t.Errorf("forbidden = %v", err)
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/public/auth/challenge":
			writeJSON(w, map[string]string{"challenge": "nonce"})
		case "/api/v1/public/login/key":
			writeJSON(w, map[string]string{"api_key": "k"})
		default:
			_, _ = io.WriteString(w, "[]")
		}
	}))
	defer empty.Close()
	cfg.Server.APIEndpoint = empty.URL
	out, _ := withStdio(t, nil, func() { _ = c.ListMachines() })
	if !strings.Contains(out, "No machines available.") {
		t.Errorf("empty list output = %q", out)
	}

	cfg.Auth.KeyFile = filepath.Join(t.TempDir(), "absent")
	if err := c.ListMachines(); err == nil || !strings.Contains(err.Error(), "failed to get API client") {
		t.Errorf("no key = %v", err)
	}
}

func fastPolling(t *testing.T, timeout time.Duration) {
	t.Helper()
	prevI, prevT := approvalPollInterval, approvalTimeout
	approvalPollInterval, approvalTimeout = time.Millisecond, timeout
	t.Cleanup(func() { approvalPollInterval, approvalTimeout = prevI, prevT })
}

func TestRequestAccess_Outcomes(t *testing.T) {
	cases := []struct {
		name     string
		statuses []string
		wantErr  string
	}{
		{"approved after polling through errors", []string{"error", "pending", "approved"}, ""},
		{"rejected", []string{"pending", "rejected"}, "rejected"},
		{"never decided", []string{"pending"}, "approval timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastPolling(t, 200*time.Millisecond)
			api := newAPIStub(t)
			api.statuses = tc.statuses
			cfg, _ := clientConfig(t, nil, api)
			c, _ := NewSSHClient(cfg, quietLogger)

			var err error
			out, _ := withStdio(t, nil, func() { err = c.RequestAccess("deploy@web-01", "hotfix", 600) })
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("RequestAccess = %v", err)
				}
				if !strings.Contains(out, "Access approved") || !strings.Contains(out, "Access expires at") {
					t.Errorf("output = %q", out)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("RequestAccess = %v, want %q", err, tc.wantErr)
			}
			if !strings.Contains(out, "Request ID: req-1") {
				t.Errorf("output lacks the request id: %q", out)
			}
		})
	}
}

func TestRequestAccess_RequestBodyAndDefaults(t *testing.T) {
	fastPolling(t, time.Second)
	api := newAPIStub(t)
	cfg, _ := clientConfig(t, nil, api)
	c, _ := NewSSHClient(cfg, quietLogger)

	t.Setenv("USER", "")
	withStdio(t, nil, func() {
		if err := c.RequestAccess("web-01", "check logs", 60); err != nil {
			t.Errorf("RequestAccess: %v", err)
		}
	})
	paths := api.seen()
	var created bool
	for _, p := range paths {
		created = created || p == "POST /api/v1/access-requests"
	}
	if !created {
		t.Fatalf("no access request created: %v", paths)
	}

	api.fail["/api/v1/access-requests"] = http.StatusBadRequest
	withStdio(t, nil, func() {
		if err := c.RequestAccess("web-01", "x", 60); err == nil || !strings.Contains(err.Error(), "failed to create access request") {
			t.Errorf("create failure = %v", err)
		}
		if err := c.RequestAccess("ghost", "x", 60); err == nil || !strings.Contains(err.Error(), "machine not found") {
			t.Errorf("unknown machine = %v", err)
		}
	})
	cfg.Auth.KeyFile = filepath.Join(t.TempDir(), "absent")
	if err := c.RequestAccess("web-01", "x", 60); err == nil || !strings.Contains(err.Error(), "failed to get API client") {
		t.Errorf("no key = %v", err)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		5 * time.Minute:  "5m ago",
		3 * time.Hour:    "3h ago",
		50 * time.Hour:   "2d ago",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
