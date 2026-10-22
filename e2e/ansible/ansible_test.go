// Package ansible_test drives the real ansible CLI through a gateway and agent
// started in-process, with the machine and remote user named in the SSH
// username and no wrapper script (issue #102). The agent runs on this host as
// the current user, so the managed "machine" is the test host itself.
package ansible_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/ssh"

	"github.com/orion-belt-dev/orion-belt/internal/pgtest"
	"github.com/orion-belt-dev/orion-belt/pkg/agent"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	"github.com/orion-belt-dev/orion-belt/pkg/database"
	"github.com/orion-belt-dev/orion-belt/pkg/server"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.Main(m))
}

func TestAnsibleThroughGateway(t *testing.T) {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-playbook not installed")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}

	dsn := pgtest.DSN(t)
	dir := t.TempDir()
	cfg := &common.Config{}
	cfg.Database.Driver = "postgres"
	cfg.Database.ConnectionString = dsn
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = freePort(t)
	cfg.Server.APIPort = freePort(t)
	cfg.Server.SSHHostKey, _ = writeKey(t, dir, "host_key")
	cfg.Recording.StoragePath = filepath.Join(dir, "recordings")
	cfg.Recording.Compression = "none"

	srv, err := server.New(cfg, common.NewLoggerTo(common.ERROR, io.Discard))
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	store, err := database.NewStore(cfg.Database.Driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	svcKeyPath, svcKey := writeKey(t, dir, "svc_key")
	svc := common.NewUser("svc", "svc@example.com", string(ssh.MarshalAuthorizedKey(svcKey.PublicKey())), false)
	if err := store.CreateUser(ctx, svc); err != nil {
		t.Fatal(err)
	}
	// Legacy agent identity: a user named after the machine, referenced by
	// the machine's agent_id.
	agentKeyPath, agentKey := writeKey(t, dir, "agent_key")
	agentUser := common.NewUser("web-01", "agent@example.com", string(ssh.MarshalAuthorizedKey(agentKey.PublicKey())), false)
	if err := store.CreateUser(ctx, agentUser); err != nil {
		t.Fatal(err)
	}
	machine := common.NewMachine("web-01", "127.0.0.1", 22, nil)
	machine.AgentID = agentUser.ID
	if err := store.CreateMachine(ctx, machine); err != nil {
		t.Fatal(err)
	}
	grant := common.NewPermission(svc.ID, machine.ID, "ssh", []string{me.Username}, svc.ID, nil)
	if err := store.CreatePermission(ctx, grant); err != nil {
		t.Fatal(err)
	}

	go func() { _ = srv.Start() }()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	waitListening(t, cfg.Server.Port)

	agentCfg := &common.Config{}
	agentCfg.Agent.Name = "web-01"
	agentCfg.Auth.KeyFile = agentKeyPath
	agentCfg.Auth.KnownHosts = filepath.Join(dir, "agent_known_hosts")
	agentCfg.Server.Host = "127.0.0.1"
	agentCfg.Server.Port = cfg.Server.Port
	ag, err := agent.New(agentCfg, common.NewLoggerTo(common.ERROR, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ag.Start() }()
	t.Cleanup(func() { _ = ag.Stop(context.Background()) })
	waitAgent(t, srv, machine.ID)

	inventory := filepath.Join(dir, "inventory.yml")
	writeFile(t, inventory, fmt.Sprintf(`all:
  hosts:
    web-01:
      ansible_host: 127.0.0.1
      ansible_port: %d
      ansible_user: svc+%s@web-01
      ansible_ssh_private_key_file: %s
      ansible_ssh_transfer_method: piped
      ansible_ssh_common_args: -F /dev/null -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
      ansible_python_interpreter: %s
      ansible_remote_tmp: %s
`, cfg.Server.Port, me.Username, svcKeyPath, python, filepath.Join(dir, "remote_tmp")))

	marker := filepath.Join(dir, "written-by-ansible")
	playbook := filepath.Join(dir, "playbook.yml")
	writeFile(t, playbook, fmt.Sprintf(`- hosts: all
  gather_facts: false
  tasks:
    - name: Pipes and quotes reach the target unchanged
      ansible.builtin.shell: echo "$HOME" | wc -c
      register: piped
    - ansible.builtin.assert:
        that: piped.stdout | int > 1
    - name: Module with arguments
      ansible.builtin.copy:
        content: "from ansible\n"
        dest: %s
`, marker))

	env := append(os.Environ(),
		"ANSIBLE_HOME="+filepath.Join(dir, "ansible_home"),
		"ANSIBLE_SSH_CONTROL_PATH_DIR="+filepath.Join(dir, "cp"),
		"ANSIBLE_HOST_KEY_CHECKING=False",
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_CONFIG="+filepath.Join(dir, "none.cfg"),
	)

	ping := runAnsible(t, env, "ansible", "all", "-i", inventory, "-m", "ping")
	if !strings.Contains(ping, "SUCCESS") || !strings.Contains(ping, `"pong"`) {
		t.Fatalf("ansible ping:\n%s", ping)
	}
	out := runAnsible(t, env, "ansible-playbook", "-i", inventory, playbook)
	if !strings.Contains(out, "failed=0") {
		t.Fatalf("playbook:\n%s", out)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "from ansible\n" {
		t.Errorf("copy task wrote %q, %v", b, err)
	}

	sessions, err := store.ListUserSessions(ctx, svc.ID, 500, 0)
	if err != nil || len(sessions) == 0 {
		t.Fatalf("sessions for the service account = %d, %v", len(sessions), err)
	}
	for _, s := range sessions {
		if s.RemoteUser != me.Username || s.MachineID != machine.ID {
			t.Errorf("session %s ran as %s on %s", s.ID, s.RemoteUser, s.MachineID)
		}
		if _, err := os.Stat(filepath.Join(cfg.Recording.StoragePath, s.ID+".cast")); err != nil {
			t.Errorf("session %s has no recording: %v", s.ID, err)
		}
	}
}

func runAnsible(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeKey(t *testing.T, dir, name string) (string, ssh.Signer) {
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
	writeFile(t, path, string(pem.EncodeToMemory(block)))
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return path, signer
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
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

func waitAgent(t *testing.T, srv *server.Server, machineID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Contains(srv.ListConnectedAgents(), machineID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never registered")
}
