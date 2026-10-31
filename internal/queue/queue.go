package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type TaskPayload struct {
	TaskID              string
	TaskName            string
	TaskFileName        string
	FilePath            string
	Execute             func(taskID string) (int, error) // Legacy fallback
	ExecuteWithContext func(ctx context.Context, taskID string) (int, error)
}

type Broadcaster interface {
	BroadcastTaskUpdate(task models.Task)
}

// TaskHandler is the signature for persistent task execution functions
type TaskHandler func(ctx context.Context, taskID string, payloadJSON string) (relatedDocID int, err error)

type Queue struct {
	db          *database.DB
	taskChan    chan TaskPayload
	broadcaster Broadcaster
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
	handlers    map[string]TaskHandler
}

func NewQueue(db *database.DB, workerCount int) *Queue {
	return NewQueueWithContext(context.Background(), db, workerCount)
}

func NewQueueWithContext(parentCtx context.Context, db *database.DB, workerCount int) *Queue {
	ctx, cancel := context.WithCancel(parentCtx)

	q := &Queue{
		db:       db,
		taskChan: make(chan TaskPayload, 100),
		ctx:      ctx,
		cancel:   cancel,
		handlers: make(map[string]TaskHandler),
	}

	for i := 0; i < workerCount; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}

	return q
}

// RegisterHandler registers a typed task handler for persistent task recovery
func (q *Queue) RegisterHandler(taskType string, h TaskHandler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.handlers[taskType] = h
}

// RecoverInterruptedTasks recovers PENDING or interrupted tasks from database
func (q *Queue) RecoverInterruptedTasks() int {
	rows, err := q.db.Query(`
		SELECT task_id, type, task_name, COALESCE(task_file_name, ''), COALESCE(payload_json, '{}'), attempt_count, max_attempts
		FROM tasks
		WHERE status IN ('PENDING', 'STARTED')
	`)
	if err != nil {
		log.Printf("[WARN] Failed querying interrupted tasks for recovery: %v", err)
		return 0
	}
	defer rows.Close()

	type item struct {
		taskID, tType, taskName, fileName, payloadJSON string
		attempts, maxAttempts                          int
	}
	var toRecover []item

	for rows.Next() {
		var it item
		if err := rows.Scan(&it.taskID, &it.tType, &it.taskName, &it.fileName, &it.payloadJSON, &it.attempts, &it.maxAttempts); err == nil {
			toRecover = append(toRecover, it)
		}
	}

	recoveredCount := 0
	for _, it := range toRecover {
		q.mu.RLock()
		h, ok := q.handlers[it.tType]
		q.mu.RUnlock()

		if !ok || it.attempts >= it.maxAttempts {
			res := fmt.Sprintf("[DLQ] Interrupted by server restart (exceeded max retries %d/%d or handler missing)", it.attempts, it.maxAttempts)
			_, _ = q.db.Exec("UPDATE tasks SET status = 'FAILURE', result = ?, date_done = CURRENT_TIMESTAMP WHERE task_id = ?", res, it.taskID)
			continue
		}

		newAttempts := it.attempts + 1
		_, _ = q.db.Exec("UPDATE tasks SET status = 'PENDING', attempt_count = ? WHERE task_id = ?", newAttempts, it.taskID)

		payloadStr := it.payloadJSON
		exec := func(ctx context.Context, tID string) (int, error) {
			return h(ctx, tID, payloadStr)
		}

		select {
		case q.taskChan <- TaskPayload{
			TaskID:              it.taskID,
			TaskName:            it.taskName,
			TaskFileName:        it.fileName,
			ExecuteWithContext: exec,
		}:
			recoveredCount++
			log.Printf("[Queue] Successfully recovered interrupted task %s (%s, attempt %d/%d)", it.taskID, it.taskName, newAttempts, it.maxAttempts)
		default:
			log.Printf("[WARN] Queue channel full during recovery of task %s", it.taskID)
		}
	}

	return recoveredCount
}

