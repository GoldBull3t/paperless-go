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

type StoragePathsHandler struct {
	db *database.DB
}

func NewStoragePathsHandler(db *database.DB) *StoragePathsHandler {
	return &StoragePathsHandler{db: db}
}

func (h *StoragePathsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var whereClause string
	var args []interface{}
	if user != nil && !user.IsSuperuser {
		whereClause = "WHERE (sp.owner_id IS NULL OR sp.owner_id = ?)"
		args = append(args, user.ID)
	}

	query := fmt.Sprintf(`
		SELECT sp.id, sp.slug, sp.name, sp.path, sp.match, sp.matching_algorithm, sp.is_insensitive, sp.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.storage_path_id = sp.id) as doc_count
		FROM storage_paths sp
		%s
		ORDER BY sp.name COLLATE NOCASE ASC
	`, whereClause)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.StoragePath
	var allIDs []int

	for rows.Next() {
		var sp models.StoragePath
		var owner sql.NullInt64
		if err := rows.Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.Path, &sp.Match, &sp.MatchingAlgorithm, &sp.IsInsensitive, &owner, &sp.DocumentCount); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				sp.Owner = &o
			}
			sp.UserCanChange = (user == nil || user.IsSuperuser || sp.Owner == nil || *sp.Owner == user.ID)
			results = append(results, sp)
			allIDs = append(allIDs, sp.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.StoragePath{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *StoragePathsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name              string `json:"name"`
		Path              string `json:"path"`
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
		INSERT INTO storage_paths (name, slug, path, match, matching_algorithm, is_insensitive, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, input.Name, slug, input.Path, input.Match, input.MatchingAlgorithm, input.IsInsensitive, ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	sp := models.StoragePath{
		ID:                int(id),
		Slug:              slug,
		Name:              input.Name,
		Path:              input.Path,
		Match:             input.Match,
		MatchingAlgorithm: input.MatchingAlgorithm,
		IsInsensitive:     input.IsInsensitive,
		Owner:             ownerID,
		UserCanChange:     true,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sp)
}

func (h *StoragePathsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var sp models.StoragePath
	var owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT sp.id, sp.slug, sp.name, sp.path, sp.match, sp.matching_algorithm, sp.is_insensitive, sp.owner_id,
		       (SELECT COUNT(*) FROM documents d WHERE d.storage_path_id = sp.id) as doc_count
		FROM storage_paths sp WHERE sp.id = ?
	`, id).Scan(&sp.ID, &sp.Slug, &sp.Name, &sp.Path, &sp.Match, &sp.MatchingAlgorithm, &sp.IsInsensitive, &owner, &sp.DocumentCount)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		sp.Owner = &o
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && sp.Owner != nil && *sp.Owner != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}
	sp.UserCanChange = (user == nil || user.IsSuperuser || sp.Owner == nil || *sp.Owner == user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sp)
}

func (h *StoragePathsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM storage_paths WHERE id = ?", id).Scan(&existingOwner)
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
		Path              string `json:"path"`
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
		UPDATE storage_paths SET name = ?, slug = ?, path = ?, match = ?, matching_algorithm = ?, is_insensitive = ?
		WHERE id = ?
	`, input.Name, slug, input.Path, input.Match, input.MatchingAlgorithm, input.IsInsensitive, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Owner != nil && (user == nil || user.IsSuperuser || *input.Owner == user.ID) {
		if *input.Owner == 0 {
			_, _ = h.db.Exec("UPDATE storage_paths SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE storage_paths SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	h.Get(w, r)
}

func (h *StoragePathsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM storage_paths WHERE id = ?", id).Scan(&existingOwner)
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

	_, err = h.db.Exec("DELETE FROM storage_paths WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
