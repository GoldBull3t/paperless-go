package api

import (
	"database/sql"
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

// Workflow Triggers CRUD (/api/workflow_triggers/)

func (h *WorkflowsHandler) ListTriggers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, type, sources_json, filter_path, filter_filename, filter_mailrule_id, matching_algorithm, match
		FROM workflow_triggers
		ORDER BY id ASC
	`)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.PaginatedResponse{Count: 0, Results: []models.WorkflowTrigger{}})
		return
	}
	defer rows.Close()

	var results []models.WorkflowTrigger
	var allIDs []int
	for rows.Next() {
		var t models.WorkflowTrigger
		var sourcesJSON string
		var mailrule sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Type, &sourcesJSON, &t.FilterPath, &t.FilterFilename, &mailrule, &t.MatchingAlgorithm, &t.Match); err == nil {
			_ = json.Unmarshal([]byte(sourcesJSON), &t.Sources)
			if t.Sources == nil {
				t.Sources = []int{}
			}
			if mailrule.Valid {
				v := int(mailrule.Int64)
				t.FilterMailrule = &v
			}
			results = append(results, t)
			allIDs = append(allIDs, t.ID)
		}
	}
	if results == nil {
		results = []models.WorkflowTrigger{}
		allIDs = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *WorkflowsHandler) GetTrigger(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var t models.WorkflowTrigger
	var sourcesJSON string
	var mailrule sql.NullInt64
	err := h.db.QueryRow(`
		SELECT id, type, sources_json, filter_path, filter_filename, filter_mailrule_id, matching_algorithm, match
		FROM workflow_triggers
		WHERE id = ?
	`, id).Scan(&t.ID, &t.Type, &sourcesJSON, &t.FilterPath, &t.FilterFilename, &mailrule, &t.MatchingAlgorithm, &t.Match)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.Unmarshal([]byte(sourcesJSON), &t.Sources)
	if t.Sources == nil {
		t.Sources = []int{}
	}
	if mailrule.Valid {
		v := int(mailrule.Int64)
		t.FilterMailrule = &v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(t)
}

func (h *WorkflowsHandler) CreateTrigger(w http.ResponseWriter, r *http.Request) {
	var input models.WorkflowTrigger
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourcesJSON, _ := json.Marshal(input.Sources)
	res, err := h.db.Exec(`
		INSERT INTO workflow_triggers (type, sources_json, filter_path, filter_filename, filter_mailrule_id, matching_algorithm, match)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, input.Type, string(sourcesJSON), input.FilterPath, input.FilterFilename, input.FilterMailrule, input.MatchingAlgorithm, input.Match)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	input.ID = int(id)
	if input.Sources == nil {
		input.Sources = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(input)
}

