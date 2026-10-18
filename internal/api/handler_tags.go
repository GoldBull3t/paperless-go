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

type TagsHandler struct {
	db *database.DB
}

func NewTagsHandler(db *database.DB) *TagsHandler {
	return &TagsHandler{db: db}
}

func (h *TagsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var whereClause string
	var args []interface{}
	if user != nil && !user.IsSuperuser {
		whereClause = "WHERE (t.owner_id IS NULL OR t.owner_id = ?)"
		args = append(args, user.ID)
	}

	query := fmt.Sprintf(`
		SELECT t.id, t.slug, t.name, t.color, t.text_color, t.match, t.matching_algorithm, t.is_insensitive, t.is_inbox_tag, t.owner_id,
		       (SELECT COUNT(*) FROM document_tags dt WHERE dt.tag_id = t.id) as doc_count
		FROM tags t
		%s
		ORDER BY t.name COLLATE NOCASE ASC
	`, whereClause)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Tag
	var allIDs []int

	for rows.Next() {
		var t models.Tag
		var owner sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Color, &t.TextColor, &t.Match, &t.MatchingAlgorithm, &t.IsInsensitive, &t.IsInboxTag, &owner, &t.DocumentCount); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				t.Owner = &o
			}
			t.UserCanChange = (user == nil || user.IsSuperuser || t.Owner == nil || *t.Owner == user.ID)
			results = append(results, t)
			allIDs = append(allIDs, t.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.Tag{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *TagsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name              string `json:"name"`
		Color             string `json:"color"`
		TextColor         string `json:"text_color"`
		Match             string `json:"match"`
		MatchingAlgorithm int    `json:"matching_algorithm"`
		IsInsensitive     bool   `json:"is_insensitive"`
		IsInboxTag        bool   `json:"is_inbox_tag"`
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

	if input.Color == "" {
		input.Color = "#a6cee3"
	}
	if input.TextColor == "" {
		input.TextColor = "#000000"
	}
	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))

	res, err := h.db.Exec(`
		INSERT INTO tags (name, slug, color, text_color, match, matching_algorithm, is_insensitive, is_inbox_tag, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.Name, slug, input.Color, input.TextColor, input.Match, input.MatchingAlgorithm, input.IsInsensitive, input.IsInboxTag, ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	tag := models.Tag{
		ID:                int(id),
		Slug:              slug,
		Name:              input.Name,
		Color:             input.Color,
		TextColor:         input.TextColor,
		Match:             input.Match,
		MatchingAlgorithm: input.MatchingAlgorithm,
		IsInsensitive:     input.IsInsensitive,
		IsInboxTag:        input.IsInboxTag,
		Owner:             ownerID,
		UserCanChange:     true,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(tag)
}

func (h *TagsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var t models.Tag
	var owner sql.NullInt64
	err := h.db.QueryRow(`
		SELECT t.id, t.slug, t.name, t.color, t.text_color, t.match, t.matching_algorithm, t.is_insensitive, t.is_inbox_tag, t.owner_id,
		       (SELECT COUNT(*) FROM document_tags dt WHERE dt.tag_id = t.id) as doc_count
		FROM tags t WHERE t.id = ?
	`, id).Scan(&t.ID, &t.Slug, &t.Name, &t.Color, &t.TextColor, &t.Match, &t.MatchingAlgorithm, &t.IsInsensitive, &t.IsInboxTag, &owner, &t.DocumentCount)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		t.Owner = &o
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && t.Owner != nil && *t.Owner != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}
	t.UserCanChange = (user == nil || user.IsSuperuser || t.Owner == nil || *t.Owner == user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(t)
}

func (h *TagsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM tags WHERE id = ?", id).Scan(&existingOwner)
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
		Color             string `json:"color"`
		TextColor         string `json:"text_color"`
		Match             string `json:"match"`
		MatchingAlgorithm int    `json:"matching_algorithm"`
		IsInsensitive     bool   `json:"is_insensitive"`
		IsInboxTag        bool   `json:"is_inbox_tag"`
		Owner             *int   `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slug := strings.ToLower(strings.ReplaceAll(input.Name, " ", "-"))
	_, err = h.db.Exec(`
		UPDATE tags SET name = ?, slug = ?, color = ?, text_color = ?, match = ?, matching_algorithm = ?, is_insensitive = ?, is_inbox_tag = ?
		WHERE id = ?
	`, input.Name, slug, input.Color, input.TextColor, input.Match, input.MatchingAlgorithm, input.IsInsensitive, input.IsInboxTag, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Owner != nil && (user == nil || user.IsSuperuser || *input.Owner == user.ID) {
		if *input.Owner == 0 {
			_, _ = h.db.Exec("UPDATE tags SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE tags SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	h.Get(w, r)
}

func (h *TagsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM tags WHERE id = ?", id).Scan(&existingOwner)
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

	_, err = h.db.Exec("DELETE FROM tags WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
