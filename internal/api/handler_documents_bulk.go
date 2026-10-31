package api

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"paperless-go/internal/compress"
	"paperless-go/internal/consumer"
	"paperless-go/internal/models"
)

// BulkDownload handles POST /api/documents/bulk_download/
func (h *DocumentsHandler) BulkDownload(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents        []int  `json:"documents"`
		Content          string `json:"content"`
		FollowFormatting bool   `json:"follow_formatting"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="paperless-documents.zip"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, id := range allowedDocs {
		filePath, _, err := h.getBestFilePath(id, input.Content == "originals")
		if err != nil {
			continue
		}
		file, err := os.Open(filePath)
		if err != nil {
			continue
		}

		var origName string
		_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)
		if origName == "" {
			origName = filepath.Base(filePath)
		}

		entryName := fmt.Sprintf("%07d_%s", id, origName)
		if input.FollowFormatting {
			var title string
			var created time.Time
			var corrName, docTypeName, spPath sql.NullString
			var asnVal sql.NullInt64
			_ = h.db.QueryRow(`
				SELECT d.title, d.created, d.archive_serial_number, c.name, dt.name, sp.path
				FROM documents d
				LEFT JOIN correspondents c ON d.correspondent_id = c.id
				LEFT JOIN document_types dt ON d.document_type_id = dt.id
				LEFT JOIN storage_paths sp ON d.storage_path_id = sp.id
				WHERE d.id = ?
			`, id).Scan(&title, &created, &asnVal, &corrName, &docTypeName, &spPath)

			ext := filepath.Ext(origName)
			if ext == "" {
				ext = ".pdf"
			}
			var asnPtr *int
			if asnVal.Valid {
				v := int(asnVal.Int64)
				asnPtr = &v
			}
			formatted := consumer.FormatStoragePath(spPath.String, title, corrName.String, docTypeName.String, created, created, asnPtr)
			if formatted != "" {
				entryName = formatted + ext
			}
		}
		fw, err := zw.Create(entryName)
		if err == nil {
			_, _ = io.Copy(fw, file)
		}
		file.Close()
	}
}

// SelectionData handles POST /api/documents/selection_data/
func (h *DocumentsHandler) SelectionData(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents []int `json:"documents"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)
	if len(input.Documents) > 0 && len(allowedDocs) != len(input.Documents) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "Insufficient permissions"}`))
		return
	}

	resp := models.SelectionDataResponse{
		SelectedStoragePaths:   []models.SelectionItem{},
		SelectedCustomFields:   []models.SelectionItem{},
		SelectedCorrespondents: []models.SelectionItem{},
		SelectedTags:           []models.SelectionItem{},
		SelectedDocumentTypes:  []models.SelectionItem{},
	}

	var inClause string
	var args []interface{}
	if len(allowedDocs) > 0 {
		var placeholders []string
		for _, id := range allowedDocs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		inClause = strings.Join(placeholders, ",")
	}

	// Correspondents
	queryC := "SELECT c.id, 0 FROM correspondents c"
	if inClause != "" {
		queryC = fmt.Sprintf("SELECT c.id, COUNT(d.id) FROM correspondents c LEFT JOIN documents d ON c.id = d.correspondent_id AND d.id IN (%s) GROUP BY c.id", inClause)
	}
	if rowsC, err := h.db.Query(queryC, args...); err == nil {
		defer rowsC.Close()
		for rowsC.Next() {
			var item models.SelectionItem
			if err := rowsC.Scan(&item.ID, &item.DocumentCount); err == nil {
				resp.SelectedCorrespondents = append(resp.SelectedCorrespondents, item)
			}
		}
		if err := rowsC.Err(); err != nil {
			log.Printf("[WARN] selection data correspondents cursor error: %v", err)
		}
	}

	// Tags
	queryT := "SELECT t.id, 0 FROM tags t"
	if inClause != "" {
		queryT = fmt.Sprintf("SELECT t.id, COUNT(dt.document_id) FROM tags t LEFT JOIN document_tags dt ON t.id = dt.tag_id AND dt.document_id IN (%s) GROUP BY t.id", inClause)
	}
	if rowsT, err := h.db.Query(queryT, args...); err == nil {
		defer rowsT.Close()
		for rowsT.Next() {
			var item models.SelectionItem
			if err := rowsT.Scan(&item.ID, &item.DocumentCount); err == nil {
				resp.SelectedTags = append(resp.SelectedTags, item)
			}
		}
		if err := rowsT.Err(); err != nil {
			log.Printf("[WARN] selection data tags cursor error: %v", err)
		}
	}

	// Document Types
	queryDT := "SELECT dt.id, 0 FROM document_types dt"
	if inClause != "" {
		queryDT = fmt.Sprintf("SELECT dt.id, COUNT(d.id) FROM document_types dt LEFT JOIN documents d ON dt.id = d.document_type_id AND d.id IN (%s) GROUP BY dt.id", inClause)
	}
	if rowsDT, err := h.db.Query(queryDT, args...); err == nil {
		defer rowsDT.Close()
		for rowsDT.Next() {
			var item models.SelectionItem
			if err := rowsDT.Scan(&item.ID, &item.DocumentCount); err == nil {
				resp.SelectedDocumentTypes = append(resp.SelectedDocumentTypes, item)
			}
		}
		if err := rowsDT.Err(); err != nil {
			log.Printf("[WARN] selection data document_types cursor error: %v", err)
		}
	}

	// Storage Paths
	querySP := "SELECT sp.id, 0 FROM storage_paths sp"
	if inClause != "" {
		querySP = fmt.Sprintf("SELECT sp.id, COUNT(d.id) FROM storage_paths sp LEFT JOIN documents d ON sp.id = d.storage_path_id AND d.id IN (%s) GROUP BY sp.id", inClause)
	}
	if rowsSP, err := h.db.Query(querySP, args...); err == nil {
		defer rowsSP.Close()
		for rowsSP.Next() {
			var item models.SelectionItem
			if err := rowsSP.Scan(&item.ID, &item.DocumentCount); err == nil {
				resp.SelectedStoragePaths = append(resp.SelectedStoragePaths, item)
			}
		}
		if err := rowsSP.Err(); err != nil {
			log.Printf("[WARN] selection data storage_paths cursor error: %v", err)
		}
	}

	// Custom Fields
	queryCF := "SELECT cf.id, 0 FROM custom_fields cf"
	if inClause != "" {
		queryCF = fmt.Sprintf("SELECT cf.id, COUNT(dcf.document_id) FROM custom_fields cf LEFT JOIN document_custom_fields dcf ON cf.id = dcf.field_id AND dcf.document_id IN (%s) GROUP BY cf.id", inClause)
	}
	if rowsCF, err := h.db.Query(queryCF, args...); err == nil {
		defer rowsCF.Close()
		for rowsCF.Next() {
			var item models.SelectionItem
			if err := rowsCF.Scan(&item.ID, &item.DocumentCount); err == nil {
				resp.SelectedCustomFields = append(resp.SelectedCustomFields, item)
			}
		}
		if err := rowsCF.Err(); err != nil {
			log.Printf("[WARN] selection data custom_fields cursor error: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// BulkEdit handles POST /api/documents/bulk_edit/
func (h *DocumentsHandler) BulkEdit(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Documents  []int                  `json:"documents"`
		Method     string                 `json:"method"`
		Parameters map[string]interface{} `json:"parameters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(payload.Documents, user)
	payload.Documents = allowedDocs

	txErr := h.db.WithTx(r.Context(), func(tx *sql.Tx) error {
		switch payload.Method {
		case "delete":
			for _, id := range payload.Documents {
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
					return fmt.Errorf("failed marking document %d as deleted: %w", id, err)
				}
			}

		case "modify_tags":
			var addTags, removeTags []int
			if adds, ok := payload.Parameters["add_tags"].([]interface{}); ok {
				for _, a := range adds {
					if val, ok := a.(float64); ok {
						addTags = append(addTags, int(val))
					}
				}
			}
			if rems, ok := payload.Parameters["remove_tags"].([]interface{}); ok {
				for _, r := range rems {
					if val, ok := r.(float64); ok {
						removeTags = append(removeTags, int(val))
					}
				}
			}

			for _, id := range payload.Documents {
				for _, tid := range addTags {
					if _, err := tx.ExecContext(r.Context(), "INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", id, tid); err != nil {
						return fmt.Errorf("failed adding tag %d to document %d: %w", tid, id, err)
					}
				}
				for _, tid := range removeTags {
					if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_tags WHERE document_id = ? AND tag_id = ?", id, tid); err != nil {
						return fmt.Errorf("failed removing tag %d from document %d: %w", tid, id, err)
					}
				}
			}

		case "set_correspondent":
			var corrID *int
			if corrVal, ok := payload.Parameters["correspondent"]; ok && corrVal != nil {
				if c, ok := corrVal.(float64); ok && c > 0 {
					idInt := int(c)
					corrID = &idInt
				}
			}
			for _, id := range payload.Documents {
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET correspondent_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", corrID, id); err != nil {
					return fmt.Errorf("failed setting correspondent for document %d: %w", id, err)
				}
			}

		case "set_document_type":
			var dtID *int
			if typeVal, ok := payload.Parameters["document_type"]; ok && typeVal != nil {
				if dt, ok := typeVal.(float64); ok && dt > 0 {
					idInt := int(dt)
					dtID = &idInt
				}
			}
			for _, id := range payload.Documents {
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET document_type_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", dtID, id); err != nil {
					return fmt.Errorf("failed setting document_type for document %d: %w", id, err)
				}
			}

		case "set_storage_path":
			var spID *int
			if pathVal, ok := payload.Parameters["storage_path"]; ok && pathVal != nil {
				if sp, ok := pathVal.(float64); ok && sp > 0 {
					idInt := int(sp)
					spID = &idInt
				}
			}
			for _, id := range payload.Documents {
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET storage_path_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", spID, id); err != nil {
					return fmt.Errorf("failed setting storage_path for document %d: %w", id, err)
				}
			}

		case "modify_custom_fields":
			if addMap, ok := payload.Parameters["add_custom_fields"].(map[string]interface{}); ok {
				for fidStr, val := range addMap {
					if fid, err := strconv.Atoi(fidStr); err == nil {
						valJSON, err := json.Marshal(val)
						if err != nil {
							return fmt.Errorf("failed marshaling custom field %d: %w", fid, err)
						}
						for _, id := range payload.Documents {
							if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_custom_fields WHERE document_id = ? AND field_id = ?", id, fid); err != nil {
								return fmt.Errorf("failed clearing custom field %d on document %d: %w", fid, id, err)
							}
							if _, err := tx.ExecContext(r.Context(), "INSERT INTO document_custom_fields (document_id, field_id, value_text) VALUES (?, ?, ?)", id, fid, string(valJSON)); err != nil {
								return fmt.Errorf("failed inserting custom field %d on document %d: %w", fid, id, err)
							}
						}
					}
				}
			}
			if rems, ok := payload.Parameters["remove_custom_fields"].([]interface{}); ok {
				for _, remItem := range rems {
					if fid, ok := remItem.(float64); ok {
						for _, id := range payload.Documents {
							if _, err := tx.ExecContext(r.Context(), "DELETE FROM document_custom_fields WHERE document_id = ? AND field_id = ?", id, int(fid)); err != nil {
								return fmt.Errorf("failed removing custom field %d on document %d: %w", int(fid), id, err)
							}
						}
					}
				}
			}

		case "set_permissions":
			var ownerID *int
			if ownerVal, ok := payload.Parameters["owner"]; ok && ownerVal != nil {
				if o, ok := ownerVal.(float64); ok && o > 0 {
					idInt := int(o)
					ownerID = &idInt
				}
			}
			for _, id := range payload.Documents {
				if ownerID == nil {
					if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET owner_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
						return fmt.Errorf("failed resetting owner on document %d: %w", id, err)
					}
				} else {
					if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET owner_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *ownerID, id); err != nil {
						return fmt.Errorf("failed updating owner on document %d: %w", id, err)
					}
				}
			}

		case "rotate":
			var degrees int
			if degVal, ok := payload.Parameters["degrees"].(float64); ok {
				degrees = int(degVal)
			}
			for _, id := range payload.Documents {
				filePath, mimeType, err := h.getOriginalFilePath(id)
				if err == nil && strings.Contains(mimeType, "pdf") {
					thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
					_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
					if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
						return fmt.Errorf("failed updating modified timestamp on rotated document %d: %w", id, err)
					}
				}
			}
			_ = degrees

		case "merge":
			deleteOriginals := false
			if delVal, ok := payload.Parameters["delete_originals"].(bool); ok {
				deleteOriginals = delVal
			}
			if len(payload.Documents) > 1 {
				primaryID := payload.Documents[0]
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", primaryID); err != nil {
					return fmt.Errorf("failed updating primary document %d: %w", primaryID, err)
				}
				if deleteOriginals {
					for _, secondaryID := range payload.Documents[1:] {
						if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", secondaryID); err != nil {
							return fmt.Errorf("failed marking secondary document %d as deleted: %w", secondaryID, err)
						}
					}
				}
			}

		case "delete_pages", "edit_pdf", "remove_password":
			for _, id := range payload.Documents {
				if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", id); err != nil {
					return fmt.Errorf("failed updating modified timestamp for document %d: %w", id, err)
				}
			}

		case "reprocess":
			for _, id := range payload.Documents {
				filePath, mimeType, err := h.getOriginalFilePath(id)
				if err == nil {
					text, _ := consumer.ExtractText(filePath, mimeType)
					if text != "" {
						compressed := compress.CompressText(text)
						if _, err := tx.ExecContext(r.Context(), "UPDATE documents SET content = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", compressed, id); err != nil {
							return fmt.Errorf("failed updating content for document %d: %w", id, err)
						}
						if _, err := tx.ExecContext(r.Context(), "INSERT OR REPLACE INTO documents_fts (rowid, title, content) VALUES (?, (SELECT title FROM documents WHERE id = ?), ?)", id, id, text); err != nil {
							return fmt.Errorf("failed updating fts for document %d: %w", id, err)
						}
					}
					thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
					_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
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

	if payload.Method == "delete" && h.wsHub != nil {
		h.wsHub.BroadcastDocumentsDeleted()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`"OK"`))
}
