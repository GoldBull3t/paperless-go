package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/exporter"
	"paperless-go/internal/models"
)

func setupParityTestEnv(t *testing.T) (*database.DB, *config.Config, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "paperless-parity-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}

	cfg := &config.Config{
		DataDir:         tmpDir,
		MediaDir:        tmpDir,
		ConsumeDir:      filepath.Join(tmpDir, "consume"),
		OCRWorkerCount:  1,
		Port:            8000,
		Host:            "127.0.0.1",
		DatabasePath:    dbPath,
	}

	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "archive"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "thumbnails"), 0755)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cfg, cleanup
}

func TestSelectionDataParity(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	// Insert test entities
	if _, err := db.Exec("INSERT INTO tags (id, name, slug) VALUES (1, 'Tag A', 'tag-a'), (2, 'Tag B', 'tag-b')"); err != nil {
		t.Fatalf("failed inserting tags: %v", err)
	}
	if _, err := db.Exec("INSERT INTO correspondents (id, name, slug) VALUES (1, 'Vendor 1', 'vendor-1')"); err != nil {
		t.Fatalf("failed inserting corr: %v", err)
	}
	if _, err := db.Exec("INSERT INTO document_types (id, name, slug) VALUES (1, 'Invoice', 'invoice')"); err != nil {
		t.Fatalf("failed inserting type: %v", err)
	}
	if _, err := db.Exec("INSERT INTO storage_paths (id, name, slug, path) VALUES (1, 'Path 1', 'path-1', '{title}')"); err != nil {
		t.Fatalf("failed inserting sp: %v", err)
	}

	// Insert test document
	if _, err := db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, correspondent_id, document_type_id, storage_path_id, created)
		VALUES (10, 'Doc 1', 'Content', 'doc1.pdf', 'cs1', 'application/pdf', 1, 1, 1, CURRENT_TIMESTAMP)
	`); err != nil {
		t.Fatalf("failed inserting doc: %v", err)
	}
	if _, err := db.Exec("INSERT INTO document_tags (document_id, tag_id) VALUES (10, 1)"); err != nil {
		t.Fatalf("failed inserting doc tag: %v", err)
	}

	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)

	// Call selection data with document 10
	payload := `{"documents": [10]}`
	req := httptest.NewRequest("POST", "/api/documents/selection_data/", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	docsH.SelectionData(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.SelectionDataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Must contain both Tag 1 (count 1) and Tag 2 (count 0)
	if len(resp.SelectedTags) != 2 {
		t.Fatalf("expected 2 tags returned, got %d", len(resp.SelectedTags))
	}
	tagCounts := make(map[int]int)
	for _, ti := range resp.SelectedTags {
		tagCounts[ti.ID] = ti.DocumentCount
	}
	if tagCounts[1] != 1 || tagCounts[2] != 0 {
		t.Errorf("expected tag counts 1:1, 2:0, got %+v", tagCounts)
	}
}

func TestTrashParity(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	// Insert deleted document
	_, _ = db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, created, deleted_at)
		VALUES (20, 'Deleted Doc', 'Some text', 'deleted.pdf', 'cs2', 'application/pdf', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`)

	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)

	// 1. List Trash
	req := httptest.NewRequest("GET", "/api/trash/", nil)
	w := httptest.NewRecorder()
	docsH.TrashList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var listResp models.PaginatedResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to parse trash list: %v", err)
	}
	if listResp.Count != 1 {
		t.Fatalf("expected count 1, got %d", listResp.Count)
	}

	// 2. Restore from trash
	actionPayload := `{"action": "restore", "documents": [20]}`
	reqAct := httptest.NewRequest("POST", "/api/trash/", bytes.NewBufferString(actionPayload))
	wAct := httptest.NewRecorder()
	docsH.TrashAction(wAct, reqAct)

	if wAct.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wAct.Code)
	}
	var actResp map[string]interface{}
	if err := json.Unmarshal(wAct.Body.Bytes(), &actResp); err != nil {
		t.Fatalf("failed to parse trash action resp: %v", err)
	}
	if actResp["result"] != "OK" {
		t.Errorf("expected result 'OK', got %v", actResp["result"])
	}

	// Verify restored in DB
	var delAt *string
	_ = db.QueryRow("SELECT deleted_at FROM documents WHERE id = 20").Scan(&delAt)
	if delAt != nil {
		t.Errorf("expected deleted_at to be NULL after restore, got %v", *delAt)
	}
}

