package api

import (
	"database/sql"
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
	base := map[string]interface{}{
		"id":                             1,
		"app_title":                      "Paperless-ngx",
		"app_logo":                       nil,
		"output_type":                    "pdfa",
		"version":                        "3.3.2",
		"has_llm":                        false,
		"user_can_view_app_config":       true,
		"externally_configured_variables": []string{},
		"pages":                          nil,
		"language":                       nil,
		"mode":                           nil,
		"archive_file_generation":        nil,
		"image_dpi":                      nil,
		"unpaper_clean":                  nil,
		"deskew":                         nil,
		"rotate_pages":                   nil,
		"rotate_pages_threshold":         nil,
		"max_image_pixels":               nil,
		"color_conversion_strategy":      nil,
		"user_args":                      nil,
		"barcodes_enabled":               false,
		"barcode_enable_asn":             false,
		"ai_enabled":                     false,
	}

	var configJSON string
	err := h.db.QueryRow("SELECT config_json FROM app_config WHERE id = 1").Scan(&configJSON)
	if err == sql.ErrNoRows {
		raw, _ := json.Marshal(base)
		_, _ = h.db.Exec("INSERT INTO app_config (id, config_json) VALUES (1, ?)", string(raw))
		return base
	} else if err == nil && configJSON != "" {
		var stored map[string]interface{}
		if err := json.Unmarshal([]byte(configJSON), &stored); err == nil {
			for k, v := range stored {
				base[k] = v
			}
		}
	}
	return base
}

func (h *ConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp := []map[string]interface{}{
		h.getConfigItem(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *ConfigHandler) Patch(w http.ResponseWriter, r *http.Request) {
	current := h.getConfigItem()

	var patch map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for k, v := range patch {
		current[k] = v
	}

	raw, _ := json.Marshal(current)
	_, _ = h.db.Exec("INSERT INTO app_config (id, config_json) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET config_json = excluded.config_json", string(raw))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(current)
}

func (h *ConfigHandler) GetRemoteVersion(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"version":          "3.3.2",
		"update_available": false,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
