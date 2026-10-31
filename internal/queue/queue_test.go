package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestQueue_PersistentTaskRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "paperless-queue-recover-*")
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

	// 1. Simulate an interrupted task left in STARTED status
	_, err = db.Exec(`
		INSERT INTO tasks (task_id, type, status, task_name, task_file_name, payload_json, attempt_count, max_attempts)
		VALUES ('task-recover-1', 'file_consume', 'STARTED', 'Consume Doc', 'invoice.pdf', '{"path": "invoice.pdf"}', 0, 3)
	`)
	if err != nil {
		t.Fatalf("failed inserting test task: %v", err)
	}

	// 2. Start a new queue and register handler for 'file_consume'
	q := NewQueue(db, 2)
	defer q.Close()

	var executed sync.WaitGroup
	executed.Add(1)

	q.RegisterHandler("file_consume", func(ctx context.Context, taskID, payloadJSON string) (int, error) {
		if taskID != "task-recover-1" {
			t.Errorf("unexpected task ID: %s", taskID)
		}
		executed.Done()
		return 0, nil
	})

	recovered := q.RecoverInterruptedTasks()
	if recovered != 1 {
		t.Fatalf("expected 1 recovered task, got %d", recovered)
	}

	// Wait for worker to finish execution
	executed.Wait()
	time.Sleep(50 * time.Millisecond)

	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE task_id = 'task-recover-1'").Scan(&status); err != nil {
		t.Fatalf("failed querying task status: %v", err)
	}
	if status != "SUCCESS" {
		t.Fatalf("expected task status SUCCESS after recovery, got %s", status)
	}
}

func TestQueue_DeadLetterQueue_RetriesAndDLQ(t *testing.T) {
	q, db, tmpDir := setupTestQueue(t, 2)
	defer os.RemoveAll(tmpDir)
	defer db.Close()
	defer q.Close()

	var attemptCount int
	var mu sync.Mutex
	allAttemptsDone := make(chan struct{})

	q.RegisterHandler("failing_job", func(ctx context.Context, taskID, payloadJSON string) (int, error) {
		mu.Lock()
		attemptCount++
		current := attemptCount
		mu.Unlock()

		if current >= 3 {
			select {
			case <-allAttemptsDone:
			default:
				close(allAttemptsDone)
			}
		}
		return 0, errors.New("persistent hardware failure simulation")
	})

	taskID, err := q.EnqueuePersistent("failing_job", "Failing DLQ Task", "fail.pdf", "/tmp/fail.pdf", map[string]string{"sample": "data"})
	if err != nil {
		t.Fatalf("failed to enqueue persistent task: %v", err)
	}

	select {
	case <-allAttemptsDone:
	case <-time.After(8 * time.Second):
		t.Fatalf("timed out waiting for 3 retries of task %s (attemptCount=%d)", taskID, attemptCount)
	}

	// Give a moment for final DB update
	time.Sleep(100 * time.Millisecond)

	var status, result string
	var attempts int
	err = db.QueryRow("SELECT status, result, attempt_count FROM tasks WHERE task_id = ?", taskID).Scan(&status, &result, &attempts)
	if err != nil {
		t.Fatalf("failed querying task from DB: %v", err)
	}

	if status != "FAILURE" {
		t.Errorf("expected final status FAILURE, got %s", status)
	}
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts)
	}
	if !strings.Contains(result, "[DLQ]") {
		t.Errorf("expected result to contain '[DLQ]', got: %s", result)
	}
}