func TestCustomFieldsDocumentCount(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	_, _ = db.Exec("INSERT INTO custom_fields (id, name, data_type, extra_data) VALUES (1, 'Invoice Total', 'float', '{}')")
	_, _ = db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, created)
		VALUES (100, 'Test Doc', 'Content', 'doc.pdf', 'cs100', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	_, _ = db.Exec("INSERT INTO document_custom_fields (document_id, field_id, value_text) VALUES (100, 1, '123.45')")

	cfH := NewCustomFieldsHandler(db)
	req := httptest.NewRequest("GET", "/api/custom_fields/", nil)
	w := httptest.NewRecorder()
	cfH.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp models.PaginatedResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	resList, ok := resp.Results.([]interface{})
	if !ok || len(resList) == 0 {
		t.Fatalf("expected results list with at least 1 item")
	}
	first := resList[0].(map[string]interface{})
	if first["document_count"].(float64) != 1 {
		t.Errorf("expected document_count 1, got %v", first["document_count"])
	}
}

func TestUsersAndGroupsCRUD(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	usersH := NewUsersHandler(db)

	// 1. Create Group
	grpPayload := `{"name": "Auditors", "permissions": ["view_document", "change_document"]}`
	reqG := httptest.NewRequest("POST", "/api/groups/", bytes.NewBufferString(grpPayload))
	wG := httptest.NewRecorder()
	usersH.CreateGroup(wG, reqG)
	if wG.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for group, got %d: %s", wG.Code, wG.Body.String())
	}

	var grp models.Group
	_ = json.Unmarshal(wG.Body.Bytes(), &grp)
	if grp.Name != "Auditors" || len(grp.Permissions) != 2 {
		t.Errorf("unexpected group data: %+v", grp)
	}

	// 2. Create User with Group
	userPayload := fmt.Sprintf(`{
		"username": "auditor_user",
		"password": "Password123!",
		"email": "auditor@example.com",
		"is_staff": true,
		"groups": [%d],
		"user_permissions": ["view_tag"]
	}`, grp.ID)

	reqU := httptest.NewRequest("POST", "/api/users/", bytes.NewBufferString(userPayload))
	wU := httptest.NewRecorder()
	usersH.Create(wU, reqU)
	if wU.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for user, got %d: %s", wU.Code, wU.Body.String())
	}

	var createdUser models.User
	_ = json.Unmarshal(wU.Body.Bytes(), &createdUser)
	if createdUser.Username != "auditor_user" {
		t.Errorf("expected username 'auditor_user', got %s", createdUser.Username)
	}
	if len(createdUser.Groups) != 1 || createdUser.Groups[0] != grp.ID {
		t.Errorf("expected user in group %d, got %+v", grp.ID, createdUser.Groups)
	}
	// Verify inherited permissions from group
	if len(createdUser.InheritedPermissions) != 2 {
		t.Errorf("expected 2 inherited permissions, got %+v", createdUser.InheritedPermissions)
	}
}

func TestConfigPatchPersistence(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	cfgH := NewConfigHandler(db)

	patchBody := `{"app_title": "My Custom Paperless", "barcodes_enabled": true}`
	req := httptest.NewRequest("PATCH", "/api/config/", bytes.NewBufferString(patchBody))
	w := httptest.NewRecorder()
	cfgH.Patch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// Read again via GET
	reqGet := httptest.NewRequest("GET", "/api/config/", nil)
	wGet := httptest.NewRecorder()
	cfgH.Get(wGet, reqGet)

	var getResp []map[string]interface{}
	_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
	if len(getResp) == 0 {
		t.Fatalf("expected config item returned")
	}
	if getResp[0]["app_title"] != "My Custom Paperless" {
		t.Errorf("expected updated app_title 'My Custom Paperless', got %v", getResp[0]["app_title"])
	}
	if getResp[0]["barcodes_enabled"] != true {
		t.Errorf("expected barcodes_enabled true, got %v", getResp[0]["barcodes_enabled"])
	}
}

