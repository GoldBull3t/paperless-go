package workflow

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

// Trigger types
const (
	TriggerConsumptionStarted = 1
	TriggerDocumentAdded       = 2
	TriggerDocumentUpdated     = 3
	TriggerScheduled           = 4
)

// Trigger sources
const (
	SourceConsumeFolder = 1
	SourceApiUpload     = 2
	SourceMailFetch     = 3
	SourceWebUI         = 4
)

// Action types
const (
	ActionAssignment        = 1
	ActionRemoval           = 2
	ActionEmail             = 3
	ActionWebhook           = 4
	ActionPasswordRemoval   = 5
	ActionMoveToTrash       = 6
	ActionRemoteOcr         = 7
	ActionApplyAiSuggestion = 8
)

type Engine struct {
	db *database.DB
}

func NewEngine(db *database.DB) *Engine {
	return &Engine{db: db}
}

// ExecuteWorkflows runs all enabled workflows matching the document, trigger type, and source
func (e *Engine) ExecuteWorkflows(doc *models.Document, triggerType int, source int) error {
	rows, err := e.db.Query(`
		SELECT id, name, order_num, enabled, triggers_json, actions_json
		FROM workflows
		WHERE enabled = 1
		ORDER BY order_num ASC, id ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	docUpdated := false

	for rows.Next() {
		var wfID, orderNum int
		var name, trigsJSON, actsJSON string
		var enabled bool

		if err := rows.Scan(&wfID, &name, &orderNum, &enabled, &trigsJSON, &actsJSON); err != nil {
			continue
		}

		var triggers []map[string]interface{}
		var actions []map[string]interface{}
		_ = json.Unmarshal([]byte(trigsJSON), &triggers)
		_ = json.Unmarshal([]byte(actsJSON), &actions)

		if !e.matchesAnyTrigger(doc, triggers, triggerType, source) {
			continue
		}

		log.Printf("[Workflow] Executing workflow %q (ID: %d) for document %q (ID: %d)", name, wfID, doc.Title, doc.ID)

		for _, action := range actions {
			actType := getInt(action["type"])
			switch actType {
			case ActionAssignment:
				// Assign title
				if t, ok := action["assign_title"].(string); ok && strings.TrimSpace(t) != "" {
					newTitle := strings.ReplaceAll(t, "{original_filename}", doc.OriginalFileName)
					newTitle = strings.ReplaceAll(newTitle, "{created}", doc.CreatedDate)
					doc.Title = newTitle
					docUpdated = true
				}

				// Assign correspondent
				if cid := getNullableInt(action["assign_correspondent"]); cid != nil {
					doc.Correspondent = cid
					docUpdated = true
				}

				// Assign document type
				if dtid := getNullableInt(action["assign_document_type"]); dtid != nil {
					doc.DocumentType = dtid
					docUpdated = true
				}

				// Assign storage path
				if spid := getNullableInt(action["assign_storage_path"]); spid != nil {
					doc.StoragePath = spid
					docUpdated = true
				}

				// Assign owner
				if oid := getNullableInt(action["assign_owner"]); oid != nil {
					doc.Owner = oid
					docUpdated = true
				}

				// Assign tags
				if tags := getIntSlice(action["assign_tags"]); len(tags) > 0 {
					existingTags := make(map[int]bool)
					for _, tid := range doc.Tags {
						existingTags[tid] = true
					}
					for _, tid := range tags {
						if !existingTags[tid] {
							doc.Tags = append(doc.Tags, tid)
							if doc.ID > 0 {
								_, _ = e.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", doc.ID, tid)
							}
						}
					}
				}

			case ActionRemoval:
				if removeAll, ok := action["remove_all_tags"].(bool); ok && removeAll {
					doc.Tags = []int{}
					if doc.ID > 0 {
						_, _ = e.db.Exec("DELETE FROM document_tags WHERE document_id = ?", doc.ID)
					}
				} else if removeTags := getIntSlice(action["remove_tags"]); len(removeTags) > 0 {
					toRemove := make(map[int]bool)
					for _, tid := range removeTags {
						toRemove[tid] = true
						if doc.ID > 0 {
							_, _ = e.db.Exec("DELETE FROM document_tags WHERE document_id = ? AND tag_id = ?", doc.ID, tid)
						}
					}
					var kept []int
					for _, tid := range doc.Tags {
						if !toRemove[tid] {
							kept = append(kept, tid)
						}
					}
					doc.Tags = kept
				}

			case ActionMoveToTrash:
				now := time.Now()
				doc.DeletedAt = &now
				if doc.ID > 0 {
					_, _ = e.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", doc.ID)
				}

			case ActionWebhook:
				if hookUrl, ok := action["webhook_url"].(string); ok && hookUrl != "" {
					go func(url string, d *models.Document) {
						payload, _ := json.Marshal(d)
						req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
						if err == nil {
							req.Header.Set("Content-Type", "application/json")
							req.Header.Set("User-Agent", "paperless-go-workflow/1.0")
							client := &http.Client{Timeout: 10 * time.Second}
							resp, err := client.Do(req)
							if err == nil {
								_ = resp.Body.Close()
							}
						}
					}(hookUrl, doc)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if docUpdated && doc.ID > 0 {
		_, _ = e.db.Exec(`
			UPDATE documents
			SET title = ?, correspondent_id = ?, document_type_id = ?, storage_path_id = ?, owner_id = ?, modified = CURRENT_TIMESTAMP
			WHERE id = ?
		`, doc.Title, doc.Correspondent, doc.DocumentType, doc.StoragePath, doc.Owner, doc.ID)
	}

	return nil
}

func (e *Engine) matchesAnyTrigger(doc *models.Document, triggers []map[string]interface{}, triggerType int, source int) bool {
	if len(triggers) == 0 {
		return false
	}

	for _, trig := range triggers {
		tt := getInt(trig["type"])
		if tt != 0 && tt != triggerType {
			continue
		}

		// Source check
		if sources := getIntSlice(trig["sources"]); len(sources) > 0 {
			matchedSource := false
			for _, s := range sources {
				if s == source {
					matchedSource = true
					break
				}
			}
			if !matchedSource {
				continue
			}
		}

		// Filter filename
		if filterFilename, ok := trig["filter_filename"].(string); ok && filterFilename != "" {
			matched, err := filepath.Match(filterFilename, doc.OriginalFileName)
			if err != nil || !matched {
				continue
			}
		}

		// Filter tags
		if reqTags := getIntSlice(trig["filter_has_tags"]); len(reqTags) > 0 {
			docTagMap := make(map[int]bool)
			for _, tid := range doc.Tags {
				docTagMap[tid] = true
			}
			matchedAny := false
			for _, tid := range reqTags {
				if docTagMap[tid] {
					matchedAny = true
					break
				}
			}
			if !matchedAny {
				continue
			}
		}

		if allTags := getIntSlice(trig["filter_has_all_tags"]); len(allTags) > 0 {
			docTagMap := make(map[int]bool)
			for _, tid := range doc.Tags {
				docTagMap[tid] = true
			}
			hasAll := true
			for _, tid := range allTags {
				if !docTagMap[tid] {
					hasAll = false
					break
				}
			}
			if !hasAll {
				continue
			}
		}

		// Filter correspondent
		if filterCorr := getNullableInt(trig["filter_has_correspondent"]); filterCorr != nil {
			if doc.Correspondent == nil || *doc.Correspondent != *filterCorr {
				continue
			}
		}

		// Filter document type
		if filterDocType := getNullableInt(trig["filter_has_document_type"]); filterDocType != nil {
			if doc.DocumentType == nil || *doc.DocumentType != *filterDocType {
				continue
			}
		}

		// If it passes all criteria of this trigger, it is a match!
		return true
	}

	return false
}

func getInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func getNullableInt(v interface{}) *int {
	if v == nil {
		return nil
	}
	switch n := v.(type) {
	case int:
		return &n
	case int64:
		i := int(n)
		return &i
	case float64:
		i := int(n)
		return &i
	default:
		return nil
	}
}

func getIntSlice(v interface{}) []int {
	if v == nil {
		return nil
	}
	var res []int
	switch arr := v.(type) {
	case []int:
		return arr
	case []interface{}:
		for _, item := range arr {
			res = append(res, getInt(item))
		}
	}
	return res
}
