package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

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
	var inboxTagID sql.NullInt64

	_ = h.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL").Scan(&totalDocs)
	_ = h.db.QueryRow("SELECT id FROM tags WHERE is_inbox_tag = 1 LIMIT 1").Scan(&inboxTagID)

	var inboxTags []int
	if inboxTagID.Valid {
		tagID := int(inboxTagID.Int64)
		inboxTags = append(inboxTags, tagID)
		_ = h.db.QueryRow(`
			SELECT COUNT(DISTINCT d.id)
			FROM documents d
			JOIN document_tags dt ON dt.document_id = d.id
			WHERE dt.tag_id = ? AND d.deleted_at IS NULL
		`, tagID).Scan(&inboxDocs)
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
		_ = rows.Err()
	}
	if fileTypeCounts == nil {
		fileTypeCounts = []models.DocumentFileTypeCountItem{}
	}

	resp := models.Statistics{
		DocumentsTotal:         totalDocs,
		DocumentsInbox:         inboxDocs,
		InboxTags:              inboxTags,
		DocumentFileTypeCounts: fileTypeCounts,
		CharacterCount:         charCount,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *StatisticsHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":  "OK",
		"version": "3.3.2-go-lite",
		"storage": map[string]interface{}{
			"total":     1000000000000,
			"available": 500000000000,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
