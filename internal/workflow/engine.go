package workflow

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"paperless-go/internal/compress"
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
	// Lazy-load uncompressed content if document content is empty but ID exists
	if doc.Content == "" && doc.ID > 0 {
		var rawContent string
		if err := e.db.QueryRow("SELECT content FROM documents WHERE id = ?", doc.ID).Scan(&rawContent); err == nil {
			doc.Content = compress.DecompressText(rawContent)
		}
	}

	type wfItem struct {
		id      int
		name    string
		trigs   []map[string]interface{}
		actions []map[string]interface{}
	}
	var workflowsToRun []wfItem

	rows, err := e.db.Query(`
		SELECT id, name, triggers_json, actions_json
		FROM workflows
		WHERE enabled = 1
		ORDER BY order_num ASC, id ASC
	`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item wfItem
		var trigsJSON, actsJSON string
		if err := rows.Scan(&item.id, &item.name, &trigsJSON, &actsJSON); err == nil {
			_ = json.Unmarshal([]byte(trigsJSON), &item.trigs)
			_ = json.Unmarshal([]byte(actsJSON), &item.actions)
			workflowsToRun = append(workflowsToRun, item)
		}
	}
	_ = rows.Close()

	docUpdatedTotal := false

	for _, wf := range workflowsToRun {
		if !e.matchesAnyTrigger(doc, wf.trigs, triggerType, source) {
			continue
		}

		log.Printf("[Workflow] Executing workflow %q (ID: %d) for document %q (ID: %d)", wf.name, wf.id, doc.Title, doc.ID)

		appliedChanges, docUpdated := e.applyActions(doc, wf.actions)
		if docUpdated {
			docUpdatedTotal = true
		}

		if len(appliedChanges) > 0 && doc.ID > 0 {
			historyPayload := map[string]interface{}{
				"workflow_id":   wf.id,
				"workflow_name": wf.name,
				"changes":       appliedChanges,
			}
			e.logHistory(doc.ID, "workflow_action", historyPayload)
		}
	}

	if docUpdatedTotal && doc.ID > 0 {
		if _, err := e.db.Exec(`
			UPDATE documents
			SET title = ?, correspondent_id = ?, document_type_id = ?, storage_path_id = ?, owner_id = ?, modified = CURRENT_TIMESTAMP
			WHERE id = ?
		`, doc.Title, doc.Correspondent, doc.DocumentType, doc.StoragePath, doc.Owner, doc.ID); err != nil {
			log.Printf("[ERROR] [Workflow] Failed updating document %d: %v", doc.ID, err)
		}
	}

	return nil
}

// ExecuteWorkflowByID executes a specific workflow by its ID on a document.
// If forceMatch is true, triggers are bypassed (useful for manual runs). If false, triggers are evaluated.
func (e *Engine) ExecuteWorkflowByID(wfID int, doc *models.Document, forceMatch bool) (bool, error) {
	if doc.Content == "" && doc.ID > 0 {
		var rawContent string
		if err := e.db.QueryRow("SELECT content FROM documents WHERE id = ?", doc.ID).Scan(&rawContent); err == nil {
			doc.Content = compress.DecompressText(rawContent)
		}
	}

	var name, trigsJSON, actsJSON string
	var enabled bool
	err := e.db.QueryRow(`
		SELECT name, enabled, triggers_json, actions_json
		FROM workflows
		WHERE id = ?
	`, wfID).Scan(&name, &enabled, &trigsJSON, &actsJSON)
	if err != nil {
		return false, fmt.Errorf("workflow %d not found: %w", wfID, err)
	}

	if !forceMatch && !enabled {
		return false, nil
	}

	var triggers []map[string]interface{}
	var actions []map[string]interface{}
	_ = json.Unmarshal([]byte(trigsJSON), &triggers)
	_ = json.Unmarshal([]byte(actsJSON), &actions)

	if !forceMatch && !e.matchesAnyTrigger(doc, triggers, TriggerDocumentUpdated, SourceWebUI) {
		return false, nil
	}

	appliedChanges, docUpdated := e.applyActions(doc, actions)
	if docUpdated && doc.ID > 0 {
		if _, err := e.db.Exec(`
			UPDATE documents
			SET title = ?, correspondent_id = ?, document_type_id = ?, storage_path_id = ?, owner_id = ?, modified = CURRENT_TIMESTAMP
			WHERE id = ?
		`, doc.Title, doc.Correspondent, doc.DocumentType, doc.StoragePath, doc.Owner, doc.ID); err != nil {
			log.Printf("[ERROR] [Workflow] Failed updating document %d: %v", doc.ID, err)
		}
	}

	if len(appliedChanges) > 0 && doc.ID > 0 {
		historyPayload := map[string]interface{}{
			"workflow_id":   wfID,
			"workflow_name": name,
			"changes":       appliedChanges,
		}
		e.logHistory(doc.ID, "workflow_action", historyPayload)
	}

	return len(appliedChanges) > 0 || docUpdated, nil
}

