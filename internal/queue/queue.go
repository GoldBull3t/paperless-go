package queue

import (
	"context"
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
	TaskID       string
	TaskName     string
	TaskFileName string
	FilePath     string
	Execute      func(taskID string) (int, error) // Returns related document ID and error
}

type Broadcaster interface {
	BroadcastTaskUpdate(task models.Task)
}

type Queue struct {
	db          *database.DB
	taskChan    chan TaskPayload
	broadcaster Broadcaster
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewQueue(db *database.DB, workerCount int) *Queue {
	return NewQueueWithContext(context.Background(), db, workerCount)
}

func NewQueueWithContext(parentCtx context.Context, db *database.DB, workerCount int) *Queue {
	// Clean up any previously interrupted tasks
	if _, err := db.Exec("UPDATE tasks SET status = 'FAILURE', result = 'Interrupted by server restart', date_done = CURRENT_TIMESTAMP WHERE status IN ('PENDING', 'STARTED')"); err != nil {
		log.Printf("[WARN] Failed cleaning up interrupted tasks on startup: %v", err)
	}

	ctx, cancel := context.WithCancel(parentCtx)

	q := &Queue{
		db:       db,
		taskChan: make(chan TaskPayload, 100),
		ctx:      ctx,
		cancel:   cancel,
	}

	for i := 0; i < workerCount; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}

	return q
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
		docID, err := p.Execute(p.TaskID)
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
		log.Printf("[Queue] Task %s failed: %v", p.TaskID, err)
		errMsg := err.Error()
		if docID > 0 {
			docIDStr := strconv.Itoa(docID)
			if _, dbErr := q.db.Exec(`
				UPDATE tasks SET status = 'FAILURE', result = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
			`, errMsg, docID, p.TaskID); dbErr != nil {
				log.Printf("[ERROR] Failed updating failed task %s in DB: %v", p.TaskID, dbErr)
			}
			if q.broadcaster != nil {
				q.broadcaster.BroadcastTaskUpdate(models.Task{
					TaskID:             p.TaskID,
					Type:               "file_handling",
					Status:             "FAILURE",
					TaskName:           p.TaskName,
					TaskFileName:       p.TaskFileName,
					DateDone:           &now,
					Result:             &errMsg,
					RelatedDocument:    &docIDStr,
					RelatedDocumentIDs: []int{docID},
				})
			}
		} else {
			if _, dbErr := q.db.Exec(`
				UPDATE tasks SET status = 'FAILURE', result = ?, date_done = CURRENT_TIMESTAMP WHERE task_id = ?
			`, errMsg, p.TaskID); dbErr != nil {
				log.Printf("[ERROR] Failed updating failed task %s in DB: %v", p.TaskID, dbErr)
			}
			if q.broadcaster != nil {
				q.broadcaster.BroadcastTaskUpdate(models.Task{
					TaskID:       p.TaskID,
					Type:         "file_handling",
					Status:       "FAILURE",
					TaskName:     p.TaskName,
					TaskFileName: p.TaskFileName,
					DateDone:     &now,
					Result:       &errMsg,
				})
			}
		}
	} else {
		log.Printf("[Queue] Task %s completed successfully. Document ID: %d", p.TaskID, docID)
		resMsg := "Success. New document added."
		if _, dbErr := q.db.Exec(`
			UPDATE tasks SET status = 'SUCCESS', result = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
		`, resMsg, docID, p.TaskID); dbErr != nil {
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
