package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type UsersHandler struct {
	db *database.DB
}

func NewUsersHandler(db *database.DB) *UsersHandler {
	return &UsersHandler{db: db}
}

func (h *UsersHandler) getUserGroups(userID int) []int {
	rows, err := h.db.Query("SELECT group_id FROM user_groups WHERE user_id = ?", userID)
	if err != nil {
		return []int{}
	}
	defer rows.Close()
	var groups []int
	for rows.Next() {
		var gid int
		if err := rows.Scan(&gid); err == nil {
			groups = append(groups, gid)
		}
	}
	if groups == nil {
		groups = []int{}
	}
	return groups
}

func (h *UsersHandler) getUserPermissions(userID int) []string {
	rows, err := h.db.Query("SELECT permission FROM user_permissions WHERE user_id = ?", userID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			perms = append(perms, p)
		}
	}
	if perms == nil {
		perms = []string{}
	}
	return perms
}

func (h *UsersHandler) getInheritedPermissions(groups []int) []string {
	if len(groups) == 0 {
		return []string{}
	}
	var placeholders []string
	var args []interface{}
	for _, gid := range groups {
		placeholders = append(placeholders, "?")
		args = append(args, gid)
	}
	query := fmt.Sprintf("SELECT DISTINCT permission FROM group_permissions WHERE group_id IN (%s)", strings.Join(placeholders, ","))
	rows, err := h.db.Query(query, args...)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			perms = append(perms, p)
		}
	}
	if perms == nil {
		perms = []string{}
	}
	return perms
}

func (h *UsersHandler) getGroupPermissions(groupID int) []string {
	rows, err := h.db.Query("SELECT permission FROM group_permissions WHERE group_id = ?", groupID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err == nil {
			perms = append(perms, p)
		}
	}
	if perms == nil {
		perms = []string{}
	}
	return perms
}

func (h *UsersHandler) fetchUser(userID int) (*models.User, error) {
	var u models.User
	err := h.db.QueryRow(`
		SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined, is_mfa_enabled
		FROM users WHERE id = ?
	`, userID).Scan(&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &u.IsSuperuser, &u.IsStaff, &u.IsActive, &u.DateJoined, &u.IsMfaEnabled)
	if err != nil {
		return nil, err
	}
	u.Groups = h.getUserGroups(u.ID)
	u.UserPermissions = h.getUserPermissions(u.ID)
	u.InheritedPermissions = h.getInheritedPermissions(u.Groups)
	return &u, nil
}

func (h *UsersHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(`
		SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined, is_mfa_enabled
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
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &u.IsSuperuser, &u.IsStaff, &u.IsActive, &u.DateJoined, &u.IsMfaEnabled); err == nil {
			u.Groups = h.getUserGroups(u.ID)
			u.UserPermissions = h.getUserPermissions(u.ID)
			u.InheritedPermissions = h.getInheritedPermissions(u.Groups)
			results = append(results, u)
			allIDs = append(allIDs, u.ID)
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
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

func (h *UsersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	u, err := h.fetchUser(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(u)
}

func (h *UsersHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username        string   `json:"username"`
		Password        string   `json:"password"`
		Email           string   `json:"email"`
		FirstName       string   `json:"first_name"`
		LastName        string   `json:"last_name"`
		IsSuperuser     bool     `json:"is_superuser"`
		IsStaff         bool     `json:"is_staff"`
		IsActive        bool     `json:"is_active"`
		Groups          []int    `json:"groups"`
		UserPermissions []string `json:"user_permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Username) == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Password) == "" {
		http.Error(w, "Password is required", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}

	res, err := h.db.Exec(`
		INSERT INTO users (username, password_hash, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, input.Username, string(hash), input.Email, input.FirstName, input.LastName, input.IsSuperuser, input.IsStaff, input.IsActive)
	if err != nil {
		http.Error(w, "Username already exists or database error", http.StatusBadRequest)
		return
	}

	userID, _ := res.LastInsertId()
	for _, gid := range input.Groups {
		_, _ = h.db.Exec("INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)", userID, gid)
	}
	for _, p := range input.UserPermissions {
		_, _ = h.db.Exec("INSERT OR IGNORE INTO user_permissions (user_id, permission) VALUES (?, ?)", userID, p)
	}

	createdUser, _ := h.fetchUser(int(userID))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createdUser)
}

func (h *UsersHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	existing, err := h.fetchUser(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var input struct {
		Username        *string   `json:"username"`
		Password        *string   `json:"password"`
		Email           *string   `json:"email"`
		FirstName       *string   `json:"first_name"`
		LastName        *string   `json:"last_name"`
		IsSuperuser     *bool     `json:"is_superuser"`
		IsStaff         *bool     `json:"is_staff"`
		IsActive        *bool     `json:"is_active"`
		Groups          *[]int    `json:"groups"`
		UserPermissions *[]string `json:"user_permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Username != nil && strings.TrimSpace(*input.Username) != "" {
		_, _ = h.db.Exec("UPDATE users SET username = ? WHERE id = ?", *input.Username, id)
	}
	if input.Password != nil && strings.TrimSpace(*input.Password) != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*input.Password), bcrypt.DefaultCost)
		if err == nil {
			_, _ = h.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", string(hash), id)
		}
	}
	if input.Email != nil {
		_, _ = h.db.Exec("UPDATE users SET email = ? WHERE id = ?", *input.Email, id)
	}
	if input.FirstName != nil {
		_, _ = h.db.Exec("UPDATE users SET first_name = ? WHERE id = ?", *input.FirstName, id)
	}
	if input.LastName != nil {
		_, _ = h.db.Exec("UPDATE users SET last_name = ? WHERE id = ?", *input.LastName, id)
	}
	if input.IsSuperuser != nil {
		_, _ = h.db.Exec("UPDATE users SET is_superuser = ? WHERE id = ?", *input.IsSuperuser, id)
	}
	if input.IsStaff != nil {
		_, _ = h.db.Exec("UPDATE users SET is_staff = ? WHERE id = ?", *input.IsStaff, id)
	}
	if input.IsActive != nil {
		_, _ = h.db.Exec("UPDATE users SET is_active = ? WHERE id = ?", *input.IsActive, id)
	}

	if input.Groups != nil {
		_, _ = h.db.Exec("DELETE FROM user_groups WHERE user_id = ?", id)
		for _, gid := range *input.Groups {
			_, _ = h.db.Exec("INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)", id, gid)
		}
	}

	if input.UserPermissions != nil {
		_, _ = h.db.Exec("DELETE FROM user_permissions WHERE user_id = ?", id)
		for _, p := range *input.UserPermissions {
			_, _ = h.db.Exec("INSERT OR IGNORE INTO user_permissions (user_id, permission) VALUES (?, ?)", id, p)
		}
	}

	_ = existing
	updated, _ := h.fetchUser(id)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

