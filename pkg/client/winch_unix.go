//go:build !windows

package client

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// forwardWindowChanges sends the local terminal size to the session whenever
// it changes (SIGWINCH), so full-screen programs on the target redraw to fit.
// The returned func stops watching.
func forwardWindowChanges(session *ssh.Session, fd int) func() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigs:
				if w, h, err := term.GetSize(fd); err == nil {
					_ = session.WindowChange(h, w)
				}
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}
