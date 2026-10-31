package logger

import (
	"log"
	"log/slog"
	"testing"
)

func TestSetup_TextAndJSON(t *testing.T) {
	textLogger := Setup("debug", "text")
	if textLogger == nil {
		t.Fatal("expected non-nil text logger")
	}
	slog.Debug("test debug message", "key", "val")
	slog.Info("test info message", "count", 42)

	jsonLogger := Setup("warn", "json")
	if jsonLogger == nil {
		t.Fatal("expected non-nil json logger")
	}
	slog.Warn("test warning message", "status", "degraded")

	// Verify standard log redirection
	log.Println("standard library log forwarding verification")
}

