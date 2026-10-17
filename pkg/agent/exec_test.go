package agent

import (
	"io"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestExec_RunsCommandAndReportsStatus(t *testing.T) {
	_, conn, _ := connected(t)

	res := session(t, conn, "", execReq("echo out; echo err >&2; exit 3", ""))
	if res.stdout != "out\n" || res.stderr != "err\n" || !res.gotStatus || res.status != 3 {
		t.Errorf("exec = %+v", res)
	}

	res = session(t, conn, "piped input", execReq("cat", ""))
	if res.stdout != "piped input" || res.status != 0 {
		t.Errorf("stdin passthrough = %+v", res)
	}
}

func TestExec_AsNamedUser(t *testing.T) {
	_, conn, _ := connected(t)
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}

	res := session(t, conn, "", execReq(`echo "$USER $HOME"; pwd`, me.Username))
	if want := me.Username + " " + me.HomeDir + "\n"; !strings.HasPrefix(res.stdout, want) || res.status != 0 {
		t.Errorf("exec as %s = %+v, want env for that user", me.Username, res)
	}

	res = session(t, conn, "", execReq("true", "no-such-user-xyz"))
	if !strings.Contains(res.stdout, `unknown user "no-such-user-xyz"`) || res.status != 1 {
		t.Errorf("unknown user = %+v", res)
	}

	if os.Geteuid() != 0 {
		res = session(t, conn, "", execReq("true", "root"))
		if !strings.Contains(res.stdout, "agent is not running as root") || res.status != 1 {
			t.Errorf("privilege drop without root = %+v", res)
		}
	}
}

// os/exec closes a command's output pipes in Wait, so output must be drained
// before Wait. Short commands lost their output intermittently when Wait ran
// first; repeating the session makes that race show up reliably.
func TestExec_OutputIsNeverDropped(t *testing.T) {
	_, conn, _ := connected(t)
	for i := 0; i < 50; i++ {
		res := session(t, conn, "", execReq("printf out; printf err >&2", ""))
		if res.stdout != "out" || res.stderr != "err" {
			t.Fatalf("run %d: stdout=%q stderr=%q, want both streams intact", i, res.stdout, res.stderr)
		}
	}
}

// A command that cannot be started must still end with EOF: the gateway
// relays exec output until EOF, so without it the client session hangs.
func TestExec_FailureEndsTheChannel(t *testing.T) {
	_, conn, _ := connected(t)
	ch, reqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	go gossh.DiscardRequests(reqs)
	if ok, err := ch.SendRequest("exec", true, gossh.Marshal(&struct{ Command, User string }{"true", "no-such-user-xyz"})); err != nil || !ok {
		t.Fatalf("exec request = %v, %v", ok, err)
	}

	got := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(ch); got <- b }()
	select {
	case b := <-got:
		if !strings.Contains(string(b), `unknown user "no-such-user-xyz"`) {
			t.Errorf("output = %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent never sent EOF after failing to start the command")
	}
}
