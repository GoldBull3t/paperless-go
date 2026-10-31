package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

func TestWorkflowExecution(t *testing.T) {
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer rawDB.Close()

	db := &database.DB{DB: rawDB}
	_, err = db.Exec(database.SchemaSQL)
	if err != nil {
		t.Fatalf("Failed to init schema: %v", err)
	}

	// Insert test tags and correspondent
	resT, _ := db.Exec("INSERT INTO tags (name, slug) VALUES ('AutoAssigned', 'auto-assigned')")
	tagID, _ := resT.LastInsertId()

	resC, _ := db.Exec("INSERT INTO correspondents (name, slug) VALUES ('WorkflowCorp', 'workflow-corp')")
	corrID, _ := resC.LastInsertId()

	// Insert Workflow: When DocumentAdded from ConsumeFolder, assign tag and correspondent
	_, err = db.Exec(`
		INSERT INTO workflows (name, order_num, enabled, triggers_json, actions_json)
		VALUES (
			'Test Ingest Workflow',
			1,
			1,
			'[{"type": 2, "sources": [1], "filter_filename": "*.pdf"}]',
			'[{"type": 1, "assign_tags": [` + fmt.Sprintf("%d", tagID) + `], "assign_correspondent": ` + fmt.Sprintf("%d", corrID) + `, "assign_title": "Processed: {original_filename}"}]'
		)
	`)
	if err != nil {
		t.Fatalf("Failed to insert workflow: %v", err)
	}

	engine := NewEngine(db)

	_, _ = db.Exec("INSERT INTO documents (id, title, original_filename, checksum, mime_type) VALUES (1, 'Scan 001', 'invoice.pdf', 'chk', 'application/pdf')")

	doc := &models.Document{
		ID:               1,
		Title:            "Scan 001",
		OriginalFileName: "invoice.pdf",
		Tags:             []int{},
	}

	// Run workflows with DocumentAdded and ConsumeFolder source
	err = engine.ExecuteWorkflows(doc, TriggerDocumentAdded, SourceConsumeFolder)
	if err != nil {
		t.Fatalf("Workflow execution failed: %v", err)
	}

	if doc.Title != "Processed: invoice.pdf" {
		t.Errorf("Expected title 'Processed: invoice.pdf', got %q", doc.Title)
	}

	if len(doc.Tags) == 0 || doc.Tags[0] != int(tagID) {
		t.Errorf("Expected tag %d assigned, got %v", tagID, doc.Tags)
	}

	if doc.Correspondent == nil || *doc.Correspondent != int(corrID) {
		t.Errorf("Expected correspondent %d, got %v", corrID, doc.Correspondent)
	}
}

