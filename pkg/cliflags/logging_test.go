package cliflags

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerRoutesLogsToFileNotTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "osh.log")
	c := &Common{LogFile: path}
	logger := c.Logger()

	logger.Info("Connecting to web-01")
	logger.Debug("dropped without --verbose")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), `"msg":"Connecting to web-01"`) {
		t.Errorf("info line missing from log file: %s", data)
	}
	if strings.Contains(string(data), "dropped without --verbose") {
		t.Errorf("debug line written without --verbose: %s", data)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file mode = %o, want 600", perm)
	}
}

func TestLogFilePathPrecedence(t *testing.T) {
	t.Setenv("ORION_LOG_FILE", "/env/osh.log")
	if got := (&Common{LogFile: "/flag/osh.log"}).LogFilePath("osh"); got != "/flag/osh.log" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := (&Common{}).LogFilePath("osh"); got != "/env/osh.log" {
		t.Errorf("env should apply, got %q", got)
	}
	t.Setenv("ORION_LOG_FILE", "")
	t.Setenv("HOME", "/home/x")
	if got := (&Common{}).LogFilePath("ocp"); got != "/home/x/.orion-belt/logs/ocp.log" {
		t.Errorf("default path = %q", got)
	}
}

func TestOpenLogFileRotatesLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "osh.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxLogBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if st, _ := f.Stat(); st.Size() != 0 {
		t.Errorf("new log size = %d, want 0", st.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Errorf("rotated file missing: %v", err)
	}
}

func TestConsoleHandler(t *testing.T) {
	var buf bytes.Buffer
	h := newConsoleHandler(&buf, "osh", slog.LevelWarn)
	logger := slog.New(h)

	logger.Info("hidden")
	logger.Warn("host key changed", "host", "gw")
	logger.Error("Connection failed")

	want := "osh: warning: host key changed host=gw\nosh: error: Connection failed\n"
	if got := buf.String(); got != want {
		t.Errorf("console output =\n%q\nwant\n%q", got, want)
	}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info should be disabled at warn threshold")
	}
}
