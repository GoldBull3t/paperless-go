package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type ShareLinksHandler struct {
	db  *database.DB
	cfg *config.Config
}

func NewShareLinksHandler(db *database.DB, cfg *config.Config) *ShareLinksHandler {
	return &ShareLinksHandler{
		db:  db,
		cfg: cfg,
	}
}

// List returns a paginated list of share links (supports ?document={id}&ordering={field})
func (h *ShareLinksHandler) List(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	user := GetUserFromContext(r.Context())
	var conditions []string
	var args []interface{}

	// Multi-user object permissions
	if user != nil && !user.IsSuperuser {
		conditions = append(conditions, "(d.owner_id IS NULL OR d.owner_id = ?)")
		args = append(args, user.ID)
	}

	// Filter by document ID
	if docFilter := r.URL.Query().Get("document"); docFilter != "" {
		if docID, err := strconv.Atoi(docFilter); err == nil {
			conditions = append(conditions, "sl.document_id = ?")
			args = append(args, docID)
		}
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM share_links sl JOIN documents d ON sl.document_id = d.id %s", whereClause)
	var total int
	_ = h.db.QueryRow(countQuery, args...).Scan(&total)

	// Ordering
	ordering := r.URL.Query().Get("ordering")
	orderClause := "ORDER BY sl.created DESC"
	switch ordering {
	case "created":
		orderClause = "ORDER BY sl.created ASC"
	case "-created":
		orderClause = "ORDER BY sl.created DESC"
	case "expiration":
		orderClause = "ORDER BY sl.expiration ASC"
	case "-expiration":
		orderClause = "ORDER BY sl.expiration DESC"
	case "document__title":
		orderClause = "ORDER BY d.title ASC"
	case "-document__title":
		orderClause = "ORDER BY d.title DESC"
	}

	dataQuery := fmt.Sprintf(`
		SELECT sl.id, sl.created, sl.expiration, sl.slug, sl.document_id, d.title, sl.file_format
		FROM share_links sl
		JOIN documents d ON sl.document_id = d.id
		%s
		%s
		LIMIT ? OFFSET ?
	`, whereClause, orderClause)

	queryArgs := append(args, pageSize, offset)
	rows, err := h.db.Query(dataQuery, queryArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	results := make([]models.ShareLink, 0)
	allIDs := make([]int, 0)
	for rows.Next() {
		var sl models.ShareLink
		var exp *time.Time
		if err := rows.Scan(&sl.ID, &sl.Created, &exp, &sl.Slug, &sl.Document, &sl.DocumentTitle, &sl.FileFormat); err != nil {
			continue
		}
		sl.Expiration = exp
		sl.FileVersion = sl.FileFormat
		results = append(results, sl)
		allIDs = append(allIDs, sl.ID)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(models.PaginatedResponse{
		Count:    total,
		All:      allIDs,
		Results:  results,
		Next:     nil,
		Previous: nil,
	})
}

// GetForDocument returns all share links specifically for one document
func (h *ShareLinksHandler) GetForDocument(w http.ResponseWriter, r *http.Request) {
	docID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	user := GetUserFromContext(r.Context())
	var ownerID *int
	var title string
	err = h.db.QueryRow("SELECT owner_id, title FROM documents WHERE id = ? AND deleted_at IS NULL", docID).Scan(&ownerID, &title)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if user != nil && !user.IsSuperuser && ownerID != nil && *ownerID != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to access this document."}`))
		return
	}

	rows, err := h.db.Query(`
		SELECT id, created, expiration, slug, file_format
		FROM share_links
		WHERE document_id = ?
		ORDER BY created DESC
	`, docID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	links := make([]models.ShareLink, 0)
	for rows.Next() {
		var sl models.ShareLink
		var exp *time.Time
		if err := rows.Scan(&sl.ID, &sl.Created, &exp, &sl.Slug, &sl.FileFormat); err != nil {
			continue
		}
		sl.Document = docID
		sl.DocumentTitle = title
		sl.Expiration = exp
		sl.FileVersion = sl.FileFormat
		links = append(links, sl)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(links)
}

// Create creates a new share link for a document
func (h *ShareLinksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Document    int        `json:"document"`
		FileVersion string     `json:"file_version"`
		FileFormat  string     `json:"file_format"`
		Expiration  *time.Time `json:"expiration"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Document <= 0 {
		http.Error(w, `{"document": ["This field is required."]}`, http.StatusBadRequest)
		return
	}

	fileFormat := req.FileVersion
	if fileFormat == "" {
		fileFormat = req.FileFormat
	}
	if fileFormat == "" {
		fileFormat = "archive"
	}

	user := GetUserFromContext(r.Context())
	var ownerID *int
	var title string
	err := h.db.QueryRow("SELECT owner_id, title FROM documents WHERE id = ? AND deleted_at IS NULL", req.Document).Scan(&ownerID, &title)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string][]string{"document": {"Invalid document ID."}})
		return
	}
	if user != nil && !user.IsSuperuser && ownerID != nil && *ownerID != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to share this document."}`))
		return
	}

	// Generate secure 40-character hexadecimal slug
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	slug := hex.EncodeToString(b)

	res, err := h.db.Exec(`
		INSERT INTO share_links (document_id, slug, expiration, file_format)
		VALUES (?, ?, ?, ?)
	`, req.Document, slug, req.Expiration, fileFormat)
	if err != nil {
		http.Error(w, "Failed to create share link: "+err.Error(), http.StatusInternalServerError)
		return
	}

	linkID64, _ := res.LastInsertId()
	linkID := int(linkID64)

	var created time.Time
	_ = h.db.QueryRow("SELECT created FROM share_links WHERE id = ?", linkID).Scan(&created)

	sl := models.ShareLink{
		ID:            linkID,
		Created:       created,
		Expiration:    req.Expiration,
		Slug:          slug,
		Document:      req.Document,
		DocumentTitle: title,
		FileVersion:   fileFormat,
		FileFormat:    fileFormat,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sl)
}

// Get returns details for a single share link
func (h *ShareLinksHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var sl models.ShareLink
	var exp *time.Time
	var ownerID *int
	err = h.db.QueryRow(`
		SELECT sl.id, sl.created, sl.expiration, sl.slug, sl.document_id, d.title, sl.file_format, d.owner_id
		FROM share_links sl
		JOIN documents d ON sl.document_id = d.id
		WHERE sl.id = ?
	`, id).Scan(&sl.ID, &sl.Created, &exp, &sl.Slug, &sl.Document, &sl.DocumentTitle, &sl.FileFormat, &ownerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && ownerID != nil && *ownerID != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to access this share link."}`))
		return
	}

	sl.Expiration = exp
	sl.FileVersion = sl.FileFormat

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sl)
}

