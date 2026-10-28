package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type CustomFieldsHandler struct {
	db *database.DB
}

func NewCustomFieldsHandler(db *database.DB) *CustomFieldsHandler {
	return &CustomFieldsHandler{db: db}
}

func (h *CustomFieldsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT cf.id, cf.name, cf.data_type, cf.extra_data, COUNT(dcf.document_id) AS doc_count
		FROM custom_fields cf
		LEFT JOIN document_custom_fields dcf ON cf.id = dcf.field_id
		GROUP BY cf.id, cf.name, cf.data_type, cf.extra_data
		ORDER BY cf.name ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.CustomField
	var allIDs []int

	for rows.Next() {
		var cf models.CustomField
		var extra string
		if err := rows.Scan(&cf.ID, &cf.Name, &cf.DataType, &extra, &cf.DocumentCount); err == nil {
			_ = json.Unmarshal([]byte(extra), &cf.ExtraData)
			if cf.ExtraData == nil {
				cf.ExtraData = make(map[string]interface{})
			}
			results = append(results, cf)
			allIDs = append(allIDs, cf.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.CustomField{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *CustomFieldsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string                 `json:"name"`
		DataType  string                 `json:"data_type"`
		ExtraData map[string]interface{} `json:"extra_data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	extraJSON, _ := json.Marshal(input.ExtraData)
	res, err := h.db.Exec(`INSERT INTO custom_fields (name, data_type, extra_data) VALUES (?, ?, ?)`, input.Name, input.DataType, string(extraJSON))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	cf := models.CustomField{
		ID:        int(id),
		Name:      input.Name,
		DataType:  input.DataType,
		ExtraData: input.ExtraData,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(cf)
}

func (h *CustomFieldsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var cf models.CustomField
	var extra string
	err := h.db.QueryRow(`
		SELECT cf.id, cf.name, cf.data_type, cf.extra_data, COUNT(dcf.document_id) AS doc_count
		FROM custom_fields cf
		LEFT JOIN document_custom_fields dcf ON cf.id = dcf.field_id
		WHERE cf.id = ?
		GROUP BY cf.id, cf.name, cf.data_type, cf.extra_data
	`, id).Scan(&cf.ID, &cf.Name, &cf.DataType, &extra, &cf.DocumentCount)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.Unmarshal([]byte(extra), &cf.ExtraData)
	if cf.ExtraData == nil {
		cf.ExtraData = make(map[string]interface{})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cf)
}

func (h *CustomFieldsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var input struct {
		Name      *string                 `json:"name"`
		DataType  *string                 `json:"data_type"`
		ExtraData *map[string]interface{} `json:"extra_data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name != nil {
		_, _ = h.db.Exec("UPDATE custom_fields SET name = ? WHERE id = ?", *input.Name, id)
	}
	if input.DataType != nil {
		_, _ = h.db.Exec("UPDATE custom_fields SET data_type = ? WHERE id = ?", *input.DataType, id)
	}
	if input.ExtraData != nil {
		extraJSON, _ := json.Marshal(*input.ExtraData)
		_, _ = h.db.Exec("UPDATE custom_fields SET extra_data = ? WHERE id = ?", string(extraJSON), id)
	}

	h.Get(w, r)
}

func (h *CustomFieldsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM custom_fields WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
