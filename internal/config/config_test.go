package config

import (
	"os"
	"testing"
)

func TestConfig_LoadDefaults(t *testing.T) {
	cfg := Load()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Port != 8000 {
		t.Errorf("expected default port 8000, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default log level info, got %s", cfg.LogLevel)
	}
}

func TestConfig_LoadCustomEnvs(t *testing.T) {
	_ = os.Setenv("PAPERLESS_PORT", "9999")
	_ = os.Setenv("PAPERLESS_LOG_LEVEL", "debug")
	_ = os.Setenv("PAPERLESS_LOG_FORMAT", "json")
	_ = os.Setenv("PAPERLESS_ADMIN_USER", "custom_adm")
	_ = os.Setenv("PAPERLESS_ADMIN_PASSWORD", "secretpass123")
	defer func() {
		_ = os.Unsetenv("PAPERLESS_PORT")
		_ = os.Unsetenv("PAPERLESS_LOG_LEVEL")
		_ = os.Unsetenv("PAPERLESS_LOG_FORMAT")
		_ = os.Unsetenv("PAPERLESS_ADMIN_USER")
		_ = os.Unsetenv("PAPERLESS_ADMIN_PASSWORD")
	}()

	cfg := Load()
	if cfg.Port != 9999 {
		t.Errorf("expected port 9999, got %d", cfg.Port)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected log format json, got %s", cfg.LogFormat)
	}
	if cfg.AdminUser != "custom_adm" {
		t.Errorf("expected admin user custom_adm, got %s", cfg.AdminUser)
	}
	if cfg.AdminPassword != "secretpass123" {
		t.Errorf("expected admin password secretpass123, got %s", cfg.AdminPassword)
	}
}

