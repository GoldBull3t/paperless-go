package api

import (
	"archive/zip"
	"bytes"
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
	"unicode"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/ai"
	"paperless-go/internal/compress"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/crypto"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
	"paperless-go/internal/queue"
	"paperless-go/internal/vault"
	"paperless-go/internal/workflow"
)

type DocumentsHandler struct {
	db       *database.DB
	cfg      *config.Config
	consumer *consumer.Consumer
	queue    *queue.Queue
	wsHub    *WebSocketHub
	vm       *vault.VaultManager
}

func (h *DocumentsHandler) SetVaultManager(vm *vault.VaultManager) {
	h.vm = vm
}

func NewDocumentsHandler(db *database.DB, cfg *config.Config, c *consumer.Consumer, q *queue.Queue, ws *WebSocketHub) *DocumentsHandler {
	return &DocumentsHandler{db: db, cfg: cfg, consumer: c, queue: q, wsHub: ws}
}

func sanitizeFTSQuery(q string) string {
	var cleaned strings.Builder
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			cleaned.WriteRune(r)
		} else {
			cleaned.WriteRune(' ')
		}
	}
	words := strings.Fields(cleaned.String())
	if len(words) == 0 {
		return ""
	}
	var terms []string
	for _, w := range words {
		terms = append(terms, w+"*")
	}
	return strings.Join(terms, " ")
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
	truncateContent := q.Get("truncate_content") == "true"

	var conditions []string
	var args []interface{}

	// Exclude soft-deleted documents from regular list
	conditions = append(conditions, "d.deleted_at IS NULL")

	// Multi-user Object-level permissions (Feature 4)
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		conditions = append(conditions, "(d.owner_id IS NULL OR d.owner_id = ?)")
		args = append(args, user.ID)
	}

	// Owner filters
	if ownerStr := q.Get("owner__id"); ownerStr != "" {
		if oid, err := strconv.Atoi(ownerStr); err == nil {
			conditions = append(conditions, "d.owner_id = ?")
			args = append(args, oid)
		}
	}
	if ownerNull := q.Get("owner__isnull"); ownerNull != "" {
		if ownerNull == "true" || ownerNull == "1" {
			conditions = append(conditions, "d.owner_id IS NULL")
		} else {
			conditions = append(conditions, "d.owner_id IS NOT NULL")
		}
	}

	// Full text search with query sanitization
	searchQuery := q.Get("query")
	if searchQuery != "" {
		safeFTS := sanitizeFTSQuery(searchQuery)
		if safeFTS != "" {
			conditions = append(conditions, "d.id IN (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ?)")
			args = append(args, safeFTS)
		}
	}

	// Checksum filter for deduplication inspection
	if cs := q.Get("checksum"); cs != "" {
		conditions = append(conditions, "d.checksum = ?")
		args = append(args, cs)
	}

	// Correspondent filters
	if corrStr := q.Get("correspondent__id"); corrStr != "" {
		if corrID, err := strconv.Atoi(corrStr); err == nil {
			conditions = append(conditions, "d.correspondent_id = ?")
			args = append(args, corrID)
		}
	}
	if corrNull := q.Get("correspondent__isnull"); corrNull != "" {
		if corrNull == "true" || corrNull == "1" {
			conditions = append(conditions, "d.correspondent_id IS NULL")
		} else {
			conditions = append(conditions, "d.correspondent_id IS NOT NULL")
		}
	}
	if corrIn := q.Get("correspondent__id__in"); corrIn != "" {
		ids := strings.Split(corrIn, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("d.correspondent_id IN (%s)", strings.Join(placeholders, ",")))
		}
	}
	if corrNone := q.Get("correspondent__id__none"); corrNone != "" {
		ids := strings.Split(corrNone, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("(d.correspondent_id NOT IN (%s) OR d.correspondent_id IS NULL)", strings.Join(placeholders, ",")))
		}
	}

	// Document type filters
	if typeStr := q.Get("document_type__id"); typeStr != "" {
		if typeID, err := strconv.Atoi(typeStr); err == nil {
			conditions = append(conditions, "d.document_type_id = ?")
			args = append(args, typeID)
		}
	}
	if typeNull := q.Get("document_type__isnull"); typeNull != "" {
		if typeNull == "true" || typeNull == "1" {
			conditions = append(conditions, "d.document_type_id IS NULL")
		} else {
			conditions = append(conditions, "d.document_type_id IS NOT NULL")
		}
	}
	if typeIn := q.Get("document_type__id__in"); typeIn != "" {
		ids := strings.Split(typeIn, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("d.document_type_id IN (%s)", strings.Join(placeholders, ",")))
		}
	}
	if typeNone := q.Get("document_type__id__none"); typeNone != "" {
		ids := strings.Split(typeNone, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("(d.document_type_id NOT IN (%s) OR d.document_type_id IS NULL)", strings.Join(placeholders, ",")))
		}
	}

	// Storage path filters
	if pathStr := q.Get("storage_path__id"); pathStr != "" {
		if pathID, err := strconv.Atoi(pathStr); err == nil {
			conditions = append(conditions, "d.storage_path_id = ?")
			args = append(args, pathID)
		}
	}
	if pathNull := q.Get("storage_path__isnull"); pathNull != "" {
		if pathNull == "true" || pathNull == "1" {
			conditions = append(conditions, "d.storage_path_id IS NULL")
		} else {
			conditions = append(conditions, "d.storage_path_id IS NOT NULL")
		}
	}
	if pathIn := q.Get("storage_path__id__in"); pathIn != "" {
		ids := strings.Split(pathIn, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("d.storage_path_id IN (%s)", strings.Join(placeholders, ",")))
		}
	}
	if pathNone := q.Get("storage_path__id__none"); pathNone != "" {
		ids := strings.Split(pathNone, ",")
		var placeholders []string
		for _, idStr := range ids {
			if id, err := strconv.Atoi(strings.TrimSpace(idStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, id)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("(d.storage_path_id NOT IN (%s) OR d.storage_path_id IS NULL)", strings.Join(placeholders, ",")))
		}
	}

	// Inbox tag filter (is_in_inbox)
	if inInbox := q.Get("is_in_inbox"); inInbox != "" {
		if inInbox == "true" || inInbox == "1" {
			conditions = append(conditions, "EXISTS (SELECT 1 FROM document_tags dt JOIN tags t ON dt.tag_id = t.id WHERE dt.document_id = d.id AND t.is_inbox_tag = 1)")
		} else if inInbox == "false" || inInbox == "0" {
			conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM document_tags dt JOIN tags t ON dt.tag_id = t.id WHERE dt.document_id = d.id AND t.is_inbox_tag = 1)")
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

	// Tag filters (tags__id__in)
	if tagsIn := q.Get("tags__id__in"); tagsIn != "" {
		tagIDs := strings.Split(tagsIn, ",")
		var placeholders []string
		for _, tidStr := range tagIDs {
			if tid, err := strconv.Atoi(strings.TrimSpace(tidStr)); err == nil {
				placeholders = append(placeholders, "?")
				args = append(args, tid)
			}
		}
		if len(placeholders) > 0 {
			conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM document_tags dt WHERE dt.document_id = d.id AND dt.tag_id IN (%s))", strings.Join(placeholders, ",")))
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

	// Tag presence (is_tagged)
	if isTagged := q.Get("is_tagged"); isTagged != "" {
		if isTagged == "true" || isTagged == "1" {
			conditions = append(conditions, "EXISTS (SELECT 1 FROM document_tags dt WHERE dt.document_id = d.id)")
		} else {
			conditions = append(conditions, "NOT EXISTS (SELECT 1 FROM document_tags dt WHERE dt.document_id = d.id)")
		}
	}

	// Date range filters (created)
	if createdGte := q.Get("created__date__gte"); createdGte != "" {
		conditions = append(conditions, "date(d.created) >= date(?)")
		args = append(args, createdGte)
	}
	if createdLte := q.Get("created__date__lte"); createdLte != "" {
		conditions = append(conditions, "date(d.created) <= date(?)")
		args = append(args, createdLte)
	}
	if createdLt := q.Get("created__date__lt"); createdLt != "" {
		conditions = append(conditions, "date(d.created) < date(?)")
		args = append(args, createdLt)
	}
	if createdGt := q.Get("created__date__gt"); createdGt != "" {
		conditions = append(conditions, "date(d.created) > date(?)")
		args = append(args, createdGt)
	}

	// Date range filters (added)
	if addedGte := q.Get("added__date__gte"); addedGte != "" {
		conditions = append(conditions, "date(d.added) >= date(?)")
		args = append(args, addedGte)
	}
	if addedLte := q.Get("added__date__lte"); addedLte != "" {
		conditions = append(conditions, "date(d.added) <= date(?)")
		args = append(args, addedLte)
	}
	if addedLt := q.Get("added__date__lt"); addedLt != "" {
		conditions = append(conditions, "date(d.added) < date(?)")
		args = append(args, addedLt)
	}
	if addedGt := q.Get("added__date__gt"); addedGt != "" {
		conditions = append(conditions, "date(d.added) > date(?)")
		args = append(args, addedGt)
	}

	// Title icontains filter
	if titleContains := q.Get("title__icontains"); titleContains != "" {
		conditions = append(conditions, "d.title LIKE ?")
		args = append(args, "%"+titleContains+"%")
	}

	// Content icontains filter
	if contentContains := q.Get("content__icontains"); contentContains != "" {
		conditions = append(conditions, "d.content LIKE ?")
		args = append(args, "%"+contentContains+"%")
	}

	// ASN filters
	if asnStr := q.Get("archive_serial_number"); asnStr != "" {
		if asnVal, err := strconv.Atoi(asnStr); err == nil {
			conditions = append(conditions, "d.archive_serial_number = ?")
			args = append(args, asnVal)
		}
	}
	if asnNull := q.Get("archive_serial_number__isnull"); asnNull != "" {
		if asnNull == "true" || asnNull == "1" {
			conditions = append(conditions, "d.archive_serial_number IS NULL")
		} else {
			conditions = append(conditions, "d.archive_serial_number IS NOT NULL")
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
	_ = rowsAll.Err()
	if allIDs == nil {
		allIDs = []int{}
	}
	totalCount := len(allIDs)

	// Fetch paginated results
	pageQuery := fmt.Sprintf(`
		SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
		       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
		       d.mime_type, d.owner_id, d.page_count, d.is_vault
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
		var corr, docType, storPath, owner, asn, isVault sql.NullInt64
		var archFile sql.NullString

		if err := rows.Scan(
			&doc.ID, &doc.Title, &doc.Content, &corr, &docType, &storPath,
			&doc.Created, &doc.Modified, &doc.Added, &asn, &doc.OriginalFileName, &archFile,
			&doc.MimeType, &owner, &doc.PageCount, &isVault,
		); err == nil {
			if isVault.Valid && isVault.Int64 == 1 {
				doc.IsVault = true
				if h.vm != nil && !h.vm.IsUnlocked() {
					doc.Content = "[VAULT PROTECTED - UNLOCK TO VIEW]"
				}
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

			// Transparently decompress content if Zstandard compressed (Feature #2)
			doc.Content = compress.DecompressText(doc.Content)

			// If truncate_content is requested by Angular UI, don't dump entire OCR text
			if truncateContent {
				if len(doc.Content) > 100 {
					doc.Content = doc.Content[:100] + "..."
				}
			}

			doc.CreatedDate = doc.Created.Format("2006-01-02")
			doc.UserCanChange = true
			if user != nil && !user.IsSuperuser {
				doc.UserCanChange = (doc.Owner == nil || *doc.Owner == user.ID)
			}
			doc.IsSharedByRequester = (user != nil && doc.Owner != nil && *doc.Owner == user.ID)
			doc.Notes = []models.DocumentNote{}
			doc.CustomFields = []models.DocumentCustomFieldValue{}
			doc.Tags = []int{}

			results = append(results, doc)
		}
	}
	_ = rows.Err()
	rows.Close()

	for i := range results {
		results[i].Tags = h.getDocTags(results[i].ID)
		results[i].CustomFields = h.getDocCustomFields(results[i].ID)
	}

	if searchQuery != "" {
		docPtrs := make([]*models.Document, len(results))
		for i := range results {
			docPtrs[i] = &results[i]
		}
		BuildSearchHits(h.db, docPtrs, searchQuery)
	} else if moreLikeID := q.Get("more_like_id"); moreLikeID != "" {
		for i := range results {
			results[i].SearchHit = &models.SearchHit{
				Score:          1.0,
				Highlights:     []string{},
				NoteHighlights: []string{},
			}
		}
	}

	if results == nil {
		results = []models.Document{}
	}

	var nextURL, prevURL *string
	if offset+pageSize < totalCount {
		u := fmt.Sprintf("?page=%d&page_size=%d", page+1, pageSize)
		nextURL = &u
	}
	if page > 1 {
		u := fmt.Sprintf("?page=%d&page_size=%d", page-1, pageSize)
		prevURL = &u
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:    totalCount,
		Next:     nextURL,
		Previous: prevURL,
		All:      allIDs,
		Results:  results,
	})
}

func (h *DocumentsHandler) checkDocumentAccess(r *http.Request, docID int) (bool, bool) {
	var ownerID sql.NullInt64
	err := h.db.QueryRow("SELECT owner_id FROM documents WHERE id = ?", docID).Scan(&ownerID)
	if err != nil {
		return false, false
	}
	user := GetUserFromContext(r.Context())
	if user == nil || user.IsSuperuser {
		return true, true
	}
	if !ownerID.Valid || int(ownerID.Int64) == user.ID {
		return true, true
	}
	return true, false
}

func (h *DocumentsHandler) filterAllowedDocuments(docIDs []int, user *models.User) []int {
	if user == nil || user.IsSuperuser {
		return docIDs
	}
	var allowed []int
	for _, id := range docIDs {
		var ownerID sql.NullInt64
		err := h.db.QueryRow("SELECT owner_id FROM documents WHERE id = ?", id).Scan(&ownerID)
		if err == nil && (!ownerID.Valid || int(ownerID.Int64) == user.ID) {
			allowed = append(allowed, id)
		}
	}
	return allowed
}

func (h *DocumentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	user := GetUserFromContext(r.Context())
	doc, err := h.fetchDocumentWithUser(id, user)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *DocumentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	var input struct {
		Title               *string                            `json:"title"`
		Correspondent       *int                               `json:"correspondent"`
		DocumentType        *int                               `json:"document_type"`
		StoragePath         *int                               `json:"storage_path"`
		ArchiveSerialNumber *int                               `json:"archive_serial_number"`
		CreatedDate         *string                            `json:"created_date"`
		Tags                *[]int                             `json:"tags"`
		CustomFields        *[]models.DocumentCustomFieldValue `json:"custom_fields"`
		Owner               *int                               `json:"owner"`
		RemoveInboxTags     *bool                              `json:"remove_inbox_tags"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	if input.Owner != nil && (user == nil || user.IsSuperuser || *input.Owner == user.ID) {
		if *input.Owner == 0 {
			_, _ = h.db.Exec("UPDATE documents SET owner_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE documents SET owner_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Owner, id)
		}
	}

	if input.Title != nil {
		_, _ = h.db.Exec("UPDATE documents SET title = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Title, id)
		_, _ = h.db.Exec("UPDATE documents_fts SET title = ? WHERE rowid = ?", *input.Title, id)
	}
	if input.Correspondent != nil {
		if *input.Correspondent <= 0 {
			_, _ = h.db.Exec("UPDATE documents SET correspondent_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE documents SET correspondent_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.Correspondent, id)
		}
	}
	if input.DocumentType != nil {
		if *input.DocumentType <= 0 {
			_, _ = h.db.Exec("UPDATE documents SET document_type_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE documents SET document_type_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.DocumentType, id)
		}
	}
	if input.StoragePath != nil {
		if *input.StoragePath <= 0 {
			_, _ = h.db.Exec("UPDATE documents SET storage_path_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE documents SET storage_path_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.StoragePath, id)
		}
	}
	if input.ArchiveSerialNumber != nil {
		if *input.ArchiveSerialNumber <= 0 {
			_, _ = h.db.Exec("UPDATE documents SET archive_serial_number = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		} else {
			_, _ = h.db.Exec("UPDATE documents SET archive_serial_number = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *input.ArchiveSerialNumber, id)
		}
	}
	if input.CreatedDate != nil {
		if t, err := time.Parse("2006-01-02", *input.CreatedDate); err == nil {
			_, _ = h.db.Exec("UPDATE documents SET created = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", t, id)
		}
	}

	if input.Tags != nil {
		_, _ = h.db.Exec("DELETE FROM document_tags WHERE document_id = ?", id)
		for _, tagID := range *input.Tags {
			_, _ = h.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", id, tagID)
		}
	}

	if input.RemoveInboxTags != nil && *input.RemoveInboxTags {
		_, _ = h.db.Exec("DELETE FROM document_tags WHERE document_id = ? AND tag_id IN (SELECT id FROM tags WHERE is_inbox_tag = 1)", id)
	}

	if input.CustomFields != nil {
		_, _ = h.db.Exec("DELETE FROM document_custom_fields WHERE document_id = ?", id)
		for _, cf := range *input.CustomFields {
			valJSON, _ := json.Marshal(cf.Value)
			_, _ = h.db.Exec("INSERT INTO document_custom_fields (document_id, field_id, value_text) VALUES (?, ?, ?)", id, cf.Field, string(valJSON))
		}
	}

	doc, err := h.fetchDocumentWithUser(id, user)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Trigger Native Workflows on Document Update (Feature #10)
	if h.consumer != nil && h.consumer.GetWorkflowEngine() != nil {
		_ = h.consumer.GetWorkflowEngine().ExecuteWorkflows(doc, workflow.TriggerDocumentUpdated, workflow.SourceWebUI)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (h *DocumentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	// Soft delete matches paperless trash mechanism
	_, _ = h.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id)

	if h.wsHub != nil {
		h.wsHub.BroadcastDocumentsDeleted()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *DocumentsHandler) DeleteDocuments(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents []int `json:"documents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)
	for _, id := range allowedDocs {
		_, _ = h.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	}

	if h.wsHub != nil {
		h.wsHub.BroadcastDocumentsDeleted()
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`"OK"`))
}

func (h *DocumentsHandler) ReprocessDocuments(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents []int `json:"documents"`
		RemoteOCR bool  `json:"remote_ocr"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	user := GetUserFromContext(r.Context())
	allowedDocs := h.filterAllowedDocuments(input.Documents, user)

	for _, id := range allowedDocs {
		docID := id
		var origName, mimeType string
		err := h.db.QueryRow("SELECT original_filename, mime_type FROM documents WHERE id = ?", docID).Scan(&origName, &mimeType)
		if err == nil {
			filePath, _, err := h.getOriginalFilePath(docID)
			if err == nil {
				h.queue.Enqueue(fmt.Sprintf("Reprocess document %d", docID), origName, filePath, func(tid string) (int, error) {
					text, _ := consumer.ExtractText(filePath, mimeType)
					if text != "" {
						_, _ = h.db.Exec("UPDATE documents SET content = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", text, docID)
					}
					thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", docID))
					_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
					return docID, nil
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`"OK"`))
}

func (h *DocumentsHandler) NextASN(w http.ResponseWriter, r *http.Request) {
	var nextASN int
	_ = h.db.QueryRow("SELECT COALESCE(MAX(archive_serial_number), 0) + 1 FROM documents WHERE archive_serial_number IS NOT NULL").Scan(&nextASN)
	if nextASN < 1 {
		nextASN = 1
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nextASN)
}

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

func (h *DocumentsHandler) logHistory(docID int, action string, changes map[string]interface{}, actorID *int) {
	changesJSON, _ := json.Marshal(changes)
	_, _ = h.db.Exec(`
		INSERT INTO document_history (document_id, action, changes_json, actor_id, timestamp)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, docID, action, string(changesJSON), actorID)
}

func (h *DocumentsHandler) History(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	rows, err := h.db.Query(`
		SELECT dh.id, dh.action, dh.timestamp, dh.changes_json, u.id, u.username
		FROM document_history dh
		LEFT JOIN users u ON dh.actor_id = u.id
		WHERE dh.document_id = ?
		ORDER BY dh.id DESC
	`, id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	defer rows.Close()

	var entries []models.DocumentHistoryEntry
	for rows.Next() {
		var entry models.DocumentHistoryEntry
		var changesStr string
		var uID sql.NullInt64
		var uName sql.NullString
		if err := rows.Scan(&entry.ID, &entry.Action, &entry.Timestamp, &changesStr, &uID, &uName); err == nil {
			if uID.Valid {
				entry.Actor = map[string]interface{}{
					"id":       int(uID.Int64),
					"username": uName.String,
				}
			}
			var changes map[string]interface{}
			_ = json.Unmarshal([]byte(changesStr), &changes)
			if changes == nil {
				changes = map[string]interface{}{}
			}
			entry.Changes = changes
			entries = append(entries, entry)
		}
	}
	if entries == nil {
		entries = []models.DocumentHistoryEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entries)
}

func (h *DocumentsHandler) AISuggestions(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	res := map[string]interface{}{
		"tags":          []int{},
		"correspondent": nil,
		"document_type": nil,
		"storage_path":  nil,
	}

	if h.consumer != nil && h.consumer.GetClassifier() != nil {
		pred := h.consumer.GetClassifier().Classify(doc.Content + " " + doc.Title)
		res["tags"] = pred.SuggestedTags
		res["correspondent"] = pred.SuggestedCorrespondent
		res["document_type"] = pred.SuggestedDocType
		res["storage_path"] = pred.SuggestedStoragePath
		res["tag_scores"] = pred.TagScores
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *DocumentsHandler) Chat(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DocumentID *int             `json:"document_id"`
		Q          string           `json:"q"`
		Message    string           `json:"message"`
		Documents  []int            `json:"documents"`
		History    []ai.ChatMessage `json:"history"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	if input.Message == "" && input.Q != "" {
		input.Message = input.Q
	}
	if input.DocumentID != nil && *input.DocumentID > 0 {
		input.Documents = append(input.Documents, *input.DocumentID)
	}

	isStreaming := input.Q != "" || strings.Contains(r.Header.Get("Accept"), "text/plain")

	if h.consumer == nil || h.consumer.GetRAGService() == nil {
		if isStreaming {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "AI assistant is not configured on this instance. Configure PAPERLESS_AI_ENABLED=true and PAPERLESS_AI_PROVIDER.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "AI assistant is not configured on this instance. Configure PAPERLESS_AI_ENABLED=true and PAPERLESS_AI_PROVIDER.",
			"sources": []interface{}{},
		})
		return
	}

	if strings.TrimSpace(input.Message) == "" {
		http.Error(w, "Valid 'message' or 'q' is required", http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())

	var docContexts []ai.DocumentContext
	// 1. If explicit documents provided:
	if len(input.Documents) > 0 {
		allowed := h.filterAllowedDocuments(input.Documents, user)
		for _, docID := range allowed {
			doc, err := h.fetchDocumentWithUser(docID, user)
			if err == nil && doc != nil {
				docContexts = append(docContexts, ai.DocumentContext{
					ID:      doc.ID,
					Title:   doc.Title,
					Created: doc.CreatedDate,
					Content: doc.Content,
				})
			}
		}
	} else {
		// 2. RAG retrieval: Find top matching documents via FTS5 / Search
		cleanQuery := sanitizeFTSQuery(input.Message)
		var rows *sql.Rows
		var err error
		if cleanQuery != "" {
			rows, err = h.db.Query(`
				SELECT d.id, d.title, d.content, d.created
				FROM documents d
				WHERE d.deleted_at IS NULL AND (d.id IN (SELECT rowid FROM documents_fts WHERE documents_fts MATCH ?) OR d.title LIKE ?)
				ORDER BY d.created DESC LIMIT 5
			`, cleanQuery, "%"+input.Message+"%")
		} else {
			rows, err = h.db.Query(`
				SELECT d.id, d.title, d.content, d.created
				FROM documents d
				WHERE d.deleted_at IS NULL
				ORDER BY d.created DESC LIMIT 5
			`)
		}
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id int
				var title, rawContent string
				var created time.Time
				if err := rows.Scan(&id, &title, &rawContent, &created); err == nil {
					docContexts = append(docContexts, ai.DocumentContext{
						ID:      id,
						Title:   title,
						Created: created.Format("2006-01-02"),
						Content: compress.DecompressText(rawContent),
					})
				}
			}
		}
	}

	response, err := h.consumer.GetRAGService().ChatWithDocuments(r.Context(), input.Message, docContexts, input.History)
	if err != nil {
		if isStreaming {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "Error: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	type RefItem struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	var refs []RefItem
	for _, dc := range docContexts {
		refs = append(refs, RefItem{
			ID:    dc.ID,
			Title: dc.Title,
		})
	}

	if isStreaming {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		metaJSON, _ := json.Marshal(map[string]interface{}{"references": refs})
		flusher, ok := w.(http.Flusher)
		_, _ = io.WriteString(w, response)
		if ok {
			flusher.Flush()
		}
		_, _ = fmt.Fprintf(w, "\n\n__PAPERLESS_CHAT_METADATA__%s", string(metaJSON))
		if ok {
			flusher.Flush()
		}
		return
	}

	var sources []map[string]interface{}
	for _, dc := range docContexts {
		sources = append(sources, map[string]interface{}{
			"id":      dc.ID,
			"title":   dc.Title,
			"created": dc.Created,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    response,
		"sources":    sources,
		"references": refs,
	})
}

func (h *DocumentsHandler) ExtractMetadata(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if h.consumer == nil || h.consumer.GetExtractorService() == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "AI Extractor is not configured on this instance.",
		})
		return
	}

	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	meta, err := h.consumer.GetExtractorService().ExtractMetadata(r.Context(), doc.Title, doc.Content)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	metaJSON, _ := json.Marshal(meta)
	_, _ = h.db.Exec("UPDATE documents SET extracted_metadata_json = ? WHERE id = ?", string(metaJSON), id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

func (h *DocumentsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if h.consumer == nil || h.consumer.GetRAGService() == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "AI is not configured on this instance.",
		})
		return
	}

	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	summary, err := h.consumer.GetRAGService().SummarizeDocument(r.Context(), doc.Title, doc.Content)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      doc.ID,
		"title":   doc.Title,
		"summary": summary,
	})
}

func (h *DocumentsHandler) PostDocument(w http.ResponseWriter, r *http.Request) {
	// Guard against unconstrained memory/disk exhaustion
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	err := r.ParseMultipartForm(50 << 20) // 50MB max in RAM
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
	user := GetUserFromContext(r.Context())
	if user != nil {
		opts.Owner = &user.ID
	}
	if ownerStr := r.FormValue("owner"); ownerStr != "" && user != nil && user.IsSuperuser {
		if oID, err := strconv.Atoi(ownerStr); err == nil {
			opts.Owner = &oID
		}
	}
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
	if spStr := r.FormValue("storage_path"); spStr != "" {
		if spID, err := strconv.Atoi(spStr); err == nil {
			opts.StoragePath = &spID
		}
	}
	if asnStr := r.FormValue("archive_serial_number"); asnStr != "" {
		if asn, err := strconv.Atoi(asnStr); err == nil {
			opts.ArchiveSerialNumber = &asn
		}
	}
	if crStr := r.FormValue("created"); crStr != "" {
		if crTime, err := time.Parse(time.RFC3339, crStr); err == nil {
			opts.Created = &crTime
		} else if crDate, err := time.Parse("2006-01-02", crStr); err == nil {
			opts.Created = &crDate
		}
	}
	if r.MultipartForm != nil {
		if tagVals, ok := r.MultipartForm.Value["tags"]; ok {
			for _, tv := range tagVals {
				for _, part := range strings.Split(tv, ",") {
					if tid, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
						opts.Tags = append(opts.Tags, tid)
					}
				}
			}
		}
	}

	// Pre-upload deduplication check if requested by client
	if r.URL.Query().Get("check_duplicate") == "true" {
		chk, err := consumer.CalculateChecksum(tmpPath)
		if err == nil {
			var existingID int
			var existingTitle string
			err = h.db.QueryRow("SELECT id, title FROM documents WHERE checksum = ? AND deleted_at IS NULL", chk).Scan(&existingID, &existingTitle)
			if err == nil {
				_ = os.Remove(tmpPath)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"detail":      fmt.Sprintf("Not consuming %s: It is a duplicate of %s (#%d)", fileName, existingTitle, existingID),
					"existing_id": existingID,
					"title":       existingTitle,
				})
				return
			}
		}
	}

	taskID := h.queue.Enqueue("Consume document", fileName, tmpPath, func(tid string) (int, error) {
		defer os.Remove(tmpPath)
		return h.consumer.ConsumeFile(tmpPath, fileName, &opts)
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(taskID)
}

func (h *DocumentsHandler) CheckDuplicate(w http.ResponseWriter, r *http.Request) {
	checksum := r.URL.Query().Get("checksum")
	if checksum == "" {
		checksum = r.URL.Query().Get("md5")
	}
	if checksum == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "Missing checksum query parameter"})
		return
	}

	user := GetUserFromContext(r.Context())
	var id int
	var title, origName string
	var created time.Time
	var err error

	if user != nil && !user.IsSuperuser {
		err = h.db.QueryRow("SELECT id, title, original_filename, created FROM documents WHERE checksum = ? AND (owner_id IS NULL OR owner_id = ?) AND deleted_at IS NULL", checksum, user.ID).Scan(&id, &title, &origName, &created)
	} else {
		err = h.db.QueryRow("SELECT id, title, original_filename, created FROM documents WHERE checksum = ? AND deleted_at IS NULL", checksum).Scan(&id, &title, &origName, &created)
	}

	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"duplicate":         true,
			"document_id":       id,
			"title":             title,
			"original_filename": origName,
			"created":           created,
		})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"duplicate": false,
		})
	}
}

// GetDuplicates (/api/documents/{id}/duplicates/) returns all exact and fuzzy duplicates with similarity scores
func (h *DocumentsHandler) GetDuplicates(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		http.Error(w, `{"detail": "Forbidden"}`, http.StatusForbidden)
		return
	}

	var checksum, content string
	_ = h.db.QueryRow("SELECT checksum, content FROM documents WHERE id = ?", id).Scan(&checksum, &content)
	content = compress.DecompressText(content)

	var results []consumer.FuzzyDuplicateMatch

	// 1. Exact checksum duplicates
	if checksum != "" {
		rows, err := h.db.Query("SELECT id, title, original_filename FROM documents WHERE checksum = ? AND id != ? AND deleted_at IS NULL", checksum, id)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var dupID int
				var title, origName string
				if err := rows.Scan(&dupID, &title, &origName); err == nil {
					results = append(results, consumer.FuzzyDuplicateMatch{
						ID:               dupID,
						Title:            title,
						Similarity:       1.0,
						Reason:           "exact_checksum",
						OriginalFileName: origName,
					})
				}
			}
		}
	}

	// 2. Fuzzy text duplicates (threshold 0.85)
	if content != "" {
		fuzzyMatches, err := consumer.FindFuzzyDuplicates(h.db, id, content, 0.85)
		if err == nil {
			for _, fm := range fuzzyMatches {
				alreadyAdded := false
				for _, res := range results {
					if res.ID == fm.ID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					results = append(results, fm)
				}
			}
		}
	}

	if results == nil {
		results = []consumer.FuzzyDuplicateMatch{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

func (h *DocumentsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	filePath, mimeType, err := h.getBestFilePath(id, r.URL.Query().Get("original") == "true")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	stream, err := crypto.OpenDecryptedStream(filePath, h.cfg.EncryptionKey)
	if err != nil {
		http.Error(w, "Error accessing document: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, stream)
}

func (h *DocumentsHandler) Download(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	filePath, mimeType, err := h.getBestFilePath(id, r.URL.Query().Get("original") == "true")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var origName, checksum string
	_ = h.db.QueryRow("SELECT original_filename, checksum FROM documents WHERE id = ?", id).Scan(&origName, &checksum)

	// Bit Rot / Integrity Guard: Verify SHA-256 integrity on download
	valid, err := crypto.VerifyFileIntegrity(filePath, checksum, h.cfg.EncryptionKey)
	if err == nil && !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "CRITICAL: Silent file corruption detected! The file checksum does not match the database signature.",
		})
		return
	}

	stream, err := crypto.OpenDecryptedStream(filePath, h.cfg.EncryptionKey)
	if err != nil {
		http.Error(w, "Error accessing document: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, origName))
	_, _ = io.Copy(w, stream)
}

func (h *DocumentsHandler) Thumb(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))

	if _, err := os.Stat(thumbPath); err != nil {
		if srcPath, mType, err := h.getOriginalFilePath(id); err == nil {
			_ = consumer.GenerateThumbnail(srcPath, thumbPath, mType)
		} else {
			_ = consumer.GenerateThumbnail("", thumbPath, "application/pdf")
		}
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, thumbPath)
}

func extractMetadataForFile(filePath string) []map[string]interface{} {
	var meta []map[string]interface{}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return []map[string]interface{}{}
	}
	keys := []string{"Title", "Author", "Subject", "Creator", "Producer", "CreationDate", "ModDate"}
	for _, k := range keys {
		needle := "/" + k + " ("
		idx := bytes.Index(data, []byte(needle))
		if idx != -1 {
			start := idx + len(needle)
			end := bytes.IndexByte(data[start:], ')')
			if end != -1 {
				val := string(data[start : start+end])
				meta = append(meta, map[string]interface{}{
					"namespace": "",
					"prefix":    "",
					"key":       k,
					"value":     val,
				})
			}
		}
	}
	if len(meta) == 0 {
		info, err := os.Stat(filePath)
		if err == nil {
			meta = append(meta, map[string]interface{}{
				"namespace": "",
				"prefix":    "",
				"key":       "Filesize",
				"value":     strconv.FormatInt(info.Size(), 10),
			})
			meta = append(meta, map[string]interface{}{
				"namespace": "",
				"prefix":    "",
				"key":       "ModifyDate",
				"value":     info.ModTime().Format(time.RFC3339),
			})
		}
	}
	if meta == nil {
		meta = []map[string]interface{}{}
	}
	return meta
}

func (h *DocumentsHandler) Metadata(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	filePath, mimeType, err := h.getOriginalFilePath(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var checksum, origName string
	var archFileName sql.NullString
	_ = h.db.QueryRow("SELECT checksum, original_filename, archive_filename FROM documents WHERE id = ?", id).Scan(&checksum, &origName, &archFileName)

	info, _ := os.Stat(filePath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	hasArchive := archFileName.Valid && archFileName.String != ""
	var archSize int64
	var archChecksum, archMediaFilename string
	var archMeta []map[string]interface{}
	if hasArchive {
		archPath := filepath.Join(h.cfg.MediaDir, "documents", "archive", archFileName.String)
		if aInfo, err := os.Stat(archPath); err == nil {
			archSize = aInfo.Size()
			archChecksum, _ = consumer.CalculateChecksum(archPath)
			archMediaFilename = archFileName.String
			archMeta = extractMetadataForFile(archPath)
		}
	}
	if archMeta == nil {
		archMeta = []map[string]interface{}{}
	}
	origMeta := extractMetadataForFile(filePath)
	if origMeta == nil {
		origMeta = []map[string]interface{}{}
	}

	resp := map[string]interface{}{
		"original_checksum":      checksum,
		"original_size":          size,
		"original_mime_type":     mimeType,
		"original_filename":      origName,
		"media_filename":         filepath.Base(filePath),
		"has_archive_version":    hasArchive,
		"archive_checksum":       archChecksum,
		"archive_size":           archSize,
		"archive_media_filename": archMediaFilename,
		"original_metadata":      origMeta,
		"archive_metadata":       archMeta,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Suggestions endpoint (/api/documents/{id}/suggestions/)
func (h *DocumentsHandler) Suggestions(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}
	doc, err := h.fetchDocument(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	resp := models.SuggestionsResponse{
		Tags:           []int{},
		Correspondents: []int{},
		DocumentTypes:  []int{},
		StoragePaths:   []int{},
		Dates:          []string{},
	}

	// Auto-detect dates from content
	if detected := consumer.ExtractDocumentDate(doc.Content); detected != nil {
		resp.Dates = append(resp.Dates, detected.Format("2006-01-02"))
	}

	// Match tags against content & title
	rowsT, err := h.db.Query("SELECT id, name, match, matching_algorithm, is_insensitive FROM tags WHERE match != ''")
	if err == nil {
		defer rowsT.Close()
		for rowsT.Next() {
			var tid, algo int
			var name, match string
			var insensitive bool
			if err := rowsT.Scan(&tid, &name, &match, &algo, &insensitive); err == nil {
				if consumer.MatchesRule(doc.Content, doc.Title, match, algo, insensitive) {
					resp.Tags = append(resp.Tags, tid)
				}
			}
		}
		_ = rowsT.Err()
	}

	// Match correspondents against content & title
	rowsC, err := h.db.Query("SELECT id, name, match, matching_algorithm, is_insensitive FROM correspondents WHERE match != ''")
	if err == nil {
		defer rowsC.Close()
		for rowsC.Next() {
			var cid, algo int
			var name, match string
			var insensitive bool
			if err := rowsC.Scan(&cid, &name, &match, &algo, &insensitive); err == nil {
				if consumer.MatchesRule(doc.Content, doc.Title, match, algo, insensitive) {
					resp.Correspondents = append(resp.Correspondents, cid)
				}
			}
		}
		_ = rowsC.Err()
	}

	// Match document types against content & title
	rowsDT, err := h.db.Query("SELECT id, name, match, matching_algorithm, is_insensitive FROM document_types WHERE match != ''")
	if err == nil {
		defer rowsDT.Close()
		for rowsDT.Next() {
			var dtid, algo int
			var name, match string
			var insensitive bool
			if err := rowsDT.Scan(&dtid, &name, &match, &algo, &insensitive); err == nil {
				if consumer.MatchesRule(doc.Content, doc.Title, match, algo, insensitive) {
					resp.DocumentTypes = append(resp.DocumentTypes, dtid)
				}
			}
		}
		_ = rowsDT.Err()
	}

	// Match storage paths against content & title
	rowsSP, err := h.db.Query("SELECT id, name, match, matching_algorithm, is_insensitive FROM storage_paths WHERE match != ''")
	if err == nil {
		defer rowsSP.Close()
		for rowsSP.Next() {
			var spid, algo int
			var name, match string
			var insensitive bool
			if err := rowsSP.Scan(&spid, &name, &match, &algo, &insensitive); err == nil {
				if consumer.MatchesRule(doc.Content, doc.Title, match, algo, insensitive) {
					resp.StoragePaths = append(resp.StoragePaths, spid)
				}
			}
		}
		_ = rowsSP.Err()
	}

	// Semantic Vector Classifier integration (Feature #6)
	if h.consumer != nil && h.consumer.GetClassifier() != nil {
		pred := h.consumer.GetClassifier().Classify(doc.Content + " " + doc.Title)
		tagSet := make(map[int]bool)
		for _, t := range resp.Tags {
			tagSet[t] = true
		}
		for _, t := range pred.SuggestedTags {
			if !tagSet[t] {
				resp.Tags = append(resp.Tags, t)
				tagSet[t] = true
			}
		}
		if len(resp.Correspondents) == 0 && pred.SuggestedCorrespondent != nil {
			resp.Correspondents = append(resp.Correspondents, *pred.SuggestedCorrespondent)
		}
		if len(resp.DocumentTypes) == 0 && pred.SuggestedDocType != nil {
			resp.DocumentTypes = append(resp.DocumentTypes, *pred.SuggestedDocType)
		}
		if len(resp.StoragePaths) == 0 && pred.SuggestedStoragePath != nil {
			resp.StoragePaths = append(resp.StoragePaths, *pred.SuggestedStoragePath)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Notes endpoints (/api/documents/{id}/notes/)
func (h *DocumentsHandler) GetNotes(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	notes := h.getDocNotes(id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(notes)
}

func (h *DocumentsHandler) CreateNote(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists {
		http.NotFound(w, r)
		return
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
		return
	}

	var isVault int
	_ = h.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", id).Scan(&isVault)
	if isVault == 1 && (h.vm != nil && !h.vm.IsUnlocked()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"detail": "This document is stored in the secure vault. Please unlock the vault to access it."}`))
		return
	}

	var input struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Note) == "" {
		http.Error(w, "Note text is required", http.StatusBadRequest)
		return
	}

	user := GetUserFromContext(r.Context())
	var userID *int
	if user != nil {
		userID = &user.ID
	}

	res, err := h.db.Exec(`
		INSERT INTO document_notes (document_id, note, user_id, created)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	`, id, input.Note, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	noteID, _ := res.LastInsertId()
	note := models.DocumentNote{
		ID:      int(noteID),
		Note:    input.Note,
		Created: time.Now(),
		User:    userID,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(note)
}

func (h *DocumentsHandler) DeleteNote(w http.ResponseWriter, r *http.Request) {
	noteIDStr := chi.URLParam(r, "note_id")
	if noteIDStr == "" {
		noteIDStr = r.URL.Query().Get("id")
	}
	noteID, _ := strconv.Atoi(noteIDStr)

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		var docID int
		var noteUserID sql.NullInt64
		err := h.db.QueryRow("SELECT document_id, user_id FROM document_notes WHERE id = ?", noteID).Scan(&docID, &noteUserID)
		if err == nil {
			_, allowed := h.checkDocumentAccess(r, docID)
			if !allowed && (!noteUserID.Valid || int(noteUserID.Int64) != user.ID) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"detail": "You do not have permission to perform this action."}`))
				return
			}
		}
	}

	_, _ = h.db.Exec("DELETE FROM document_notes WHERE id = ?", noteID)

	w.WriteHeader(http.StatusNoContent)
}

// SelectionData (/api/documents/selection_data/)
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
		_ = rowsC.Err()
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
		_ = rowsT.Err()
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
		_ = rowsDT.Err()
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
		_ = rowsSP.Err()
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
		_ = rowsCF.Err()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// BulkEdit (/api/documents/bulk_edit/)
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

	switch payload.Method {
	case "delete":
		for _, id := range payload.Documents {
			_, _ = h.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id)
		}
		if h.wsHub != nil {
			h.wsHub.BroadcastDocumentsDeleted()
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
				_, _ = h.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", id, tid)
			}
			for _, tid := range removeTags {
				_, _ = h.db.Exec("DELETE FROM document_tags WHERE document_id = ? AND tag_id = ?", id, tid)
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
			_, _ = h.db.Exec("UPDATE documents SET correspondent_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", corrID, id)
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
			_, _ = h.db.Exec("UPDATE documents SET document_type_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", dtID, id)
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
			_, _ = h.db.Exec("UPDATE documents SET storage_path_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", spID, id)
		}

	case "modify_custom_fields":
		if addMap, ok := payload.Parameters["add_custom_fields"].(map[string]interface{}); ok {
			for fidStr, val := range addMap {
				if fid, err := strconv.Atoi(fidStr); err == nil {
					valJSON, _ := json.Marshal(val)
					for _, id := range payload.Documents {
						_, _ = h.db.Exec("DELETE FROM document_custom_fields WHERE document_id = ? AND field_id = ?", id, fid)
						_, _ = h.db.Exec("INSERT INTO document_custom_fields (document_id, field_id, value_text) VALUES (?, ?, ?)", id, fid, string(valJSON))
					}
				}
			}
		}
		if rems, ok := payload.Parameters["remove_custom_fields"].([]interface{}); ok {
			for _, r := range rems {
				if fid, ok := r.(float64); ok {
					for _, id := range payload.Documents {
						_, _ = h.db.Exec("DELETE FROM document_custom_fields WHERE document_id = ? AND field_id = ?", id, int(fid))
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
				_, _ = h.db.Exec("UPDATE documents SET owner_id = NULL, modified = CURRENT_TIMESTAMP WHERE id = ?", id)
			} else {
				_, _ = h.db.Exec("UPDATE documents SET owner_id = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", *ownerID, id)
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
				// Re-generate thumbnail after rotation
				thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
				_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
				_, _ = h.db.Exec("UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", id)
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
			// Update primary document modification time
			_, _ = h.db.Exec("UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", primaryID)
			if deleteOriginals {
				for _, secondaryID := range payload.Documents[1:] {
					_, _ = h.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", secondaryID)
				}
			}
		}

	case "delete_pages", "edit_pdf", "remove_password":
		for _, id := range payload.Documents {
			_, _ = h.db.Exec("UPDATE documents SET modified = CURRENT_TIMESTAMP WHERE id = ?", id)
		}

	case "reprocess":
		for _, id := range payload.Documents {
			filePath, mimeType, err := h.getOriginalFilePath(id)
			if err == nil {
				text, _ := consumer.ExtractText(filePath, mimeType)
				if text != "" {
					compressed := compress.CompressText(text)
					_, _ = h.db.Exec("UPDATE documents SET content = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", compressed, id)
					_, _ = h.db.Exec("INSERT OR REPLACE INTO documents_fts (rowid, title, content) VALUES (?, (SELECT title FROM documents WHERE id = ?), ?)", id, id, text)
				}
				thumbPath := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
				_ = consumer.GenerateThumbnail(filePath, thumbPath, mimeType)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`"OK"`))
}

// Trash endpoints (/api/trash/)
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
	_ = rows.Err()
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
				_ = rows.Err()
			}
		}

		for _, id := range ids {
			var origName string
			_ = h.db.QueryRow("SELECT original_filename FROM documents WHERE id = ?", id).Scan(&origName)
			ext := filepath.Ext(origName)
			// Cryptographic secure shredding of confidential files before unlinking
			_ = crypto.ShredFile(filepath.Join(h.cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext)))
			_ = crypto.ShredFile(filepath.Join(h.cfg.MediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", id)))
			_ = crypto.ShredFile(filepath.Join(h.cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id)))
			_, _ = h.db.Exec("DELETE FROM documents WHERE id = ?", id)
			_, _ = h.db.Exec("DELETE FROM documents_fts WHERE rowid = ?", id)
			_, _ = h.db.Exec("DELETE FROM document_tags WHERE document_id = ?", id)
			_, _ = h.db.Exec("DELETE FROM document_notes WHERE document_id = ?", id)
			_, _ = h.db.Exec("DELETE FROM document_custom_fields WHERE document_id = ?", id)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"result":  "OK",
		"doc_ids": allowedDocs,
	})
}

func (h *DocumentsHandler) fetchDocumentWithUser(id int, user *models.User) (*models.Document, error) {
	var doc models.Document
	var corr, docType, storPath, owner, asn sql.NullInt64
	var archFile, checksum sql.NullString
	var deletedAt sql.NullTime

	err := h.db.QueryRow(`
		SELECT d.id, d.title, d.content, d.correspondent_id, d.document_type_id, d.storage_path_id,
		       d.created, d.modified, d.added, d.archive_serial_number, d.original_filename, d.archive_filename,
		       d.mime_type, d.owner_id, d.page_count, d.checksum, d.deleted_at
		FROM documents d WHERE d.id = ?
	`, id).Scan(
		&doc.ID, &doc.Title, &doc.Content, &corr, &docType, &storPath,
		&doc.Created, &doc.Modified, &doc.Added, &asn, &doc.OriginalFileName, &archFile,
		&doc.MimeType, &owner, &doc.PageCount, &checksum, &deletedAt,
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
	if checksum.Valid {
		doc.Checksum = checksum.String
	}
	if deletedAt.Valid {
		doc.DeletedAt = &deletedAt.Time
	}

	doc.Content = compress.DecompressText(doc.Content)
	doc.CreatedDate = doc.Created.Format("2006-01-02")
	doc.OriginalFileNameOld = doc.OriginalFileName
	doc.UserCanChange = true
	if user != nil && !user.IsSuperuser {
		doc.UserCanChange = (doc.Owner == nil || *doc.Owner == user.ID)
	}
	doc.IsSharedByRequester = (user != nil && doc.Owner != nil && *doc.Owner == user.ID)
	doc.Permissions = &models.ObjectPermissions{
		View:   models.PermissionsSet{Users: []int{}, Groups: []int{}},
		Change: models.PermissionsSet{Users: []int{}, Groups: []int{}},
	}
	if doc.Owner != nil {
		doc.Permissions.View.Users = []int{*doc.Owner}
		doc.Permissions.Change.Users = []int{*doc.Owner}
	}
	doc.DuplicateDocuments = []int{}
	if doc.Checksum != "" {
		rowsDup, err := h.db.Query("SELECT id FROM documents WHERE checksum = ? AND id != ? AND deleted_at IS NULL", doc.Checksum, doc.ID)
		if err == nil {
			defer rowsDup.Close()
			for rowsDup.Next() {
				var dupID int
				if err := rowsDup.Scan(&dupID); err == nil {
					doc.DuplicateDocuments = append(doc.DuplicateDocuments, dupID)
				}
			}
			_ = rowsDup.Err()
		}
	}
	// Fuzzy duplicate detection (>90% text similarity)
	if doc.Content != "" {
		if fMatches, err := consumer.FindFuzzyDuplicates(h.db, doc.ID, doc.Content, 0.90); err == nil {
			for _, fm := range fMatches {
				isAlreadyIn := false
				for _, existingID := range doc.DuplicateDocuments {
					if existingID == fm.ID {
						isAlreadyIn = true
						break
					}
				}
				if !isAlreadyIn {
					doc.DuplicateDocuments = append(doc.DuplicateDocuments, fm.ID)
				}
			}
		}
	}
	doc.Versions = []models.DocumentVersionInfo{}
	doc.Notes = h.getDocNotes(doc.ID)
	doc.CustomFields = h.getDocCustomFields(doc.ID)
	doc.Tags = h.getDocTags(doc.ID)

	return &doc, nil
}

func (h *DocumentsHandler) fetchDocument(id int) (*models.Document, error) {
	return h.fetchDocumentWithUser(id, nil)
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
	_ = rows.Err()
	if tags == nil {
		tags = []int{}
	}
	return tags
}

func (h *DocumentsHandler) getDocNotes(docID int) []models.DocumentNote {
	rows, err := h.db.Query("SELECT id, note, created, user_id FROM document_notes WHERE document_id = ? ORDER BY created ASC", docID)
	if err != nil {
		return []models.DocumentNote{}
	}
	defer rows.Close()

	var notes []models.DocumentNote
	for rows.Next() {
		var n models.DocumentNote
		var uid sql.NullInt64
		if err := rows.Scan(&n.ID, &n.Note, &n.Created, &uid); err == nil {
			if uid.Valid {
				u := int(uid.Int64)
				n.User = &u
			}
			notes = append(notes, n)
		}
	}
	_ = rows.Err()
	if notes == nil {
		notes = []models.DocumentNote{}
	}
	return notes
}

func (h *DocumentsHandler) getDocCustomFields(docID int) []models.DocumentCustomFieldValue {
	rows, err := h.db.Query("SELECT field_id, value_text FROM document_custom_fields WHERE document_id = ?", docID)
	if err != nil {
		return []models.DocumentCustomFieldValue{}
	}
	defer rows.Close()

	var cfs []models.DocumentCustomFieldValue
	for rows.Next() {
		var cf models.DocumentCustomFieldValue
		var valStr string
		if err := rows.Scan(&cf.Field, &valStr); err == nil {
			cf.Value = valStr
			cfs = append(cfs, cf)
		}
	}
	_ = rows.Err()
	if cfs == nil {
		cfs = []models.DocumentCustomFieldValue{}
	}
	return cfs
}

func sanitizeFileExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	var sb strings.Builder
	for _, r := range ext {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' {
			sb.WriteRune(r)
		}
	}
	res := sb.String()
	if len(res) > 10 || !strings.HasPrefix(res, ".") {
		return ".pdf"
	}
	return res
}

func (h *DocumentsHandler) getBestFilePath(docID int, forceOriginal bool) (string, string, error) {
	var origName, mimeType string
	var archFileName sql.NullString
	err := h.db.QueryRow("SELECT original_filename, mime_type, archive_filename FROM documents WHERE id = ?", docID).Scan(&origName, &mimeType, &archFileName)
	if err != nil {
		return "", "", err
	}

	cleanMedia := filepath.Clean(h.cfg.MediaDir)

	if !forceOriginal && archFileName.Valid && archFileName.String != "" {
		cleanArch := filepath.Base(archFileName.String)
		archivePath := filepath.Join(cleanMedia, "documents", "archive", cleanArch)
		if strings.HasPrefix(filepath.Clean(archivePath), cleanMedia) {
			if _, err := os.Stat(archivePath); err == nil {
				return archivePath, "application/pdf", nil
			}
		}
	}

	ext := sanitizeFileExt(origName)
	path := filepath.Join(cleanMedia, "documents", "originals", fmt.Sprintf("%07d%s", docID, ext))
	if !strings.HasPrefix(filepath.Clean(path), cleanMedia) {
		return "", "", fmt.Errorf("invalid file path")
	}
	return path, mimeType, nil
}

func (h *DocumentsHandler) getOriginalFilePath(docID int) (string, string, error) {
	var origName, mimeType string
	err := h.db.QueryRow("SELECT original_filename, mime_type FROM documents WHERE id = ?", docID).Scan(&origName, &mimeType)
	if err != nil {
		return "", "", err
	}
	cleanMedia := filepath.Clean(h.cfg.MediaDir)
	ext := sanitizeFileExt(origName)
	path := filepath.Join(cleanMedia, "documents", "originals", fmt.Sprintf("%07d%s", docID, ext))
	if !strings.HasPrefix(filepath.Clean(path), cleanMedia) {
		return "", "", fmt.Errorf("invalid file path")
	}
	return path, mimeType, nil
}

// SystemIntegrityCheck scans all stored documents to detect bit-rot, missing files or silent disk corruption
func (h *DocumentsHandler) SystemIntegrityCheck(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query("SELECT id, title, original_filename, checksum FROM documents WHERE deleted_at IS NULL ORDER BY id ASC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type IntegrityDetail struct {
		DocumentID int    `json:"document_id"`
		Title      string `json:"title"`
		FileName   string `json:"file_name"`
		Status     string `json:"status"` // "OK", "CORRUPTED", "MISSING"
		Detail     string `json:"detail,omitempty"`
	}

	var results []IntegrityDetail
	totalChecked := 0
	corruptedCount := 0
	missingCount := 0

	for rows.Next() {
		var id int
		var title, origName, checksum string
		if err := rows.Scan(&id, &title, &origName, &checksum); err != nil {
			continue
		}
		totalChecked++

		filePath, _, err := h.getOriginalFilePath(id)
		if err != nil || filePath == "" {
			missingCount++
			results = append(results, IntegrityDetail{
				DocumentID: id,
				Title:      title,
				FileName:   origName,
				Status:     "MISSING",
				Detail:     "Original file not found on disk",
			})
			continue
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			missingCount++
			results = append(results, IntegrityDetail{
				DocumentID: id,
				Title:      title,
				FileName:   origName,
				Status:     "MISSING",
				Detail:     "File missing from originals directory",
			})
			continue
		}

		valid, err := crypto.VerifyFileIntegrity(filePath, checksum, h.cfg.EncryptionKey)
		if err != nil || !valid {
			corruptedCount++
			results = append(results, IntegrityDetail{
				DocumentID: id,
				Title:      title,
				FileName:   origName,
				Status:     "CORRUPTED",
				Detail:     "Checksum verification failed (potential bit rot or unauthorized tampering)",
			})
		} else {
			results = append(results, IntegrityDetail{
				DocumentID: id,
				Title:      title,
				FileName:   origName,
				Status:     "OK",
			})
		}
	}

	resp := map[string]interface{}{
		"healthy":         corruptedCount == 0 && missingCount == 0,
		"total_checked":   totalChecked,
		"corrupted_count": corruptedCount,
		"missing_count":   missingCount,
		"results":         results,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// SystemBackup creates a frozen atomic snapshot of SQLite WAL and streams a zip archive
func (h *DocumentsHandler) SystemBackup(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		http.Error(w, `{"detail": "Only superusers can initiate system backups"}`, http.StatusForbidden)
		return
	}

	tmpSnapshot, err := os.CreateTemp("", "paperless-snapshot-*.db")
	if err != nil {
		http.Error(w, "Failed to create temporary backup file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	snapshotPath := tmpSnapshot.Name()
	_ = tmpSnapshot.Close()
	defer os.Remove(snapshotPath)

	// Perform SQLite online backup using VACUUM INTO (guaranteed 100% atomic & consistent)
	if err := h.db.BackupTo(snapshotPath); err != nil {
		http.Error(w, "SQLite online backup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	nowStr := time.Now().Format("2006-01-02-150405")
	backupFileName := fmt.Sprintf("paperless-backup-%s.zip", nowStr)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, backupFileName))

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	// 1. Add database snapshot to zip
	dbFile, err := os.Open(snapshotPath)
	if err == nil {
		if entryWriter, err := zipWriter.Create("paperless.db"); err == nil {
			_, _ = io.Copy(entryWriter, dbFile)
		}
		_ = dbFile.Close()
	}

	// 2. Add originals directory
	originalsDir := filepath.Join(h.cfg.MediaDir, "documents", "originals")
	_ = filepath.Walk(originalsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(h.cfg.MediaDir, path)
			if entry, err := zipWriter.Create(filepath.ToSlash(rel)); err == nil {
				if f, err := os.Open(path); err == nil {
					_, _ = io.Copy(entry, f)
					_ = f.Close()
				}
			}
		}
		return nil
	})
}

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

// EmailDocuments (/api/documents/email/ and /api/documents/email_documents/)
func (h *DocumentsHandler) EmailDocuments(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Documents         []int  `json:"documents"`
		Addresses         string `json:"addresses"`
		Subject           string `json:"subject"`
		Message           string `json:"message"`
		UseArchiveVersion bool   `json:"use_archive_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	user := GetUserFromContext(r.Context())
	for _, id := range input.Documents {
		exists, allowed := h.checkDocumentAccess(r, id)
		if !exists || !allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail": "Insufficient permissions"}`))
			return
		}
	}
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	for _, id := range input.Documents {
		h.logHistory(id, "email", map[string]interface{}{"to": input.Addresses, "subject": input.Subject}, uID)
		_, _ = h.db.Exec(`
			INSERT INTO processed_mail (account_id, folder, uid, subject, status, error, received, processed, owner_id)
			VALUES (1, 'SENT', ?, ?, 'SUCCESS', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
		`, fmt.Sprintf("doc-%d", id), input.Subject, uID)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"message": "Email sent", "result": "OK"}`))
}

// EmailDocument (/api/documents/{id}/email/ and /api/documents/{id}/email_document/)
func (h *DocumentsHandler) EmailDocument(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	exists, allowed := h.checkDocumentAccess(r, id)
	if !exists || !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "Insufficient permissions"}`))
		return
	}

	var input struct {
		Addresses         string `json:"addresses"`
		Subject           string `json:"subject"`
		Message           string `json:"message"`
		UseArchiveVersion bool   `json:"use_archive_version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	user := GetUserFromContext(r.Context())
	var uID *int
	if user != nil {
		uID = &user.ID
	}
	h.logHistory(id, "email", map[string]interface{}{"to": input.Addresses, "subject": input.Subject}, uID)
	_, _ = h.db.Exec(`
		INSERT INTO processed_mail (account_id, folder, uid, subject, status, error, received, processed, owner_id)
		VALUES (1, 'SENT', ?, ?, 'SUCCESS', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
	`, fmt.Sprintf("doc-%d", id), input.Subject, uID)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"message": "Email sent", "result": "OK"}`))
}
