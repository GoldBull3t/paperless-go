package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"paperless-go/internal/database"
)

func setupTestQueue(t *testing.T, workers int) (*Queue, *database.DB, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "paperless-queue-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}
	q := NewQueue(db, workers)
	return q, db, tmpDir
}

func TestQueue_ConcurrentTasks(t *testing.T) {
	q, db, tmpDir := setupTestQueue(t, 4)
	defer os.RemoveAll(tmpDir)
	defer db.Close()
	defer q.Close()

	const numTasks = 20
	var completedCount sync.WaitGroup
	completedCount.Add(numTasks)

	// Pre-insert valid documents for tasks that succeed with a document ID
	for i := 0; i < numTasks; i++ {
		_, err := db.Exec("INSERT INTO documents (id, title, content, original_filename, checksum, mime_type) VALUES (?, ?, ?, ?, ?, ?)",
			i+1, fmt.Sprintf("Doc %d", i), "content", fmt.Sprintf("file_%d.pdf", i), fmt.Sprintf("chk_%d", i), "application/pdf")
		if err != nil {
			t.Fatalf("failed pre-inserting doc: %v", err)
		}
	}

	for i := 0; i < numTasks; i++ {
		idx := i
		q.Enqueue(fmt.Sprintf("Task %d", idx), fmt.Sprintf("file_%d.pdf", idx), "", func(tid string) (int, error) {
			defer completedCount.Done()
			time.Sleep(10 * time.Millisecond)
			if idx%5 == 0 {
				return 0, errors.New("simulated error")
			}
			return idx + 1, nil
		})
	}

	completedCount.Wait()

	// Wait brief moment for DB writes to settle
	time.Sleep(100 * time.Millisecond)

	var successCount, failureCount int
	err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'SUCCESS'").Scan(&successCount)
	if err != nil {
		t.Fatalf("failed querying successful tasks: %v", err)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'FAILURE'").Scan(&failureCount)
	if err != nil {
		t.Fatalf("failed querying failed tasks: %v", err)
	}

	if successCount+failureCount != numTasks {
		t.Fatalf("expected %d total processed tasks, got %d (success: %d, failure: %d)", numTasks, successCount+failureCount, successCount, failureCount)
	}
}

func TestQueue_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tmpDir, err := os.MkdirTemp("", "paperless-queue-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	q := NewQueueWithContext(ctx, db, 2)

	// Cancel context to trigger shutdown
	cancel()
	q.Close()
}
