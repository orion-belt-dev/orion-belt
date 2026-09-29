package cliflags

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// maxLogBytes is the size at which the CLI log is rotated to <file>.1 on the
// next start, so a long-lived install never grows it without bound.
const maxLogBytes = 5 << 20

// Logger returns the CLI logger. Full JSON logs go to a per-CLI file
// (--log-file, $ORION_LOG_FILE, or ~/.orion-belt/logs/<cli>.log) so they never
// mix with session output; the terminal only gets warnings and errors as short
// "<cli>: ..." lines. --verbose also mirrors debug detail to the terminal.
func (c *Common) Logger() *common.Logger {
	prog := filepath.Base(os.Args[0])
	level := common.INFO
	console := slog.LevelWarn
	if c.Verbose {
		level = common.DEBUG
		console = slog.LevelDebug
	}

	handlers := []slog.Handler{newConsoleHandler(os.Stderr, prog, console)}
	path := c.LogFilePath(prog)
	if f, err := openLogFile(path); err == nil {
		handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: common.SlogLevel(level)}))
	} else if c.Verbose {
		fmt.Fprintf(os.Stderr, "%s: cannot open log file %s: %v\n", prog, path, err)
	}
	return common.NewLoggerWithHandler(level, slog.NewMultiHandler(handlers...))
}

// LogFilePath resolves where prog writes its log file.
func (c *Common) LogFilePath(prog string) string {
	if c.LogFile != "" {
		return expandPath(c.LogFile)
	}
	if v := os.Getenv("ORION_LOG_FILE"); v != "" {
		return expandPath(v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), prog+".log")
	}
	return filepath.Join(home, ".orion-belt", "logs", prog+".log")
}

// openLogFile opens path for appending, owner-only, rotating it first when it
// has outgrown maxLogBytes. Logs can carry hostnames and usernames, hence 0600.
func openLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// consoleHandler prints records as one plain line: "osh: warning: msg k=v".
type consoleHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	prog  string
	min   slog.Level
	attrs []slog.Attr
}

func newConsoleHandler(w io.Writer, prog string, min slog.Level) *consoleHandler {
	return &consoleHandler{mu: &sync.Mutex{}, w: w, prog: prog, min: min}
}

func (h *consoleHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.min }

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(h.prog)
	b.WriteString(": ")
	switch {
	case r.Level >= slog.LevelError:
		b.WriteString("error: ")
	case r.Level >= slog.LevelWarn:
		b.WriteString("warning: ")
	case r.Level < slog.LevelInfo:
		b.WriteString("debug: ")
	}
	b.WriteString(r.Message)
	write := func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &n
}

// Groups are flattened; the console line is for people, not parsers.
func (h *consoleHandler) WithGroup(string) slog.Handler { return h }
