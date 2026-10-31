package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type BulkObjectsHandler struct {
	db *database.DB
}

func NewBulkObjectsHandler(db *database.DB) *BulkObjectsHandler {
	return &BulkObjectsHandler{db: db}
}

func (h *BulkObjectsHandler) BulkEditObjects(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ObjectType  string                    `json:"object_type"`
		Operation   string                    `json:"operation"`
		Objects     []int                     `json:"objects"`
		All         bool                      `json:"all"`
		Owner       *int                      `json:"owner"`
		Permissions *models.ObjectPermissions `json:"permissions"`
		Merge       bool                      `json:"merge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())

	txErr := h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		if input.Operation == "set_permissions" {
			targetTable := ""
			switch input.ObjectType {
			case "tags":
				targetTable = "tags"
			case "correspondents":
				targetTable = "correspondents"
			case "document_types":
				targetTable = "document_types"
			case "storage_paths":
				targetTable = "storage_paths"
			}
			if targetTable != "" {
				for _, id := range input.Objects {
					if input.Owner != nil {
						if *input.Owner <= 0 {
							if _, err := tx.ExecContext(r.Context(), fmt.Sprintf("UPDATE %s SET owner_id = NULL WHERE id = ?", targetTable), id); err != nil {
								return fmt.Errorf("failed to reset owner for %s ID %d: %w", targetTable, id, err)
							}
						} else {
							if _, err := tx.ExecContext(r.Context(), fmt.Sprintf("UPDATE %s SET owner_id = ? WHERE id = ?", targetTable), *input.Owner, id); err != nil {
								return fmt.Errorf("failed to update owner for %s ID %d: %w", targetTable, id, err)
							}
						}
					}
				}
			}
		} else if input.Operation == "delete" {
			switch input.ObjectType {
			case "tags":
				for _, id := range input.Objects {
					if user != nil && !user.IsSuperuser {
						res, err := tx.ExecContext(r.Context(), "DELETE FROM tags WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
						if err != nil {
							return fmt.Errorf("failed to delete tag %d: %w", id, err)
						}
						if rows, _ := res.RowsAffected(); rows > 0 {
							if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_tags WHERE tag_id = ?", id); err != nil {
								return fmt.Errorf("failed to remove document relations for tag %d: %w", id, err)
							}
						}
					} else {
						if _, err := tx.ExecContext(r.Context(), "DELETE FROM tags WHERE id = ?", id); err != nil {
							return fmt.Errorf("failed to delete tag %d: %w", id, err)
						}
						if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_tags WHERE tag_id = ?", id); err != nil {
							return fmt.Errorf("failed to remove document relations for tag %d: %w", id, err)
						}
					}
				}
			case "correspondents":
				for _, id := range input.Objects {
					if user != nil && !user.IsSuperuser {
						res, err := tx.ExecContext(r.Context(), "DELETE FROM correspondents WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
						if err != nil {
							return fmt.Errorf("failed to delete correspondent %d: %w", id, err)
						}
						if rows, _ := res.RowsAffected(); rows > 0 {
							if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET correspondent_id = NULL WHERE correspondent_id = ?", id); err != nil {
								return fmt.Errorf("failed to nullify correspondent relation %d: %w", id, err)
							}
						}
					} else {
						if _, err := tx.ExecContext(r.Context(), "DELETE FROM correspondents WHERE id = ?", id); err != nil {
							return fmt.Errorf("failed to delete correspondent %d: %w", id, err)
						}
						if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET correspondent_id = NULL WHERE correspondent_id = ?", id); err != nil {
							return fmt.Errorf("failed to nullify correspondent relation %d: %w", id, err)
						}
					}
				}
			case "document_types":
				for _, id := range input.Objects {
					if user != nil && !user.IsSuperuser {
						res, err := tx.ExecContext(r.Context(), "DELETE FROM document_types WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
						if err != nil {
							return fmt.Errorf("failed to delete document_type %d: %w", id, err)
						}
						if rows, _ := res.RowsAffected(); rows > 0 {
							if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET document_type_id = NULL WHERE document_type_id = ?", id); err != nil {
								return fmt.Errorf("failed to nullify document_type relation %d: %w", id, err)
							}
						}
					} else {
						if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_types WHERE id = ?", id); err != nil {
							return fmt.Errorf("failed to delete document_type %d: %w", id, err)
						}
						if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET document_type_id = NULL WHERE document_type_id = ?", id); err != nil {
							return fmt.Errorf("failed to nullify document_type relation %d: %w", id, err)
						}
					}
				}
			case "storage_paths":
				for _, id := range input.Objects {
					if user != nil && !user.IsSuperuser {
						res, err := tx.ExecContext(r.Context(), "DELETE FROM storage_paths WHERE id = ? AND (owner_id IS NULL OR owner_id = ?)", id, user.ID)
						if err != nil {
							return fmt.Errorf("failed to delete storage_path %d: %w", id, err)
						}
						if rows, _ := res.RowsAffected(); rows > 0 {
							if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET storage_path_id = NULL WHERE storage_path_id = ?", id); err != nil {
								return fmt.Errorf("failed to nullify storage_path relation %d: %w", id, err)
							}
						}
					} else {
						if _, err := tx.ExecContext(r.Context(), "DELETE FROM storage_paths WHERE id = ?", id); err != nil {
							return fmt.Errorf("failed to delete storage_path %d: %w", id, err)
						}
						if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET storage_path_id = NULL WHERE storage_path_id = ?", id); err != nil {
							return fmt.Errorf("failed to nullify storage_path relation %d: %w", id, err)
						}
					}
				}
			case "custom_fields":
				for _, id := range input.Objects {
					if _, err := tx.ExecContext(r.Context(), "DELETE FROM custom_fields WHERE id = ?", id); err != nil {
						return fmt.Errorf("failed to delete custom_field %d: %w", id, err)
					}
					if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_custom_fields WHERE custom_field_id = ?", id); err != nil {
						return fmt.Errorf("failed to remove document relations for custom_field %d: %w", id, err)
					}
				}
			}
		}
		return nil
	})

	if txErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": txErr.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}
