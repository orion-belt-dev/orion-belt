package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/ca"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// withAgentBridge makes the fake bridge resolve the env's machine and answer
// execs with handle.
func (e *env) withAgentBridge(handle func(cmd string, stdin []byte) (string, uint32)) {
	e.bridge.mu.Lock()
	defer e.bridge.mu.Unlock()
	e.bridge.machines = map[string]*common.Machine{e.machine.Name: e.machine}
	e.bridge.handle = handle
}

func (e *env) grant(user *common.User, remote string) {
	e.t.Helper()
	p := common.NewPermission(user.ID, e.machine.ID, "ssh", []string{remote}, e.admin.ID, nil)
	if err := e.store.CreatePermission(context.Background(), p); err != nil {
		e.t.Fatal(err)
	}
}

// dialWS opens a WebSocket against a live copy of the router.
func (e *env) dialWS(path, key string) (*websocket.Conn, *http.Response, error) {
	e.t.Helper()
	srv := httptest.NewServer(e.srv.Router())
	e.t.Cleanup(srv.Close)
	hdr := http.Header{}
	if key != "" {
		hdr.Set("X-API-Key", key)
	}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+path, hdr)
	if err == nil {
		e.t.Cleanup(func() { conn.Close() })
	}
	return conn, resp, err
}

func readUntil(t *testing.T, conn *websocket.Conn, want string) string {
	t.Helper()
	var got strings.Builder
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !strings.Contains(got.String(), want) {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q, got %q: %v", want, got.String(), err)
		}
		got.Write(data)
	}
	return got.String()
}

// --- web terminal -----------------------------------------------------------------------

