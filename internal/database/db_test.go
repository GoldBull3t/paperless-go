package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "paperless-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}
	return db, tmpDir
}

func TestWithTx_Commit(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "TestTag", "test-tag")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx returned unexpected error: %v", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'test-tag'").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count tags: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 tag committed, got %d", count)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	simulatedErr := errors.New("simulated failure")

	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "RollbackTag", "rollback-tag")
		if err != nil {
			return err
		}
		return simulatedErr
	})
	if !errors.Is(err, simulatedErr) {
		t.Fatalf("expected simulatedErr, got %v", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'rollback-tag'").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query tags: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 tags after rollback, got %d", count)
	}
}

func TestWithTx_RollbackOnPanic(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to be propagated")
		}

		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'panic-tag'").Scan(&count)
		if err != nil {
			t.Fatalf("failed to query tags after panic: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected 0 tags after panic rollback, got %d", count)
		}
	}()

	_ = db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "PanicTag", "panic-tag")
		if err != nil {
			return err
		}
		panic("critical crash inside transaction")
	})
}

func TestDB_Maintenance(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Direct execution
	db.RunMaintenance()

	// Periodic maintenance startup & shutdown
	db.StartMaintenance(ctx, 50*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	cancel()
}