// EnqueuePersistent adds a task with persistent JSON payload to the database and worker queue
func (q *Queue) EnqueuePersistent(taskType, taskName, fileName, filePath string, payload any) (string, error) {
	taskID := uuid.New().String()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed marshaling task payload: %w", err)
	}
	payloadJSON := string(payloadBytes)

	_, err = q.db.Exec(`
		INSERT INTO tasks (task_id, type, status, task_name, task_file_name, payload_json, attempt_count, max_attempts, date_created)
		VALUES (?, ?, 'PENDING', ?, ?, ?, 0, 3, CURRENT_TIMESTAMP)
	`, taskID, taskType, taskName, fileName, payloadJSON)
	if err != nil {
		log.Printf("[ERROR] Failed creating persistent task %s in DB: %v", taskID, err)
		return "", err
	}

	task := models.Task{
		TaskID:       taskID,
		Type:         taskType,
		Status:       "PENDING",
		TaskName:     taskName,
		TaskFileName: fileName,
		DateCreated:  time.Now(),
	}
	if q.broadcaster != nil {
		q.broadcaster.BroadcastTaskUpdate(task)
	}

	exec := func(ctx context.Context, tID string) (int, error) {
		q.mu.RLock()
		h, ok := q.handlers[taskType]
		q.mu.RUnlock()
		if !ok {
			return 0, fmt.Errorf("no registered handler for task type %s", taskType)
		}
		return h(ctx, tID, payloadJSON)
	}

	select {
	case q.taskChan <- TaskPayload{
		TaskID:              taskID,
		TaskName:            taskName,
		TaskFileName:        fileName,
		FilePath:            filePath,
		ExecuteWithContext: exec,
	}:
	case <-q.ctx.Done():
		log.Printf("[WARN] Queue is shutting down; task %s dropped from memory channel", taskID)
	}

	return taskID, nil
}

func (q *Queue) SetBroadcaster(b Broadcaster) {
	q.broadcaster = b
}

func (q *Queue) Enqueue(taskName, fileName, filePath string, exec func(taskID string) (int, error)) string {
	taskID := uuid.New().String()

	_, err := q.db.Exec(`
		INSERT INTO tasks (task_id, type, status, task_name, task_file_name, date_created)
		VALUES (?, 'file_handling', 'PENDING', ?, ?, CURRENT_TIMESTAMP)
	`, taskID, taskName, fileName)
	if err != nil {
		log.Printf("[ERROR] Failed creating task %s in DB: %v", taskID, err)
	}

	task := models.Task{
		TaskID:       taskID,
		Type:         "file_handling",
		Status:       "PENDING",
		TaskName:     taskName,
		TaskFileName: fileName,
		DateCreated:  time.Now(),
	}
	if q.broadcaster != nil {
		q.broadcaster.BroadcastTaskUpdate(task)
	}

	select {
	case q.taskChan <- TaskPayload{
		TaskID:       taskID,
		TaskName:     taskName,
		TaskFileName: fileName,
		FilePath:     filePath,
		Execute:      exec,
	}:
	case <-q.ctx.Done():
		log.Printf("[WARN] Queue is shutting down; task %s dropped from memory channel", taskID)
	}

	return taskID
}

func (q *Queue) worker(id int) {
	defer q.wg.Done()
	for {
		select {
		case payload := <-q.taskChan:
			q.processTask(payload)
		case <-q.ctx.Done():
			return
		}
	}
}

