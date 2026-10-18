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

func (h *ConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"app_title":                "Paperless-ngx",
		"app_logo":                 nil,
		"output_type":              "pdfa",
		"version":                  "3.3.2",
		"has_llm":                  false,
		"user_can_view_app_config": true,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) Patch(w http.ResponseWriter, r *http.Request) {
	// Return the config
	h.Get(w, r)
}
