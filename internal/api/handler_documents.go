package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
	"paperless-go/internal/queue"
)

type DocumentsHandler struct {
	db       *database.DB
	cfg      *config.Config
	consumer *consumer.Consumer
	queue    *queue.Queue
}

func NewDocumentsHandler(db *database.DB, cfg *config.Config, c *consumer.Consumer, q *queue.Queue) *DocumentsHandler {
	return &DocumentsHandler{db: db, cfg: cfg, consumer: c, queue: q}
}

func (h *DocumentsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	var conditions []string
	var args []interface{}

	// Full text search
	searchQuery := q.Get("query")
	if searchQuery != "" {
		conditions = append(conditions, "d.id IN (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ?)")
		args = append(args, searchQuery+"*")
	}

	// Correspondent filter
	if corrStr := q.Get("correspondent__id"); corrStr != "" {
		if corrID, err := strconv.Atoi(corrStr); err == nil {
			conditions = append(conditions, "d.correspondent_id = ?")
			args = append(args, corrID)
		}
	}

	// Document type filter
	if typeStr := q.Get("document_type__id"); typeStr != "" {
		if typeID, err := strconv.Atoi(typeStr); err == nil {
			conditions = append(conditions, "d.document_type_id = ?")
			args = append(args, typeID)
		}
	}

	// Storage path filter
	if pathStr := q.Get("storage_path__id"); pathStr != "" {
		if pathID, err := strconv.Atoi(pathStr); err == nil {
			conditions = append(conditions, "d.storage_path_id = ?")
			args = append(args, pathID)
		}
	}

	// Tag filters (tags__id__all)
	if tagsAll := q.Get("tags__id__all"); tagsAll != "" {
		tagIDs := strings.Split(tagsAll, ",")
		for _, tidStr := range tagIDs {
			if tid, err := strconv.Atoi(strings.TrimSpace(tidStr)); err == nil {
				conditions = append(conditions, "EXISTS (SELECT 1 FROM document_tags dt WHERE dt.document_id = d.id AND dt.tag_id = ?)")
				args = append(args, tid)
			}
		}
	}

	// Tag filters (tags__id__none)
	if tagsNone := q.Get("tags__id__none"); tagsNone != "" {
		tagIDs := strings.Split(tagsNone, ",")
		for _, tidStr := range tagIDs {
			if tid, err := strconv.Atoi(strings.TrimSpace(tidStr)); err == nil {
				conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM document_tags dt WHERE dt.document_id = d.id AND dt.tag_id = ?)")
				args = append(args, tid)
			}
		}
	}

	// Ordering
	ordering := q.Get("ordering")
	orderClause := "ORDER BY d.created DESC"
	switch ordering {
	case "created":
		orderClause = "ORDER BY d.created ASC"
	case "-created":
		orderClause = "ORDER BY d.created DESC"
	case "added":
		orderClause = "ORDER BY d.added ASC"
	case "-added":
		orderClause = "ORDER BY d.added DESC"
	case "title":
		orderClause = "ORDER BY d.title COLLATE NOCASE ASC"
	case "-title":
		orderClause = "ORDER BY d.title COLLATE NOCASE DESC"
	case "archive_serial_number":
		orderClause = "ORDER BY d.archive_serial_number ASC"
	case "-archive_serial_number":
		orderClause = "ORDER BY d.archive_serial_number DESC"
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total & fetch all matching IDs
	allIDsQuery := fmt.Sprintf("SELECT d.id FROM documents d %s %s", whereClause, orderClause)
	rowsAll, err := h.db.Query(allIDsQuery, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rowsAll.Close()

	var allIDs []int
	for rowsAll.Next() {
		var id int
		if err := rowsAll.Scan(&id); err == nil {
			allIDs = append(allIDs, id)
		}
	}
	if allIDs == nil {
		allIDs = []int{}
	}
	totalCount := len(allIDs)

	// Fetch paginated results
	pageQuery := fmt.Sprintf(`
		SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
		       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
		       d.owner_id, d.page_count
		FROM documents d %s %s LIMIT ? OFFSET ?
	`, whereClause, orderClause)

	pageArgs := append(args, pageSize, offset)
	rows, err := h.db.Query(pageQuery, pageArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var results []models.Document
	for rows.Next() {
		var doc models.Document
		var corr, docType, storPath, owner, asn sql.NullInt64
		var archFile sql.NullString

		if err := rows.Scan(
			&doc.ID, &doc.Title, &doc.Content, &corr, &docType, &storPath,
			&doc.Created, &doc.Modified, &doc.Added, &asn, &doc.OriginalFileName, &archFile,
			&owner, &doc.PageCount,
		); err == nil {
			if corr.Valid {
				c := int(corr.Int64)
				doc.Correspondent = &c
			}
			if docType.Valid {
				dt := int(docType.Int64)
				doc.DocumentType = &dt
			}
			if storPath.Valid {
				sp := int(storPath.Int64)
				doc.StoragePath = &sp
			}
			if owner.Valid {
				o := int(owner.Int64)
				doc.Owner = &o
			}
			if asn.Valid {
				a := int(asn.Int64)
				doc.ArchiveSerialNumber = &a
			}
			if archFile.Valid {
				doc.ArchivedFileName = &archFile.String
			}

			doc.CreatedDate = doc.Created.Format("2006-01-02")
			doc.UserCanChange = true
			doc.Notes = []models.DocumentNote{}
			doc.CustomFields = []models.DocumentCustomFieldValue{}
			doc.Tags = []int{}

			results = append(results, doc)
		}
	}
	rows.Close()

	for i := range results {
		results[i].Tags = h.getDocTags(results[i].ID)
	}

	if results == nil {
		results = []models.Document{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:   totalCount,
		All:     allIDs,
		Results: results,
	})
}

func (h *DocumentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *DocumentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var input struct {
		Title               *string    `json:"title"`
		Correspondent       *int       `json:"correspondent"`
		DocumentType        *int       `json:"document_type"`
		StoragePath         *int       `json:"storage_path"`
		ArchiveSerialNumber *int       `json:"archive_serial_number"`
		Created             *time.Time `json:"created"`
		Tags                *[]int     `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Title != nil {
		_, _ = h.db.Exec("UPDATE documents SET title = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Title, id)
	}
	if input.Correspondent != nil {
		_, _ = h.db.Exec("UPDATE documents SET correspondent_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Correspondent, id)
	}
	if input.DocumentType != nil {
		_, _ = h.db.Exec("UPDATE documents SET document_type_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.DocumentType, id)
	}
	if input.StoragePath != nil {
		_, _ = h.db.Exec("UPDATE documents SET storage_path_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.StoragePath, id)
	}
	if input.ArchiveSerialNumber != nil {
		_, _ = h.db.Exec("UPDATE documents SET archive_serial_number = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.ArchiveSerialNumber, id)
	}
	if input.Created != nil {
		_, _ = h.db.Exec("UPDATE documents SET created = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Created, id)
	}
	if input.Tags != nil {
		_, _ = h.db.Exec("DELETE FROM document_tags WHERE document_id = ?", id)
		for _, tagID := range *input.Tags {
			_, _ = h.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", id, tagID)
		}
	}

	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *DocumentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	var origName string
	_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)

	ext := filepath.Ext(origName)
	origPath := filepath.Join(h.cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext))
	thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))

	_ = os.Remove(origPath)
	_ = os.Remove(thumbPath)

	_, err := h.db.Exec("DELETE FROM documents WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentsHandler) PostDocument(w http.ResponseWriter, r *http.Request) {
	// Multipart form upload
	err := r.ParseMultipartForm(50 << 20) // 50MB max
	if err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("document")
	if err != nil {
		http.Error(w, "Missing document file in request", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save to temp folder for consumer
	tmpFile, err := os.CreateTemp("", "paperless-upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, file); err != nil {
		http.Error(w, "Failed to write temp file", http.StatusInternalServerError)
		return
	}

	tmpPath := tmpFile.Name()
	fileName := header.Filename

	var opts consumer.ConsumeOptions
	if t := r.FormValue("title"); t != "" {
		opts.Title = t
	}
	if cStr := r.FormValue("correspondent"); cStr != "" {
		if cID, err := strconv.Atoi(cStr); err == nil {
			opts.Correspondent = &cID
		}
	}
	if dtStr := r.FormValue("document_type"); dtStr != "" {
		if dtID, err := strconv.Atoi(dtStr); err == nil {
			opts.DocumentType = &dtID
		}
	}

	// Enqueue in worker queue
	taskID := h.queue.Enqueue("Consume document", fileName, tmpPath, func(tid string) (int, error) {
		defer os.Remove(tmpPath)
		return h.consumer.ConsumeFile(tmpPath, fileName, &opts)
	})

	// Paperless-ngx API returns the task UUID as a JSON string
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(taskID)
}

func (h *DocumentsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	filePath, mimeType, err := h.getOriginalFilePath(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, filePath)
}

func (h *DocumentsHandler) Download(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	filePath, mimeType, err := h.getOriginalFilePath(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var origName string
	_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, origName))
	http.ServeFile(w, r, filePath)
}

func (h *DocumentsHandler) Thumb(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))

	if _, err := os.Stat(thumbPath); err != nil {
		// If thumbnail not yet generated, generate placeholder
		_ = consumer.GenerateThumbnail("", thumbPath, "application/pdf")
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, thumbPath)
}

func (h *DocumentsHandler) Metadata(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	filePath, mimeType, err := h.getOriginalFilePath(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var checksum string
	_ = h.db.QueryRow("SELECT checksum FROM documents WHERE id = ?", id).Scan(&checksum)

	info, _ := os.Stat(filePath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	resp := map[string]interface{}{
		"original_checksum":   checksum,
		"original_size":       size,
		"original_mime_type":  mimeType,
		"media_filename":      filepath.Base(filePath),
		"has_archive_version": false,
		"original_metadata":   []interface{}{},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

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

	switch payload.Method {
	case "delete":
		for _, id := range payload.Documents {
			var origName string
			_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)
			ext := filepath.Ext(origName)
			_ = os.Remove(filepath.Join(h.cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext)))
			_ = os.Remove(filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id)))
			_, _ = h.db.Exec("DELETE FROM documents WHERE id = ?", id)
		}
	case "set_correspondent":
		if corrVal, ok := payload.Parameters["correspondent"]; ok {
			var corrID *int
			if c, ok := corrVal.(float64); ok && c > 0 {
				idInt := int(c)
				corrID = &idInt
			}
			for _, id := range payload.Documents {
				_, _ = h.db.Exec("UPDATE documents SET correspondent_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", corrID, id)
			}
		}
	case "set_document_type":
		if typeVal, ok := payload.Parameters["document_type"]; ok {
			var dtID *int
			if dt, ok := typeVal.(float64); ok && dt > 0 {
				idInt := int(dt)
				dtID = &idInt
			}
			for _, id := range payload.Documents {
				_, _ = h.db.Exec("UPDATE documents SET document_type_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", dtID, id)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`"OK"`))
}

func (h *DocumentsHandler) fetchDocument(id int) (*models.Document, error) {
	var doc models.Document
	var corr, docType, storPath, owner, asn sql.NullInt64
	var archFile sql.NullString

	err := h.db.QueryRow(`
		SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
		       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
		       d.owner_id, d.page_count
		FROM documents d WHERE d.id = ?
	`, id).Scan(
		&doc.ID, &doc.Title, &doc.Content, &corr, &docType, &storPath,
		&doc.Created, &doc.Modified, &doc.Added, &asn, &doc.OriginalFileName, &archFile,
		&owner, &doc.PageCount,
	)
	if err != nil {
		return nil, err
	}

	if corr.Valid {
		c := int(corr.Int64)
		doc.Correspondent = &c
	}
	if docType.Valid {
		dt := int(docType.Int64)
		doc.DocumentType = &dt
	}
	if storPath.Valid {
		sp := int(storPath.Int64)
		doc.StoragePath = &sp
	}
	if owner.Valid {
		o := int(owner.Int64)
		doc.Owner = &o
	}
	if asn.Valid {
		a := int(asn.Int64)
		doc.ArchiveSerialNumber = &a
	}
	if archFile.Valid {
		doc.ArchivedFileName = &archFile.String
	}

	doc.CreatedDate = doc.Created.Format("2006-01-02")
	doc.UserCanChange = true
	doc.Notes = []models.DocumentNote{}
	doc.CustomFields = []models.DocumentCustomFieldValue{}
	doc.Tags = h.getDocTags(doc.ID)

	return &doc, nil
}

func (h *DocumentsHandler) getDocTags(docID int) []int {
	rows, err := h.db.Query("SELECT tag_id FROM document_tags WHERE document_id = ?", docID)
	if err != nil {
		return []int{}
	}
	defer rows.Close()

	var tags []int
	for rows.Next() {
		var tid int
		if err := rows.Scan(&tid); err == nil {
			tags = append(tags, tid)
		}
	}
	if tags == nil {
		tags = []int{}
	}
	return tags
}

func (h *DocumentsHandler) getOriginalFilePath(docID int) (string, string, error) {
	var origName, mimeType string
	err := h.db.QueryRow("SELECT original_filename, mime_type FROM documents WHERE id = ?", docID).Scan(&origName, &mimeType)
	if err != nil {
		return "", "", err
	}
	ext := filepath.Ext(origName)
	path := filepath.Join(h.cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", docID, ext))
	return path, mimeType, nil
}