func TestWorkflow_TextMatchingAndHistoryAudit(t *testing.T) {
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer rawDB.Close()
	rawDB.SetMaxOpenConns(1)

	db := &database.DB{DB: rawDB}
	_, err = db.Exec(database.SchemaSQL)
	if err != nil {
		t.Fatalf("Failed to init schema: %v", err)
	}

	// Insert document in DB
	resDoc, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Initial Scan', 'Contrato de servicos com FATURA-2026 no valor de 500 euros', 'scan_doc.pdf', 'chk123', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to insert document: %v", err)
	}
	docID64, _ := resDoc.LastInsertId()
	docID := int(docID64)

	// Insert tag
	resTag, _ := db.Exec("INSERT INTO tags (name, slug) VALUES ('FaturaAuto', 'fatura-auto')")
	tagID64, _ := resTag.LastInsertId()
	tagID := int(tagID64)

	// Create workflow with Regex Text Match on content ("FATURA-\\d{4}")
	trigs := []map[string]interface{}{
		{
			"type":               TriggerDocumentUpdated,
			"matching_algorithm": MatchRegex,
			"match":              `FATURA-\d{4}`,
		},
	}
	trigsJSON, _ := json.Marshal(trigs)

	acts := []map[string]interface{}{
		{
			"type":         ActionAssignment,
			"assign_title": "Classified: {title}",
			"assign_tags":  []int{tagID},
		},
	}
	actsJSON, _ := json.Marshal(acts)

	_, err = db.Exec(`
		INSERT INTO workflows (name, order_num, enabled, triggers_json, actions_json)
		VALUES ('Fatura Regex Automation', 1, 1, ?, ?)
	`, string(trigsJSON), string(actsJSON))
	if err != nil {
		t.Fatalf("Failed to insert workflow: %v", err)
	}

	engine := NewEngine(db)

	// Fetch document and execute workflows on it
	doc, err := engine.FetchDocument(docID)
	if err != nil {
		t.Fatalf("FetchDocument failed: %v", err)
	}

	err = engine.ExecuteWorkflows(doc, TriggerDocumentUpdated, SourceWebUI)
	if err != nil {
		t.Fatalf("ExecuteWorkflows failed: %v", err)
	}

	if doc.Title != "Classified: Initial Scan" {
		t.Errorf("Expected title 'Classified: Initial Scan', got %q", doc.Title)
	}

	// Verify document updated in SQLite
	var dbTitle string
	_ = db.QueryRow("SELECT title FROM documents WHERE id = ?", docID).Scan(&dbTitle)
	if dbTitle != "Classified: Initial Scan" {
		t.Errorf("Expected DB title 'Classified: Initial Scan', got %q", dbTitle)
	}

	// Verify document_history entry was created with audit details
	var histAction, changesJSON string
	err = db.QueryRow("SELECT action, changes_json FROM document_history WHERE document_id = ?", docID).Scan(&histAction, &changesJSON)
	if err != nil {
		t.Fatalf("Expected document_history entry, got error: %v", err)
	}

	if histAction != "workflow_action" {
		t.Errorf("Expected action 'workflow_action', got %q", histAction)
	}

	var histData struct {
		WorkflowID   int                    `json:"workflow_id"`
		WorkflowName string                 `json:"workflow_name"`
		Changes      map[string]interface{} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(changesJSON), &histData); err != nil {
		t.Fatalf("Failed unmarshaling changes_json: %v", err)
	}

	if histData.WorkflowName != "Fatura Regex Automation" {
		t.Errorf("Expected workflow name 'Fatura Regex Automation', got %q", histData.WorkflowName)
	}

	if histData.Changes["title"] == nil {
		t.Errorf("Expected title mutation recorded in audit changes: %v", histData.Changes)
	}
	if histData.Changes["tags_added"] == nil {
		t.Errorf("Expected tags_added recorded in audit changes: %v", histData.Changes)
	}
}

func TestWorkflow_ExecuteWorkflowByID(t *testing.T) {
	rawDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer rawDB.Close()

	db := &database.DB{DB: rawDB}
	_, err = db.Exec(database.SchemaSQL)
	if err != nil {
		t.Fatalf("Failed to init schema: %v", err)
	}

	resDoc, _ := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Manual Doc', 'Content without matching words', 'manual.pdf', 'chk333', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	docID64, _ := resDoc.LastInsertId()
	docID := int(docID64)

	resWf, err := db.Exec(`
		INSERT INTO workflows (name, order_num, enabled, triggers_json, actions_json)
		VALUES (
			'Manual Trigger Workflow',
			1,
			1,
			'[{"type": 3, "matching_algorithm": 3, "match": "SpecificWord"}]',
			'[{"type": 1, "assign_title": "Manual Run: {title}"}]'
		)
	`)
	if err != nil {
		t.Fatalf("Failed to insert workflow: %v", err)
	}
	wfID64, _ := resWf.LastInsertId()
	wfID := int(wfID64)

	engine := NewEngine(db)
	doc, err := engine.FetchDocument(docID)
	if err != nil {
		t.Fatalf("FetchDocument failed: %v", err)
	}

	// 1. Without forceMatch, triggers are checked, document does NOT match "SpecificWord"
	ran, err := engine.ExecuteWorkflowByID(wfID, doc, false)
	if err != nil {
		t.Fatalf("ExecuteWorkflowByID failed: %v", err)
	}
	if ran {
		t.Errorf("Expected workflow not to run when triggers don't match without force")
	}

	// 2. With forceMatch=true, triggers are bypassed and actions applied
	ran, err = engine.ExecuteWorkflowByID(wfID, doc, true)
	if err != nil {
		t.Fatalf("ExecuteWorkflowByID with force failed: %v", err)
	}
	if !ran {
		t.Errorf("Expected workflow to run when forceMatch=true")
	}

	if doc.Title != "Manual Run: Manual Doc" {
		t.Errorf("Expected title 'Manual Run: Manual Doc', got %q", doc.Title)
	}
}
