// Package logging wires slog to a rotating file and, optionally, the console.
package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"github.com/sjkim/jarvis/internal/config"
)

// Setup returns a logger and a closer. When console is true the logger also
// writes to stderr; the service and shortcut-launch modes disable it because they have no
// usable console.
func Setup(dir string, cfg config.LogConfig, console bool) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, nil, err
	}

	rot := &lumberjack.Logger{
		Filename:   filepath.Join(dir, "jarvis.log"),
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		Compress:   true,
		LocalTime:  true,
	}

	var w io.Writer = rot
	if console {
		w = io.MultiWriter(rot, os.Stderr)
	}

	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: parseLevel(cfg.Level)})
	return slog.New(h), rot, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Discard is handy in tests and in early bootstrap paths.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
