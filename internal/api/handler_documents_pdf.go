package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"paperless-go/internal/consumer"
)

// Rotate (/api/documents/rotate/)
func (h *DocumentsHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents []int `json:"documents"`
		Degrees   int   `json:"degrees"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	if input.Degrees == 0 {
		input.Degrees = 90
	}
	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	pdfProc := consumer.NewPDFProcessor()
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)
	for _, id := range allowedDocs {
		h.logHistory(id, "rotate", map[string]interface{}{"rotation": input.Degrees}, uID)
		filePath, mimeType, err := h.getOriginalFilePath(id)
		if err == nil {
			if strings.Contains(mimeType, "pdf") || strings.HasSuffix(strings.ToLower(filePath), ".pdf") {
				if err := pdfProc.RotatePDF(filePath, filePath, input.Degrees, nil); err == nil {
					if newSum, err := consumer.CalculateChecksum(filePath); err == nil {
						_, _ = h.db.Exec("UPDATE documents SET checksum = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newSum, id)
					}
				}
			}
			thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
			_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}

// Merge (/api/documents/merge/)
func (h *DocumentsHandler) Merge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents       []int `json:"documents"`
		DeleteOriginals bool  `json:"delete_originals"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	if len(input.Documents) < 2 {
		http.Error(w, "At least 2 documents required to merge", http.StatusBadRequest)
		return
	}
	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	targetID := input.Documents[0]
	pdfProc := consumer.NewPDFProcessor()

	targetPath, targetMime, errTarget := h.getOriginalFilePath(targetID)
	var pdfFiles []string
	if errTarget == nil && (strings.Contains(targetMime, "pdf") || strings.HasSuffix(strings.ToLower(targetPath), ".pdf")) {
		pdfFiles = append(pdfFiles, targetPath)
	}

	for i := 1; i < len(input.Documents); i++ {
		sourceID := input.Documents[i]
		var sourceContent string
		_ = h.db.QueryRow("SELECT content FROM documents WHERE id = ?", sourceID).Scan(&sourceContent)
		if sourceContent != "" {
			_, _ = h.db.Exec("UPDATE documents SET content = content || '\n\n' || ?, modified = CURRENT_TIMESTAMP WHERE id = ?", sourceContent, targetID)
		}
		if len(pdfFiles) > 0 {
			srcPath, srcMime, errSrc := h.getOriginalFilePath(sourceID)
			if errSrc == nil && (strings.Contains(srcMime, "pdf") || strings.HasSuffix(strings.ToLower(srcPath), ".pdf")) {
				pdfFiles = append(pdfFiles, srcPath)
			}
		}
		if input.DeleteOriginals {
			_, _ = h.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", sourceID)
			h.logHistory(sourceID, "delete", map[string]interface{}{"reason": "merged into document " + strconv.Itoa(targetID)}, uID)
		}
	}

	if len(pdfFiles) == len(input.Documents) && len(pdfFiles) > 1 {
		tmpMerged := targetPath + ".merge.pdf"
		if err := pdfProc.MergePDFs(pdfFiles, tmpMerged); err == nil {
			_ = os.Rename(tmpMerged, targetPath)
			if pc, err := pdfProc.GetPageCount(targetPath); err == nil && pc > 0 {
				_, _ = h.db.Exec("UPDATE documents SET page_count = ? WHERE id = ?", pc, targetID)
			}
			if newSum, err := consumer.CalculateChecksum(targetPath); err == nil {
				_, _ = h.db.Exec("UPDATE documents SET checksum = ? WHERE id = ?", newSum, targetID)
			}
			thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", targetID))
			_ = consumer.GenerateThumbnail(targetPath, thumbPath, targetMime)
		} else {
			_ = os.Remove(tmpMerged)
		}
	}

	h.logHistory(targetID, "merge", map[string]interface{}{"merged_documents": input.Documents[1:]}, uID)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}

