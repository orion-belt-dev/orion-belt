package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLogger_LevelFiltering(t *testing.T) {
	cases := []struct {
		level LogLevel
		want  []string
	}{
		{DEBUG, []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		{INFO, []string{"INFO", "WARN", "ERROR"}},
		{WARN, []string{"WARN", "ERROR"}},
		{ERROR, []string{"ERROR"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.want, ","), func(t *testing.T) {
			var buf bytes.Buffer
			l := NewLoggerTo(tc.level, &buf)
			l.Debug("d %d", 1)
			l.Info("i %d", 2)
			l.Warn("w %d", 3)
			l.Error("e %d", 4)

			var got []string
			for _, m := range decodeLogLines(t, &buf) {
				got = append(got, m["level"].(string))
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("levels emitted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLogger_FormatsMessage(t *testing.T) {
	var buf bytes.Buffer
	NewLoggerTo(DEBUG, &buf).Info("user %s logged in from %s", "alice", "10.0.0.1")
	lines := decodeLogLines(t, &buf)
	if len(lines) != 1 || lines[0]["msg"] != "user alice logged in from 10.0.0.1" {
		t.Errorf("log = %v, want the formatted message", lines)
	}
}

func TestLogger_WithFieldAddsAttributesWithoutMutatingParent(t *testing.T) {
	var buf bytes.Buffer
	parent := NewLoggerTo(DEBUG, &buf)
	child := parent.WithField("session", "s-1").WithField("user", "alice")

	child.Info("child")
	parent.Info("parent")

	lines := decodeLogLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0]["session"] != "s-1" || lines[0]["user"] != "alice" {
		t.Errorf("child line = %v, want session and user attributes", lines[0])
	}
	if _, ok := lines[1]["session"]; ok {
		t.Errorf("parent line = %v, WithField leaked into the parent", lines[1])
	}
}

func TestNewLoggerWithHandler_UsesGivenHandler(t *testing.T) {
	var buf bytes.Buffer
	l := NewLoggerWithHandler(INFO, slog.NewTextHandler(&buf, nil))
	l.Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Errorf("text handler output = %q, want msg=hello", buf.String())
	}
}

func TestNewLogger_WritesToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	l := NewLogger(INFO)
	os.Stdout = prev

	l.Info("to stdout")
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	if !strings.Contains(buf.String(), `"msg":"to stdout"`) {
		t.Errorf("stdout = %q, want the JSON log line", buf.String())
	}
}

func TestSlogLevel(t *testing.T) {
	cases := map[LogLevel]slog.Level{
		DEBUG:        slog.LevelDebug,
		INFO:         slog.LevelInfo,
		WARN:         slog.LevelWarn,
		ERROR:        slog.LevelError,
		FATAL:        slog.LevelError,
		LogLevel(99): slog.LevelInfo,
	}
	for in, want := range cases {
		if got := SlogLevel(in); got != want {
			t.Errorf("SlogLevel(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := map[string]LogLevel{
		"debug":   DEBUG,
		" DEBUG ": DEBUG,
		"warn":    WARN,
		"Warning": WARN,
		"error":   ERROR,
		"info":    INFO,
		"":        INFO,
		"verbose": INFO,
	}
	for in, want := range cases {
		if got := ParseLogLevel(in); got != want {
			t.Errorf("ParseLogLevel(%q) = %d, want %d", in, got, want)
		}
	}
}

// Fatal exits the process, so it runs in a child copy of the test binary.
func TestLogger_FatalLogsAndExits(t *testing.T) {
	if os.Getenv("ORION_TEST_LOGGER_FATAL") == "1" {
		NewLoggerTo(ERROR, os.Stderr).Fatal("cannot continue: %s", "boom")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestLogger_FatalLogsAndExits$")
	cmd.Env = append(os.Environ(), "ORION_TEST_LOGGER_FATAL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("child exited with %v, want exit status 1", err)
	}
	if !strings.Contains(stderr.String(), "cannot continue: boom") {
		t.Errorf("child stderr = %q, want the fatal message", stderr.String())
	}
}