func (h *WorkflowsHandler) UpdateTrigger(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var input models.WorkflowTrigger
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourcesJSON, _ := json.Marshal(input.Sources)
	_, err := h.db.Exec(`
		UPDATE workflow_triggers
		SET type = ?, sources_json = ?, filter_path = ?, filter_filename = ?, filter_mailrule_id = ?, matching_algorithm = ?, match = ?
		WHERE id = ?
	`, input.Type, string(sourcesJSON), input.FilterPath, input.FilterFilename, input.FilterMailrule, input.MatchingAlgorithm, input.Match, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	input.ID = id
	if input.Sources == nil {
		input.Sources = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(input)
}

func (h *WorkflowsHandler) DeleteTrigger(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, _ = h.db.Exec("DELETE FROM workflow_triggers WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

// Workflow Actions CRUD (/api/workflow_actions/)

func (h *WorkflowsHandler) ListActions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, type, assign_title, assign_tags_json, assign_correspondent_id, assign_document_type_id,
		       assign_storage_path_id, assign_owner_id, assign_view_users_json, assign_view_groups_json,
		       assign_change_users_json, assign_change_groups_json, assign_custom_fields_json, email_json, webhook_json
		FROM workflow_actions
		ORDER BY id ASC
	`)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(models.PaginatedResponse{Count: 0, Results: []models.WorkflowAction{}})
		return
	}
	defer rows.Close()

	var results []models.WorkflowAction
	var allIDs []int
	for rows.Next() {
		var a models.WorkflowAction
		var tagsJSON, viewUsersJSON, viewGroupsJSON, chUsersJSON, chGroupsJSON, cfJSON, emailJSON, webhookJSON string
		var corr, dt, sp, owner sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Type, &a.AssignTitle, &tagsJSON, &corr, &dt, &sp, &owner,
			&viewUsersJSON, &viewGroupsJSON, &chUsersJSON, &chGroupsJSON, &cfJSON, &emailJSON, &webhookJSON); err == nil {
			_ = json.Unmarshal([]byte(tagsJSON), &a.AssignTags)
			_ = json.Unmarshal([]byte(viewUsersJSON), &a.AssignViewUsers)
			_ = json.Unmarshal([]byte(viewGroupsJSON), &a.AssignViewGroups)
			_ = json.Unmarshal([]byte(chUsersJSON), &a.AssignChangeUsers)
			_ = json.Unmarshal([]byte(chGroupsJSON), &a.AssignChangeGroups)
			_ = json.Unmarshal([]byte(cfJSON), &a.AssignCustomFields)
			_ = json.Unmarshal([]byte(emailJSON), &a.Email)
			_ = json.Unmarshal([]byte(webhookJSON), &a.Webhook)
			if a.AssignTags == nil {
				a.AssignTags = []int{}
			}
			if corr.Valid {
				v := int(corr.Int64)
				a.AssignCorrespondent = &v
			}
			if dt.Valid {
				v := int(dt.Int64)
				a.AssignDocumentType = &v
			}
			if sp.Valid {
				v := int(sp.Int64)
				a.AssignStoragePath = &v
			}
			if owner.Valid {
				v := int(owner.Int64)
				a.AssignOwner = &v
			}
			results = append(results, a)
			allIDs = append(allIDs, a.ID)
		}
	}
	if results == nil {
		results = []models.WorkflowAction{}
		allIDs = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *WorkflowsHandler) GetAction(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var a models.WorkflowAction
	var tagsJSON, viewUsersJSON, viewGroupsJSON, chUsersJSON, chGroupsJSON, cfJSON, emailJSON, webhookJSON string
	var corr, dt, sp, owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT id, type, assign_title, assign_tags_json, assign_correspondent_id, assign_document_type_id,
		       assign_storage_path_id, assign_owner_id, assign_view_users_json, assign_view_groups_json,
		       assign_change_users_json, assign_change_groups_json, assign_custom_fields_json, email_json, webhook_json
		FROM workflow_actions
		WHERE id = ?
	`, id).Scan(&a.ID, &a.Type, &a.AssignTitle, &tagsJSON, &corr, &dt, &sp, &owner,
		&viewUsersJSON, &viewGroupsJSON, &chUsersJSON, &chGroupsJSON, &cfJSON, &emailJSON, &webhookJSON)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.Unmarshal([]byte(tagsJSON), &a.AssignTags)
	_ = json.Unmarshal([]byte(viewUsersJSON), &a.AssignViewUsers)
	_ = json.Unmarshal([]byte(viewGroupsJSON), &a.AssignViewGroups)
	_ = json.Unmarshal([]byte(chUsersJSON), &a.AssignChangeUsers)
	_ = json.Unmarshal([]byte(chGroupsJSON), &a.AssignChangeGroups)
	_ = json.Unmarshal([]byte(cfJSON), &a.AssignCustomFields)
	_ = json.Unmarshal([]byte(emailJSON), &a.Email)
	_ = json.Unmarshal([]byte(webhookJSON), &a.Webhook)
	if a.AssignTags == nil {
		a.AssignTags = []int{}
	}
	if corr.Valid {
		v := int(corr.Int64)
		a.AssignCorrespondent = &v
	}
	if dt.Valid {
		v := int(dt.Int64)
		a.AssignDocumentType = &v
	}
	if sp.Valid {
		v := int(sp.Int64)
		a.AssignStoragePath = &v
	}
	if owner.Valid {
		v := int(owner.Int64)
		a.AssignOwner = &v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(a)
}

func (h *WorkflowsHandler) CreateAction(w http.ResponseWriter, r *http.Request) {
	var input models.WorkflowAction
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tagsJSON, _ := json.Marshal(input.AssignTags)
	viewUsersJSON, _ := json.Marshal(input.AssignViewUsers)
	viewGroupsJSON, _ := json.Marshal(input.AssignViewGroups)
	chUsersJSON, _ := json.Marshal(input.AssignChangeUsers)
	chGroupsJSON, _ := json.Marshal(input.AssignChangeGroups)
	cfJSON, _ := json.Marshal(input.AssignCustomFields)
	emailJSON, _ := json.Marshal(input.Email)
	webhookJSON, _ := json.Marshal(input.Webhook)

	res, err := h.db.Exec(`
		INSERT INTO workflow_actions (type, assign_title, assign_tags_json, assign_correspondent_id, assign_document_type_id,
		       assign_storage_path_id, assign_owner_id, assign_view_users_json, assign_view_groups_json,
		       assign_change_users_json, assign_change_groups_json, assign_custom_fields_json, email_json, webhook_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Type, input.AssignTitle, string(tagsJSON), input.AssignCorrespondent, input.AssignDocumentType,
		input.AssignStoragePath, input.AssignOwner, string(viewUsersJSON), string(viewGroupsJSON),
		string(chUsersJSON), string(chGroupsJSON), string(cfJSON), string(emailJSON), string(webhookJSON))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, _ := res.LastInsertId()
	input.ID = int(id)
	if input.AssignTags == nil {
		input.AssignTags = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(input)
}

func (h *WorkflowsHandler) UpdateAction(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var input models.WorkflowAction
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tagsJSON, _ := json.Marshal(input.AssignTags)
	viewUsersJSON, _ := json.Marshal(input.AssignViewUsers)
	viewGroupsJSON, _ := json.Marshal(input.AssignViewGroups)
	chUsersJSON, _ := json.Marshal(input.AssignChangeUsers)
	chGroupsJSON, _ := json.Marshal(input.AssignChangeGroups)
	cfJSON, _ := json.Marshal(input.AssignCustomFields)
	emailJSON, _ := json.Marshal(input.Email)
	webhookJSON, _ := json.Marshal(input.Webhook)

	_, err := h.db.Exec(`
		UPDATE workflow_actions
		SET type = ?, assign_title = ?, assign_tags_json = ?, assign_correspondent_id = ?, assign_document_type_id = ?,
		    assign_storage_path_id = ?, assign_owner_id = ?, assign_view_users_json = ?, assign_view_groups_json = ?,
		    assign_change_users_json = ?, assign_change_groups_json = ?, assign_custom_fields_json = ?,
		    email_json = ?, webhook_json = ?
		WHERE id = ?
	`, input.Type, input.AssignTitle, string(tagsJSON), input.AssignCorrespondent, input.AssignDocumentType,
		input.AssignStoragePath, input.AssignOwner, string(viewUsersJSON), string(viewGroupsJSON),
		string(chUsersJSON), string(chGroupsJSON), string(cfJSON), string(emailJSON), string(webhookJSON), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	input.ID = id
	if input.AssignTags == nil {
		input.AssignTags = []int{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(input)
}

func (h *WorkflowsHandler) DeleteAction(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, _ = h.db.Exec("DELETE FROM workflow_actions WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