// Delete removes a share link
func (h *ShareLinksHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var ownerID *int
	err = h.db.QueryRow(`
		SELECT d.owner_id
		FROM share_links sl
		JOIN documents d ON sl.document_id = d.id
		WHERE sl.id = ?
	`, id).Scan(&ownerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser && ownerID != nil && *ownerID != user.ID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to delete this share link."}`))
		return
	}

	_, _ = h.db.Exec("DELETE FROM share_links WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func sanitizeShareExt(name string) string {
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

// PublicAccess streams the shared document or renders an elegant landing page for browser visits
func (h *ShareLinksHandler) PublicAccess(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		http.NotFound(w, r)
		return
	}

	var linkID, docID int
	var exp *time.Time
	var fileFormat, title, origName string
	var archName, mimeType *string

	err := h.db.QueryRow(`
		SELECT sl.id, sl.expiration, sl.file_format, d.id, d.title, d.original_filename, d.archive_filename, d.mime_type
		FROM share_links sl
		JOIN documents d ON sl.document_id = d.id
		WHERE sl.slug = ? AND d.deleted_at IS NULL
	`, slug).Scan(&linkID, &exp, &fileFormat, &docID, &title, &origName, &archName, &mimeType)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	isHtmlClient := strings.Contains(r.Header.Get("Accept"), "text/html")

	// Expiration check
	if exp != nil && time.Now().After(*exp) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		if isHtmlClient {
			htmlGone := `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Link Expired - Paperless-ngx</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" href="/static/bootstrap.min.css">
	<style>
		:root { --bg-app: #f4f6f8; --card-bg: #fff; --text-color: #212529; --border: #dee2e6; }
		@media (prefers-color-scheme: dark) { :root { --bg-app: #121315; --card-bg: #242529; --text-color: #e2e8f0; --border: #34373d; } }
		body { background: var(--bg-app); color: var(--text-color); display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; font-family: system-ui, sans-serif; }
		.card-box { background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px; padding: 2.5rem; text-align: center; max-width: 440px; box-shadow: 0 10px 25px rgba(0,0,0,0.1); }
	</style>
</head>
<body>
	<div class="card-box">
		<div style="font-size: 3rem; margin-bottom: 1rem;">&#8987;</div>
		<h2 class="h4 mb-2">Share Link Expired</h2>
		<p class="text-muted small mb-0">This document share link has expired and is no longer accessible. Please contact the owner for a new link.</p>
	</div>
</body>
</html>`
			_, _ = w.Write([]byte(htmlGone))
		} else {
			_, _ = w.Write([]byte("This share link has expired."))
		}
		return
	}

	cleanMedia := filepath.Clean(h.cfg.MediaDir)
	ext := sanitizeShareExt(origName)
	origPath := filepath.Join(cleanMedia, "documents", "originals", fmt.Sprintf("%07d%s", docID, ext))

	targetPath := origPath
	targetMime := "application/octet-stream"
	if mimeType != nil && *mimeType != "" {
		targetMime = *mimeType
	}
	downloadName := origName

	if (fileFormat == "archive" || strings.ToLower(fileFormat) == "pdf") && archName != nil && *archName != "" {
		archPath := filepath.Join(cleanMedia, "documents", "archive", fmt.Sprintf("%07d.pdf", docID))
		if strings.HasPrefix(filepath.Clean(archPath), cleanMedia) {
			if _, err := os.Stat(archPath); err == nil {
				targetPath = archPath
				targetMime = "application/pdf"
				downloadName = title + ".pdf"
			}
		}
	}

	if !strings.HasPrefix(filepath.Clean(targetPath), cleanMedia) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(targetPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	isDownload := r.URL.Query().Get("download") == "true" || r.URL.Query().Get("download") == "1"
	isRaw := r.URL.Query().Get("raw") == "true" || r.URL.Query().Get("raw") == "1"

	// If explicit download or raw preview is requested, or not a browser HTML request, stream file directly
	if isDownload || isRaw || !isHtmlClient {
		w.Header().Set("Content-Type", targetMime)
		if isDownload {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadName))
		} else {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, downloadName))
		}
		http.ServeFile(w, r, targetPath)
		return
	}

	// Render responsive document preview landing page
	fileSizeStr := formatFileSize(info.Size())
	expText := "No expiration"
	if exp != nil {
		expText = fmt.Sprintf("Expires %s", exp.Format("Jan 02, 2006"))
	}

	previewHtml := ""
	if targetMime == "application/pdf" {
		previewHtml = `<iframe src="?raw=1#toolbar=0" title="Document Preview" class="pdf-viewer"></iframe>`
	} else if strings.HasPrefix(targetMime, "image/") {
		previewHtml = `<div class="img-container"><img src="?raw=1" alt="Document Preview" class="img-fluid rounded shadow-sm"></div>`
	} else {
		previewHtml = fmt.Sprintf(`
		<div class="generic-preview text-center py-5">
			<svg width="64" height="64" fill="currentColor" class="text-muted mb-3" viewBox="0 0 16 16">
				<path d="M14 4.5V14a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V2a2 2 0 0 1 2-2h5.5L14 4.5zm-3 0A1.5 1.5 0 0 1 9.5 3V1H4a1 1 0 0 0-1 1v12a1 1 0 0 0 1 1h8a1 1 0 0 0 1-1V4.5h-2z"/>
			</svg>
			<h4 class="h5 mb-2">%s</h4>
			<p class="text-muted small mb-3">Direct preview is not available for this file type.</p>
			<a href="?download=1" class="btn btn-primary px-4">Download File (%s)</a>
		</div>`, html.EscapeString(downloadName), fileSizeStr)
	}

	landingHtml := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>%s - Paperless-ngx Shared Document</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" href="/static/bootstrap.min.css">
	<style>
		:root {
			--bg-app: #f4f6f8;
			--bg-nav: #ffffff;
			--text-main: #212529;
			--text-muted: #6c757d;
			--border-color: #dee2e6;
			--primary-color: #17541f;
			--primary-hover: #124018;
		}
		@media (prefers-color-scheme: dark) {
			:root {
				--bg-app: #121315;
				--bg-nav: #242529;
				--text-main: #e2e8f0;
				--text-muted: #94a3b8;
				--border-color: #34373d;
				--primary-color: #1f7a2d;
				--primary-hover: #279939;
			}
		}
		html, body {
			height: 100%%;
			margin: 0;
			background-color: var(--bg-app);
			color: var(--text-main);
			font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
			display: flex;
			flex-direction: column;
		}
		.share-nav {
			background-color: var(--bg-nav);
			border-bottom: 1px solid var(--border-color);
			padding: 0.75rem 1.25rem;
			display: flex;
			align-items: center;
			justify-content: space-between;
			flex-wrap: wrap;
			gap: 12px;
		}
		.doc-info {
			display: flex;
			align-items: center;
			gap: 12px;
			min-width: 0;
		}
		.doc-title {
			font-size: 1.1rem;
			font-weight: 600;
			margin: 0;
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
			max-width: 450px;
		}
		.badge-meta {
			font-size: 0.75rem;
			font-weight: 500;
			background: rgba(108, 117, 125, 0.15);
			color: var(--text-muted);
			padding: 0.25rem 0.6rem;
			border-radius: 6px;
		}
		.action-group {
			display: flex;
			align-items: center;
			gap: 8px;
		}
		.btn-primary {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
			font-weight: 500;
		}
		.btn-primary:hover {
			background-color: var(--primary-hover);
			border-color: var(--primary-hover);
		}
		.btn-outline-secondary {
			color: var(--text-main);
			border-color: var(--border-color);
		}
		.btn-outline-secondary:hover {
			background-color: rgba(108, 117, 125, 0.1);
			color: var(--text-main);
			border-color: var(--border-color);
		}
		.content-area {
			flex: 1;
			display: flex;
			align-items: stretch;
			padding: 1rem;
			overflow: hidden;
		}
		.pdf-viewer {
			width: 100%%;
			height: 100%%;
			border: 1px solid var(--border-color);
			border-radius: 8px;
			background: #fff;
		}
		.img-container {
			width: 100%%;
			height: 100%%;
			display: flex;
			align-items: center;
			justify-content: center;
			overflow: auto;
		}
		.img-container img {
			max-width: 100%%;
			max-height: 100%%;
			object-fit: contain;
		}
		#copyToast {
			position: fixed;
			bottom: 20px;
			right: 20px;
			background: #198754;
			color: #fff;
			padding: 8px 16px;
			border-radius: 6px;
			font-size: 0.85rem;
			display: none;
			box-shadow: 0 4px 12px rgba(0,0,0,0.15);
			z-index: 9999;
		}
	</style>
