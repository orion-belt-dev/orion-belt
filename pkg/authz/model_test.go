package authz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExampleModelWritesNormalizedDSL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.fga")

	if err := WriteExampleModel(path); err != nil {
		t.Fatalf("WriteExampleModel: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(raw)

	if strings.HasPrefix(got, "\n") {
		t.Error("written DSL starts with a blank line, want the leading whitespace trimmed")
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("written DSL has no trailing newline")
	}
	if strings.HasSuffix(got, "\n\n") {
		t.Error("written DSL ends with a blank line, want exactly one trailing newline")
	}

	// The relation the client defaults to must actually exist in the model it
	// tells operators to install, or every Check fails at runtime.
	for _, want := range []string{"model", "schema 1.1", "type user", "type machine", "can_access"} {
		if !strings.Contains(got, want) {
			t.Errorf("written DSL is missing %q:\n%s", want, got)
		}
	}
}

func TestWriteExampleModelIsReadableByOthers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.fga")

	if err := WriteExampleModel(path); err != nil {
		t.Fatalf("WriteExampleModel: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// 0644: operator-facing documentation, not a secret.
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("permissions = %04o, want 0644", perm)
	}
}

func TestWriteExampleModelOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.fga")
	if err := os.WriteFile(path, []byte("stale contents that must not survive"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteExampleModel(path); err != nil {
		t.Fatalf("WriteExampleModel: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "stale contents") {
		t.Error("previous file contents survived the overwrite")
	}
}

func TestWriteExampleModelReportsUnwritablePath(t *testing.T) {
	// A path under a file (not a directory) can never be created.
	dir := t.TempDir()
	notADir := filepath.Join(dir, "regular-file")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteExampleModel(filepath.Join(notADir, "model.fga")); err == nil {
		t.Error("WriteExampleModel succeeded on an unwritable path, want an error")
	}
}

func TestErrNotConfiguredIsStable(t *testing.T) {
	if ErrNotConfigured == nil {
		t.Fatal("ErrNotConfigured is nil")
	}
	if !strings.Contains(ErrNotConfigured.Error(), "openfga") {
		t.Errorf("ErrNotConfigured = %q, want it to mention openfga", ErrNotConfigured)
	}
}
