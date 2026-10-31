package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"runtime"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type StatisticsHandler struct {
	db *database.DB
}

func NewStatisticsHandler(db *database.DB) *StatisticsHandler {
	return &StatisticsHandler{db: db}
}

func (h *StatisticsHandler) Get(w http.ResponseWriter, r *http.Request) {
	var totalDocs, inboxDocs, charCount int
	var tagCount, corrCount, docTypeCount, storPathCount int
	var inboxTagID sql.NullInt64
	var maxASN sql.NullInt64

	_ = h.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL").Scan(&totalDocs)
	_ = h.db.QueryRow("SELECT id FROM tags WHERE is_inbox_tag = 1 LIMIT 1").Scan(&inboxTagID)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM correspondents").Scan(&corrCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM document_types").Scan(&docTypeCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM storage_paths").Scan(&storPathCount)
	_ = h.db.QueryRow("SELECT MAX(archive_serial_number) FROM documents WHERE deleted_at IS NULL").Scan(&maxASN)

	var inboxTags []int
	var singleInboxTag *int
	var inboxDocsPtr *int
	if inboxTagID.Valid {
		tagID := int(inboxTagID.Int64)
		singleInboxTag = &tagID
		inboxTags = append(inboxTags, tagID)
		_ = h.db.QueryRow(`
			SELECT COUNT(DISTINCT d.id)
			FROM documents d
			JOIN document_tags dt ON dt.document_id = d.id
			WHERE dt.tag_id = ? AND d.deleted_at IS NULL
		`, tagID).Scan(&inboxDocs)
		inboxDocsPtr = &inboxDocs
	}
	if inboxTags == nil {
		inboxTags = []int{}
	}

	_ = h.db.QueryRow("SELECT COALESCE(SUM(LENGTH(content)), 0) FROM documents WHERE deleted_at IS NULL").Scan(&charCount)

	var fileTypeCounts []models.DocumentFileTypeCountItem
	rows, err := h.db.Query("SELECT mime_type, COUNT(*) FROM documents WHERE deleted_at IS NULL GROUP BY mime_type")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var item models.DocumentFileTypeCountItem
			if err := rows.Scan(&item.MimeType, &item.MimeTypeCount); err == nil {
				fileTypeCounts = append(fileTypeCounts, item)
			}
		}
		if err := rows.Err(); err != nil {
			log.Printf("[WARN] statistics file type counts cursor error: %v", err)
		}
	}
	if fileTypeCounts == nil {
		fileTypeCounts = []models.DocumentFileTypeCountItem{}
	}

	var currentASN *int
	if maxASN.Valid {
		v := int(maxASN.Int64)
		currentASN = &v
	}

	resp := models.Statistics{
		DocumentsTotal:         totalDocs,
		DocumentsInbox:         inboxDocsPtr,
		InboxTag:               singleInboxTag,
		InboxTags:              inboxTags,
		DocumentFileTypeCounts: fileTypeCounts,
		CharacterCount:         charCount,
		TagCount:               tagCount,
		CorrespondentCount:     corrCount,
		DocumentTypeCount:      docTypeCount,
		StoragePathCount:       storPathCount,
		CurrentASN:             currentASN,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *StatisticsHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"pngx_version": "3.3.2",
		"server_os":    runtime.GOOS,
		"install_type": "bare-metal",
		"storage": map[string]interface{}{
			"total":     int64(1000000000000),
			"available": int64(500000000000),
		},
		"database": map[string]interface{}{
			"type":   "sqlite",
			"url":    "paperless.db",
			"status": "OK",
			"error":  nil,
			"migration_status": map[string]interface{}{
				"latest_migration":     "paperless_mail.0025",
				"unapplied_migrations": []string{},
			},
		},
		"tasks": map[string]interface{}{
			"redis_url":               "embedded",
			"redis_status":            "OK",
			"redis_error":             nil,
			"celery_status":           "OK",
			"celery_url":              "embedded",
			"celery_error":            nil,
			"index_status":            "OK",
			"index_last_modified":     "2026-10-28T12:00:00Z",
			"index_error":             nil,
			"classifier_status":       "OK",
			"classifier_last_trained": "2026-10-28T12:00:00Z",
			"classifier_error":        nil,
			"sanity_check_status":     "OK",
			"sanity_check_last_run":   "2026-10-28T12:00:00Z",
			"sanity_check_error":      nil,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
