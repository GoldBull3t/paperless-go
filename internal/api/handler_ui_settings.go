package api

import (
	"encoding/json"
	"net/http"

	"paperless-go/internal/database"
)

var AllPermissions = []string{
	"documents.view_document",
	"documents.change_document",
	"documents.add_document",
	"documents.delete_document",
	"documents.view_tag",
	"documents.change_tag",
	"documents.add_tag",
	"documents.delete_tag",
	"documents.view_correspondent",
	"documents.change_correspondent",
	"documents.add_correspondent",
	"documents.delete_correspondent",
	"documents.view_documenttype",
	"documents.change_documenttype",
	"documents.add_documenttype",
	"documents.delete_documenttype",
	"documents.view_storagepath",
	"documents.change_storagepath",
	"documents.add_storagepath",
	"documents.delete_storagepath",
	"documents.view_customfield",
	"documents.change_customfield",
	"documents.add_customfield",
	"documents.delete_customfield",
	"documents.view_savedview",
	"documents.change_savedview",
	"documents.add_savedview",
	"documents.delete_savedview",
	"documents.view_uisettings",
	"documents.change_uisettings",
	"documents.view_task",
	"documents.change_task",
	"documents.view_workflow",
	"documents.change_workflow",
	"documents.add_workflow",
	"documents.delete_workflow",
	"paperless.view_workflow",
	"paperless.change_workflow",
	"paperless.add_workflow",
	"paperless.delete_workflow",
	"paperless_mail.view_mailaccount",
	"paperless_mail.view_mailrule",
	"auth.view_user",
	"auth.change_user",
	"auth.add_user",
	"auth.delete_user",
	"auth.view_group",
	"auth.change_group",
}

type UISettingsResponse struct {
	Settings    map[string]interface{} `json:"settings"`
	Permissions []string               `json:"permissions"`
	User        map[string]interface{} `json:"user"`
}

type UISettingsHandler struct {
	db *database.DB
}

func NewUISettingsHandler(db *database.DB) *UISettingsHandler {
	return &UISettingsHandler{db: db}
}

func (h *UISettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	userID := 1
	username := "admin"
	isSuper := true
	firstName := "Admin"
	lastName := "User"
	if user != nil {
		userID = user.ID
		username = user.Username
		isSuper = user.IsSuperuser
		if user.FirstName != "" {
			firstName = user.FirstName
		}
		if user.LastName != "" {
			lastName = user.LastName
		}
	}

	var settingsJSON string
	err := h.db.QueryRow("SELECT settings_json FROM ui_settings WHERE user_id = ?", userID).Scan(&settingsJSON)
	if err != nil || settingsJSON == "" {
		settingsJSON = `{"documentListSize": 25, "theme": "auto", "update_checking": {"backend_setting": "default"}}`
	}

	var settingsMap map[string]interface{}
	_ = json.Unmarshal([]byte(settingsJSON), &settingsMap)
	if settingsMap == nil {
		settingsMap = make(map[string]interface{})
	}

	resp := UISettingsResponse{
		Settings:    settingsMap,
		Permissions: AllPermissions,
		User: map[string]interface{}{
			"id":           userID,
			"username":     username,
			"is_superuser": isSuper,
			"is_staff":     true,
			"first_name":   firstName,
			"last_name":    lastName,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *UISettingsHandler) Post(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	userID := 1
	username := "admin"
	isSuper := true
	firstName := "Admin"
	lastName := "User"
	if user != nil {
		userID = user.ID
		username = user.Username
		isSuper = user.IsSuperuser
		if user.FirstName != "" {
			firstName = user.FirstName
		}
		if user.LastName != "" {
			lastName = user.LastName
		}
	}

	var payload struct {
		Settings map[string]interface{} `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	raw, _ := json.Marshal(payload.Settings)
	_, _ = h.db.Exec(`
		INSERT INTO ui_settings (user_id, settings_json)
		VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET settings_json = excluded.settings_json
	`, userID, string(raw))

	resp := UISettingsResponse{
		Settings:    payload.Settings,
		Permissions: AllPermissions,
		User: map[string]interface{}{
			"id":           userID,
			"username":     username,
			"is_superuser": isSuper,
			"is_staff":     true,
			"first_name":   firstName,
			"last_name":    lastName,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
