package auth

import (
	"path/filepath"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestEnsureAdminUser_CustomCredentials(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	svc := NewService(db, cfg)

	// First run with custom credentials
	created, genPass, err := svc.EnsureAdminUser("myadmin", "mySecret123!", "myadmin@example.com")
	if err != nil {
		t.Fatalf("EnsureAdminUser failed: %v", err)
	}
	if !created {
		t.Fatalf("expected admin user to be created")
	}
	if genPass != "" {
		t.Fatalf("expected empty genPass since password was provided, got %s", genPass)
	}

	// Verify user exists
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'myadmin' AND is_superuser = 1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 superuser 'myadmin', found %d (err: %v)", count, err)
	}

	// Second run should NOT recreate or overwrite
	created2, _, err := svc.EnsureAdminUser("other", "otherPass", "other@example.com")
	if err != nil {
		t.Fatalf("EnsureAdminUser second run failed: %v", err)
	}
	if created2 {
		t.Fatalf("expected created2 to be false since user already exists")
	}
}

func TestEnsureAdminUser_GeneratedPassword(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	svc := NewService(db, cfg)

	// Run with empty password -> should generate a secure random password
	created, genPass, err := svc.EnsureAdminUser("", "", "")
	if err != nil {
		t.Fatalf("EnsureAdminUser failed: %v", err)
	}
	if !created {
		t.Fatalf("expected admin user to be created")
	}
	if len(genPass) < 16 {
		t.Fatalf("expected generated password with at least 16 chars, got: %s", genPass)
	}

	// Verify authentication works with generated password
	var hash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE username = 'admin'").Scan(&hash); err != nil {
		t.Fatalf("failed to query hash: %v", err)
	}
	if !svc.CheckPassword(hash, genPass) {
		t.Fatalf("password check failed against generated password")
	}
}

func TestCreateSuperuser(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("database.Open failed: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{}
	svc := NewService(db, cfg)

	if err := svc.CreateSuperuser("cli_user", "cli_pass_999", "cli@local.test"); err != nil {
		t.Fatalf("CreateSuperuser failed: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username = 'cli_user' AND is_superuser = 1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected 1 superuser 'cli_user', got %d (err: %v)", count, err)
	}
}

