package queue

import (
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
	quit        chan struct{}
}

func NewQueue(db *database.DB, workerCount int) *Queue {
	// Clean up any previously interrupted tasks
	_, _ = db.Exec("UPDATE tasks SET status = 'FAILURE', result = 'Interrupted by server restart', date_done = CURRENT_TIMESTAMP WHERE status IN ('PENDING', 'STARTED')")

	q := &Queue{
		db:       db,
		taskChan: make(chan TaskPayload, 100),
		quit:     make(chan struct{}),
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
		log.Printf("Error creating task in DB: %v", err)
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

	q.taskChan <- TaskPayload{
		TaskID:       taskID,
		TaskName:     taskName,
		TaskFileName: fileName,
		FilePath:     filePath,
		Execute:      exec,
	}

	return taskID
}

func (q *Queue) worker(id int) {
	defer q.wg.Done()
	for {
		select {
		case payload := <-q.taskChan:
			q.processTask(payload)
		case <-q.quit:
			return
		}
	}
}

func (q *Queue) processTask(p TaskPayload) {
	log.Printf("[Queue] Starting task %s (%s)", p.TaskID, p.TaskFileName)

	// Update to STARTED
	_, _ = q.db.Exec(`UPDATE tasks SET status = 'STARTED' WHERE task_id = ?`, p.TaskID)
	if q.broadcaster != nil {
		q.broadcaster.BroadcastTaskUpdate(models.Task{
			TaskID:       p.TaskID,
			Type:         "file_handling",
			Status:       "STARTED",
			TaskName:     p.TaskName,
			TaskFileName: p.TaskFileName,
		})
	}

	docID, err := p.Execute(p.TaskID)
	now := time.Now()

	if err != nil {
		log.Printf("[Queue] Task %s failed: %v", p.TaskID, err)
		errMsg := err.Error()
		if docID > 0 {
			docIDStr := strconv.Itoa(docID)
			_, _ = q.db.Exec(`
				UPDATE tasks SET status = 'FAILURE', result = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
			`, errMsg, docID, p.TaskID)
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
			_, _ = q.db.Exec(`
				UPDATE tasks SET status = 'FAILURE', result = ?, date_done = CURRENT_TIMESTAMP WHERE task_id = ?
			`, errMsg, p.TaskID)
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
		_, _ = q.db.Exec(`
			UPDATE tasks SET status = 'SUCCESS', result = ?, date_done = CURRENT_TIMESTAMP, related_document_id = ? WHERE task_id = ?
		`, resMsg, docID, p.TaskID)
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
	close(q.quit)
	q.wg.Wait()
}