func TestMailCRUD(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	mailH := NewMailHandler(db)

	// Create Account
	acctPayload := `{"name": "Office IMAP", "imap_server": "mail.example.com", "username": "office@example.com", "password": "SecretPassword"}`
	reqA := httptest.NewRequest("POST", "/api/mail_accounts/", bytes.NewBufferString(acctPayload))
	wA := httptest.NewRecorder()
	mailH.CreateAccount(wA, reqA)

	if wA.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for mail account, got %d: %s", wA.Code, wA.Body.String())
	}
	var acct models.MailAccount
	_ = json.Unmarshal(wA.Body.Bytes(), &acct)
	if acct.Password != "**********" {
		t.Errorf("expected obfuscated password, got %s", acct.Password)
	}

	// Create Rule
	rulePayload := fmt.Sprintf(`{"name": "Invoices Rule", "account": %d, "filter_subject": "Invoice", "folder": "INBOX"}`, acct.ID)
	reqR := httptest.NewRequest("POST", "/api/mail_rules/", bytes.NewBufferString(rulePayload))
	wR := httptest.NewRecorder()
	mailH.CreateRule(wR, reqR)

	if wR.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for mail rule, got %d: %s", wR.Code, wR.Body.String())
	}
}

func TestExporterBackupAndSanity(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	// Seed document
	_, _ = db.Exec("INSERT INTO tags (id, name, slug) VALUES (1, 'Receipt', 'receipt')")
	_, _ = db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, created)
		VALUES (50, 'Store Receipt', 'Receipt content', 'receipt.pdf', 'sum50', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	_, _ = db.Exec("INSERT INTO document_tags (document_id, tag_id) VALUES (50, 1)")

	// Create original dummy file
	origPath := filepath.Join(cfg.MediaDir, "documents", "originals", "0000050.pdf")
	_ = os.WriteFile(origPath, []byte("PDF content dummy"), 0644)

	// Run Export
	exportDir := filepath.Join(cfg.DataDir, "backup_out")
	if err := exporter.Export(db, cfg, exportDir); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	manifestFile := filepath.Join(exportDir, "manifest.json")
	if _, err := os.Stat(manifestFile); err != nil {
		t.Fatalf("manifest.json not created in export dir")
	}

	// Run Sanity Check
	issues, err := exporter.SanityCheck(db, cfg)
	if err != nil {
		t.Fatalf("sanity check failed: %v", err)
	}
	_ = issues
}

func TestStoragePathFormatting(t *testing.T) {
	created := time.Date(2026, time.March, 15, 10, 0, 0, 0, time.UTC)
	added := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	asn := 42

	tmpl := "{created_year}/{correspondent}/{title}"
	res := consumer.FormatStoragePath(tmpl, "Invoice 123", "Acme Corp", "Invoice", created, added, &asn)

	expected := filepath.Join("2026", "Acme Corp", "Invoice 123")
	if res != expected {
		t.Errorf("expected %s, got %s", expected, res)
	}

	// Test Jinja2 style
	tmplJinja := "{{ created_year }}/{{ correspondent }}/{{ title }}"
	resJinja := consumer.FormatStoragePath(tmplJinja, "Invoice 123", "Acme Corp", "Invoice", created, added, &asn)
	if resJinja != expected {
		t.Errorf("expected %s, got %s", expected, resJinja)
	}
}

func TestChatStreamingMetadata(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)

	// When consumer/RAG is nil, streaming returns friendly plain text notification
	chatPayload := `{"document_id": 1, "q": "What is the total?"}`
	req := httptest.NewRequest("POST", "/api/documents/chat/", bytes.NewBufferString(chatPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/plain")

	w := httptest.NewRecorder()
	docsH.Chat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("expected text/plain content type, got %s", w.Header().Get("Content-Type"))
	}
}

