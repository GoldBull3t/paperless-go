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

type SavedViewsHandler struct {
	db *database.DB
}

func NewSavedViewsHandler(db *database.DB) *SavedViewsHandler {
	return &SavedViewsHandler{db: db}
}

func (h *SavedViewsHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id
		FROM saved_views ORDER BY name ASC
	`)
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
			sv.UserCanChange = true
			_ = json.Unmarshal([]byte(rulesJSON), &sv.FilterRules)
			if sv.FilterRules == nil {
				sv.FilterRules = []models.SavedViewFilterRule{}
			}
			results = append(results, sv)
			allIDs = append(allIDs, sv.ID)
		}
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
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rulesJSON, _ := json.Marshal(input.FilterRules)
	res, err := h.db.Exec(`
		INSERT INTO saved_views (name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, 1)
	`, input.Name, input.ShowOnDashboard, input.ShowInSidebar, input.SortField, input.SortReverse, string(rulesJSON))
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
	sv.UserCanChange = true
	_ = json.Unmarshal([]byte(rulesJSON), &sv.FilterRules)
	if sv.FilterRules == nil {
		sv.FilterRules = []models.SavedViewFilterRule{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sv)
}

func (h *SavedViewsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, err := h.db.Exec("DELETE FROM saved_views WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
