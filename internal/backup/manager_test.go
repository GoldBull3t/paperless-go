package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestBackupManager_CreateSnapshotAndRotation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	backupDir := filepath.Join(tmpDir, "backups")
	cfg := &config.Config{
		DatabasePath:        dbPath,
		BackupEnabled:       true,
		BackupDir:           backupDir,
		BackupRetentionDays: 2,
	}

	mgr := NewManager(db, cfg)

	// 1. Create a snapshot
	snapPath, err := mgr.CreateSnapshot()
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	if fi, err := os.Stat(snapPath); err != nil || fi.Size() == 0 {
		t.Fatalf("Expected non-empty backup file at %s", snapPath)
	}

	// 2. Simulate an expired backup file (> 2 days old)
	oldName := filepath.Join(backupDir, "paperless-backup-2020-01-01_00-00-00.db")
	_ = os.WriteFile(oldName, []byte("legacy-backup"), 0644)
	oldTime := time.Now().AddDate(0, 0, -10)
	_ = os.Chtimes(oldName, oldTime, oldTime)

	// Run cleanup
	mgr.cleanupExpiredSnapshots()

	if _, err := os.Stat(oldName); !os.IsNotExist(err) {
		t.Errorf("Expected expired backup to be pruned, but it still exists")
	}

	// 3. Test background scheduler start and graceful stop
	ctx, cancel := context.WithCancel(context.Background())
	mgr.Start(ctx)
	cancel() // should stop gracefully
}

