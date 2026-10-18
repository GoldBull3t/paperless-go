package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

	resp := map[string]int{
		"all":             all,
		"needs_attention": needsAttention,
		"in_progress":     inProgress,
		"completed":       completed,
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