func (q *Queue) processTask(p TaskPayload) {
	log.Printf("[Queue] Starting task %s (%s)", p.TaskID, p.TaskFileName)

	// Update to STARTED
	if _, err := q.db.Exec(`UPDATE tasks SET status = 'STARTED' WHERE task_id = ?`, p.TaskID); err != nil {
		log.Printf("[ERROR] Failed updating task %s to STARTED: %v", p.TaskID, err)
	}
	if q.broadcaster != nil {
		q.broadcaster.BroadcastTaskUpdate(models.Task{
			TaskID:       p.TaskID,
			Type:         "file_handling",
			Status:       "STARTED",
			TaskName:     p.TaskName,
			TaskFileName: p.TaskFileName,
		})
	}

	// Run task with dedicated timeout context
	taskCtx, taskCancel := context.WithTimeout(q.ctx, 15*time.Minute)
	defer taskCancel()

	type taskResult struct {
		docID int
		err   error
	}
	resChan := make(chan taskResult, 1)

	go func() {
		var docID int
		var err error
		if p.ExecuteWithContext != nil {
			docID, err = p.ExecuteWithContext(taskCtx, p.TaskID)
		} else if p.Execute != nil {
			docID, err = p.Execute(p.TaskID)
		}
		resChan <- taskResult{docID: docID, err: err}
	}()

	var docID int
	var err error

	select {
	case res := <-resChan:
		docID = res.docID
		err = res.err
	case <-taskCtx.Done():
		if q.ctx.Err() != nil {
			err = fmt.Errorf("task canceled: server shutdown")
		} else {
			err = fmt.Errorf("task timeout: execution exceeded 15 minutes limit")
		}
	}

	now := time.Now()

	if err != nil {
		log.Printf("[Queue] Task %s execution failed: %v", p.TaskID, err)

		var tType string
		var attempts, maxAttempts int
		var payloadJSON sql.NullString
		_ = q.db.QueryRow(`SELECT type, attempt_count, max_attempts, payload_json FROM tasks WHERE task_id = ?`, p.TaskID).
			Scan(&tType, &attempts, &maxAttempts, &payloadJSON)
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		attempts++

		q.mu.RLock()
		_, hasHandler := q.handlers[tType]
		q.mu.RUnlock()

		// Check if eligible for retry (persistent payload, registered handler, attempts remaining, queue not stopping)
		if attempts < maxAttempts && hasHandler && payloadJSON.Valid && payloadJSON.String != "" && payloadJSON.String != "{}" && q.ctx.Err() == nil {
			backoff := time.Duration(1<<uint(attempts-1)) * time.Second
			retryMsg := fmt.Sprintf("Attempt %d/%d failed: %v. Retrying in %v...", attempts, maxAttempts, err, backoff)
			_, _ = q.db.Exec(`UPDATE tasks SET status = 'PENDING', attempt_count = ?, result = ? WHERE task_id = ?`,
				attempts, retryMsg, p.TaskID)
			log.Printf("[Queue] Task %s failed (attempt %d/%d). Retrying in %v: %v", p.TaskID, attempts, maxAttempts, backoff, err)

			if q.broadcaster != nil {
				q.broadcaster.BroadcastTaskUpdate(models.Task{
					TaskID:       p.TaskID,
					Type:         "file_handling",
					Status:       "PENDING",
					TaskName:     p.TaskName,
					TaskFileName: p.TaskFileName,
					Result:       &retryMsg,
				})
			}

			go func(payload TaskPayload, delay time.Duration) {
				select {
				case <-time.After(delay):
					select {
					case q.taskChan <- payload:
					case <-q.ctx.Done():
					}
				case <-q.ctx.Done():
				}
			}(p, backoff)
			return
		}

		// Transition to Dead-Letter Queue (DLQ)
		dlqMsg := fmt.Sprintf("[DLQ] Task permanently failed after %d/%d attempts: %v", attempts, maxAttempts, err)
		log.Printf("[Queue] [DLQ] Task %s moved to Dead-Letter Queue: %v", p.TaskID, dlqMsg)

		var relDocID any = nil
		if docID > 0 {
			relDocID = docID
		}
		if _, dbErr := q.db.Exec(`
			UPDATE tasks SET status = 'FAILURE', result = ?, attempt_count = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
		`, dlqMsg, attempts, relDocID, p.TaskID); dbErr != nil {
			log.Printf("[ERROR] Failed updating DLQ task %s in DB: %v", p.TaskID, dbErr)
		}

		if q.broadcaster != nil {
			var docIDStr *string
			var relDocIDs []int
			if docID > 0 {
				s := strconv.Itoa(docID)
				docIDStr = &s
				relDocIDs = []int{docID}
			}
			q.broadcaster.BroadcastTaskUpdate(models.Task{
				TaskID:             p.TaskID,
				Type:               "file_handling",
				Status:             "FAILURE",
				TaskName:           p.TaskName,
				TaskFileName:       p.TaskFileName,
				DateDone:           &now,
				Result:             &dlqMsg,
				RelatedDocument:    docIDStr,
				RelatedDocumentIDs: relDocIDs,
			})
		}
	} else {
		log.Printf("[Queue] Task %s completed successfully. Document ID: %d", p.TaskID, docID)
		resMsg := "Success. New document added."
		var relDocID any
		if docID > 0 {
			relDocID = docID
		}
		if _, dbErr := q.db.Exec(`
			UPDATE tasks SET status = 'SUCCESS', result = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
		`, resMsg, relDocID, p.TaskID); dbErr != nil {
			log.Printf("[ERROR] Failed updating successful task %s in DB: %v", p.TaskID, dbErr)
		}
		if q.broadcaster != nil {
			docIDStr := strconv.Itoa(docID)
			q.broadcaster.BroadcastTaskUpdate(models.Task{
				TaskID:               p.TaskID,
				Type:                 "file_handling",
				Status:               "SUCCESS",
				TaskName:             p.TaskName,
				TaskFileName:         p.TaskFileName,
				DateDone:             &now,
				Result:               &resMsg,
				RelatedDocument:      &docIDStr,
				RelatedDocumentIDs:   []int{docID},
				TriggerSource:        "Consumption Directory",
				TriggerSourceDisplay: "Auto",
			})
		}
	}
}

func (q *Queue) Close() {
	q.cancel()
	q.wg.Wait()
}