func TestTerminalWS_ShellSessionIsRecordedAndAudited(t *testing.T) {
	e := newEnv(t, nil)
	e.withAgentBridge(nil)
	e.grant(e.alice, "deploy")

	conn, _, err := e.dialWS("/api/v1/terminal/ws?machine=web-01&user=deploy", e.aliceKey)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readUntil(t, conn, "welcome")
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`))
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte("ls"))
	readUntil(t, conn, "echo:ls")
	conn.Close()

	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	var sessions []*common.Session
	for time.Now().Before(deadline) {
		sessions, _ = e.store.ListUserSessions(ctx, e.alice.ID, 10, 0)
		if len(sessions) == 1 && sessions[0].Status == "completed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(sessions) != 1 || sessions[0].Source != "web" || sessions[0].Status != "completed" {
		t.Fatalf("web sessions = %+v", sessions)
	}
	if !e.audited("session.web_terminal.start") {
		t.Error("terminal start not audited")
	}
}

func TestTerminalWS_Refusals(t *testing.T) {
	e := newEnv(t, nil)
	e.withAgentBridge(nil)
	e.grant(e.alice, "root")

	cases := []struct {
		name, path string
		code       int
	}{
		{"no machine", "/api/v1/terminal/ws", 400},
		{"unknown machine", "/api/v1/terminal/ws?machine=db-99", 404},
		{"no permission for user", "/api/v1/terminal/ws?machine=web-01&user=deploy", 403},
	}
	for _, tc := range cases {
		e.expect(e.do("GET", tc.path, e.aliceKey, nil), tc.code)
	}

	// An upgrade that never happens still closes the session record.
	e.expect(e.do("GET", "/api/v1/terminal/ws?machine=web-01", e.aliceKey, nil), 400)
	if s, _ := e.store.ListUserSessions(context.Background(), e.alice.ID, 10, 0); len(s) != 1 || s[0].Status != "completed" {
		t.Errorf("session after failed upgrade = %+v", s)
	}

	e.bridge.openErr = errors.New("agent not connected")
	conn, _, err := e.dialWS("/api/v1/terminal/ws?machine=web-01", e.aliceKey)
	if err != nil {
		t.Fatal(err)
	}
	if msg := readUntil(t, conn, "error:"); !strings.Contains(msg, "agent not connected") {
		t.Errorf("message = %q", msg)
	}

	e.srv.recorder = nil
	e.expect(e.do("GET", "/api/v1/terminal/ws?machine=web-01", e.aliceKey, nil), 503)
	e.srv.terminalBridge = nil
	e.expect(e.do("GET", "/api/v1/terminal/ws?machine=web-01", e.aliceKey, nil), 503)
}

// --- live session watch -----------------------------------------------------------------

func TestSessionWatchWS(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	sess := common.NewSession(e.alice.ID, e.machine.ID, "root", "/r")
	_ = e.store.CreateSession(ctx, sess)
	done := common.NewSession(e.alice.ID, e.machine.ID, "root", "/r")
	done.Status = "completed"
	_ = e.store.CreateSession(ctx, done)

	path := "/api/v1/sessions/" + sess.ID + "/watch"
	e.expect(e.do("GET", "/api/v1/sessions/missing/watch", e.adminKey, nil), 404)
	e.expect(e.do("GET", "/api/v1/sessions/"+done.ID+"/watch", e.adminKey, nil), 409)
	e.expect(e.do("GET", path, e.bobKey, nil), 404)
	e.expect(e.do("GET", path, e.adminKey, nil), 409) // not being recorded here

	rec, err := e.recorder.StartRecording(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := e.dialWS(path, e.aliceKey)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// The subscription is set up after the upgrade; keep writing until it lands.
	got := make(chan string, 1)
	go func() {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, _ := conn.ReadMessage()
		got <- string(data)
	}()
	deadline := time.After(5 * time.Second)
	for {
		_ = rec.Write([]byte("live output"))
		select {
		case msg := <-got:
			if msg != "live output" {
				t.Errorf("watched %q", msg)
			}
			_ = e.recorder.StopRecording(sess.ID)
			if !e.audited("session.watch.start") {
				t.Error("watch not audited")
			}
			e.srv.recorder = nil
			e.expect(e.do("GET", path, e.adminKey, nil), 503)
			return
		case <-deadline:
			t.Fatal("no output reached the watcher")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// --- file browser -----------------------------------------------------------------------

func TestFiles_ThroughAgent(t *testing.T) {
	e := newEnv(t, nil)
	e.grant(e.alice, "root")
	var seen []string
	e.withAgentBridge(func(cmd string, _ []byte) (string, uint32) {
		seen = append(seen, cmd)
		switch {
		case strings.HasPrefix(cmd, "python3"):
			return `{"path":"/etc","entries":[{"name":"hosts"}]}`, 0
		case strings.HasPrefix(cmd, "base64 "):
			return base64.StdEncoding.EncodeToString([]byte("127.0.0.1 localhost")), 0
		default:
			return "", 0
		}
	})

	r := e.do("GET", "/api/v1/files/list?machine=web-01&path=/etc", e.aliceKey, nil)
	e.expect(r, 200)
	if !strings.Contains(string(r.body), `"hosts"`) {
		t.Errorf("list = %s", r.body)
	}

	r = e.do("GET", "/api/v1/files/download?machine=web-01&path=/etc/hosts", e.aliceKey, nil)
	e.expect(r, 200)
	if string(r.body) != "127.0.0.1 localhost" || !strings.Contains(r.hdr.Get("Content-Disposition"), "filename=hosts") {
		t.Errorf("download = %q %q", r.body, r.hdr.Get("Content-Disposition"))
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("machine", "web-01")
	_ = mw.WriteField("path", "/tmp/it's.txt")
	fw, _ := mw.CreateFormFile("file", "x.txt")
	_, _ = fw.Write([]byte("payload"))
	_ = mw.Close()
	r = e.do("POST", "/api/v1/files/upload", e.aliceKey, body.String(), "Content-Type", mw.FormDataContentType())
	e.expect(r, 200)
	if r.obj(t)["size"] != float64(7) {
		t.Errorf("upload = %s", r.body)
	}
	e.expect(e.do("POST", "/api/v1/files/upload", e.aliceKey, "", "Content-Type", "multipart/form-data; boundary=x"), 400)

	e.expect(e.do("POST", "/api/v1/files/mkdir", e.aliceKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/files/mkdir", e.aliceKey, map[string]any{"machine": "web-01", "path": "/tmp/d"}), 200)
	e.expect(e.do("DELETE", "/api/v1/files?machine=web-01&path=/tmp/d", e.aliceKey, nil), 200)

	// Paths reach the agent shell-quoted, so a hostile name cannot break out.
	var upload string
	for _, c := range seen {
		if strings.Contains(c, "base64 -d") {
			upload = c
		}
	}
	if !strings.Contains(upload, `'/tmp/it'\''s.txt'`) {
		t.Errorf("upload command = %q, want the quoted path", upload)
	}
}

