package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
	"paperless-go/internal/queue"
)

func setupTestRouterFull(t *testing.T) (*chiMuxWrapper, *database.DB, *config.Config, *consumer.Consumer, *queue.Queue, func()) {
	tmpDir, err := os.MkdirTemp("", "paperless-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cfg := &config.Config{
		Host:            "127.0.0.1",
		Port:            8000,
		DataDir:         tmpDir,
		MediaDir:        filepath.Join(tmpDir, "media"),
		ConsumeDir:      filepath.Join(tmpDir, "consume"),
		StaticDir:       filepath.Join(tmpDir, "static"),
		DatabasePath:    filepath.Join(tmpDir, "test.db"),
		SecretKey:       "test-secret",
		AutoLogin:       true,
		DefaultLanguage: "en-US",
	}

	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "archive"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "thumbnails"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.StaticDir, "frontend", "en-US"), 0755)
	_ = os.WriteFile(filepath.Join(cfg.StaticDir, "frontend", "en-US", "index.html"), []byte("<html>Mock SPA</html>"), 0644)

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}

	authSvc := auth.NewService(db, cfg)
	_ = authSvc.EnsureDefaultAdmin("admin", "admin")

	q := queue.NewQueue(db, 1)
	wsHub := NewWebSocketHub()
	q.SetBroadcaster(wsHub)
	c := consumer.NewConsumer(db, cfg)

	router := NewRouter(cfg, db, authSvc, q, c, wsHub)

	cleanup := func() {
		q.Close()
		wsHub.Close()
		db.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return &chiMuxWrapper{Handler: router}, db, cfg, c, q, cleanup
}

func setupTestRouter(t *testing.T) (*chiMuxWrapper, *database.DB, func()) {
	w, db, _, _, _, cleanup := setupTestRouterFull(t)
	return w, db, cleanup
}

type chiMuxWrapper struct {
	http.Handler
}

func (w *chiMuxWrapper) Do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	w.ServeHTTP(rec, req)
	return rec
}

func TestUISettingsAndConfig(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. GET /api/ui_settings/
	req := httptest.NewRequest("GET", "/api/ui_settings/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from ui_settings, got %d: %s", rec.Code, rec.Body.String())
	}

	var uiResp UISettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &uiResp); err != nil {
		t.Fatalf("Failed to parse ui_settings: %v", err)
	}
	if len(uiResp.Permissions) == 0 {
		t.Errorf("Expected permissions in ui_settings")
	}

	// 2. GET /api/config/ (MUST return array for Angular e => e[0])
	req = httptest.NewRequest("GET", "/api/config/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from config, got %d", rec.Code)
	}
	var configArr []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &configArr); err != nil {
		t.Fatalf("Expected array from /api/config/, got error: %v", err)
	}
	if len(configArr) == 0 || configArr[0]["app_title"] != "Paperless-ngx" {
		t.Errorf("Invalid config array response: %v", configArr)
	}

	// 3. PATCH /api/config/1/
	patchBody := bytes.NewBufferString(`{"app_title": "Paperless-ngx"}`)
	req = httptest.NewRequest("PATCH", "/api/config/1/", patchBody)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 from PATCH config, got %d", rec.Code)
	}
}

func TestStatisticsFormat(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/statistics/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from statistics, got %d", rec.Code)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("Failed to unmarshal statistics: %v", err)
	}

	// Verify inbox_tags is an array
	if _, ok := raw["inbox_tags"].([]interface{}); !ok {
		t.Errorf("Expected inbox_tags to be an array, got %T: %v", raw["inbox_tags"], raw["inbox_tags"])
	}

	// Verify document_file_type_counts is an array
	if _, ok := raw["document_file_type_counts"].([]interface{}); !ok {
		t.Errorf("Expected document_file_type_counts to be an array, got %T: %v", raw["document_file_type_counts"], raw["document_file_type_counts"])
	}
}

