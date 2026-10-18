package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type SavedViewsHandler struct {
	db *database.DB
}

func NewSavedViewsHandler(db *database.DB) *SavedViewsHandler {
	return &SavedViewsHandler{db: db}
}

func (h *SavedViewsHandler) List(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	var whereClause string
	var args []interface{}
	if user != nil && !user.IsSuperuser {
		whereClause = "WHERE (owner_id IS NULL OR owner_id = ?)"
		args = append(args, user.ID)
	}

	query := fmt.Sprintf(`
		SELECT id, name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id
		FROM saved_views
		%s
		ORDER BY name ASC
	`, whereClause)

	rows, err := h.db.Query(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.SavedView
	var allIDs []int

	for rows.Next() {
		var sv models.SavedView
		var owner sql.NullInt64
		var rulesJSON string
		if err := rows.Scan(&sv.ID, &sv.Name, &sv.ShowOnDashboard, &sv.ShowInSidebar, &sv.SortField, &sv.SortReverse, &rulesJSON, &owner); err == nil {
			if owner.Valid {
				o := int(owner.Int64)
				sv.Owner = &o
			}
			sv.UserCanChange = (user == nil || user.IsSuperuser || sv.Owner == nil || *sv.Owner == user.ID)
			_ = json.Unmarshal([]byte(rulesJSON), &sv.FilterRules)
			if sv.FilterRules == nil {
				sv.FilterRules = []models.SavedViewFilterRule{}
			}
			results = append(results, sv)
			allIDs = append(allIDs, sv.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []models.SavedView{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *SavedViewsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name            string                       `json:"name"`
		ShowOnDashboard bool                         `json:"show_on_dashboard"`
		ShowInSidebar   bool                         `json:"show_in_sidebar"`
		SortField       string                       `json:"sort_field"`
		SortReverse     bool                         `json:"sort_reverse"`
		FilterRules     []models.SavedViewFilterRule `json:"filter_rules"`
		Owner           *int                         `json:"owner"`
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

	rulesJSON, _ := json.Marshal(input.FilterRules)
	res, err := h.db.Exec(`
		INSERT INTO saved_views (name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, input.Name, input.ShowOnDashboard, input.ShowInSidebar, input.SortField, input.SortReverse, string(rulesJSON), ownerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, _ := res.LastInsertId()
	sv := models.SavedView{
		ID:              int(id),
		Name:            input.Name,
		ShowOnDashboard: input.ShowOnDashboard,
		ShowInSidebar:   input.ShowInSidebar,
		SortField:       input.SortField,
		SortReverse:     input.SortReverse,
		FilterRules:     input.FilterRules,
		Owner:           ownerID,
		UserCanChange:   true,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sv)
}

func (h *SavedViewsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var sv models.SavedView
	var owner sql.NullInt64
	var rulesJSON string
	err := h.db.QueryRow(`
		SELECT id, name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id
		FROM saved_views WHERE id = ?
	`, id).Scan(&sv.ID, &sv.Name, &sv.ShowOnDashboard, &sv.ShowInSidebar, &sv.SortField, &sv.SortReverse, &rulesJSON, &owner)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if owner.Valid {
		o := int(owner.Int64)
		sv.Owner = &o
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && sv.Owner != nil && *sv.Owner != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}
	sv.UserCanChange = (user == nil || user.IsSuperuser || sv.Owner == nil || *sv.Owner == user.ID)

	_ = json.Unmarshal([]byte(rulesJSON), &sv.FilterRules)
	if sv.FilterRules == nil {
		sv.FilterRules = []models.SavedViewFilterRule{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sv)
}

func (h *SavedViewsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM saved_views WHERE id = ?", id).Scan(&existingOwner)
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
		Name            *string                       `json:"name"`
		ShowOnDashboard *bool                         `json:"show_on_dashboard"`
		ShowInSidebar   *bool                         `json:"show_in_sidebar"`
		SortField       *string                       `json:"sort_field"`
		SortReverse     *bool                         `json:"sort_reverse"`
		FilterRules     *[]models.SavedViewFilterRule `json:"filter_rules"`
		Owner           *int                          `json:"owner"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name != nil {
		_, _ = h.db.Exec("UPDATE saved_views SET name = ? WHERE id = ?", *input.Name, id)
	}
	if input.ShowOnDashboard != nil {
		_, _ = h.db.Exec("UPDATE saved_views SET show_on_dashboard = ? WHERE id = ?", *input.ShowOnDashboard, id)
	}
	if input.ShowInSidebar != nil {
		_, _ = h.db.Exec("UPDATE saved_views SET show_in_sidebar = ? WHERE id = ?", *input.ShowInSidebar, id)
	}
	if input.SortField != nil {
		_, _ = h.db.Exec("UPDATE saved_views SET sort_field = ? WHERE id = ?", *input.SortField, id)
	}
	if input.SortReverse != nil {
		_, _ = h.db.Exec("UPDATE saved_views SET sort_reverse = ? WHERE id = ?", *input.SortReverse, id)
	}
	if input.FilterRules != nil {
		rulesJSON, _ := json.Marshal(*input.FilterRules)
		_, _ = h.db.Exec("UPDATE saved_views SET filter_rules = ? WHERE id = ?", string(rulesJSON), id)
	}
	if input.Owner != nil && (user == nil || user.IsSuperuser || *input.Owner == user.ID) {
		if *input.Owner == 0 {
			_, _ = h.db.Exec("UPDATE saved_views SET owner_id = NULL WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE saved_views SET owner_id = ? WHERE id = ?", *input.Owner, id)
		}
	}

	h.Get(w, r)
}

func (h *SavedViewsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var existingOwner sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM saved_views WHERE id = ?", id).Scan(&existingOwner)
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

	_, err = h.db.Exec("DELETE FROM saved_views WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
