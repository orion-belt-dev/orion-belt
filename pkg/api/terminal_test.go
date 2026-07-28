package api

import (
	"os/exec"
	"testing"
)

// shellQuote must produce a single POSIX shell token whose contents are inert —
// this guards the fix for the file-browser command injection where a path like
// "/tmp/$(id > /tmp/pwned)" was previously interpolated with Go's %q (which only
// escapes Go-string syntax, not shell metacharacters) instead of POSIX quoting.
func TestShellQuoteNeutralizesShellMetacharacters(t *testing.T) {
	dangerous := []string{
		`/tmp/$(id)`,
		"/tmp/`id`",
		"/tmp/foo; rm -rf /",
		"/tmp/foo && echo pwned",
		"/tmp/foo' ; echo pwned ; echo '",
		"$HOME/../../etc/passwd",
	}
	for _, path := range dangerous {
		quoted := shellQuote(path)
		// printf %s rather than `echo -n`: the -n flag is not POSIX, and the
		// /bin/sh builtin echo on macOS (bash in POSIX mode) prints it
		// literally instead of suppressing the trailing newline.
		cmd := exec.Command("/bin/sh", "-c", "printf %s "+quoted)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("shellQuote(%q) produced invalid shell syntax: %v", path, err)
		}
		if got := string(out); got != path {
			t.Errorf("shellQuote(%q) round-tripped to %q through the shell; want the literal input back with no expansion/execution", path, got)
		}
	}
}

func TestShellQuoteEmptyString(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote(\"\") = %q, want \"''\"", got)
	}
}