// FetchDocument loads a document by ID with tags and metadata
func (e *Engine) FetchDocument(docID int) (*models.Document, error) {
	var doc models.Document
	var origFileName, mimeType, checksum, rawContent, createdStr string
	var corrID, docTypeID, storagePathID, ownerID sql.NullInt64
	var deletedAt sql.NullTime

	row := e.db.QueryRow(`
		SELECT id, title, content, original_filename, mime_type, checksum, created,
		       correspondent_id, document_type_id, storage_path_id, owner_id, deleted_at
		FROM documents
		WHERE id = ?
	`, docID)
	err := row.Scan(
		&doc.ID, &doc.Title, &rawContent, &origFileName, &mimeType, &checksum, &createdStr,
		&corrID, &docTypeID, &storagePathID, &ownerID, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	doc.OriginalFileName = origFileName
	doc.MimeType = mimeType
	doc.Checksum = checksum
	doc.Content = compress.DecompressText(rawContent)
	if parsedCreated, err := time.Parse("2006-01-02 15:04:05", createdStr); err == nil {
		doc.Created = parsedCreated
		doc.CreatedDate = parsedCreated.Format("2006-01-02")
	} else if parsedCreated, err := time.Parse(time.RFC3339, createdStr); err == nil {
		doc.Created = parsedCreated
		doc.CreatedDate = parsedCreated.Format("2006-01-02")
	} else {
		doc.CreatedDate = createdStr
	}
	if corrID.Valid {
		c := int(corrID.Int64)
		doc.Correspondent = &c
	}
	if docTypeID.Valid {
		dt := int(docTypeID.Int64)
		doc.DocumentType = &dt
	}
	if storagePathID.Valid {
		sp := int(storagePathID.Int64)
		doc.StoragePath = &sp
	}
	if ownerID.Valid {
		o := int(ownerID.Int64)
		doc.Owner = &o
	}
	if deletedAt.Valid {
		doc.DeletedAt = &deletedAt.Time
	}

	rows, err := e.db.Query("SELECT tag_id FROM document_tags WHERE document_id = ?", docID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var tid int
			if err := rows.Scan(&tid); err == nil {
				doc.Tags = append(doc.Tags, tid)
			}
		}
	}
	return &doc, nil
}

// ExecuteWorkflowsOnDocumentID loads a document and executes all matching workflows
func (e *Engine) ExecuteWorkflowsOnDocumentID(docID int, triggerType int, source int) (*models.Document, error) {
	doc, err := e.FetchDocument(docID)
	if err != nil {
		return nil, err
	}
	if err := e.ExecuteWorkflows(doc, triggerType, source); err != nil {
		return nil, err
	}
	return doc, nil
}

