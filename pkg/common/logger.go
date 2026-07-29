package common

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LogLevel represents logging level
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// Logger provides structured JSON logging (slog) suitable for Loki/ELK.
type Logger struct {
	level  LogLevel
	logger *slog.Logger
	attrs  []slog.Attr
}

// NewLogger creates a JSON logger writing to stdout.
func NewLogger(level LogLevel) *Logger {
	return NewLoggerTo(level, os.Stdout)
}

// NewLoggerTo creates a JSON logger writing to w. Useful when log output must
// not land on stdout — benchmarks discard it so log lines neither pollute
// machine-readable results nor charge stdout I/O to the measurement, while the
// formatting cost stays in it.
func NewLoggerTo(level LogLevel, w io.Writer) *Logger {
	opts := &slog.HandlerOptions{Level: slogLevel(level)}
	h := slog.NewJSONHandler(w, opts)
	return &Logger{
		level:  level,
		logger: slog.New(h),
	}
}

func slogLevel(level LogLevel) slog.Level {
	switch level {
	case DEBUG:
		return slog.LevelDebug
	case WARN:
		return slog.LevelWarn
	case ERROR, FATAL:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (l *Logger) log(level slog.Level, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	args := make([]any, 0, len(l.attrs)*2)
	for _, a := range l.attrs {
		args = append(args, a.Key, a.Value.Any())
	}
	l.logger.Log(context.Background(), level, msg, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.level <= DEBUG {
		l.log(slog.LevelDebug, format, v...)
	}
}

// Info logs an info message
func (l *Logger) Info(format string, v ...interface{}) {
	if l.level <= INFO {
		l.log(slog.LevelInfo, format, v...)
	}
}

// Warn logs a warning message
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.level <= WARN {
		l.log(slog.LevelWarn, format, v...)
	}
}

// Error logs an error message
func (l *Logger) Error(format string, v ...interface{}) {
	if l.level <= ERROR {
		l.log(slog.LevelError, format, v...)
	}
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(format string, v ...interface{}) {
	l.log(slog.LevelError, format, v...)
	os.Exit(1)
}

// WithField returns a new logger with a field
func (l *Logger) WithField(key, value string) *Logger {
	attrs := append([]slog.Attr{}, l.attrs...)
	attrs = append(attrs, slog.String(key, value))
	return &Logger{
		level:  l.level,
		logger: l.logger,
		attrs:  attrs,
	}
}

// ParseLogLevel parses a level name (debug/info/warn/error).
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return DEBUG
	case "warn", "warning":
		return WARN
	case "error":
		return ERROR
	default:
		return INFO
	}
}
