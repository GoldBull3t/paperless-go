package api

import (
	"encoding/json"
	"net/http"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type UsersHandler struct {
	db *database.DB
}

func NewUsersHandler(db *database.DB) *UsersHandler {
	return &UsersHandler{db: db}
}

func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined
		FROM users ORDER BY username ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.User
	var allIDs []int

	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &u.IsSuperuser, &u.IsStaff, &u.IsActive, &u.DateJoined); err == nil {
			results = append(results, u)
			allIDs = append(allIDs, u.ID)
		}
	}

	if results == nil {
		results = []models.User{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *UsersHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   0,
		All:     []int{},
		Results: []interface{}{},
	})
}

func (h *UsersHandler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   0,
		All:     []int{},
		Results: []interface{}{},
	})
}

func (h *UsersHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]string{"paperless"})
}

