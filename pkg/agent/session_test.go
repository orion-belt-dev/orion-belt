package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestSession_RequestHandling(t *testing.T) {
	_, conn, _ := connected(t)
	res := session(t, conn, "",
		request{"env", gossh.Marshal(&struct{ Name, Value string }{"LANG", "C"}), true, true},
		request{"pty-req", []byte{1}, true, false},          // malformed
		request{"exec", []byte{1}, true, false},             // malformed
		request{"subsystem", []byte("sftp"), true, false},   // unsupported
		request{"keepalive@openssh.com", nil, false, false}, // no reply wanted
		execReq("echo ok", ""),
	)
	if res.stdout != "ok\n" {
		t.Errorf("exec after rejected requests = %+v", res)
	}

	// A session closed before any exec or shell request just ends.
	ch, reqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go gossh.DiscardRequests(reqs)
	_ = ch.Close()

	if _, _, err := conn.OpenChannel("direct-tcpip", nil); err == nil {
		t.Error("agent accepted a non-session channel")
	}
}

// --- control commands -------------------------------------------------------------------

func TestControlCommands(t *testing.T) {
	a, conn, _ := connected(t)
	a.machineID = "m-1"

	restarted := make(chan struct{}, 1)
	prev := restartAgent
	restartAgent = func(*Agent) { restarted <- struct{}{} }
	t.Cleanup(func() { restartAgent = prev })

	cases := []struct {
		cmd    string
		status int
		check  func(map[string]any) bool
	}{
		{"orion:ping", 0, func(m map[string]any) bool { return m["pong"] == true }},
		{"orion:health", 0, func(m map[string]any) bool {
			return m["status"] == "healthy" && m["machine_id"] == "m-1" && m["connected"] == true
		}},
		{"orion:status", 0, func(m map[string]any) bool { return m["agent"] == "agent-1" }},
		{"orion:info", 0, func(m map[string]any) bool { return m["version"] != "" && m["tags"].(map[string]any)["env"] == "test" }},
		{"orion:restart", 0, func(m map[string]any) bool { return m["message"] == "restart scheduled" }},
		{"orion:rm -rf /", 1, func(m map[string]any) bool { return m["ok"] == false && m["error"] == "unknown control command" }},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			res := session(t, conn, "", execReq(tc.cmd, ""))
			var m map[string]any
			if err := json.Unmarshal([]byte(res.stdout), &m); err != nil {
				t.Fatalf("reply %q is not JSON: %v", res.stdout, err)
			}
			if res.status != tc.status || !tc.check(m) {
				t.Errorf("%s = %v (status %d)", tc.cmd, m, res.status)
			}
		})
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Error("orion:restart did not schedule a restart")
	}
}

// --- interactive shell ------------------------------------------------------------------

// fakeLoginShell points the user database at a script standing in for the
// current user's login shell, so tests never start the real one.
func fakeLoginShell(t *testing.T, script string) string {
	t.Helper()
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := filepath.Join(dir, "passwd")
	line := fmt.Sprintf("%s:x:%s:%s::%s:%s\n", me.Username, me.Uid, me.Gid, me.HomeDir, shell)
	if err := os.WriteFile(passwd, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := passwdPath
	passwdPath = passwd
	t.Cleanup(func() { passwdPath = prev })
	return me.Username
}

func shellReq(username string) request {
	return request{"shell", gossh.Marshal(&struct{ User string }{username}), true, true}
}

func ptyReq(term string, cols, rows uint32) request {
	return request{"pty-req", gossh.Marshal(&ptyRequestMsg{Term: term, Columns: cols, Rows: rows}), true, true}
}

func TestShell_RunsLoginShellOnPTY(t *testing.T) {
	// The shell's input is checked through a file rather than its last line of
	// output: startShell can drop trailing PTY output (see FINDINGS).
	got := filepath.Join(t.TempDir(), "got")
	username := fakeLoginShell(t, fmt.Sprintf(`echo "term=$TERM size=$(stty size)"; read line; printf %%s "$line" > %q; exit 4`, got))
	_, conn, _ := connected(t)

	res := session(t, conn, "hello\n",
		ptyReq("vt100", 90, 33),
		shellReq(username),
		request{"window-change", gossh.Marshal(&windowChangeMsg{Columns: 100, Rows: 40}), true, true},
		request{"window-change", []byte{1}, true, true}, // malformed: logged, still acknowledged
		request{"signal", nil, true, false},
	)
	if !strings.Contains(res.stdout, "term=vt100") {
		t.Errorf("shell output = %q", res.stdout)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "hello" {
		t.Errorf("shell read %q (%v), want the client's input", b, err)
	}
	if !res.gotStatus || res.status != 4 {
		t.Errorf("shell exit = %+v, want 4", res)
	}
}

func TestShell_Failures(t *testing.T) {
	_, conn, _ := connected(t)

	res := session(t, conn, "", shellReq("no-such-user-xyz"))
	if !strings.Contains(res.stdout, "Failed to start shell for no-such-user-xyz") || res.status != 1 {
		t.Errorf("unknown user shell = %+v", res)
	}

	res = session(t, conn, "", request{"shell", []byte{1}, true, false}, execReq("echo still here", ""))
	if res.stdout != "still here\n" {
		t.Errorf("after malformed shell request = %+v", res)
	}

	username := fakeLoginShell(t, "")
	_ = os.Chmod(passwdPath, 0o644)
	_ = os.WriteFile(passwdPath, []byte(strings.Replace(mustRead(t, passwdPath), "fake-shell", "missing-shell", 1)), 0o644)
	res = session(t, conn, "", shellReq(username))
	if !strings.Contains(res.stdout, "Failed to start shell") || res.status != 1 {
		t.Errorf("missing shell binary = %+v", res)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildShellCommand_DefaultsAndHomeFallback(t *testing.T) {
	username := fakeLoginShell(t, "")
	a, _ := agentFixture(t, nil)
	cmd, err := a.buildShellCommand(username, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cmd.Env, "TERM=xterm-256color") || !strings.HasPrefix(cmd.Args[0], "-") {
		t.Errorf("cmd = %v env=%v", cmd.Args, cmd.Env)
	}
	if cmd.SysProcAttr != nil {
		t.Error("dropping privileges to the current user should be a no-op")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// An empty remote user means root; an agent not running as root must refuse
// rather than start a shell as itself.
func TestShell_DefaultsToRootAndRefusesWithoutPrivilege(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root this would start root's real login shell")
	}
	_, conn, _ := connected(t)
	res := session(t, conn, "", shellReq(""))
	if !strings.Contains(res.stdout, "Failed to start shell for root") || !strings.Contains(res.stdout, "not running as root") || res.status != 1 {
		t.Errorf("shell as default user = %+v", res)
	}
}
