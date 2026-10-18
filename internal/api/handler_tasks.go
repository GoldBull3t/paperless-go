package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

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
	rows, err := h.db.Query(`
		SELECT id, task_id, type, status, task_name, task_file_name, date_created, date_done, result, acknowledged, related_document_id
		FROM tasks
		ORDER BY id DESC LIMIT 50
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
				t.RelatedDocument = &d
			}
			results = append(results, t)
		}
	}

	if results == nil {
		results = []models.Task{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *TasksHandler) Acknowledge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Tasks []int `json:"tasks"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	for _, taskID := range input.Tasks {
		_, _ = h.db.Exec("UPDATE tasks SET acknowledged = 1 WHERE id = ?", taskID)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "Tasks acknowledged"}`))
}