func TestDocumentOperationsEndpoints(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)

	// Create test document 1 and 2
	_, _ = db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, page_count)
		VALUES (1, 'Doc One', 'First document text', 'doc1.pdf', 'chk1', 'application/pdf', 1),
		       (2, 'Doc Two', 'Second document text', 'doc2.pdf', 'chk2', 'application/pdf', 2)
	`)

	// 1. Rotate
	rotReq := httptest.NewRequest("POST", "/api/documents/rotate/", bytes.NewBufferString(`{"documents": [1], "degrees": 90}`))
	rotReq.Header.Set("Content-Type", "application/json")
	wRot := httptest.NewRecorder()
	docsH.Rotate(wRot, rotReq)
	if wRot.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from Rotate, got %d: %s", wRot.Code, wRot.Body.String())
	}
	var rotResp map[string]interface{}
	_ = json.Unmarshal(wRot.Body.Bytes(), &rotResp)
	if rotResp["result"] != "OK" {
		t.Errorf("expected result OK, got %v", rotResp["result"])
	}

	// 2. Merge As Versions
	mVerReq := httptest.NewRequest("POST", "/api/documents/merge_as_versions/", bytes.NewBufferString(`{"documents": [2], "root_document_id": 1}`))
	mVerReq.Header.Set("Content-Type", "application/json")
	wMVer := httptest.NewRecorder()
	docsH.MergeAsVersions(wMVer, mVerReq)
	if wMVer.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from MergeAsVersions, got %d: %s", wMVer.Code, wMVer.Body.String())
	}
	var rootID int
	_ = db.QueryRow("SELECT root_document_id FROM documents WHERE id = 2").Scan(&rootID)
	if rootID != 1 {
		t.Errorf("expected document 2 root_document_id = 1, got %d", rootID)
	}

	// 3. Edit PDF
	editReq := httptest.NewRequest("POST", "/api/documents/edit_pdf/", bytes.NewBufferString(`{"documents": [1], "operations": [{"page": 1, "rotate": 90}]}`))
	editReq.Header.Set("Content-Type", "application/json")
	wEdit := httptest.NewRecorder()
	docsH.EditPDF(wEdit, editReq)
	if wEdit.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from EditPDF, got %d: %s", wEdit.Code, wEdit.Body.String())
	}

	// 4. Remove Password
	pwReq := httptest.NewRequest("POST", "/api/documents/remove_password/", bytes.NewBufferString(`{"documents": [1], "password": "secret"}`))
	pwReq.Header.Set("Content-Type", "application/json")
	wPw := httptest.NewRecorder()
	docsH.RemovePassword(wPw, pwReq)
	if wPw.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from RemovePassword, got %d: %s", wPw.Code, wPw.Body.String())
	}

	// 5. Email Documents
	emailReq := httptest.NewRequest("POST", "/api/documents/email/", bytes.NewBufferString(`{"documents": [1], "addresses": "test@example.com", "subject": "Test Subj", "message": "Body"}`))
	emailReq.Header.Set("Content-Type", "application/json")
	wEmail := httptest.NewRecorder()
	docsH.EmailDocuments(wEmail, emailReq)
	if wEmail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from EmailDocuments, got %d: %s", wEmail.Code, wEmail.Body.String())
	}

	// 6. Merge
	mergeReq := httptest.NewRequest("POST", "/api/documents/merge/", bytes.NewBufferString(`{"documents": [1, 2], "delete_originals": true}`))
	mergeReq.Header.Set("Content-Type", "application/json")
	wMerge := httptest.NewRecorder()
	docsH.Merge(wMerge, mergeReq)
	if wMerge.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from Merge, got %d: %s", wMerge.Code, wMerge.Body.String())
	}
	var mergedContent string
	_ = db.QueryRow("SELECT content FROM documents WHERE id = 1").Scan(&mergedContent)
	if !bytes.Contains([]byte(mergedContent), []byte("Second document text")) {
		t.Errorf("expected merged content to include document 2 text, got %s", mergedContent)
	}
}

func TestDocumentHistoryAndMetadataParity(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)

	// Create document with file on disk
	docFile := filepath.Join(cfg.MediaDir, "documents", "originals", "0000001.pdf")
	_ = os.WriteFile(docFile, []byte("%PDF-1.4 /Title (Annual Report 2026) /Author (Finance Dept)"), 0644)

	_, _ = db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, page_count)
		VALUES (1, 'Report', 'Content', 'report.pdf', 'chk123', 'application/pdf', 1)
	`)

	// Log some history entries
	docsH.logHistory(1, "create", map[string]interface{}{"title": "Report"}, nil)
	docsH.logHistory(1, "rotate", map[string]interface{}{"rotation": 180}, nil)

	// Check History endpoint
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")

	reqH := httptest.NewRequest("GET", "/api/documents/1/history/", nil)
	reqH = reqH.WithContext(context.WithValue(reqH.Context(), chi.RouteCtxKey, rctx))
	wH := httptest.NewRecorder()
	docsH.History(wH, reqH)
	if wH.Code != http.StatusOK {
		t.Fatalf("expected 200 from History, got %d", wH.Code)
	}
	var history []models.DocumentHistoryEntry
	_ = json.Unmarshal(wH.Body.Bytes(), &history)
	if len(history) < 2 {
		t.Fatalf("expected at least 2 history entries, got %d", len(history))
	}
	if history[0].Action != "rotate" {
		t.Errorf("expected latest action rotate, got %s", history[0].Action)
	}

	// Check Metadata endpoint
	reqM := httptest.NewRequest("GET", "/api/documents/1/metadata/", nil)
	reqM = reqM.WithContext(context.WithValue(reqM.Context(), chi.RouteCtxKey, rctx))
	wM := httptest.NewRecorder()
	docsH.Metadata(wM, reqM)
	if wM.Code != http.StatusOK {
		t.Fatalf("expected 200 from Metadata, got %d", wM.Code)
	}
	var metaResp map[string]interface{}
	_ = json.Unmarshal(wM.Body.Bytes(), &metaResp)
	origMeta, ok := metaResp["original_metadata"].([]interface{})
	if !ok || len(origMeta) == 0 {
		t.Errorf("expected non-empty original_metadata in response, got %v", metaResp["original_metadata"])
	}
}

