//go:build windows

package client

import "golang.org/x/crypto/ssh"

// forwardWindowChanges is a no-op: Windows consoles have no SIGWINCH.
func forwardWindowChanges(*ssh.Session, int) func() { return func() {} }