// MergeAsVersions (/api/documents/merge_as_versions/)
func (h *DocumentsHandler) MergeAsVersions(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents      []int `json:"documents"`
		RootDocumentID int   `json:"root_document_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	if input.RootDocumentID <= 0 && len(input.Documents) > 0 {
		input.RootDocumentID = input.Documents[0]
	}
	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	for _, id := range input.Documents {
		if id != input.RootDocumentID {
			_, _ = h.db.Exec("UPDATE documents SET root_document_id = ? WHERE id = ?", input.RootDocumentID, id)
			h.logHistory(id, "update_version", map[string]interface{}{"root_document_id": input.RootDocumentID}, uID)
		}
	}
	h.logHistory(input.RootDocumentID, "update_version", map[string]interface{}{"added_versions": input.Documents}, uID)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}

// EditPDF (/api/documents/edit_pdf/)
func (h *DocumentsHandler) EditPDF(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents  []int                    `json:"documents"`
		Operations []map[string]interface{} `json:"operations"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	pdfProc := consumer.NewPDFProcessor()
	for _, id := range input.Documents {
		h.logHistory(id, "edit_pdf", map[string]interface{}{"operations": input.Operations}, uID)
		filePath, mimeType, err := h.getOriginalFilePath(id)
		if err == nil && (strings.Contains(mimeType, "pdf") || strings.HasSuffix(strings.ToLower(filePath), ".pdf")) {
			var pagesToRemove []string
			for _, op := range input.Operations {
				if isDel, ok := op["delete"].(bool); ok && isDel {
					if pageNum, ok := op["page"].(float64); ok {
						pagesToRemove = append(pagesToRemove, strconv.Itoa(int(pageNum)))
					} else if pageStr, ok := op["page"].(string); ok {
						pagesToRemove = append(pagesToRemove, pageStr)
					}
				}
				if rotVal, ok := op["rotate"].(float64); ok && int(rotVal) != 0 {
					var targetPages []string
					if pageNum, ok := op["page"].(float64); ok {
						targetPages = []string{strconv.Itoa(int(pageNum))}
					} else if pageStr, ok := op["page"].(string); ok {
						targetPages = []string{pageStr}
					}
					_ = pdfProc.RotatePDF(filePath, filePath, int(rotVal), targetPages)
				}
			}
			if len(pagesToRemove) > 0 {
				_ = pdfProc.RemovePages(filePath, filePath, pagesToRemove)
			}
			if pc, err := pdfProc.GetPageCount(filePath); err == nil && pc > 0 {
				_, _ = h.db.Exec("UPDATE documents SET page_count = ? WHERE id = ?", pc, id)
			}
			if newSum, err := consumer.CalculateChecksum(filePath); err == nil {
				_, _ = h.db.Exec("UPDATE documents SET checksum = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newSum, id)
			}
			thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
			_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}

// RemovePassword (/api/documents/remove_password/)
func (h *DocumentsHandler) RemovePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents []int  `json:"documents"`
		Password  string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	pdfProc := consumer.NewPDFProcessor()
	for _, id := range input.Documents {
		h.logHistory(id, "remove_password", map[string]interface{}{"status": "unlocked"}, uID)
		filePath, mimeType, err := h.getOriginalFilePath(id)
		if err == nil && (strings.Contains(mimeType, "pdf") || strings.HasSuffix(strings.ToLower(filePath), ".pdf")) {
			if err := pdfProc.DecryptPDF(filePath, filePath, input.Password); err == nil {
				if pc, err := pdfProc.GetPageCount(filePath); err == nil && pc > 0 {
					_, _ = h.db.Exec("UPDATE documents SET page_count = ? WHERE id = ?", pc, id)
				}
				if newSum, err := consumer.CalculateChecksum(filePath); err == nil {
					_, _ = h.db.Exec("UPDATE documents SET checksum = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newSum, id)
				}
				thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
				_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"result": "OK"}`))
}