func TestFiles_Failures(t *testing.T) {
	e := newEnv(t, nil)
	e.grant(e.alice, "root")

	// python3 missing on the target: the listing falls back to ls.
	e.withAgentBridge(func(cmd string, _ []byte) (string, uint32) {
		if strings.HasPrefix(cmd, "python3") {
			return rejectExec, 0
		}
		if strings.HasPrefix(cmd, "base64 ") {
			return "%%% not base64", 0
		}
		return "total 0\n", 0
	})
	r := e.do("GET", "/api/v1/files/list?machine=web-01", e.aliceKey, nil)
	e.expect(r, 200)
	if r.obj(t)["raw"] != "total 0\n" {
		t.Errorf("fallback listing = %s", r.body)
	}
	e.expect(e.do("GET", "/api/v1/files/download?machine=web-01&path=/x", e.aliceKey, nil), 500)

	e.withAgentBridge(func(string, []byte) (string, uint32) { return rejectExec, 0 })
	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v1/files/list?machine=web-01"},
		{"GET", "/api/v1/files/download?machine=web-01&path=/x"},
		{"DELETE", "/api/v1/files?machine=web-01&path=/x"},
		{"GET", "/api/v1/files/list?machine=ghost"},
		{"GET", "/api/v1/files/list?machine=web-01&user=deploy"},
	} {
		e.expect(e.do(req.method, req.path, e.aliceKey, nil), 502)
	}
	e.expect(e.do("POST", "/api/v1/files/mkdir", e.aliceKey, map[string]any{"machine": "web-01", "path": "/x"}), 502)

	e.bridge.openErr = errors.New("agent offline")
	if r := e.do("GET", "/api/v1/files/list?machine=web-01", e.aliceKey, nil); !strings.Contains(string(r.body), "agent offline") {
		t.Errorf("open error = %s", r.body)
	}
	e.srv.terminalBridge = nil
	if r := e.do("DELETE", "/api/v1/files?machine=web-01&path=/x", e.aliceKey, nil); !strings.Contains(string(r.body), "bridge unavailable") {
		t.Errorf("no bridge = %s", r.body)
	}
}

// --- SSH keys --------------------------------------------------------------------------