func (h *UsersHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// Check if this is the last superuser
	var superCount int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_superuser = 1").Scan(&superCount)
	var isSuper bool
	_ = h.db.QueryRow("SELECT is_superuser FROM users WHERE id = ?", id).Scan(&isSuper)
	if isSuper && superCount <= 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "Cannot delete the last superuser."}`))
		return
	}

	_, _ = h.db.Exec("DELETE FROM user_groups WHERE user_id = ?", id)
	_, _ = h.db.Exec("DELETE FROM user_permissions WHERE user_id = ?", id)
	_, _ = h.db.Exec("DELETE FROM ui_settings WHERE user_id = ?", id)
	_, err := h.db.Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Groups endpoints (/api/groups/)
func (h *UsersHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query("SELECT id, name FROM groups ORDER BY name ASC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Group
	var allIDs []int
	for rows.Next() {
		var g models.Group
		if err := rows.Scan(&g.ID, &g.Name); err == nil {
			g.Permissions = h.getGroupPermissions(g.ID)
			results = append(results, g)
			allIDs = append(allIDs, g.ID)
		}
	}
	_ = rows.Err()

	if results == nil {
		results = []models.Group{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   len(results),
		All:     allIDs,
		Results: results,
	})
}

func (h *UsersHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var g models.Group
	err := h.db.QueryRow("SELECT id, name FROM groups WHERE id = ?", id).Scan(&g.ID, &g.Name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g.Permissions = h.getGroupPermissions(g.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g)
}

func (h *UsersHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		http.Error(w, "Group name is required", http.StatusBadRequest)
		return
	}

	res, err := h.db.Exec("INSERT INTO groups (name) VALUES (?)", input.Name)
	if err != nil {
		http.Error(w, "Group name already exists", http.StatusBadRequest)
		return
	}
	gid, _ := res.LastInsertId()

	for _, p := range input.Permissions {
		_, _ = h.db.Exec("INSERT OR IGNORE INTO group_permissions (group_id, permission) VALUES (?, ?)", gid, p)
	}

	g := models.Group{
		ID:          int(gid),
		Name:        input.Name,
		Permissions: input.Permissions,
	}
	if g.Permissions == nil {
		g.Permissions = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(g)
}

func (h *UsersHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var exists int
	if err := h.db.QueryRow("SELECT id FROM groups WHERE id = ?", id).Scan(&exists); err != nil {
		http.NotFound(w, r)
		return
	}

	var input struct {
		Name        *string   `json:"name"`
		Permissions *[]string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Name != nil && strings.TrimSpace(*input.Name) != "" {
		_, _ = h.db.Exec("UPDATE groups SET name = ? WHERE id = ?", *input.Name, id)
	}
	if input.Permissions != nil {
		_, _ = h.db.Exec("DELETE FROM group_permissions WHERE group_id = ?", id)
		for _, p := range *input.Permissions {
			_, _ = h.db.Exec("INSERT OR IGNORE INTO group_permissions (group_id, permission) VALUES (?, ?)", id, p)
		}
	}

	var g models.Group
	_ = h.db.QueryRow("SELECT id, name FROM groups WHERE id = ?", id).Scan(&g.ID, &g.Name)
	g.Permissions = h.getGroupPermissions(g.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(g)
}

func (h *UsersHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var exists int
	if err := h.db.QueryRow("SELECT id FROM groups WHERE id = ?", id).Scan(&exists); err != nil {
		http.NotFound(w, r)
		return
	}

	_, _ = h.db.Exec("DELETE FROM user_groups WHERE group_id = ?", id)
	_, _ = h.db.Exec("DELETE FROM group_permissions WHERE group_id = ?", id)
	_, _ = h.db.Exec("DELETE FROM groups WHERE id = ?", id)

	w.WriteHeader(http.StatusNoContent)
}

// DeactivateUserTotp allows administrators to deactivate TOTP MFA for a user
func (h *UsersHandler) DeactivateUserTotp(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	_, err := h.db.Exec("UPDATE users SET is_mfa_enabled = 0, totp_secret = '', recovery_codes_json = '[]' WHERE id = ?", idStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(true)
}

func (h *UsersHandler) ListLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GlobalLogBuffer.Channels())
}

func (h *UsersHandler) GetLog(w http.ResponseWriter, r *http.Request) {
	logName := chi.URLParam(r, "log")
	if logName == "" {
		logName = "paperless"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(GlobalLogBuffer.Get(logName))
}