func TestTasksStatusCountsAndAcknowledge(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// Insert test tasks
	_, err := db.Exec(`
		INSERT INTO tasks (task_id, type, status, task_name, task_file_name, date_created, acknowledged)
		VALUES ('task-1', 'file_handling', 'SUCCESS', 'Test 1', 'file1.pdf', CURRENT_TIMESTAMP, 0),
		       ('task-2', 'file_handling', 'FAILURE', 'Test 2', 'file2.pdf', CURRENT_TIMESTAMP, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert tasks: %v", err)
	}

	// 1. GET /api/tasks/
	req := httptest.NewRequest("GET", "/api/tasks/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from tasks list, got %d", rec.Code)
	}
	var paginated models.PaginatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &paginated); err != nil {
		t.Fatalf("Expected PaginatedResponse from tasks, got error: %v", err)
	}
	if paginated.Count != 2 {
		t.Errorf("Expected count 2, got %d", paginated.Count)
	}

	// 2. GET /api/tasks/status_counts/
	req = httptest.NewRequest("GET", "/api/tasks/status_counts/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from status_counts, got %d", rec.Code)
	}
	var counts map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatalf("Failed to parse status_counts: %v", err)
	}
	if counts["all"] != 2 || counts["needs_attention"] != 1 || counts["completed"] != 1 {
		t.Errorf("Unexpected status_counts: %+v", counts)
	}

	// 3. POST /api/tasks/acknowledge/ with all: true
	ackBody := bytes.NewBufferString(`{"all": true}`)
	req = httptest.NewRequest("POST", "/api/tasks/acknowledge/", ackBody)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 from acknowledge, got %d", rec.Code)
	}

	// Status counts should now be 0 unacknowledged
	req = httptest.NewRequest("GET", "/api/tasks/status_counts/", nil)
	rec = router.Do(req)
	_ = json.Unmarshal(rec.Body.Bytes(), &counts)
	if counts["all"] != 0 {
		t.Errorf("Expected 0 unacknowledged after acknowledge all, got %d", counts["all"])
	}
}

func TestSearchAndAutocomplete(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// Insert test tag and document
	_, _ = db.Exec("INSERT INTO tags (name, slug, color, text_color) VALUES ('Invoice', 'invoice', '#ff0000', '#ffffff')")
	_, _ = db.Exec(`
		INSERT INTO documents (title, content, created, original_filename, checksum, mime_type)
		VALUES ('Electric Bill 2026', 'Total due: $50.00 to Electric Company', CURRENT_TIMESTAMP, 'bill.pdf', 'c1', 'application/pdf')
	`)

	// 1. GET /api/search/?query=Electric
	req := httptest.NewRequest("GET", "/api/search/?query=Electric", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from search, got %d: %s", rec.Code, rec.Body.String())
	}
	var searchRes map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &searchRes); err != nil {
		t.Fatalf("Failed to parse search: %v", err)
	}
	docs := searchRes["documents"].([]interface{})
	if len(docs) == 0 {
		t.Errorf("Expected at least 1 document match for 'Electric'")
	}

	// 2. GET /api/search/autocomplete/?term=Elec
	req = httptest.NewRequest("GET", "/api/search/autocomplete/?term=Elec", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from autocomplete, got %d", rec.Code)
	}
	var autoRes []string
	if err := json.Unmarshal(rec.Body.Bytes(), &autoRes); err != nil {
		t.Fatalf("Failed to parse autocomplete: %v", err)
	}
	if len(autoRes) == 0 || autoRes[0] != "Electric Bill 2026" {
		t.Errorf("Unexpected autocomplete response: %v", autoRes)
	}
}

func TestDocumentActionsAndTrash(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// Insert test document
	res, err := db.Exec(`
		INSERT INTO documents (title, content, created, original_filename, checksum, mime_type, archive_serial_number)
		VALUES ('Tax Document 2026', 'Confidential Tax Form', CURRENT_TIMESTAMP, 'tax.pdf', 'c2', 'application/pdf', 10)
	`)
	if err != nil {
		t.Fatalf("Failed to insert document: %v", err)
	}
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	// 1. GET /api/documents/next_asn/
	req := httptest.NewRequest("GET", "/api/documents/next_asn/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from next_asn, got %d", rec.Code)
	}
	var asn int
	if err := json.Unmarshal(rec.Body.Bytes(), &asn); err != nil {
		t.Fatalf("Failed to parse next_asn: %v", err)
	}
	if asn != 11 {
		t.Errorf("Expected next ASN to be 11, got %d", asn)
	}

	// 2. POST /api/documents/delete/ (multi-document soft delete)
	delBody := bytes.NewBufferString(fmt.Sprintf(`{"documents": [%d]}`, docID))
	req = httptest.NewRequest("POST", "/api/documents/delete/", delBody)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from delete documents, got %d", rec.Code)
	}

	// 3. GET /api/trash/ (should have 1 document)
	req = httptest.NewRequest("GET", "/api/trash/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from trash list, got %d", rec.Code)
	}
	var trashResp models.PaginatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &trashResp); err != nil {
		t.Fatalf("Failed to parse trash: %v", err)
	}
	if trashResp.Count != 1 {
		t.Errorf("Expected 1 document in trash, got %d", trashResp.Count)
	}

	// 4. POST /api/trash/ with action: "restore"
	restoreBody := bytes.NewBufferString(fmt.Sprintf(`{"action": "restore", "documents": [%d]}`, docID))
	req = httptest.NewRequest("POST", "/api/trash/", restoreBody)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from trash restore, got %d", rec.Code)
	}

	// Document should no longer be in trash
	req = httptest.NewRequest("GET", "/api/trash/", nil)
	rec = router.Do(req)
	_ = json.Unmarshal(rec.Body.Bytes(), &trashResp)
	if trashResp.Count != 0 {
		t.Errorf("Expected 0 documents in trash after restore, got %d", trashResp.Count)
	}
}

func TestBulkEditObjects(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	_, _ = db.Exec("INSERT INTO tags (name, slug, color, text_color) VALUES ('TestTag1', 'testtag1', '#fff', '#000'), ('TestTag2', 'testtag2', '#fff', '#000')")

	body := bytes.NewBufferString(`{"object_type": "tags", "operation": "delete", "objects": [1, 2, 3]}`)
	req := httptest.NewRequest("POST", "/api/bulk_edit_objects/", body)
	req.Header.Set("Content-Type", "application/json")
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from bulk_edit_objects, got %d", rec.Code)
	}

	var tagCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	if tagCount != 0 {
		t.Errorf("Expected 0 tags after bulk delete, got %d", tagCount)
	}
}

func TestZstandardCompressionInDocuments(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Ingest a document with long text that will trigger Zstandard compression (>128 chars)
	longText := "Invoice #98765. This is a very detailed invoice for cloud server hosting services and datacenter bandwidth with redundant electrical power and high availability enterprise support. Total due is $1,250.00."
	
	// Create dummy test file
	tmpFile, err := os.CreateTemp("", "zstd-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	_, _ = tmpFile.WriteString(longText)
	_ = tmpFile.Close()

	// Consumer instance from test router
	c := consumer.NewConsumer(db, &config.Config{MediaDir: os.TempDir()})
	docID, err := c.ConsumeFile(tmpFile.Name(), "hosting_invoice.txt", nil)
	if err != nil {
		t.Fatalf("ConsumeFile failed: %v", err)
	}

	// 2. Verify stored in SQLite documents.content with zstd: prefix
	var rawContentInDB string
	err = db.QueryRow("SELECT content FROM documents WHERE id = ?", docID).Scan(&rawContentInDB)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if !strings.HasPrefix(rawContentInDB, "zstd:") {
		t.Errorf("Expected content in SQLite to have 'zstd:' prefix, got: %s", rawContentInDB)
	}

	// 3. Verify GET /api/documents/{id}/ transparently decompresses
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/", docID), nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from GET document, got %d", rec.Code)
	}
	var doc models.Document
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("Failed to unmarshal document: %v", err)
	}
	if doc.Content != longText {
		t.Errorf("Expected decompressed content to match original text, got: %s", doc.Content)
	}

	// 4. Verify GET /api/documents/ also decompresses
	req = httptest.NewRequest("GET", "/api/documents/", nil)
	rec = router.Do(req)
	var paginated models.PaginatedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &paginated)
	if paginated.Count == 0 {
		t.Fatalf("Expected at least 1 document in list")
	}
}

func TestSemanticClassifierAndSuggestions(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// Insert test tag
	resT, _ := db.Exec("INSERT INTO tags (name, slug, match, matching_algorithm) VALUES ('Hosting', 'hosting', 'hosting', 1)")
	tagID, _ := resT.LastInsertId()

	// Insert document
	resD, _ := db.Exec(`
		INSERT INTO documents (title, content, created, original_filename, checksum, mime_type)
		VALUES ('Server Invoice', 'Datacenter cloud server hosting invoice and monthly statement', CURRENT_TIMESTAMP, 'server.txt', 'chk1', 'text/plain')
	`)
	docID, _ := resD.LastInsertId()

	// 1. GET /api/documents/{id}/suggestions/
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/suggestions/", docID), nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from suggestions, got %d", rec.Code)
	}
	var sug models.SuggestionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &sug); err != nil {
		t.Fatalf("Failed to parse suggestions: %v", err)
	}
	if len(sug.Tags) == 0 || sug.Tags[0] != int(tagID) {
		t.Errorf("Expected suggested tag %d, got %v", tagID, sug.Tags)
	}

	// 2. GET /api/documents/{id}/ai_suggestions/
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/ai_suggestions/", docID), nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from ai_suggestions, got %d", rec.Code)
	}
	var aiSug map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &aiSug); err != nil {
		t.Fatalf("Failed to parse ai_suggestions: %v", err)
	}
	if _, ok := aiSug["tags"]; !ok {
		t.Errorf("Expected tags key in ai_suggestions")
	}
}

func TestWorkflowsCRUD(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. POST /api/workflows/
	wfPayload := `{
		"name": "Auto Ingest Invoices",
		"order": 1,
		"enabled": true,
		"triggers": [{"type": 2, "sources": [1], "filter_filename": "*.pdf"}],
		"actions": [{"type": 1, "assign_title": "Processed Document"}]
	}`
	req := httptest.NewRequest("POST", "/api/workflows/", bytes.NewBufferString(wfPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := router.Do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201 from create workflow, got %d: %s", rec.Code, rec.Body.String())
	}
	var createdWf models.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &createdWf); err != nil {
		t.Fatalf("Failed to parse created workflow: %v", err)
	}
	if createdWf.ID == 0 || createdWf.Name != "Auto Ingest Invoices" {
		t.Errorf("Unexpected created workflow: %+v", createdWf)
	}

	// 2. GET /api/workflows/
	req = httptest.NewRequest("GET", "/api/workflows/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from list workflows, got %d", rec.Code)
	}
	var paginated models.PaginatedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &paginated)
	if paginated.Count != 1 {
		t.Errorf("Expected 1 workflow, got %d", paginated.Count)
	}

	// 3. PATCH /api/workflows/{id}/
	patchBody := `{"name": "Updated Workflow Name", "enabled": false}`
	req = httptest.NewRequest("PATCH", fmt.Sprintf("/api/workflows/%d/", createdWf.ID), bytes.NewBufferString(patchBody))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from patch workflow, got %d", rec.Code)
	}
	var updatedWf models.Workflow
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedWf)
	if updatedWf.Name != "Updated Workflow Name" || updatedWf.Enabled != false {
		t.Errorf("Patch did not apply: %+v", updatedWf)
	}

	// 4. DELETE /api/workflows/{id}/
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/workflows/%d/", createdWf.ID), nil)
	rec = router.Do(req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("Expected 204 from delete workflow, got %d", rec.Code)
	}
}

func TestTOTPFlow(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. GET /api/profile/totp/
	req := httptest.NewRequest("GET", "/api/profile/totp/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from GET totp settings, got %d: %s", rec.Code, rec.Body.String())
	}
	var settings map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &settings); err != nil {
		t.Fatalf("Failed to parse totp settings: %v", err)
	}
	secret := settings["secret"]
	qrSVG := settings["qr_svg"]
	if secret == "" || !strings.HasPrefix(qrSVG, "<svg") {
		t.Fatalf("Invalid totp settings: %+v", settings)
	}

	// 2. Generate valid TOTP code
	validCode, err := auth.CalculateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("CalculateTOTP failed: %v", err)
	}

	// 3. POST /api/profile/totp/ with valid code
	activatePayload := fmt.Sprintf(`{"secret": %q, "code": %q}`, secret, validCode)
	req = httptest.NewRequest("POST", "/api/profile/totp/", bytes.NewBufferString(activatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from activate totp, got %d: %s", rec.Code, rec.Body.String())
	}
	var actResp struct {
		Success       bool     `json:"success"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &actResp); err != nil || !actResp.Success || len(actResp.RecoveryCodes) != 8 {
		t.Fatalf("Failed activation response: %s", rec.Body.String())
	}

	// 4. Verify profile shows is_mfa_enabled = true
	req = httptest.NewRequest("GET", "/api/profile/", nil)
	rec = router.Do(req)
	var prof map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &prof)
	if prof["is_mfa_enabled"] != true {
		t.Errorf("Expected is_mfa_enabled: true in profile, got %v", prof["is_mfa_enabled"])
	}

	// 5. DELETE /api/profile/totp/
	req = httptest.NewRequest("DELETE", "/api/profile/totp/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from delete totp, got %d", rec.Code)
	}

	// 6. Admin deactivation POST /api/users/{id}/deactivate_totp/
	req = httptest.NewRequest("POST", "/api/users/1/deactivate_totp/", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from admin deactivate totp, got %d", rec.Code)
	}
}

