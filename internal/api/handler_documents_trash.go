package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"paperless-go/internal/crypto"
	"paperless-go/internal/models"
)

// TrashList handles GET /api/trash/
func (h *DocumentsHandler) TrashList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize := 25
	offset := (page - 1) * pageSize

	user := GetUserFromContext(r.Context())
	var conditions []string
	var args []interface{}
	conditions = append(conditions, "deleted_at IS NOT NULL")
	if user != nil && !user.IsSuperuser {
		conditions = append(conditions, "(owner_id IS NULL OR owner_id = ?)")
		args = append(args, user.ID)
	}
	whereClause := "WHERE " + strings.Join(conditions, " AND ")

	var totalCount int
	_ = h.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM documents %s", whereClause), args...).Scan(&totalCount)

	pageArgs := append(args, pageSize, offset)
	rows, err := h.db.Query(fmt.Sprintf(`
		SELECT id FROM documents %s ORDER BY deleted_at DESC LIMIT ? OFFSET ?
	`, whereClause), pageArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []*models.Document
	var allIDs []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			allIDs = append(allIDs, id)
			if doc, err := h.fetchDocumentWithUser(id, user); err == nil && doc != nil {
				results = append(results, doc)
			}
		}
	}
	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf("cursor error fetching trash documents: %v", err), http.StatusInternalServerError)
		return
	}
	if results == nil {
		results = []*models.Document{}
		allIDs = []int{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   totalCount,
		All:     allIDs,
		Results: results,
	})
}

// TrashAction handles POST /api/trash/
func (h *DocumentsHandler) TrashAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action    string `json:"action"` // "restore" or "empty"
		Documents []int  `json:"documents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)

	if input.Action == "restore" {
		for _, id := range allowedDocs {
			_, _ = h.db.Exec("UPDATE documents SET deleted_at = NULL WHERE id = ?", id)
		}
		if h.wsHub != nil {
			h.wsHub.BroadcastDocumentsDeleted()
		}
	} else if input.Action == "empty" {
		var ids []int
		if len(input.Documents) > 0 {
			ids = allowedDocs
		} else {
			query := "SELECT id FROM documents WHERE deleted_at IS NOT NULL"
			var qArgs []interface{}
			if user != nil && !user.IsSuperuser {
				query += " AND (owner_id IS NULL OR owner_id = ?)"
				qArgs = append(qArgs, user.ID)
			}
			rows, err := h.db.Query(query, qArgs...)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var id int
					if err := rows.Scan(&id); err == nil {
						ids = append(ids, id)
					}
				}
				if err := rows.Err(); err != nil {
					http.Error(w, fmt.Sprintf("failed iterating trash documents: %v", err), http.StatusInternalServerError)
					return
				}
			}
		}

		type fileToDelete struct {
			orig  string
			arch  string
			thumb string
		}
		var filesToShred []fileToDelete

		for _, id := range ids {
			var origName string
			_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)
			ext := filepath.Ext(origName)
			filesToShred = append(filesToShred, fileToDelete{
				orig:  filepath.Join(h.cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext)),
				arch:  filepath.Join(h.cfg.MediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", id)),
				thumb: filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id)),
			})
		}

		txErr := h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
			for _, id := range ids {
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM documents WHERE id = ?", id); err != nil {
					return fmt.Errorf("failed deleting document %d: %w", id, err)
				}
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM documents_fts WHERE rowid = ?", id); err != nil {
					return fmt.Errorf("failed deleting fts for document %d: %w", id, err)
				}
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_tags WHERE document_id = ?", id); err != nil {
					return fmt.Errorf("failed deleting tags for document %d: %w", id, err)
				}
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_notes WHERE document_id = ?", id); err != nil {
					return fmt.Errorf("failed deleting notes for document %d: %w", id, err)
				}
				if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_custom_fields WHERE document_id = ?", id); err != nil {
					return fmt.Errorf("failed deleting custom fields for document %d: %w", id, err)
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

		// Cryptographic secure shredding only after transactional database deletion succeeds
		for _, f := range filesToShred {
			_ = crypto.ShredFile(f.orig)
			_ = crypto.ShredFile(f.arch)
			_ = crypto.ShredFile(f.thumb)
		}

		if h.wsHub != nil {
			h.wsHub.BroadcastDocumentsDeleted()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"result":  "OK",
		"doc_ids": allowedDocs,
	})
}
