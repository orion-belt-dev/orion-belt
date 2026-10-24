package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/kube"
)

// control runs an orion:* command and returns its JSON reply and status.
func control(t *testing.T, conn *gossh.ServerConn, cmd string) (map[string]any, int) {
	t.Helper()
	res := session(t, conn, "", execReq(cmd, ""))
	var m map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &m); err != nil {
		t.Fatalf("%s: reply %q is not JSON: %v", cmd, res.stdout, err)
	}
	return m, res.status
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestReloadAppliesLiveSettings(t *testing.T) {
	a, conn, _ := connected(t)
	if m, status := control(t, conn, "orion:reload"); status != 1 || !strings.Contains(m["error"].(string), "config path unknown") {
		t.Fatalf("reload without a path = %v", m)
	}

	path := filepath.Join(t.TempDir(), "agent.yaml")
	next := *a.config
	next.Agent.Tags = map[string]string{"env": "prod", "team": "payments"}
	next.Agent.Tunnels = map[string]string{"rdp": "10.0.0.5:3389"}
	off := false
	next.Agent.RemoteUpdate = &off
	if err := common.SaveConfig(path, &next); err != nil {
		t.Fatal(err)
	}
	a.SetConfigPath(path)

	m, status := control(t, conn, "orion:reload")
	if status != 0 || m["ok"] != true {
		t.Fatalf("reload = %v", m)
	}
	applied := strings.Join(strs(m["applied"]), ",")
	for _, want := range []string{"agent.tags", "agent.tunnels", "agent.remote_update"} {
		if !strings.Contains(applied, want) {
			t.Errorf("applied = %s, missing %s", applied, want)
		}
	}
	if len(strs(append([]any{}, m["restart_required"].([]any)...))) != 0 {
		t.Errorf("restart_required = %v", m["restart_required"])
	}
	if got := a.agentConfig().TunnelAddress("rdp"); got != "10.0.0.5:3389" {
		t.Errorf("tunnel address after reload = %q", got)
	}
	if a.remoteUpdateAllowed() {
		t.Error("remote_update: false not applied")
	}

	// Identity and connection changes are reported, not applied.
	next.Agent.Name = "renamed"
	next.Server.Port = next.Server.Port + 1
	next.Auth.KeyFile = "/elsewhere"
	next.Agent.Kubernetes.Enabled = true
	if err := common.SaveConfig(path, &next); err != nil {
		t.Fatal(err)
	}
	m, _ = control(t, conn, "orion:reload")
	restart := strings.Join(strs(m["restart_required"]), ",")
	for _, want := range []string{"server", "auth", "agent.name", "agent.kubernetes.enabled"} {
		if !strings.Contains(restart, want) {
			t.Errorf("restart_required = %s, missing %s", restart, want)
		}
	}
	if a.agentConfig().Name != "agent-1" {
		t.Error("agent name changed without a restart")
	}

	// A broken file leaves the running config alone.
	if err := os.WriteFile(path, []byte("agent: [not, a, map"), 0o600); err != nil {
		t.Fatal(err)
	}
	if m, status := control(t, conn, "orion:reload"); status != 1 || m["ok"] != false {
		t.Errorf("reload of a broken file = %v", m)
	}
	if got := a.agentConfig().TunnelAddress("rdp"); got != "10.0.0.5:3389" {
		t.Errorf("broken reload changed the config: %q", got)
	}
}

