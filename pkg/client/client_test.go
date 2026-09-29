package client

import (
	"bytes"
	"testing"
)

func TestMissingRemoteUser(t *testing.T) {
	out := []byte(`Failed to start shell for admin: unknown user "admin": user: unknown user admin` + "\r\n")
	if !missingRemoteUser(out, "admin") {
		t.Error("expected agent's unknown-user message to match")
	}
	if missingRemoteUser(out, "alice") {
		t.Error("must not match a different user")
	}
	if missingRemoteUser([]byte("bash: exit 1\n"), "admin") {
		t.Error("must not match unrelated output")
	}
}

func TestHeadBufferKeepsOnlyHead(t *testing.T) {
	h := &headBuffer{max: 8}
	for _, s := range []string{"hello ", "world", "!!!"} {
		if n, err := h.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("Write(%q) = %d, %v; want full length, nil", s, n, err)
		}
	}
	if got := h.Bytes(); !bytes.Equal(got, []byte("hello wo")) {
		t.Errorf("head = %q, want %q", got, "hello wo")
	}
}
