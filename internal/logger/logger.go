package logger

import (
	"log"
	"log/slog"
	"os"
	"strings"
)

// Setup initializes the global default structured logger (log/slog).
// Format can be "json" or "text" (default).
// Level can be "debug", "info", "warn", "error" (default "info").
func Setup(levelStr, formatStr string) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if strings.ToLower(strings.TrimSpace(formatStr)) == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Redirect standard log output to slog so third-party logs pass through structured log handler
	log.SetFlags(0)
	log.SetOutput(&slogWriter{logger: logger})

	return logger
}

type slogWriter struct {
	logger *slog.Logger
}

func (w *slogWriter) Write(p []byte) (n int, err error) {
	msg := strings.TrimRight(string(p), "\r\n")
	w.logger.Info(msg)
	return len(p), nil
}