func (e *Engine) applyActions(doc *models.Document, actions []map[string]interface{}) (map[string]interface{}, bool) {
	appliedChanges := make(map[string]interface{})
	docUpdated := false

	for _, action := range actions {
		actType := getInt(action["type"])
		switch actType {
		case ActionAssignment:
			// Assign title
			if t, ok := action["assign_title"].(string); ok && strings.TrimSpace(t) != "" {
				newTitle := strings.ReplaceAll(t, "{original_filename}", doc.OriginalFileName)
				newTitle = strings.ReplaceAll(newTitle, "{created}", doc.CreatedDate)
				newTitle = strings.ReplaceAll(newTitle, "{title}", doc.Title)
				if newTitle != doc.Title {
					appliedChanges["title"] = map[string]string{"old": doc.Title, "new": newTitle}
					doc.Title = newTitle
					docUpdated = true
				}
			}

			// Assign correspondent
			if cid := getNullableInt(action["assign_correspondent"]); cid != nil {
				if doc.Correspondent == nil || *doc.Correspondent != *cid {
					old := doc.Correspondent
					appliedChanges["correspondent"] = map[string]interface{}{"old": old, "new": *cid}
					doc.Correspondent = cid
					docUpdated = true
				}
			}

			// Assign document type
			if dtid := getNullableInt(action["assign_document_type"]); dtid != nil {
				if doc.DocumentType == nil || *doc.DocumentType != *dtid {
					old := doc.DocumentType
					appliedChanges["document_type"] = map[string]interface{}{"old": old, "new": *dtid}
					doc.DocumentType = dtid
					docUpdated = true
				}
			}

			// Assign storage path
			if spid := getNullableInt(action["assign_storage_path"]); spid != nil {
				if doc.StoragePath == nil || *doc.StoragePath != *spid {
					old := doc.StoragePath
					appliedChanges["storage_path"] = map[string]interface{}{"old": old, "new": *spid}
					doc.StoragePath = spid
					docUpdated = true
				}
			}

			// Assign owner
			if oid := getNullableInt(action["assign_owner"]); oid != nil {
				if doc.Owner == nil || *doc.Owner != *oid {
					old := doc.Owner
					appliedChanges["owner"] = map[string]interface{}{"old": old, "new": *oid}
					doc.Owner = oid
					docUpdated = true
				}
			}

			// Assign tags
			if tags := getIntSlice(action["assign_tags"]); len(tags) > 0 {
				existingTags := make(map[int]bool)
				for _, tid := range doc.Tags {
					existingTags[tid] = true
				}
				var newlyAdded []int
				for _, tid := range tags {
					if !existingTags[tid] {
						doc.Tags = append(doc.Tags, tid)
						existingTags[tid] = true
						newlyAdded = append(newlyAdded, tid)
						if doc.ID > 0 {
							_, _ = e.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", doc.ID, tid)
						}
					}
				}
				if len(newlyAdded) > 0 {
					appliedChanges["tags_added"] = newlyAdded
				}
			}

		case ActionRemoval:
			if removeAll, ok := action["remove_all_tags"].(bool); ok && removeAll {
				if len(doc.Tags) > 0 {
					appliedChanges["tags_removed"] = doc.Tags
					doc.Tags = []int{}
					if doc.ID > 0 {
						_, _ = e.db.Exec("DELETE FROM document_tags WHERE document_id = ?", doc.ID)
					}
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
				var removed []int
				for _, tid := range doc.Tags {
					if !toRemove[tid] {
						kept = append(kept, tid)
					} else {
						removed = append(removed, tid)
					}
				}
				doc.Tags = kept
				if len(removed) > 0 {
					appliedChanges["tags_removed"] = removed
				}
			}

		case ActionMoveToTrash:
			now := time.Now()
			doc.DeletedAt = &now
			appliedChanges["moved_to_trash"] = true
			if doc.ID > 0 {
				_, _ = e.db.Exec("UPDATE documents SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", doc.ID)
			}

		case ActionEmail:
			if emailConfig, ok := action["email"].(map[string]interface{}); ok {
				to, _ := emailConfig["to"].(string)
				subject, _ := emailConfig["subject"].(string)
				if subject == "" {
					subject = "Document: " + doc.Title
				}
				log.Printf("[Workflow] Email action triggered: to=%s, subject=%s, docID=%d", to, subject, doc.ID)
				if _, err := e.db.Exec(`
					INSERT INTO processed_mail (account_id, folder, uid, subject, status, error, received, processed)
					VALUES (1, 'SENT', ?, ?, 'SUCCESS', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
				`, fmt.Sprintf("doc-%d", doc.ID), subject); err != nil {
					log.Printf("[ERROR] [Workflow] Failed recording email action for doc %d: %v", doc.ID, err)
				}
				appliedChanges["email_sent"] = map[string]string{"to": to, "subject": subject}
			}

		case ActionPasswordRemoval:
			log.Printf("[Workflow] Password removal action triggered for docID=%d", doc.ID)
			appliedChanges["password_removal"] = true

		case ActionWebhook:
			if hookUrl, ok := action["webhook_url"].(string); ok && hookUrl != "" {
				appliedChanges["webhook_called"] = hookUrl
				go func(url string, d *models.Document) {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()

					payload, err := json.Marshal(d)
					if err != nil {
						log.Printf("[ERROR] [Workflow] Failed marshaling document for webhook %s: %v", url, err)
						return
					}
					req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
					if err != nil {
						log.Printf("[ERROR] [Workflow] Failed creating webhook request for %s: %v", url, err)
						return
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("User-Agent", "paperless-go-workflow/1.0")

					client := &http.Client{}
					resp, err := client.Do(req)
					if err != nil {
						log.Printf("[WARN] [Workflow] Webhook request to %s failed: %v", url, err)
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode >= 400 {
						log.Printf("[WARN] [Workflow] Webhook to %s returned HTTP status %d", url, resp.StatusCode)
					}
				}(hookUrl, doc)
			}
		}
	}

	return appliedChanges, docUpdated
}

func (e *Engine) logHistory(docID int, action string, changes map[string]interface{}) {
	if docID <= 0 {
		return
	}
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return
	}
	if _, err := e.db.Exec(`
		INSERT INTO document_history (document_id, action, changes_json, actor_id, timestamp)
		VALUES (?, ?, ?, NULL, CURRENT_TIMESTAMP)
	`, docID, action, string(changesJSON)); err != nil {
		log.Printf("[ERROR] [Workflow] logHistory insert failed: %v", err)
	}
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

		// Filter filename (supports wildcard and case-insensitivity)
		if filterFilename, ok := trig["filter_filename"].(string); ok && strings.TrimSpace(filterFilename) != "" {
			ff := strings.TrimSpace(filterFilename)
			m1, err1 := filepath.Match(ff, doc.OriginalFileName)
			m2, err2 := filepath.Match(strings.ToLower(ff), strings.ToLower(doc.OriginalFileName))
			if (err1 != nil || !m1) && (err2 != nil || !m2) {
				continue
			}
		}

		// Filter path
		if filterPath, ok := trig["filter_path"].(string); ok && strings.TrimSpace(filterPath) != "" {
			fp := strings.TrimSpace(filterPath)
			if !strings.Contains(strings.ToLower(doc.OriginalFileName), strings.ToLower(fp)) {
				continue
			}
		}

		// Filter tags (has any)
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

		// Filter tags (has all)
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

		// Filter match rule & algorithm on content + title
		if matchStr, ok := trig["match"].(string); ok && strings.TrimSpace(matchStr) != "" {
			algo := getInt(trig["matching_algorithm"])
			insensitive := true
			if ins, ok := trig["is_insensitive"].(bool); ok {
				insensitive = ins
			}
			if !MatchesRule(doc.Content, doc.Title, matchStr, algo, insensitive) {
				continue
			}
		}

		// Passed all criteria of this trigger!
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
