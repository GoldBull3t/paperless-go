package workflow

import (
	"database/sql"
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
			'[{"type": 1, "assign_tags": [` + string(rune(tagID+48)) + `], "assign_correspondent": ` + string(rune(corrID+48)) + `, "assign_title": "Processed: {original_filename}"}]'
		)
	`)
	if err != nil {
		t.Fatalf("Failed to insert workflow: %v", err)
	}

	engine := NewEngine(db)

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