func TestSystemStatusContract(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	statsH := NewStatisticsHandler(db)

	req := httptest.NewRequest("GET", "/api/status/", nil)
	w := httptest.NewRecorder()
	statsH.GetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GetStatus, got %d", w.Code)
	}

	var status map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &status)

	if status["pngx_version"] != "3.3.2" {
		t.Errorf("expected pngx_version 3.3.2, got %v", status["pngx_version"])
	}
	if status["server_os"] == nil {
		t.Error("expected server_os to be present")
	}
	if status["database"] == nil {
		t.Error("expected database block to be present")
	}
	if status["tasks"] == nil {
		t.Error("expected tasks block to be present")
	}
}

func TestWorkflowTriggersAndActionsCRUD(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	wfH := NewWorkflowsHandler(db)

	// Create Trigger
	trigReq := httptest.NewRequest("POST", "/api/workflow_triggers/", bytes.NewBufferString(`{
		"type": 2,
		"sources": [1, 2],
		"filter_path": "/inbox/*",
		"filter_filename": "*.pdf",
		"matching_algorithm": 1,
		"match": "test"
	}`))
	trigReq.Header.Set("Content-Type", "application/json")
	wTrig := httptest.NewRecorder()
	wfH.CreateTrigger(wTrig, trigReq)
	if wTrig.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from CreateTrigger, got %d: %s", wTrig.Code, wTrig.Body.String())
	}

	// List Triggers
	listReq := httptest.NewRequest("GET", "/api/workflow_triggers/", nil)
	wList := httptest.NewRecorder()
	wfH.ListTriggers(wList, listReq)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 from ListTriggers, got %d", wList.Code)
	}

	// Create Action
	actReq := httptest.NewRequest("POST", "/api/workflow_actions/", bytes.NewBufferString(`{
		"type": 1,
		"assign_title": "Action Title",
		"assign_tags": [1, 2]
	}`))
	actReq.Header.Set("Content-Type", "application/json")
	wAct := httptest.NewRecorder()
	wfH.CreateAction(wAct, actReq)
	if wAct.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from CreateAction, got %d: %s", wAct.Code, wAct.Body.String())
	}

	// List Actions
	listActReq := httptest.NewRequest("GET", "/api/workflow_actions/", nil)
	wListAct := httptest.NewRecorder()
	wfH.ListActions(wListAct, listActReq)
	if wListAct.Code != http.StatusOK {
		t.Fatalf("expected 200 from ListActions, got %d", wListAct.Code)
	}
}

func TestShareLinkBundlesParity(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	shH := NewShareLinksHandler(db, cfg)

	_, _ = db.Exec("INSERT INTO documents (id, title, content, original_filename, checksum, mime_type) VALUES (1, 'BundleDoc', 'txt', 'b.pdf', 'chk', 'application/pdf')")

	// Create bundle
	bundleReq := httptest.NewRequest("POST", "/api/share_link_bundles/", bytes.NewBufferString(`{
		"document_ids": [1],
		"description": "My test bundle",
		"file_version": "original"
	}`))
	bundleReq.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	shH.CreateBundle(wB, bundleReq)
	if wB.Code != http.StatusCreated {
		t.Fatalf("expected 201 from CreateBundle, got %d: %s", wB.Code, wB.Body.String())
	}
	var created models.ShareLinkBundle
	_ = json.Unmarshal(wB.Body.Bytes(), &created)
	if created.DocumentCount != 1 || len(created.Documents) != 1 {
		t.Errorf("expected 1 document in bundle, got count=%d, len=%d", created.DocumentCount, len(created.Documents))
	}

	// List bundles
	listReq := httptest.NewRequest("GET", "/api/share_link_bundles/", nil)
	wList := httptest.NewRecorder()
	shH.ListBundles(wList, listReq)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 from ListBundles, got %d", wList.Code)
	}
}

