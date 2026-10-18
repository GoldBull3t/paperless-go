package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	user := GetUserFromContext(r.Context())
	var whereClause string
	var args []interface{}
	if user != nil && !user.IsSuperuser {
		whereClause = "WHERE (c.owner_id IS NULL OR c.owner_id = ?)"
		args = append(args, user.ID)
	}

	query := fmt.Sprintf(`
		SELECT c.id, c.slug, c.name, c.match, c.matching_algorithm, c.is_insensitive, c.last_correspondence, c.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.correspondent_id = c.id) as doc_count
		FROM correspondents c
		%s
		ORDER BY c.name COLLATE NOCASE ASC
	`, whereClause)

	rows, err := h.db.Query(query, args...)
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
			c.UserCanChange = (user == nil || user.IsSuperuser || c.Owner == nil || *c.Owner == user.ID)
			results = append(results, c)
			allIDs = append(allIDs, c.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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
		Owner             *int   `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	var ownerID *int
	if user != nil {
		ownerID = &user.ID
	}
	if input.Owner != nil && user != nil && user.IsSuperuser {
		ownerID = input.Owner
	}

	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
	res, err := h.db.Exec(`
		INSERT INTO correspondents (name, slug, match, matching_algorithm, is_insensitive, owner_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive, ownerID)
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
		Owner:             ownerID,
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

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && c.Owner != nil && *c.Owner != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}
	c.UserCanChange = (user == nil || user.IsSuperuser || c.Owner == nil || *c.Owner == user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

func (h *CorrespondentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM correspondents WHERE id = ?", id).Scan(&existingOwner)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && existingOwner.Valid && int(existingOwner.Int64) != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var input struct {
		Name              string `json:"name"`
		Match             string `json:"match"`
		MatchingAlgorithm int    `json:"matching_algorithm"`
		IsInsensitive     bool   `json:"is_insensitive"`
		Owner             *int   `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
	_, err = h.db.Exec(`
		UPDATE correspondents SET name = ?, slug = ?, match = ?, matching_algorithm = ?, is_insensitive = ?
		WHERE id = ?
	`, input.Name, slug, input.Match, input.MatchingAlgorithm, input.IsInsensitive, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Owner != nil && (user == nil || user.IsSuperuser || *input.Owner == user.ID) {
		if *input.Owner == 0 {
			_, _ = h.db.Exec("UPDATE correspondents SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE correspondents SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	h.Get(w, r)
}

func (h *CorrespondentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM correspondents WHERE id = ?", id).Scan(&existingOwner)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && existingOwner.Valid && int(existingOwner.Int64) != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	_, err = h.db.Exec("DELETE FROM correspondents WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