func TestNetworkScanAPI(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// GET /api/scan/scanners/
	req := httptest.NewRequest("GET", "/api/scan/scanners/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from scan scanners, got %d: %s", rec.Code, rec.Body.String())
	}
	var scanners []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &scanners); err != nil || len(scanners) == 0 {
		t.Fatalf("Expected scanners list, got: %s", rec.Body.String())
	}
}

func createMockDocx(t *testing.T, filePath string, textContent string) {
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("Failed to create docx file: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("Failed to create entry in zip: %v", err)
	}
	xmlContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
    <w:body>
        <w:p>
            <w:r>
                <w:t>%s</w:t>
            </w:r>
        </w:p>
    </w:body>
</w:document>`, textContent)
	_, _ = w.Write([]byte(xmlContent))
	_ = zw.Close()
}

func TestOfficeDocumentParity(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	tmpDir := t.TempDir()
	docxPath := filepath.Join(tmpDir, "Sample_Agreement_2026.docx")
	secretText := "Confidential Agreement Parity Verification 998877"
	createMockDocx(t, docxPath, secretText)

	// Verify detection
	if !consumer.IsOfficeDocument(docxPath, "application/vnd.openxmlformats-officedocument.wordprocessingml.document") {
		t.Fatalf("Expected docx to be recognized as office document")
	}

	// Verify pure Go OpenXML text extraction
	extracted, err := consumer.ExtractOfficeText(docxPath)
	if err != nil {
		t.Fatalf("ExtractOfficeText failed: %v", err)
	}
	if !strings.Contains(extracted, secretText) {
		t.Fatalf("Expected extracted text to contain %q, got: %q", secretText, extracted)
	}

	// Consume mock docx through consumer
	c := consumer.NewConsumer(db, &config.Config{
		MediaDir: tmpDir,
	})
	docID, err := c.ConsumeFile(docxPath, "Sample_Agreement_2026.docx", &consumer.ConsumeOptions{
		Title: "Office Agreement Test",
	})
	if err != nil {
		t.Fatalf("ConsumeFile failed: %v", err)
	}

	// Verify document was indexed and page count is set
	var content string
	var pageCount int
	err = db.QueryRow("SELECT content, page_count FROM documents WHERE id = ?", docID).Scan(&content, &pageCount)
	if err != nil {
		t.Fatalf("Failed to query document: %v", err)
	}
	if !strings.Contains(content, secretText) {
		t.Errorf("Expected content in db to contain %q, got: %q", secretText, content)
	}
	if pageCount < 1 {
		t.Errorf("Expected page_count >= 1, got: %d", pageCount)
	}

	// Verify search works for the Office text via API
	req := httptest.NewRequest("GET", "/api/documents/?query=998877", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from document search, got %d: %s", rec.Code, rec.Body.String())
	}
	var pageResp models.PaginatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pageResp); err != nil {
		t.Fatalf("Failed to unmarshal search response: %v", err)
	}
	if pageResp.Count != 1 {
		t.Errorf("Expected 1 document matching 998877, got: %d", pageResp.Count)
	}
}

func TestMultiPageDetection(t *testing.T) {
	tmpDir := t.TempDir()
	pdfContent := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Page >>\nendobj\n2 0 obj\n<< /Type /Page >>\nendobj\n3 0 obj\n<< /Type /Page >>\nendobj\n%%EOF")
	mockPdfPath := filepath.Join(tmpDir, "multipage.pdf")
	if err := os.WriteFile(mockPdfPath, pdfContent, 0644); err != nil {
		t.Fatalf("Failed to write mock pdf: %v", err)
	}

	pages := consumer.DetectPageCount(mockPdfPath, "application/pdf")
	if pages != 3 {
		t.Errorf("Expected DetectPageCount to return 3, got: %d", pages)
	}
}

func TestMultiUserObjectPermissions(t *testing.T) {
	router, db, cleanup := setupTestRouter(t)
	defer cleanup()

	// Retrieve default admin token (user 1, superuser)
	var adminToken string
	if err := db.QueryRow("SELECT auth_token FROM users WHERE id = 1").Scan(&adminToken); err != nil {
		t.Fatalf("Failed to get admin token: %v", err)
	}

	// Create regular non-superuser (user 2)
	stdToken := "standard-user-token-456"
	res, err := db.Exec(`
		INSERT INTO users (username, password_hash, email, first_name, last_name, is_superuser, is_staff, is_active, auth_token)
		VALUES ('john', 'hash', 'john@example.com', 'John', 'Doe', 0, 0, 1, ?)
	`, stdToken)
	if err != nil {
		t.Fatalf("Failed to insert standard user: %v", err)
	}
	userID64, _ := res.LastInsertId()
	stdUserID := int(userID64)

	// Create Document 1 owned by Admin
	_, err = db.Exec(`
		INSERT INTO documents (id, title, content, created, original_filename, checksum, mime_type, owner_id)
		VALUES (101, 'Admin Top Secret Budget', 'Confidential executive roadmap 2027', CURRENT_TIMESTAMP, 'admin.pdf', 'chk101', 'application/pdf', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create admin doc: %v", err)
	}

	// Create Document 2 owned by Standard User
	_, err = db.Exec(`
		INSERT INTO documents (id, title, content, created, original_filename, checksum, mime_type, owner_id)
		VALUES (102, 'John Personal Expenses', 'Receipt for coffee and office desk', CURRENT_TIMESTAMP, 'john.pdf', 'chk102', 'application/pdf', ?)
	`, stdUserID)
	if err != nil {
		t.Fatalf("Failed to create standard user doc: %v", err)
	}

	// Create Document 3 public (no owner)
	_, err = db.Exec(`
		INSERT INTO documents (id, title, content, created, original_filename, checksum, mime_type, owner_id)
		VALUES (103, 'Public Company Handbook', 'Company vacation and holiday policy', CURRENT_TIMESTAMP, 'public.pdf', 'chk103', 'application/pdf', NULL)
	`)
	if err != nil {
		t.Fatalf("Failed to create public doc: %v", err)
	}

	// Create Tag 1 owned by Admin
	_, _ = db.Exec(`INSERT INTO tags (id, name, slug, owner_id) VALUES (201, 'AdminOnlyTag', 'admin-tag', 1)`)

	// Create Tag 2 owned by Standard User
	_, _ = db.Exec(`INSERT INTO tags (id, name, slug, owner_id) VALUES (202, 'JohnTag', 'john-tag', ?)`, stdUserID)

	// --- 1. Standard user listings isolation ---
	req := httptest.NewRequest("GET", "/api/documents/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for standard user document list, got %d: %s", rec.Code, rec.Body.String())
	}
	var docList models.PaginatedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &docList)

	// Convert results to slice of map
	rawDocs, _ := json.Marshal(docList.Results)
	var docs []models.Document
	_ = json.Unmarshal(rawDocs, &docs)

	// Standard user should only see John Personal doc (102) and Public doc (103), NOT Admin doc (101)
	foundAdminDoc := false
	foundJohnDoc := false
	foundPublicDoc := false
	for _, d := range docs {
		if d.ID == 101 {
			foundAdminDoc = true
		}
		if d.ID == 102 {
			foundJohnDoc = true
			if !d.UserCanChange {
				t.Errorf("Expected UserCanChange=true for owned document 102")
			}
			if !d.IsSharedByRequester {
				t.Errorf("Expected IsSharedByRequester=true for owned document 102")
			}
		}
		if d.ID == 103 {
			foundPublicDoc = true
			if !d.UserCanChange {
				t.Errorf("Expected UserCanChange=true for public document 103")
			}
			if d.IsSharedByRequester {
				t.Errorf("Expected IsSharedByRequester=false for public document 103")
			}
		}
	}
	if foundAdminDoc {
		t.Fatalf("Security failure: Standard user was able to list Admin's private document!")
	}
	if !foundJohnDoc || !foundPublicDoc {
		t.Fatalf("Standard user failed to see their own doc or public doc: %+v", docs)
	}

	// --- 2. Standard user object-level 403 Forbidden checks ---
	// GET admin doc -> 403
	req = httptest.NewRequest("GET", "/api/documents/101/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on GET admin doc, got: %d", rec.Code)
	}

	// PATCH admin doc -> 403
	req = httptest.NewRequest("PATCH", "/api/documents/101/", bytes.NewBufferString(`{"title": "Hacked Title"}`))
	req.Header.Set("Authorization", "Token "+stdToken)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on PATCH admin doc, got: %d", rec.Code)
	}

	// DELETE admin doc -> 403
	req = httptest.NewRequest("DELETE", "/api/documents/101/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on DELETE admin doc, got: %d", rec.Code)
	}

	// PREVIEW admin doc -> 403
	req = httptest.NewRequest("GET", "/api/documents/101/preview/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on PREVIEW admin doc, got: %d", rec.Code)
	}

	// DOWNLOAD admin doc -> 403
	req = httptest.NewRequest("GET", "/api/documents/101/download/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on DOWNLOAD admin doc, got: %d", rec.Code)
	}

	// THUMB admin doc -> 403
	req = httptest.NewRequest("GET", "/api/documents/101/thumb/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on THUMB admin doc, got: %d", rec.Code)
	}

	// METADATA admin doc -> 403
	req = httptest.NewRequest("GET", "/api/documents/101/metadata/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden on METADATA admin doc, got: %d", rec.Code)
	}

	// --- 3. Standard user allowed operations on own document ---
	req = httptest.NewRequest("GET", "/api/documents/102/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK on GET own document 102, got: %d", rec.Code)
	}

	req = httptest.NewRequest("PATCH", "/api/documents/102/", bytes.NewBufferString(`{"title": "Updated John Expenses"}`))
	req.Header.Set("Authorization", "Token "+stdToken)
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 OK on PATCH own document 102, got: %d", rec.Code)
	}

	// --- 4. Tag isolation ---
	req = httptest.NewRequest("GET", "/api/tags/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on tags list, got: %d", rec.Code)
	}
	var tagList models.PaginatedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tagList)
	rawTags, _ := json.Marshal(tagList.Results)
	var tags []models.Tag
	_ = json.Unmarshal(rawTags, &tags)

	for _, tItem := range tags {
		if tItem.ID == 201 {
			t.Fatalf("Security failure: Standard user was able to view admin's tag 201!")
		}
	}

	// GET admin tag directly -> 403
	req = httptest.NewRequest("GET", "/api/tags/201/", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Expected 403 on admin tag access, got: %d", rec.Code)
	}

	// --- 5. Superuser omniscient access ---
	req = httptest.NewRequest("GET", "/api/documents/", nil)
	req.Header.Set("Authorization", "Token "+adminToken)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 for admin document list, got: %d", rec.Code)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &docList)
	rawDocs, _ = json.Marshal(docList.Results)
	_ = json.Unmarshal(rawDocs, &docs)

	// Admin must see all 3 documents
	if len(docs) < 3 {
		t.Errorf("Expected admin to see at least 3 documents, got: %d", len(docs))
	}
	for _, d := range docs {
		if !d.UserCanChange {
			t.Errorf("Expected superuser to have UserCanChange=true on all docs, false on doc %d", d.ID)
		}
	}

	// --- 6. Search isolation ---
	// Standard user searches "roadmap" (in admin's doc)
	req = httptest.NewRequest("GET", "/api/search/?query=roadmap", nil)
	req.Header.Set("Authorization", "Token "+stdToken)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on search, got: %d", rec.Code)
	}
	var searchResp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &searchResp)
	searchDocs, _ := searchResp["documents"].([]interface{})
	if len(searchDocs) != 0 {
		t.Errorf("Security failure: Admin confidential document leaked in standard user search!")
	}
}