func TestBulkEditObjectsSetPermissions(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	bulkH := NewBulkObjectsHandler(db)

	_, _ = db.Exec("INSERT INTO tags (id, name, slug, color, text_color, match, matching_algorithm) VALUES (1, 'Tag A', 'tag-a', '#fff', '#000', '', 0)")
	_, _ = db.Exec("INSERT INTO users (id, username, password_hash) VALUES (42, 'testuser', 'hash')")

	req := httptest.NewRequest("POST", "/api/bulk_edit_objects/", bytes.NewBufferString(`{
		"object_type": "tags",
		"operation": "set_permissions",
		"objects": [1],
		"owner": 42
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	bulkH.BulkEditObjects(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from BulkEditObjects, got %d", w.Code)
	}

	var ownerID int
	_ = db.QueryRow("SELECT owner_id FROM tags WHERE id = 1").Scan(&ownerID)
	if ownerID != 42 {
		t.Errorf("expected tag owner_id 42, got %d", ownerID)
	}
}

func TestTasksActiveAndRunParity(t *testing.T) {
	db, _, cleanup := setupParityTestEnv(t)
	defer cleanup()

	tasksH := NewTasksHandler(db)

	// Run manual task
	runReq := httptest.NewRequest("POST", "/api/tasks/run/", bytes.NewBufferString(`{"task_type": "train_classifier"}`))
	runReq.Header.Set("Content-Type", "application/json")
	wRun := httptest.NewRecorder()
	tasksH.Run(wRun, runReq)
	if wRun.Code != http.StatusOK {
		t.Fatalf("expected 200 from Tasks Run, got %d", wRun.Code)
	}

	// Summary
	sumReq := httptest.NewRequest("GET", "/api/tasks/summary/", nil)
	wSum := httptest.NewRecorder()
	tasksH.Summary(wSum, sumReq)
	if wSum.Code != http.StatusOK {
		t.Fatalf("expected 200 from Tasks Summary, got %d", wSum.Code)
	}

	// Active
	actReq := httptest.NewRequest("GET", "/api/tasks/active/", nil)
	wAct := httptest.NewRecorder()
	tasksH.Active(wAct, actReq)
	if wAct.Code != http.StatusOK {
		t.Fatalf("expected 200 from Tasks Active, got %d", wAct.Code)
	}
}

func TestBarcodeASNAndSeparation(t *testing.T) {
	asnText := "Official Invoice Document ASN-004289 Approved"
	asn := consumer.DetectASNFromBarcode(asnText, "ASN")
	if asn == nil || *asn != 4289 {
		t.Errorf("expected detected ASN 4289, got %v", asn)
	}

	sepText := "Some header\nPATCHT\nFooter"
	isSep := consumer.DetectSeparatorBarcode(sepText, "PATCHT")
	if !isSep {
		t.Error("expected separator detection to return true")
	}
}

func TestDocumentRenamer(t *testing.T) {
	db, cfg, cleanup := setupParityTestEnv(t)
	defer cleanup()

	_, _ = db.Exec("INSERT INTO storage_paths (id, name, path, slug, match, matching_algorithm) VALUES (1, 'Year Path', '{created_year}/{title}', 'year-path', '', 0)")
	_, _ = db.Exec("INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, created, storage_path_id) VALUES (1, 'Invoice1', 'txt', 'inv.pdf', 'chk', 'application/pdf', '2026-05-10 12:00:00', 1)")

	// Create physical original file
	origFile := filepath.Join(cfg.MediaDir, "documents", "originals", "0000001.pdf")
	_ = os.WriteFile(origFile, []byte("%PDF-1.4 sample content"), 0644)

	reorgCount, err := consumer.RenameAllDocuments(db, cfg)
	if err != nil {
		t.Fatalf("RenameAllDocuments failed: %v", err)
	}
	if reorgCount != 1 {
		t.Errorf("expected 1 file reorganized, got %d", reorgCount)
	}

	expectedPath := filepath.Join(cfg.MediaDir, "documents", "originals", "2026", "Invoice1", "0000001.pdf")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("expected reorganized file at %s, but file does not exist", expectedPath)
	}
}
