package api

import (
	"encoding/json"
	"net/http"

	"paperless-go/internal/database"
)

type BulkObjectsHandler struct {
	db *database.DB
}

func NewBulkObjectsHandler(db *database.DB) *BulkObjectsHandler {
	return &BulkObjectsHandler{db: db}
}

func (h *BulkObjectsHandler) BulkEditObjects(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ObjectType string `json:"object_type"`
		Operation  string `json:"operation"`
		Objects    []int  `json:"objects"`
		All        bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())

	if input.Operation == "delete" {
		switch input.ObjectType {
		case "tags":
			for _, id := range input.Objects {
				if user != nil && !user.IsSuperuser {
					res, _ := h.db.Exec("DELETE FROM tags WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
					if rows, _ := res.RowsAffected(); rows > 0 {
						_, _ = h.db.Exec("DELETE FROM document_tags WHERE tag_id = ?", id)
					}
				} else {
					_, _ = h.db.Exec("DELETE FROM tags WHERE id = ?", id)
					_, _ = h.db.Exec("DELETE FROM document_tags WHERE tag_id = ?", id)
				}
			}
		case "correspondents":
			for _, id := range input.Objects {
				if user != nil && !user.IsSuperuser {
					res, _ := h.db.Exec("DELETE FROM correspondents WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
					if rows, _ := res.RowsAffected(); rows > 0 {
						_, _ = h.db.Exec("UPDATE documents SET correspondent_id = NULL WHERE correspondent_id = ?", id)
					}
				} else {
					_, _ = h.db.Exec("DELETE FROM correspondents WHERE id = ?", id)
					_, _ = h.db.Exec("UPDATE documents SET correspondent_id = NULL WHERE correspondent_id = ?", id)
				}
			}
		case "document_types":
			for _, id := range input.Objects {
				if user != nil && !user.IsSuperuser {
					res, _ := h.db.Exec("DELETE FROM document_types WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
					if rows, _ := res.RowsAffected(); rows > 0 {
						_, _ = h.db.Exec("UPDATE documents SET document_type_id = NULL WHERE document_type_id = ?", id)
					}
				} else {
					_, _ = h.db.Exec("DELETE FROM document_types WHERE id = ?", id)
					_, _ = h.db.Exec("UPDATE documents SET document_type_id = NULL WHERE document_type_id = ?", id)
				}
			}
		case "storage_paths":
			for _, id := range input.Objects {
				if user != nil && !user.IsSuperuser {
					res, _ := h.db.Exec("DELETE FROM storage_paths WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
					if rows, _ := res.RowsAffected(); rows > 0 {
						_, _ = h.db.Exec("UPDATE documents SET storage_path_id = NULL WHERE storage_path_id = ?", id)
					}
				} else {
					_, _ = h.db.Exec("DELETE FROM storage_paths WHERE id = ?", id)
					_, _ = h.db.Exec("UPDATE documents SET storage_path_id = NULL WHERE storage_path_id = ?", id)
				}
			}
		case "custom_fields":
			for _, id := range input.Objects {
				_, _ = h.db.Exec("DELETE FROM custom_fields WHERE id = ?", id)
				_, _ = h.db.Exec("DELETE FROM document_custom_fields WHERE field_id = ?", id)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}