func TestReloadResetsKubernetesClient(t *testing.T) {
	a, _, _, api := kubeConnected(t)
	if _, err := a.kubeClient(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent.yaml")
	next := *a.config
	next.Agent.Kubernetes.APIServer = api.URL + "/" // a changed setting
	if err := common.SaveConfig(path, &next); err != nil {
		t.Fatal(err)
	}
	a.SetConfigPath(path)
	applied, _, err := a.reload()
	if err != nil || !strings.Contains(strings.Join(applied, ","), "agent.kubernetes") {
		t.Fatalf("reload = %v, %v", applied, err)
	}
	a.kube.mu.Lock()
	cached := a.kube.client
	a.kube.mu.Unlock()
	if cached != nil {
		t.Error("kubernetes client kept after its settings changed")
	}
}

// A drain refuses new shells, commands and tunnels, lets running ones finish
// and keeps answering control commands; undrain resumes.
func TestDrainRefusesNewWorkAndWaits(t *testing.T) {
	a, conn, _ := connected(t)
	a.config.Agent.Tunnels = map[string]string{common.AccessTypeRDP: echoServer(t)}

	// A tunnel in progress.
	running, err := openTunnel(conn, common.AccessTypeRDP)
	if err != nil {
		t.Fatal(err)
	}
	if m, status := control(t, conn, "orion:drain 60"); status != 0 || m["draining"] != true || m["active"] != float64(1) {
		t.Fatalf("drain = %v", m)
	}

	_, err = openTunnel(conn, common.AccessTypeRDP)
	var oe *gossh.OpenChannelError
	if !errors.As(err, &oe) || oe.Reason != gossh.ConnectionFailed || !strings.Contains(oe.Message, "draining") {
		t.Errorf("new tunnel while draining = %v", err)
	}
	res := session(t, conn, "", request{"exec", gossh.Marshal(&struct{ Command, User string }{"true", ""}), true, false})
	if !strings.Contains(res.stderr, "draining") {
		t.Errorf("exec while draining: stderr %q", res.stderr)
	}
	if m, _ := control(t, conn, "orion:status"); m["status"] != "draining" || m["active"] != float64(1) {
		t.Errorf("status while draining = %v", m)
	}

	// The running tunnel still works.
	_, _ = running.Write([]byte("still here"))
	_ = running.CloseWrite()
	if got, _ := io.ReadAll(running); string(got) != "still here" {
		t.Errorf("running tunnel = %q", got)
	}
	running.Close()
	eventuallyAgent(t, "the tunnel to end", func() bool { _, n := a.workStatus(); return n == 0 })

	if m, _ := control(t, conn, "orion:undrain"); m["draining"] != false {
		t.Errorf("undrain = %v", m)
	}
	ch, err := openTunnel(conn, common.AccessTypeRDP)
	if err != nil {
		t.Fatalf("tunnel after undrain: %v", err)
	}
	ch.Close()

	for _, bad := range []string{"orion:drain soon", "orion:drain 1 2"} {
		if m, status := control(t, conn, bad); status != 1 || m["ok"] != false {
			t.Errorf("%s = %v", bad, m)
		}
	}
}

// What is still running when the drain times out is closed.
func TestDrainTimeoutClosesStragglers(t *testing.T) {
	a, conn, _ := connected(t)
	a.config.Agent.Tunnels = map[string]string{common.AccessTypeRDP: echoServer(t)}
	ch, err := openTunnel(conn, common.AccessTypeRDP)
	if err != nil {
		t.Fatal(err)
	}
	eventuallyAgent(t, "the tunnel to register", func() bool { _, n := a.workStatus(); return n == 1 })
	done := a.drain(50 * time.Millisecond)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain never finished")
	}
	if _, err := io.ReadAll(ch); err != nil {
		t.Errorf("straggler read = %v", err)
	}
	// Draining with nothing running finishes at once.
	a.undrain()
	select {
	case <-a.drain(time.Hour):
	case <-time.After(time.Second):
		t.Error("idle drain waited")
	}
}

func TestKubeProxyRefusedWhileDraining(t *testing.T) {
	a, conn, _, _ := kubeConnected(t)
	a.drain(time.Hour)
	data, _ := json.Marshal(kube.Request{Op: kube.OpProxy, User: "alice"})
	_, _, err := conn.OpenChannel(kube.AgentChannelType, data)
	var oe *gossh.OpenChannelError
	if !errors.As(err, &oe) || !strings.Contains(oe.Message, "draining") {
		t.Errorf("kubectl while draining = %v", err)
	}
	// Binding management carries on.
	if _, err := kubeOp(t, conn, kube.Request{Op: kube.OpList}); err != nil {
		t.Errorf("list while draining = %v", err)
	}
}