func TestSSHKeys_OwnKeys(t *testing.T) {
	e := newEnv(t, nil)
	e.expect(e.do("POST", "/api/v1/ssh-keys", e.aliceKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/ssh-keys", e.aliceKey, map[string]any{"name": "x", "public_key": "garbage"}), 400)
	r := e.do("POST", "/api/v1/ssh-keys", e.aliceKey, map[string]any{"name": "laptop", "public_key": testPubKey})
	e.expect(r, 201)
	key := r.obj(t)
	if key["key_type"] != ssh.KeyAlgoED25519 {
		t.Errorf("key = %v", key)
	}
	if l := e.do("GET", "/api/v1/ssh-keys", e.aliceKey, nil).list(t); len(l) != 1 {
		t.Errorf("alice keys = %d", len(l))
	}
	if l := e.do("GET", "/api/v1/ssh-keys", e.bobKey, nil).list(t); len(l) != 0 {
		t.Errorf("bob sees %d keys", len(l))
	}
	e.expect(e.do("DELETE", "/api/v1/ssh-keys/"+key["id"].(string), e.aliceKey, nil), 200)
	e.expect(e.do("DELETE", "/api/v1/ssh-keys/"+key["id"].(string), e.aliceKey, nil), 404)
}

// --- SSH certificate authority ----------------------------------------------------------

func TestSSHCA_Endpoints(t *testing.T) {
	e := newEnv(t, nil)
	if r := e.do("GET", "/api/v1/ssh-cert/ca", e.aliceKey, nil).obj(t); r["enabled"] != false {
		t.Errorf("without CA = %v", r)
	}
	e.expect(e.do("POST", "/api/v1/ssh-cert", e.aliceKey, map[string]any{"public_key": testPubKey}), 404)
	e.expect(e.do("POST", "/api/v1/admin/ssh-certificates/1/revoke", e.adminKey, nil), 404)
	var empty struct{ Certificates []any }
	e.do("GET", "/api/v1/admin/ssh-certificates", e.adminKey, nil).json(t, &empty)
	if empty.Certificates == nil || len(empty.Certificates) != 0 {
		t.Errorf("certificates without CA = %v", empty.Certificates)
	}

	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, e.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.ca = authority

	trust := e.do("GET", "/api/v1/ssh-cert/ca", e.aliceKey, nil).obj(t)
	if trust["enabled"] != true || trust["user_ca"] == "" || trust["host_ca"] == "" {
		t.Errorf("trust = %v", trust)
	}
	e.expect(e.do("GET", "/api/v1/admin/ca/export", e.aliceKey, nil), 403)
	e.expect(e.do("GET", "/api/v1/admin/ca/export", e.adminKey, nil), 200)

	e.expect(e.do("POST", "/api/v1/ssh-cert", e.aliceKey, `{`), 400)
	e.expect(e.do("POST", "/api/v1/ssh-cert", e.aliceKey, map[string]any{"public_key": "garbage"}), 400)
	r := e.do("POST", "/api/v1/ssh-cert", e.aliceKey, map[string]any{"public_key": testPubKey, "ttl_hours": 2})
	e.expect(r, 200)
	issued := r.obj(t)
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(issued["certificate"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	if cert := pub.(*ssh.Certificate); cert.KeyId != "alice" || cert.ValidPrincipals[0] != "alice" {
		t.Errorf("cert key id %q principals %v", cert.KeyId, cert.ValidPrincipals)
	}
	_ = e.do("POST", "/api/v1/ssh-cert", e.bobKey, map[string]any{"public_key": testPubKey})

	list := func(q string) int {
		var out struct{ Certificates []map[string]any }
		e.do("GET", "/api/v1/admin/ssh-certificates"+q, e.adminKey, nil).json(t, &out)
		return len(out.Certificates)
	}
	if n := list(""); n != 2 {
		t.Errorf("all certs = %d", n)
	}
	if n := list("?subject_id=" + e.alice.ID + "&cert_type=user"); n != 1 {
		t.Errorf("alice's certs = %d", n)
	}
	if n := list("?limit=1&offset=1"); n != 1 {
		t.Errorf("paged certs = %d", n)
	}

	serial := issued["serial"].(string)
	e.expect(e.do("POST", "/api/v1/admin/ssh-certificates/"+serial+"/revoke", e.adminKey, map[string]any{"reason": "lost"}), 200)
	e.expect(e.do("POST", "/api/v1/admin/ssh-certificates/"+serial+"/revoke", e.adminKey, nil), 404)
	if n := list("?active=false"); n != 1 {
		t.Errorf("inactive certs = %d", n)
	}
	if n := list("?active=true"); n != 1 {
		t.Errorf("active certs = %d", n)
	}
	if !e.audited("ssh_cert.revoke") {
		t.Error("revocation not audited")
	}
}

// --- agent install script ---------------------------------------------------------------

func TestAgentInstallScript(t *testing.T) {
	e := newEnv(t, nil)
	path := "/api/v1/admin/agents/install-script"
	body := func(kv ...any) map[string]any {
		m := map[string]any{"name": "app-01", "os": "ubuntu", "gateway_host": "gw.example.com", "package_base_url": "https://pkg.example.com/"}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	e.expect(e.do("POST", path, e.adminKey, `{`), 400)
	e.expect(e.do("POST", path, e.adminKey, body("name", "bad name")), 400)
	e.expect(e.do("POST", path, e.adminKey, body("os", "windows")), 400)
	e.expect(e.do("POST", path, e.adminKey, body("package_base_url", "   ")), 400)
	e.expect(e.do("POST", path, e.adminKey, body("gateway_host", "   ")), 400)
	e.expect(e.do("POST", path, e.adminKey, body("name", "web-01")), 409)
	e.expect(e.do("POST", path, e.aliceKey, body()), 403)

	r := e.do("POST", path, e.adminKey, body("tags", map[string]string{"env": "prod"}, "version", "v1.2.3"))
	e.expect(r, 201)
	var resp AgentInstallScriptResponse
	r.json(t, &resp)
	if resp.UserID == "" || !strings.Contains(resp.Script, "1.2.3") || resp.Filename != "orion-belt-install-app-01.sh" {
		t.Errorf("legacy install = %+v", resp)
	}
	m, err := e.store.GetMachineByName(context.Background(), "app-01")
	if err != nil || m.Tags["os"] != "debian" || m.Tags["env"] != "prod" || m.Port != 22 {
		t.Errorf("machine = %+v, %v", m, err)
	}

	authority, err := ca.New(common.SSHCAConfig{Enabled: true, MasterKey: "0123456789abcdef0123456789abcdef"}, e.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.srv.ca = authority
	for _, osName := range []string{"rocky", "opensuse", "generic", "alpine"} {
		r := e.do("POST", path, e.adminKey, body("name", "ca-"+osName, "os", osName, "port", 2200, "gateway_port", 2022, "hostname", "h"))
		e.expect(r, 201)
		var got AgentInstallScriptResponse
		r.json(t, &got)
		if got.UserID != "" || !strings.Contains(got.Script, "cert") {
			t.Errorf("%s CA install = %+v", osName, got.Message)
		}
	}
	if !e.audited("agent.install_script") {
		t.Error("install script not audited")
	}
}
