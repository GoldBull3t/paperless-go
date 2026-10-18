package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type WorkflowsHandler struct {
	db *database.DB
}

func NewWorkflowsHandler(db *database.DB) *WorkflowsHandler {
	return &WorkflowsHandler{db: db}
}

func (h *WorkflowsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, name, order_num, enabled, triggers_json, actions_json
		FROM workflows
		ORDER BY order_num ASC, id ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Workflow
	var allIDs []int

	for rows.Next() {
		var wf models.Workflow
		var trigsJSON, actsJSON string

		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Order, &wf.Enabled, &trigsJSON, &actsJSON); err == nil {
			_ = json.Unmarshal([]byte(trigsJSON), &wf.Triggers)
			if wf.Triggers == nil {
				wf.Triggers = []map[string]interface{}{}
			}
			_ = json.Unmarshal([]byte(actsJSON), &wf.Actions)
			if wf.Actions == nil {
				wf.Actions = []map[string]interface{}{}
			}

			results = append(results, wf)
			allIDs = append(allIDs, wf.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.Workflow{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *WorkflowsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	wf, err := h.fetchWorkflow(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input models.Workflow
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trigsJSON, _ := json.Marshal(input.Triggers)
	if input.Triggers == nil {
		trigsJSON = []byte("[]")
	}
	actsJSON, _ := json.Marshal(input.Actions)
	if input.Actions == nil {
		actsJSON = []byte("[]")
	}

	res, err := h.db.Exec(`
		INSERT INTO workflows (name, order_num, enabled, triggers_json, actions_json)
		VALUES (?, ?, ?, ?, ?)
	`, input.Name, input.Order, input.Enabled, string(trigsJSON), string(actsJSON))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	newID, _ := res.LastInsertId()
	wf, err := h.fetchWorkflow(int(newID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(wf)
}

func (h *WorkflowsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	var input map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	current, err := h.fetchWorkflow(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if name, ok := input["name"].(string); ok {
		current.Name = name
	}
	if order, ok := input["order"].(float64); ok {
		current.Order = int(order)
	}
	if enabled, ok := input["enabled"].(bool); ok {
		current.Enabled = enabled
	}
	if trigs, ok := input["triggers"]; ok {
		var trigsList []map[string]interface{}
		b, _ := json.Marshal(trigs)
		_ = json.Unmarshal(b, &trigsList)
		current.Triggers = trigsList
	}
	if acts, ok := input["actions"]; ok {
		var actsList []map[string]interface{}
		b, _ := json.Marshal(acts)
		_ = json.Unmarshal(b, &actsList)
		current.Actions = actsList
	}

	trigsJSON, _ := json.Marshal(current.Triggers)
	actsJSON, _ := json.Marshal(current.Actions)

	_, err = h.db.Exec(`
		UPDATE workflows
		SET name = ?, order_num = ?, enabled = ?, triggers_json = ?, actions_json = ?
		WHERE id = ?
	`, current.Name, current.Order, current.Enabled, string(trigsJSON), string(actsJSON), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(current)
}

func (h *WorkflowsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, _ = h.db.Exec("DELETE FROM workflows WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *WorkflowsHandler) fetchWorkflow(id int) (*models.Workflow, error) {
	var wf models.Workflow
	var trigsJSON, actsJSON string

	err := h.db.QueryRow(`
		SELECT id, name, order_num, enabled, triggers_json, actions_json
		FROM workflows
		WHERE id = ?
	`, id).Scan(&wf.ID, &wf.Name, &wf.Order, &wf.Enabled, &trigsJSON, &actsJSON)
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(trigsJSON), &wf.Triggers)
	if wf.Triggers == nil {
		wf.Triggers = []map[string]interface{}{}
	}
	_ = json.Unmarshal([]byte(actsJSON), &wf.Actions)
	if wf.Actions == nil {
		wf.Actions = []map[string]interface{}{}
	}

	return &wf, nil
}