// fakeExecutable points agentExecutable at a temp file and records restarts.
func fakeExecutable(t *testing.T) (exe string, restarted chan struct{}) {
	t.Helper()
	exe = filepath.Join(t.TempDir(), "orion-belt-agent")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	restarted = make(chan struct{}, 1)
	prevExe, prevRestart := agentExecutable, restartAgent
	agentExecutable = func() (string, error) { return exe, nil }
	restartAgent = func(*Agent) { restarted <- struct{}{} }
	t.Cleanup(func() { agentExecutable, restartAgent = prevExe, prevRestart })
	return exe, restarted
}

func serveUpdate(t *testing.T, body []byte) (url string, sum string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	prev := updateHTTPClient
	updateHTTPClient = srv.Client()
	updateHTTPClient.Timeout = 10 * time.Second
	t.Cleanup(func() { updateHTTPClient = prev })
	h := sha256.Sum256(body)
	return srv.URL + "/agent", hex.EncodeToString(h[:])
}

func TestUpdateVerifiesStagesAndRestarts(t *testing.T) {
	a, conn, _ := connected(t)
	exe, restarted := fakeExecutable(t)
	url, sum := serveUpdate(t, []byte("new binary"))

	wrong := strings.Repeat("0", 64)
	for _, bad := range []string{
		"orion:update",
		"orion:update http://insecure/agent " + sum,
		"orion:update " + url + " nothex",
		"orion:update " + url + " " + sum + " never",
		"orion:update " + url + " " + wrong, // checksum mismatch
		"orion:update " + strings.Replace(url, "/agent", "/missing", 1) + " " + sum,
	} {
		if m, status := control(t, conn, bad); status != 1 || m["ok"] != false {
			t.Errorf("%q = %v", bad, m)
		}
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("a rejected update touched the binary")
	}

	m, status := control(t, conn, "orion:update "+url+" "+sum+" 5")
	if status != 0 || m["ok"] != true {
		t.Fatalf("update = %v", m)
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart after the update")
	}
	if got, _ := os.ReadFile(exe); string(got) != "new binary" {
		t.Errorf("binary after update = %q", got)
	}
	if got, _ := os.ReadFile(exe + ".old"); string(got) != "old binary" {
		t.Errorf("previous binary kept as %q", got)
	}
	if info, _ := os.Stat(exe); info.Mode().Perm()&0o100 == 0 {
		t.Error("updated binary is not executable")
	}
	if draining, _ := a.workStatus(); !draining {
		t.Error("agent took new work while restarting into the update")
	}
}