</head>
<body>
	<header class="share-nav">
		<div class="doc-info">
			<svg width="28" height="28" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
				<rect width="48" height="48" rx="8" fill="var(--primary-color)"/>
				<path d="M14 12C14 10.8954 14.8954 10 16 10H26L34 18V36C34 37.1046 33.1046 38 32 38H16C14.8954 38 14 37.1046 14 36V12Z" fill="white" fill-opacity="0.95"/>
				<path d="M26 10V18H34L26 10Z" fill="white" fill-opacity="0.6"/>
			</svg>
			<div>
				<h1 class="doc-title" title="%s">%s</h1>
				<div class="d-flex align-items-center gap-2 mt-1">
					<span class="badge-meta">%s</span>
					<span class="badge-meta">%s</span>
					<span class="badge-meta">%s</span>
				</div>
			</div>
		</div>

		<div class="action-group">
			<button class="btn btn-outline-secondary btn-sm" onclick="copyLink()" title="Copy Link to Clipboard">
				<svg width="14" height="14" fill="currentColor" class="me-1" viewBox="0 0 16 16"><path d="M4 1.5H3a2 2 0 0 0-2 2V14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V3.5a2 2 0 0 0-2-2h-1v1h1a1 1 0 0 1 1 1V14a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V3.5a1 1 0 0 1 1-1h1v-1z"/><path d="M9.5 1a.5.5 0 0 1 .5.5v1a.5.5 0 0 1-.5.5h-3a.5.5 0 0 1-.5-.5v-1a.5.5 0 0 1 .5-.5h3zm-3-1A1.5 1.5 0 0 0 5 1.5v1A1.5 1.5 0 0 0 6.5 4h3A1.5 1.5 0 0 0 11 2.5v-1A1.5 1.5 0 0 0 9.5 0h-3z"/></svg>
				Copy Link
			</button>
			<a href="?raw=1" target="_blank" class="btn btn-outline-secondary btn-sm" title="Open Raw in New Tab">
				<svg width="14" height="14" fill="currentColor" class="me-1" viewBox="0 0 16 16"><path fill-rule="evenodd" d="M8.636 3.5a.5.5 0 0 0-.5-.5H1.5A1.5 1.5 0 0 0 0 4.5v10A1.5 1.5 0 0 0 1.5 16h10a1.5 1.5 0 0 0 1.5-1.5V7.864a.5.5 0 0 0-1 0V14.5a.5.5 0 0 1-.5.5h-10a.5.5 0 0 1-.5-.5v-10a.5.5 0 0 1 .5-.5h6.636a.5.5 0 0 0 .5-.5z"/><path fill-rule="evenodd" d="M16 .5a.5.5 0 0 0-.5-.5h-5a.5.5 0 0 0 0 1h3.793L6.146 9.146a.5.5 0 1 0 .708.708L15 1.707V5.5a.5.5 0 0 0 1 0v-5z"/></svg>
				Fullscreen
			</a>
			<a href="?download=1" class="btn btn-primary btn-sm">
				<svg width="14" height="14" fill="currentColor" class="me-1" viewBox="0 0 16 16"><path d="M.5 9.9a.5.5 0 0 1 .5.5v2.5a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-2.5a.5.5 0 0 1 1 0v2.5a2 2 0 0 1-2 2H2a2 2 0 0 1-2-2v-2.5a.5.5 0 0 1 .5-.5z"/><path d="M7.646 11.854a.5.5 0 0 0 .708 0l3-3a.5.5 0 0 0-.708-.708L8.5 10.293V1.5a.5.5 0 0 0-1 0v8.793L5.354 8.146a.5.5 0 1 0-.708.708l3 3z"/></svg>
				Download
			</a>
		</div>
	</header>

	<main class="content-area">
		%s
	</main>

	<div id="copyToast">Link copied to clipboard!</div>

	<script>
		function copyLink() {
			navigator.clipboard.writeText(window.location.href.split('?')[0]).then(() => {
				const toast = document.getElementById('copyToast');
				toast.style.display = 'block';
				setTimeout(() => { toast.style.display = 'none'; }, 2500);
			});
		}
	</script>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(title),
		strings.ToUpper(strings.TrimPrefix(ext, ".")),
		fileSizeStr,
		expText,
		previewHtml,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(landingHtml))
}

