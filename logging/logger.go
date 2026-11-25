// Package logging formats native Ponder diagnostics and runtime log records.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
)

type Settings struct {
	Level  string
	Format string
}

var levels = map[string]slog.Level{"trace": -8, "debug": -4, "info": 0, "warn": 4, "error": 8, "fatal": 12, "silent": 127}

// Environment returns the CLI defaults, or the settings inherited by a child.
func Environment() Settings {
	s := Settings{Level: "info", Format: "pretty"}
	if v := os.Getenv("PONDER_LOG_LEVEL"); ValidLevel(v) {
		s.Level = v
	}
	if v := os.Getenv("PONDER_LOG_FORMAT"); v == "json" || v == "pretty" {
		s.Format = v
	}
	return s
}

func ValidLevel(level string) bool { _, ok := levels[level]; return ok }

type Logger struct {
	log      *slog.Logger
	settings Settings
}

// New creates an independent logger. JSON uses Ponder's numeric level and
// epoch-millisecond time fields. Pretty output is intended for a terminal.
func New(out io.Writer, s Settings) (*Logger, error) {
	lvl, ok := levels[s.Level]
	if !ok {
		return nil, fmt.Errorf("invalid log level %q", s.Level)
	}
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) > 0 {
			return a
		}
		switch a.Key {
		case slog.TimeKey:
			if a.Value.Kind() != slog.KindTime {
				return a
			}
			if s.Format == "json" {
				return slog.Int64("time", a.Value.Time().UnixMilli())
			}
			return slog.String("time", a.Value.Time().Format("15:04:05.000"))
		case slog.LevelKey:
			level, ok := a.Value.Any().(slog.Level)
			if !ok {
				return a
			}
			if s.Format == "json" {
				return slog.Int("level", 30+int(level)*10/4)
			}
			switch level {
			case -8:
				return slog.String("level", "TRACE")
			case 12:
				return slog.String("level", "FATAL")
			}
		}
		return a
	}}
	var handler slog.Handler
	switch s.Format {
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	case "pretty":
		handler = slog.NewTextHandler(out, opts)
	default:
		return nil, fmt.Errorf("invalid log format %q", s.Format)
	}
	return &Logger{log: slog.New(handler), settings: s}, nil
}

func (l *Logger) Settings() Settings              { return l.settings }
func (l *Logger) Info(msg string, fields ...any)  { l.log.Info(msg, fields...) }
func (l *Logger) Error(msg string, fields ...any) { l.log.Error(msg, fields...) }
func (l *Logger) Trace(msg string, fields ...any) {
	l.log.Log(context.Background(), -8, msg, fields...)
}

// Debug and Warn implement the native runtime's RPC and event log interfaces.
func (l *Logger) Debug(fields map[string]any) { l.record(slog.LevelDebug, fields) }
func (l *Logger) Warn(fields map[string]any)  { l.record(slog.LevelWarn, fields) }
func (l *Logger) record(level slog.Level, fields map[string]any) {
	ctx := context.Background()
	if !l.log.Enabled(ctx, level) {
		return
	}
	msg, _ := fields["msg"].(string)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "msg" && k != "time" && k != "level" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	attrs := make([]slog.Attr, 0, len(keys))
	for _, k := range keys {
		attrs = append(attrs, slog.Any(k, fields[k]))
	}
	l.log.LogAttrs(ctx, level, msg, attrs...)
}

// Diagnostics receives text emitted by tools such as go build and flag.FlagSet.
// Contributors to the same stream use the same logger and severity filtering.
func (l *Logger) Diagnostics() io.Writer { return diagnosticWriter{l} }

type diagnosticWriter struct{ logger *Logger }

func (w diagnosticWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		if line != "" {
			w.logger.Error(strings.TrimSuffix(line, "\r"))
		}
	}
	return len(p), nil
}
