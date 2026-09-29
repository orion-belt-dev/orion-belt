package agent

import (
	"io"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/orion-belt-dev/orion-belt/pkg/common"
	gossh "golang.org/x/crypto/ssh"
)

func TestApplyWindowChange(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty not available: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})

	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("initial Setsize: %v", err)
	}

	payload := gossh.Marshal(&windowChangeMsg{Columns: 200, Rows: 55, Width: 1600, Height: 900})
	if err := applyWindowChange(ptmx, payload); err != nil {
		t.Fatalf("applyWindowChange: %v", err)
	}

	// The slave side is what `stty size` in the shell reads.
	got, err := pty.GetsizeFull(tty)
	if err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	}
	if got.Rows != 55 || got.Cols != 200 || got.X != 1600 || got.Y != 900 {
		t.Fatalf("size = %+v, want rows=55 cols=200 x=1600 y=900", *got)
	}
}

func TestApplyWindowChangeRejectsMalformedPayload(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty not available: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})

	if err := applyWindowChange(ptmx, []byte{0, 1}); err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestClampUint16(t *testing.T) {
	if got := clampUint16(70000); got != 0xffff {
		t.Errorf("clampUint16(70000) = %d, want 65535", got)
	}
	if got := clampUint16(80); got != 80 {
		t.Errorf("clampUint16(80) = %d, want 80", got)
	}
}

// A burst of resizes (window drag) must be fully drained: x/crypto/ssh blocks
// the connection's mux once a channel's 16-slot request buffer is full.
func TestServeShellRequestsDrainsBurst(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty not available: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})

	a := &Agent{logger: common.NewLoggerTo(common.DEBUG, io.Discard)}
	reqs := make(chan *gossh.Request)
	done := make(chan struct{})
	go func() {
		a.serveShellRequests(reqs, ptmx)
		close(done)
	}()

	for i := 1; i <= 100; i++ {
		payload := gossh.Marshal(&windowChangeMsg{Columns: uint32(80 + i), Rows: uint32(24 + i)})
		select {
		case reqs <- &gossh.Request{Type: "window-change", Payload: payload}:
		case <-time.After(2 * time.Second):
			t.Fatalf("request %d not consumed", i)
		}
	}
	// Unknown request types are consumed too, not left to back up.
	reqs <- &gossh.Request{Type: "x11-req"}
	close(reqs)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveShellRequests did not return after channel close")
	}

	got, err := pty.GetsizeFull(tty)
	if err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	}
	if got.Rows != 124 || got.Cols != 180 {
		t.Fatalf("size = %dx%d, want last resize 124x180", got.Rows, got.Cols)
	}
}
