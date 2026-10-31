package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

func TestWorkflowAutomation_RunEndpointAndAuditHistory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_wf.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}
	defer db.Close()

	// Insert test document
	resDoc, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Raw Scanned Doc', 'Important statement from EDP Energia regarding contract 2026', 'edp_doc.pdf', 'chk_wf1', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Failed to insert document: %v", err)
	}
	docID64, _ := resDoc.LastInsertId()
	docID := int(docID64)

	// Insert correspondent
	resCorr, _ := db.Exec("INSERT INTO correspondents (name, slug) VALUES ('EDP Energia', 'edp-energia')")
	corrID64, _ := resCorr.LastInsertId()
	corrID := int(corrID64)

	// Create workflow matching "EDP Energia" with exact word match
	trigs := []map[string]interface{}{
		{
			"type":               3, // TriggerDocumentUpdated
			"matching_algorithm": 1, // Any word
			"match":              "EDP Energia",
		},
	}
	trigsJSON, _ := json.Marshal(trigs)

	acts := []map[string]interface{}{
		{
			"type":                 1, // Assignment
			"assign_title":         "Processed EDP: {title}",
			"assign_correspondent": corrID,
		},
	}
	actsJSON, _ := json.Marshal(acts)

	resWf, err := db.Exec(`
		INSERT INTO workflows (name, order_num, enabled, triggers_json, actions_json)
		VALUES ('EDP Auto Classifier', 1, 1, ?, ?)
	`, string(trigsJSON), string(actsJSON))
	if err != nil {
		t.Fatalf("Failed to insert workflow: %v", err)
	}
	wfID64, _ := resWf.LastInsertId()
	wfID := int(wfID64)

	// Set up router with WorkflowsHandler and DocumentsHandler
	wfH := NewWorkflowsHandler(db)
	docsH := NewDocumentsHandler(db, nil, nil, nil, nil)

	r := chi.NewRouter()
	r.Post("/api/workflows/{id}/run/", wfH.RunWorkflow)
	r.Post("/api/workflows/run/", wfH.RunAllWorkflows)
	r.Get("/api/documents/{id}/history/", docsH.History)

	// 1. Test POST /api/workflows/{id}/run/
	runPayload := fmt.Sprintf(`{"documents": [%d]}`, docID)
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/workflows/%d/run/", wfID), bytes.NewBufferString(runPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var runResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &runResp); err != nil {
		t.Fatalf("Failed to parse run response: %v", err)
	}
	if runResp["processed"].(float64) != 1 || runResp["modified"].(float64) != 1 {
		t.Errorf("Expected processed=1, modified=1, got: %v", runResp)
	}

	// Verify document updated in DB
	var dbTitle string
	var dbCorr int
	_ = db.QueryRow("SELECT title, correspondent_id FROM documents WHERE id = ?", docID).Scan(&dbTitle, &dbCorr)
	if dbTitle != "Processed EDP: Raw Scanned Doc" {
		t.Errorf("Expected title 'Processed EDP: Raw Scanned Doc', got %q", dbTitle)
	}
	if dbCorr != corrID {
		t.Errorf("Expected correspondent %d, got %d", corrID, dbCorr)
	}

	// 2. Test GET /api/documents/{id}/history/ returns audit entry
	histReq := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/history/", docID), nil)
	histRec := httptest.NewRecorder()
	r.ServeHTTP(histRec, histReq)

	if histRec.Code != http.StatusOK {
		t.Fatalf("Expected status 200 for history, got %d: %s", histRec.Code, histRec.Body.String())
	}

	var entries []models.DocumentHistoryEntry
	if err := json.Unmarshal(histRec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("Failed to parse history entries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("Expected at least 1 history entry, got 0")
	}

	foundWorkflowAudit := false
	for _, entry := range entries {
		if entry.Action == "workflow_action" {
			foundWorkflowAudit = true
			if entry.Changes["workflow_name"] != "EDP Auto Classifier" {
				t.Errorf("Expected workflow_name 'EDP Auto Classifier', got: %v", entry.Changes["workflow_name"])
			}
			changes, ok := entry.Changes["changes"].(map[string]interface{})
			if !ok {
				t.Fatalf("Expected nested changes map in audit entry: %v", entry.Changes)
			}
			if changes["title"] == nil {
				t.Errorf("Expected title change in audit entry")
			}
			if changes["correspondent"] == nil {
				t.Errorf("Expected correspondent change in audit entry")
			}
		}
	}
	if !foundWorkflowAudit {
		t.Errorf("Expected to find action 'workflow_action' in history entries")
	}
}
