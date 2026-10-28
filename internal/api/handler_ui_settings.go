package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"paperless-go/internal/database"
)

var AllPermissions = []string{
	// Bare permission codes (consumed by Angular PermissionType.replace)
	"view_document",
	"change_document",
	"add_document",
	"delete_document",
	"view_tag",
	"change_tag",
	"add_tag",
	"delete_tag",
	"view_correspondent",
	"change_correspondent",
	"add_correspondent",
	"delete_correspondent",
	"view_documenttype",
	"change_documenttype",
	"add_documenttype",
	"delete_documenttype",
	"view_storagepath",
	"change_storagepath",
	"add_storagepath",
	"delete_storagepath",
	"view_customfield",
	"change_customfield",
	"add_customfield",
	"delete_customfield",
	"view_savedview",
	"change_savedview",
	"add_savedview",
	"delete_savedview",
	"view_uisettings",
	"change_uisettings",
	"view_paperlesstask",
	"change_paperlesstask",
	"view_workflow",
	"change_workflow",
	"add_workflow",
	"delete_workflow",
	"view_mailaccount",
	"change_mailaccount",
	"add_mailaccount",
	"delete_mailaccount",
	"view_mailrule",
	"change_mailrule",
	"add_mailrule",
	"delete_mailrule",
	"view_user",
	"change_user",
	"add_user",
	"delete_user",
	"view_group",
	"change_group",
	"add_group",
	"delete_group",
	"view_sharelink",
	"change_sharelink",
	"add_sharelink",
	"delete_sharelink",
	"view_applicationconfiguration",
	"change_applicationconfiguration",
	"view_global_statistics",
	"view_system_monitoring",

	// App-prefixed codes (for backwards compatibility)
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

func (h *UISettingsHandler) getUserPermissions(userID int, isSuper bool) ([]string, []int) {
	// 1. Get groups
	var groupIDs []int
	rowsG, err := h.db.Query("SELECT group_id FROM user_groups WHERE user_id = ?", userID)
	if err == nil {
		defer rowsG.Close()
		for rowsG.Next() {
			var gid int
			if err := rowsG.Scan(&gid); err == nil {
				groupIDs = append(groupIDs, gid)
			}
		}
	}
	if groupIDs == nil {
		groupIDs = []int{}
	}

	if isSuper {
		return AllPermissions, groupIDs
	}

	permMap := make(map[string]bool)

	// Direct user permissions
	rowsP, err := h.db.Query("SELECT permission FROM user_permissions WHERE user_id = ?", userID)
	if err == nil {
		defer rowsP.Close()
		for rowsP.Next() {
			var p string
			if err := rowsP.Scan(&p); err == nil {
				permMap[p] = true
				// Also strip prefix if present
				parts := strings.Split(p, ".")
				permMap[parts[len(parts)-1]] = true
			}
		}
	}

	// Group inherited permissions
	if len(groupIDs) > 0 {
		var placeholders []string
		var args []interface{}
		for _, gid := range groupIDs {
			placeholders = append(placeholders, "?")
			args = append(args, gid)
		}
		query := fmt.Sprintf("SELECT permission FROM group_permissions WHERE group_id IN (%s)", strings.Join(placeholders, ","))
		rowsGP, err := h.db.Query(query, args...)
		if err == nil {
			defer rowsGP.Close()
			for rowsGP.Next() {
				var p string
				if err := rowsGP.Scan(&p); err == nil {
					permMap[p] = true
					parts := strings.Split(p, ".")
					permMap[parts[len(parts)-1]] = true
				}
			}
		}
	}

	var perms []string
	for p := range permMap {
		perms = append(perms, p)
	}
	if perms == nil {
		perms = []string{}
	}
	return perms, groupIDs
}

func (h *UISettingsHandler) enrichSettings(settingsMap map[string]interface{}) map[string]interface{} {
	if settingsMap == nil {
		settingsMap = make(map[string]interface{})
	}
	if _, ok := settingsMap["update_checking"]; !ok {
		settingsMap["update_checking"] = map[string]interface{}{"backend_setting": false}
	}
	if _, ok := settingsMap["trash_delay"]; !ok {
		settingsMap["trash_delay"] = 30
	}
	if _, ok := settingsMap["version"]; !ok {
		settingsMap["version"] = "3.3.2"
	}
	if _, ok := settingsMap["app_title"]; !ok {
		settingsMap["app_title"] = "Paperless-ngx"
	}
	if _, ok := settingsMap["app_logo"]; !ok {
		settingsMap["app_logo"] = nil
	}
	if _, ok := settingsMap["auditlog_enabled"]; !ok {
		settingsMap["auditlog_enabled"] = false
	}
	if _, ok := settingsMap["remote_ocr"]; !ok {
		settingsMap["remote_ocr"] = map[string]interface{}{
			"configured": false,
			"mode":       "off",
		}
	}
	if _, ok := settingsMap["email_enabled"]; !ok {
		settingsMap["email_enabled"] = true
	}
	if _, ok := settingsMap["ai_enabled"]; !ok {
		settingsMap["ai_enabled"] = true
	}
	return settingsMap
}

func (h *UISettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	userID := 1
	username := "admin"
	isSuper := true
	isStaff := true
	firstName := "Admin"
	lastName := "User"
	if user != nil {
		userID = user.ID
		username = user.Username
		isSuper = user.IsSuperuser
		isStaff = user.IsStaff || user.IsSuperuser
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
		settingsJSON = `{"documentListSize": 25, "theme": "auto", "update_checking": {"backend_setting": false}}`
	}

	var settingsMap map[string]interface{}
	_ = json.Unmarshal([]byte(settingsJSON), &settingsMap)
	settingsMap = h.enrichSettings(settingsMap)

	perms, groups := h.getUserPermissions(userID, isSuper)

	resp := UISettingsResponse{
		Settings:    settingsMap,
		Permissions: perms,
		User: map[string]interface{}{
			"id":           userID,
			"username":     username,
			"is_superuser": isSuper,
			"is_staff":     isStaff,
			"first_name":   firstName,
			"last_name":    lastName,
			"groups":       groups,
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
	isStaff := true
	firstName := "Admin"
	lastName := "User"
	if user != nil {
		userID = user.ID
		username = user.Username
		isSuper = user.IsSuperuser
		isStaff = user.IsStaff || user.IsSuperuser
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

	settingsMap := h.enrichSettings(payload.Settings)
	perms, groups := h.getUserPermissions(userID, isSuper)

	resp := UISettingsResponse{
		Settings:    settingsMap,
		Permissions: perms,
		User: map[string]interface{}{
			"id":           userID,
			"username":     username,
			"is_superuser": isSuper,
			"is_staff":     isStaff,
			"first_name":   firstName,
			"last_name":    lastName,
			"groups":       groups,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