func TestUpdateFromReleaseArchive(t *testing.T) {
	_, conn, _ := connected(t)
	exe, restarted := fakeExecutable(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Real release archives carry an init script with the agent's name
	// before the binary; it must be skipped.
	for _, f := range []struct{ name, body string }{
		{"LICENSE", "license"},
		{"packaging/sysv/orion-belt-agent", "#!/bin/sh init script"},
		{"orion-belt-agent", "archived binary"},
	} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(f.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	url, sum := serveUpdate(t, buf.Bytes())

	if m, status := control(t, conn, "orion:update "+url+" "+sum+" 0"); status != 0 {
		t.Fatalf("update = %v", m)
	}
	<-restarted
	if got, _ := os.ReadFile(exe); string(got) != "archived binary" {
		t.Errorf("binary = %q", got)
	}

	// An archive without the agent is refused.
	var empty bytes.Buffer
	gz = gzip.NewWriter(&empty)
	tw = tar.NewWriter(gz)
	_ = tw.Close()
	_ = gz.Close()
	url, sum = serveUpdate(t, empty.Bytes())
	if m, status := control(t, conn, "orion:update "+url+" "+sum); status != 1 || !strings.Contains(m["error"].(string), "no orion-belt-agent") {
		t.Errorf("empty archive = %v", m)
	}
}

func TestUpdateCanBeDisabled(t *testing.T) {
	a, conn, _ := connected(t)
	off := false
	a.config.Agent.RemoteUpdate = &off
	if m, status := control(t, conn, "orion:update https://x/agent "+strings.Repeat("a", 64)); status != 1 || !strings.Contains(m["error"].(string), "disabled") {
		t.Errorf("update with remote_update off = %v", m)
	}
}

func eventuallyAgent(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestUpdateFailurePaths(t *testing.T) {
	a, _, _ := connected(t)
	exe, restarted := fakeExecutable(t)

	// Too big.
	url, sum := serveUpdate(t, bytes.Repeat([]byte("x"), 64))
	prev := maxUpdateBytes
	maxUpdateBytes = 16
	_, _, err := stageUpdate(context.Background(), updateRequest{URL: url, SHA256: sum})
	maxUpdateBytes = prev
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized download = %v", err)
	}

	// Looks like gzip, is not.
	url, sum = serveUpdate(t, []byte{0x1f, 0x8b, 0, 1, 2})
	if _, _, err := stageUpdate(context.Background(), updateRequest{URL: url, SHA256: sum}); err == nil || !strings.Contains(err.Error(), "archive") {
		t.Errorf("corrupt archive = %v", err)
	}

	// Nowhere to stage it.
	url, sum = serveUpdate(t, []byte("new"))
	agentExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "gone", "agent"), nil }
	if _, _, err := stageUpdate(context.Background(), updateRequest{URL: url, SHA256: sum}); err == nil || !strings.Contains(err.Error(), "stage") {
		t.Errorf("unwritable stage = %v", err)
	}
	agentExecutable = func() (string, error) { return "", errors.New("no executable") }
	if _, _, err := stageUpdate(context.Background(), updateRequest{URL: url, SHA256: sum}); err == nil {
		t.Error("missing executable path accepted")
	}

	// An install that cannot swap the binary keeps the old one, takes work
	// again and does not restart.
	a.installUpdate(filepath.Join(t.TempDir(), "missing.new"), exe, 0)
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Errorf("failed install changed the binary: %q", got)
	}
	if draining, _ := a.workStatus(); draining {
		t.Error("still draining after a failed install")
	}
	select {
	case <-restarted:
		t.Error("restarted after a failed install")
	default:
	}
}

func TestMapsEqual(t *testing.T) {
	cases := []struct {
		a, b map[string]string
		want bool
	}{
		{nil, map[string]string{}, true},
		{map[string]string{"a": "1"}, map[string]string{"a": "1"}, true},
		{map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
		{map[string]string{"a": "1"}, nil, false},
	}
	for _, c := range cases {
		if got := mapsEqual(c.a, c.b); got != c.want {
			t.Errorf("mapsEqual(%v, %v) = %v", c.a, c.b, got)
		}
	}
	on, off := true, false
	if !boolPtrEqual(nil, nil) || boolPtrEqual(&on, nil) || boolPtrEqual(&on, &off) || !boolPtrEqual(&on, &on) {
		t.Error("boolPtrEqual")
	}
}

func TestAgentEntryMatchesOnlyTheBinary(t *testing.T) {
	for name, want := range map[string]bool{
		"orion-belt-agent":                     true,
		"./orion-belt-agent":                   true,
		"orion-belt_1.5.0/orion-belt-agent":    true,
		"packaging/sysv/orion-belt-agent":      false,
		"packaging/runit/orion-belt-agent/run": false,
		"orion-belt-agent.sig":                 false,
	} {
		if got := agentEntry.MatchString(name); got != want {
			t.Errorf("agentEntry(%q) = %v, want %v", name, got, want)
		}
	}
}
