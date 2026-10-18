package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type DocumentTypesHandler struct {
	db *database.DB
}

func NewDocumentTypesHandler(db *database.DB) *DocumentTypesHandler {
	return &DocumentTypesHandler{db: db}
}

func (h *DocumentTypesHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT dt.id, dt.slug, dt.name, dt.match, dt.matching_algorithm, dt.is_insensitive, dt.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.document_type_id = dt.id) as doc_count
		FROM document_types dt
		ORDER BY dt.name COLLATE NOCASE ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.DocumentType
	var allIDs []int

	for rows.Next() {
		var dt models.DocumentType
		var owner sql.NullInt64
		if err := rows.Scan(&dt.ID, &dt.Slug, &dt.Name, &dt.Match, &dt.MatchingAlgorithm, &dt.IsInsensitive, &owner, &dt.DocumentCount); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				dt.Owner = &o
			}
			dt.UserCanChange = true
			results = append(results, dt)
			allIDs = append(allIDs, dt.ID)
		}
	}

	if results == nil {
		results = []models.DocumentType{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *DocumentTypesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name              string `json:"name"`
		Match             string `json:"match"`
		MatchingAlgorithm int    `json:"matching_algorithm"`
		IsInsensitive     bool   `json:"is_insensitive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
	res, err := h.db.Exec(`
		INSERT INTO document_types (name, slug, match, matching_algorithm, is_insensitive)
		VALUES (?, ?, ?, ?, ?)
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	dt := models.DocumentType{
		ID:                int(id),
		Slug:              slug,
		Name:              input.Name,
		Match:             input.Match,
		MatchingAlgorithm: input.MatchingAlgorithm,
		IsInsensitive:     input.IsInsensitive,
		UserCanChange:     true,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(dt)
}

func (h *DocumentTypesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var dt models.DocumentType
	var owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT dt.id, dt.slug, dt.name, dt.match, dt.matching_algorithm, dt.is_insensitive, dt.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.document_type_id = dt.id) as doc_count
		FROM document_types dt WHERE dt.id = ?
	`, id).Scan(&dt.ID, &dt.Slug, &dt.Name, &dt.Match, &dt.MatchingAlgorithm, &dt.IsInsensitive, &owner, &dt.DocumentCount)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		dt.Owner = &o
	}
	dt.UserCanChange = true

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dt)
}

func (h *DocumentTypesHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var input struct {
		Name              string `json:"name"`
		Match             string `json:"match"`
		MatchingAlgorithm int    `json:"matching_algorithm"`
		IsInsensitive     bool   `json:"is_insensitive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
	_, err := h.db.Exec(`
		UPDATE document_types SET name = ?, slug = ?, match = ?, matching_algorithm = ?, is_insensitive = ?
		WHERE id = ?
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.Get(w, r)
}

func (h *DocumentTypesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM document_types WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
