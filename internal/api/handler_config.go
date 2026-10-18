package api

import (
	"encoding/json"
	"net/http"

	"paperless-go/internal/database"
)

type ConfigHandler struct {
	db *database.DB
}

func NewConfigHandler(db *database.DB) *ConfigHandler {
	return &ConfigHandler{db: db}
}

func (h *ConfigHandler) getConfigItem() map[string]interface{} {
	return map[string]interface{}{
		"id":                       1,
		"app_title":                "Paperless-ngx",
		"app_logo":                 nil,
		"output_type":              "pdfa",
		"version":                  "3.3.2",
		"has_llm":                  false,
		"user_can_view_app_config": true,
	}
}

func (h *ConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Paperless-ngx frontend expects an array [ { ... } ] because of e => e[0]
	resp := []map[string]interface{}{
		h.getConfigItem(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) Patch(w http.ResponseWriter, r *http.Request) {
	// Return the updated config object
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.getConfigItem())
}

func (h *ConfigHandler) GetRemoteVersion(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"version":          "3.3.2",
		"update_available": false,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
