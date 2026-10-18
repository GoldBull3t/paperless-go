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

type CorrespondentsHandler struct {
	db *database.DB
}

func NewCorrespondentsHandler(db *database.DB) *CorrespondentsHandler {
	return &CorrespondentsHandler{db: db}
}

func (h *CorrespondentsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT c.id, c.slug, c.name, c.match, c.matching_algorithm, c.is_insensitive, c.last_correspondence, c.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.correspondent_id = c.id) as doc_count
		FROM correspondents c
		ORDER BY c.name COLLATE NOCASE ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Correspondent
	var allIDs []int

	for rows.Next() {
		var c models.Correspondent
		var owner sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Match, &c.MatchingAlgorithm, &c.IsInsensitive, &c.LastCorrespondence, &owner, &c.DocumentCount); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				c.Owner = &o
			}
			c.UserCanChange = true
			results = append(results, c)
			allIDs = append(allIDs, c.ID)
		}
	}

	if results == nil {
		results = []models.Correspondent{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *CorrespondentsHandler) Create(w http.ResponseWriter, r *http.Request) {
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
		INSERT INTO correspondents (name, slug, match, matching_algorithm, is_insensitive)
		VALUES (?, ?, ?, ?, ?)
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	c := models.Correspondent{
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
	_ = json.NewEncoder(w).Encode(c)
}

func (h *CorrespondentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var c models.Correspondent
	var owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT c.id, c.slug, c.name, c.match, c.matching_algorithm, c.is_insensitive, c.last_correspondence, c.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.correspondent_id = c.id) as doc_count
		FROM correspondents c WHERE c.id = ?
	`, id).Scan(&c.ID, &c.Slug, &c.Name, &c.Match, &c.MatchingAlgorithm, &c.IsInsensitive, &c.LastCorrespondence, &owner, &c.DocumentCount)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		c.Owner = &o
	}
	c.UserCanChange = true

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

func (h *CorrespondentsHandler) Update(w http.ResponseWriter, r *http.Request) {
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
		UPDATE correspondents SET name = ?, slug = ?, match = ?, matching_algorithm = ?, is_insensitive = ?
		WHERE id = ?
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.Get(w, r)
}

func (h *CorrespondentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM correspondents WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
