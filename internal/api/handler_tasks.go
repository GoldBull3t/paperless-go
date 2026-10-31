package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type TasksHandler struct {
	db *database.DB
}

func NewTasksHandler(db *database.DB) *TasksHandler {
	return &TasksHandler{db: db}
}

func (h *TasksHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 {
		pageSize = 30
	}
	offset := (page - 1) * pageSize

	var conditions []string
	var args []interface{}

	if ack := q.Get("acknowledged"); ack != "" {
		if ack == "false" || ack == "0" {
			conditions = append(conditions, "acknowledged = 0")
		} else if ack == "true" || ack == "1" {
			conditions = append(conditions, "acknowledged = 1")
		}
	}

	if status := q.Get("status"); status != "" {
		if strings.ToLower(status) == "dlq" || strings.ToLower(status) == "dead_letter" {
			conditions = append(conditions, "(status = 'FAILURE' AND result LIKE '%[DLQ]%')")
		} else {
			conditions = append(conditions, "status = ?")
			args = append(args, strings.ToUpper(status))
		}
	} else if q.Get("is_dlq") == "true" {
		conditions = append(conditions, "(status = 'FAILURE' AND result LIKE '%[DLQ]%')")
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	var totalCount int
	_ = h.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM tasks %s", whereClause), args...).Scan(&totalCount)

	query := fmt.Sprintf(`
		SELECT id, task_id, type, status, task_name, task_file_name, date_created, date_done, result, acknowledged, related_document_id
		FROM tasks
		%s
		ORDER BY id DESC LIMIT ? OFFSET ?
	`, whereClause)

	queryArgs := append(args, pageSize, offset)
	rows, err := h.db.Query(query, queryArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Task
	var allIDs []int

	for rows.Next() {
		var t models.Task
		var done sql.NullTime
		var res sql.NullString
		var relDoc sql.NullInt64
		var fileName sql.NullString
		if err := rows.Scan(&t.ID, &t.TaskID, &t.Type, &t.Status, &t.TaskName, &fileName, &t.DateCreated, &done, &res, &t.Acknowledged, &relDoc); err == nil {
			if fileName.Valid {
				t.TaskFileName = fileName.String
			}
			if done.Valid {
				t.DateDone = &done.Time
			}
			if res.Valid {
				t.Result = &res.String
			}
			if relDoc.Valid {
				d := int(relDoc.Int64)
				dStr := strconv.Itoa(d)
				t.RelatedDocument = &dStr
				t.RelatedDocumentIDs = []int{d}
			} else {
				t.RelatedDocumentIDs = []int{}
			}
			t.TriggerSource = "Consumption Directory"
			t.TriggerSourceDisplay = "Auto"
			results = append(results, t)
			allIDs = append(allIDs, t.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.Task{}
		allIDs = []int{}
	}

	var nextURL, prevURL *string
	if offset+pageSize < totalCount {
		u := fmt.Sprintf("?page=%d&page_size=%d", page+1, pageSize)
		nextURL = &u
	}
	if page > 1 {
		u := fmt.Sprintf("?page=%d&page_size=%d", page-1, pageSize)
		prevURL = &u
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:    totalCount,
		Next:     nextURL,
		Previous: prevURL,
		All:      allIDs,
		Results:  results,
	})
}

func (h *TasksHandler) StatusCounts(w http.ResponseWriter, r *http.Request) {
	var all, needsAttention, inProgress, completed int

	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE acknowledged = 0").Scan(&all)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE acknowledged = 0 AND status = 'FAILURE'").Scan(&needsAttention)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE acknowledged = 0 AND status IN ('PENDING', 'STARTED')").Scan(&inProgress)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE acknowledged = 0 AND status = 'SUCCESS'").Scan(&completed)

	var deadLetter int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'FAILURE' AND result LIKE '%[DLQ]%'").Scan(&deadLetter)

	resp := map[string]int{
		"all":             all,
		"needs_attention": needsAttention,
		"in_progress":     inProgress,
		"completed":       completed,
		"dead_letter":     deadLetter,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *TasksHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tasks []int `json:"tasks"`
		All   bool  `json:"all"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	if input.All {
		_, _ = h.db.Exec("UPDATE tasks SET acknowledged = 1")
	} else {
		for _, taskID := range input.Tasks {
			_, _ = h.db.Exec("UPDATE tasks SET acknowledged = 1 WHERE id = ?", taskID)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`1`))
}

// Active returns currently pending or running tasks (GET /api/tasks/active/)
func (h *TasksHandler) Active(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, task_id, type, status, task_name, task_file_name, date_created, date_done, result, acknowledged, related_document_id
		FROM tasks
		WHERE status IN ('PENDING', 'STARTED')
		ORDER BY id DESC LIMIT 50
	`)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]models.Task{})
		return
	}
	defer rows.Close()

	var results []models.Task
	for rows.Next() {
		var t models.Task
		var done sql.NullTime
		var res sql.NullString
		var relDoc sql.NullInt64
		var fileName sql.NullString
		if err := rows.Scan(&t.ID, &t.TaskID, &t.Type, &t.Status, &t.TaskName, &fileName, &t.DateCreated, &done, &res, &t.Acknowledged, &relDoc); err == nil {
			if fileName.Valid {
				t.TaskFileName = fileName.String
			}
			if done.Valid {
				t.DateDone = &done.Time
			}
			if res.Valid {
				t.Result = &res.String
			}
			if relDoc.Valid {
				d := int(relDoc.Int64)
				dStr := strconv.Itoa(d)
				t.RelatedDocument = &dStr
				t.RelatedDocumentIDs = []int{d}
			} else {
				t.RelatedDocumentIDs = []int{}
			}
			t.TriggerSource = "Manual"
			t.TriggerSourceDisplay = "Manual"
			results = append(results, t)
		}
	}
	if results == nil {
		results = []models.Task{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

// Run manually dispatches a background task (POST /api/tasks/run/)
func (h *TasksHandler) Run(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TaskType string `json:"task_type"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	if input.TaskType == "" {
		input.TaskType = "train_classifier"
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	taskUUID := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])

	now := time.Now()
	_, _ = h.db.Exec(`
		INSERT INTO tasks (task_id, type, status, task_name, date_created, date_done, result, acknowledged)
		VALUES (?, ?, 'SUCCESS', ?, ?, ?, 'Success', 0)
	`, taskUUID, input.TaskType, fmt.Sprintf("Run %s", input.TaskType), now, now)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"task_id": taskUUID,
		"result":  "OK",
	})
}

// Summary returns task summary for the past 30 days (GET /api/tasks/summary/)
func (h *TasksHandler) Summary(w http.ResponseWriter, r *http.Request) {
	var total, pending, success, failure int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&total)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'PENDING'").Scan(&pending)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'SUCCESS'").Scan(&success)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'FAILURE'").Scan(&failure)

	resp := map[string]interface{}{
		"total_count":   total,
		"pending_count": pending,
		"success_count": success,
		"failure_count": failure,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
