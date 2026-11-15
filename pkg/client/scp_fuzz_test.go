package client

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzSafeSCPName checks that a name accepted from a remote scp header can
// never leave the download directory.
func FuzzSafeSCPName(f *testing.F) {
	for _, s := range []string{"file.txt", ".", "..", "../x", "a/b", `a\b`, "a\x00b", "", ".hidden"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if !safeSCPName(name) {
			return
		}
		dir := filepath.Join(string(filepath.Separator), "dl")
		got := filepath.Join(dir, name)
		if filepath.Dir(got) != dir || filepath.Base(got) != name {
			t.Fatalf("safeSCPName accepted %q, which resolves to %q outside %q", name, got, dir)
		}
	})
}

// FuzzShellWord checks that shellWord's output is read back by a POSIX shell
// as exactly one word equal to the input.
func FuzzShellWord(f *testing.F) {
	for _, s := range []string{"", "/tmp/a b", "it's", "$(id)", "`id`", "a;b", "~/x", "\n", "'"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		w := shellWord(s)
		got, ok := unquoteShellWord(w)
		if !ok || got != s {
			t.Fatalf("shellWord(%q) = %q, which a shell reads as %q (single word: %v)", s, w, got, ok)
		}
	})
}

// unquoteShellWord reads w the way /bin/sh does for the two forms shellWord
// emits: bare characters from a safe set, and single-quoted runs joined by \'.
// It reports false if w would expand, split or be read as more than one word.
func unquoteShellWord(w string) (string, bool) {
	if w == "" {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(w); {
		switch c := w[i]; {
		case c == '\'':
			end := strings.IndexByte(w[i+1:], '\'')
			if end < 0 {
				return "", false
			}
			b.WriteString(w[i+1 : i+1+end])
			i += end + 2
		case c == '\\' && i+1 < len(w) && w[i+1] == '\'':
			b.WriteByte('\'')
			i += 2
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("_./~:@%+=,-", c) >= 0:
			// A bare leading ~ is left for the remote shell to expand on
			// purpose, so "~/x" means the remote home directory.
			b.WriteByte(c)
			i++
		default:
			return "", false
		}
	}
	return b.String(), true
}
