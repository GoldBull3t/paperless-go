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

	_ = h.db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&totalDocs)
	_ = h.db.QueryRow("SELECT id FROM tags WHERE is_inbox_tag = 1 LIMIT 1").Scan(&inboxTagID)

	inboxTag := 0
	if inboxTagID.Valid {
		inboxTag = int(inboxTagID.Int64)
		_ = h.db.QueryRow("SELECT COUNT(*) FROM document_tags WHERE tag_id = ?", inboxTag).Scan(&inboxDocs)
	}

	_ = h.db.QueryRow("SELECT COALESCE(SUM(LENGTH(content)), 0) FROM documents").Scan(&charCount)

	fileTypeCount := make(map[string]int)
	rows, err := h.db.Query("SELECT mime_type, COUNT(*) FROM documents GROUP BY mime_type")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var mime string
			var count int
			if err := rows.Scan(&mime, &count); err == nil {
				fileTypeCount[mime] = count
			}
		}
	}

	resp := models.Statistics{
		DocumentsTotal:        totalDocs,
		DocumentsInbox:        inboxDocs,
		InboxTag:              inboxTag,
		DocumentFileTypeCount: fileTypeCount,
		CharacterCount:        charCount,
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