func TestShareLinksFullLifecycle(t *testing.T) {
	router, db, cfg, _, _, cleanup := setupTestRouterFull(t)
	defer cleanup()

	// Create physical media file for document 501
	origDir := filepath.Join(cfg.MediaDir, "documents", "originals")
	_ = os.MkdirAll(origDir, 0755)
	dummyPDF := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF")
	_ = os.WriteFile(filepath.Join(origDir, "0000501.pdf"), dummyPDF, 0644)

	// Insert document 501
	_, err := db.Exec(`
		INSERT INTO documents (id, title, content, created, original_filename, checksum, mime_type, owner_id)
		VALUES (501, 'Quarterly Financials', 'Revenue report Q3 2026', CURRENT_TIMESTAMP, 'financials.pdf', 'chk501', 'application/pdf', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to insert doc 501: %v", err)
	}

	// 1. Create a share link via POST /api/share_links/
	bodyJSON := `{"document": 501, "file_version": "original", "expiration": "2035-01-01T00:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/share_links/", bytes.NewBufferString(bodyJSON))
	req.Header.Set("Authorization", "Token test-token-123")
	req.Header.Set("Content-Type", "application/json")
	rec := router.Do(req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created on share link creation, got %d: %s", rec.Code, rec.Body.String())
	}

	var sl models.ShareLink
	if err := json.Unmarshal(rec.Body.Bytes(), &sl); err != nil {
		t.Fatalf("Failed to parse ShareLink response: %v", err)
	}
	if sl.Slug == "" {
		t.Fatalf("Expected non-empty slug, got empty")
	}
	if sl.Document != 501 || sl.DocumentTitle != "Quarterly Financials" {
		t.Errorf("Expected document 501 and title Quarterly Financials, got doc=%d, title=%s", sl.Document, sl.DocumentTitle)
	}
	if sl.FileVersion != "original" {
		t.Errorf("Expected file_version 'original', got %s", sl.FileVersion)
	}

	// 2. List share links via GET /api/share_links/
	req = httptest.NewRequest("GET", "/api/share_links/", nil)
	req.Header.Set("Authorization", "Token test-token-123")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on share links list, got %d", rec.Code)
	}
	var pagResp models.PaginatedResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pagResp)
	if pagResp.Count != 1 {
		t.Errorf("Expected count 1, got %d", pagResp.Count)
	}

	// 3. Document-specific share links GET /api/documents/501/share_links/
	req = httptest.NewRequest("GET", "/api/documents/501/share_links/", nil)
	req.Header.Set("Authorization", "Token test-token-123")
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on doc share links, got %d", rec.Code)
	}
	var docLinks []models.ShareLink
	_ = json.Unmarshal(rec.Body.Bytes(), &docLinks)
	if len(docLinks) != 1 || docLinks[0].Slug != sl.Slug {
		t.Errorf("Expected docLinks to contain slug %s", sl.Slug)
	}

	// 4. Public unauthenticated access GET /share/{slug}
	pubReq := httptest.NewRequest("GET", "/share/"+sl.Slug, nil)
	pubRec := router.Do(pubReq)
	if pubRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on public share link, got %d: %s", pubRec.Code, pubRec.Body.String())
	}
	if pubRec.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("Expected Content-Type application/pdf, got %s", pubRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(pubRec.Header().Get("Content-Disposition"), "inline") {
		t.Errorf("Expected inline Content-Disposition, got %s", pubRec.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(pubRec.Body.Bytes(), dummyPDF) {
		t.Errorf("Public share response body does not match original file!")
	}

	// 5. Public download parameter GET /share/{slug}?download=true
	pubDlReq := httptest.NewRequest("GET", "/share/"+sl.Slug+"?download=true", nil)
	pubDlRec := router.Do(pubDlReq)
	if pubDlRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on public download share link, got %d", pubDlRec.Code)
	}
	if !strings.Contains(pubDlRec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Expected attachment Content-Disposition, got %s", pubDlRec.Header().Get("Content-Disposition"))
	}

	// 6. Test expired share link
	expiredJSON := `{"document": 501, "file_version": "original", "expiration": "2020-01-01T00:00:00Z"}`
	expReq := httptest.NewRequest("POST", "/api/share_links/", bytes.NewBufferString(expiredJSON))
	expReq.Header.Set("Authorization", "Token test-token-123")
	expReq.Header.Set("Content-Type", "application/json")
	expRec := router.Do(expReq)
	var expSL models.ShareLink
	_ = json.Unmarshal(expRec.Body.Bytes(), &expSL)

	// Accessing expired slug must yield 410 Gone
	accExpReq := httptest.NewRequest("GET", "/share/"+expSL.Slug, nil)
	accExpRec := router.Do(accExpReq)
	if accExpRec.Code != http.StatusGone {
		t.Errorf("Expected 410 Gone on expired link, got %d", accExpRec.Code)
	}

	// 7. Delete share link
	delReq := httptest.NewRequest("DELETE", fmt.Sprintf("/api/share_links/%d/", sl.ID), nil)
	delReq.Header.Set("Authorization", "Token test-token-123")
	delRec := router.Do(delReq)
	if delRec.Code != http.StatusNoContent {
		t.Errorf("Expected 204 No Content on delete, got %d", delRec.Code)
	}

	// Accessing deleted slug must yield 404 Not Found
	accDelReq := httptest.NewRequest("GET", "/share/"+sl.Slug, nil)
	accDelRec := router.Do(accDelReq)
	if accDelRec.Code != http.StatusNotFound {
		t.Errorf("Expected 404 Not Found after deletion, got %d", accDelRec.Code)
	}
}

func TestDeduplicationAndSHA256(t *testing.T) {
	router, db, _, consumerObj, queueObj, cleanup := setupTestRouterFull(t)
	defer cleanup()

	// Create an existing document with known content and checksum
	docContent := "Unique invoice content for energy supplier electricity"
	tmpOrig, err := os.CreateTemp("", "dedup-test-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpOrig.Name())
	_, _ = tmpOrig.WriteString(docContent)
	_ = tmpOrig.Close()

	docID, err := consumerObj.ConsumeFile(tmpOrig.Name(), "electricity-january.txt", nil)
	if err != nil {
		t.Fatalf("Failed to consume original document: %v", err)
	}
	if docID <= 0 {
		t.Fatalf("Expected valid docID > 0, got %d", docID)
	}

	var checksum string
	_ = db.QueryRow("SELECT checksum FROM documents WHERE id = ?", docID).Scan(&checksum)
	if checksum == "" {
		t.Fatalf("Expected document checksum to be populated!")
	}

	// 1. GET /api/documents/check_duplicate/?checksum={hash}
	checkReq := httptest.NewRequest("GET", "/api/documents/check_duplicate/?checksum="+checksum, nil)
	checkReq.Header.Set("Authorization", "Token test-token-123")
	checkRec := router.Do(checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on check_duplicate, got %d: %s", checkRec.Code, checkRec.Body.String())
	}
	var checkResult map[string]interface{}
	_ = json.Unmarshal(checkRec.Body.Bytes(), &checkResult)
	if checkResult["duplicate"] != true {
		t.Errorf("Expected duplicate: true, got %v", checkResult["duplicate"])
	}
	if int(checkResult["document_id"].(float64)) != docID {
		t.Errorf("Expected document_id %d, got %v", docID, checkResult["document_id"])
	}

	// 2. Check non-existent checksum
	nonCheckReq := httptest.NewRequest("GET", "/api/documents/check_duplicate/?checksum=nonexistent123", nil)
	nonCheckReq.Header.Set("Authorization", "Token test-token-123")
	nonCheckRec := router.Do(nonCheckReq)
	var nonResult map[string]interface{}
	_ = json.Unmarshal(nonCheckRec.Body.Bytes(), &nonResult)
	if nonResult["duplicate"] != false {
		t.Errorf("Expected duplicate: false for non-existent checksum, got %v", nonResult["duplicate"])
	}

	// 3. Document query by checksum: GET /api/documents/?checksum={hash}
	filterReq := httptest.NewRequest("GET", "/api/documents/?checksum="+checksum, nil)
	filterReq.Header.Set("Authorization", "Token test-token-123")
	filterRec := router.Do(filterReq)
	if filterRec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on documents filter by checksum, got %d", filterRec.Code)
	}
	var filterResp models.PaginatedResponse
	_ = json.Unmarshal(filterRec.Body.Bytes(), &filterResp)
	if filterResp.Count != 1 {
		t.Errorf("Expected 1 document matching checksum, got %d", filterResp.Count)
	}

	// 4. Attempt to consume identical file directly -> must return ErrDuplicateDocument
	tmpDup, err := os.CreateTemp("", "dedup-dup-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp duplicate: %v", err)
	}
	defer os.Remove(tmpDup.Name())
	_, _ = tmpDup.WriteString(docContent)
	_ = tmpDup.Close()

	dupDocID, err := consumerObj.ConsumeFile(tmpDup.Name(), "electricity-copy.txt", nil)
	if err == nil {
		t.Fatalf("Expected ConsumeFile to return error on duplicate file, got nil!")
	}
	if dupDocID != docID {
		t.Errorf("Expected returned duplicate ID %d, got %d", docID, dupDocID)
	}

	var dupErr *consumer.ErrDuplicateDocument
	if !errors.As(err, &dupErr) {
		t.Fatalf("Expected error to be of type *consumer.ErrDuplicateDocument, got %T: %v", err, err)
	}
	expectedMsg := fmt.Sprintf("Not consuming electricity-copy.txt: It is a duplicate of electricity-january (#%d)", docID)
	if dupErr.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, dupErr.Error())
	}

	// 5. Test queue handling of duplicate: must save related_document_id on FAILURE
	taskID := queueObj.Enqueue("Consume document", "electricity-copy.txt", tmpDup.Name(), func(tid string) (int, error) {
		return consumerObj.ConsumeFile(tmpDup.Name(), "electricity-copy.txt", nil)
	})

	// Wait for queue worker to process
	time.Sleep(200 * time.Millisecond)

	var taskStatus, taskResult string
	var relatedDocID *int
	err = db.QueryRow("SELECT status, result, related_document_id FROM tasks WHERE task_id = ?", taskID).Scan(&taskStatus, &taskResult, &relatedDocID)
	if err != nil {
		t.Fatalf("Failed to query task status: %v", err)
	}
	if taskStatus != "FAILURE" {
		t.Errorf("Expected task status FAILURE on duplicate, got %s", taskStatus)
	}
	if relatedDocID == nil || *relatedDocID != docID {
		t.Errorf("Expected related_document_id %d on duplicate task failure, got %v", docID, relatedDocID)
	}
	if !strings.Contains(taskResult, "It is a duplicate of") {
		t.Errorf("Expected task result to explain duplicate, got %s", taskResult)
	}
}



