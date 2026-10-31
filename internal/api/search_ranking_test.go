package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"paperless-go/internal/api"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
	"paperless-go/internal/queue"
)

func TestBM25SearchRanking_TitleOverContent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed opening test db: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "thumbnails"), 0755)

	docConsumer := consumer.NewConsumer(db, cfg)
	q := queue.NewQueue(db, 1)
	defer q.Close()

	wsHub := api.NewWebSocketHub()
	defer wsHub.Close()

	docHandler := api.NewDocumentsHandler(db, cfg, docConsumer, q, wsHub)

	// Insert Doc 1: Title match ("EDP Comercial") -> should rank #1
	res1, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('EDP Comercial Factura', 'O consumo deste período foi normal.', 'doc1.pdf', 'sum1', 'application/pdf', '2026-01-01 10:00:00')
	`)
	if err != nil {
		t.Fatalf("failed inserting doc1: %v", err)
	}
	doc1ID, _ := res1.LastInsertId()

	// Insert Doc 2: Content-only match ("EDP" only in body) -> should rank #2 despite being newer
	res2, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Relatório Anual de Despesas', 'Inclui pagamentos efetuados à EDP e serviços de telecomunicação.', 'doc2.pdf', 'sum2', 'application/pdf', '2026-02-01 10:00:00')
	`)
	if err != nil {
		t.Fatalf("failed inserting doc2: %v", err)
	}
	doc2ID, _ := res2.LastInsertId()

	// Query /api/documents/?query=EDP&ordering=-score
	req := httptest.NewRequest("GET", "/api/documents/?query=EDP&ordering=-score", nil)
	rec := httptest.NewRecorder()

	docHandler.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Count   int               `json:"count"`
		Results []models.Document `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed parsing response: %v", err)
	}

	if resp.Count != 2 {
		t.Fatalf("expected 2 matching documents, got %d", resp.Count)
	}

	if len(resp.Results) < 2 {
		t.Fatalf("expected 2 results in results slice, got: %+v", resp.Results)
	}

	// Doc 1 (title match) MUST rank ahead of Doc 2 (content match)
	if resp.Results[0].ID != int(doc1ID) {
		t.Errorf("expected top ranked document to be ID %d (Title match), got %d (Title: %s)", doc1ID, resp.Results[0].ID, resp.Results[0].Title)
	}
	if resp.Results[1].ID != int(doc2ID) {
		t.Errorf("expected second ranked document to be ID %d (Content match), got %d (Title: %s)", doc2ID, resp.Results[1].ID, resp.Results[1].Title)
	}
}

func TestSearch_SpecialCharactersSafety(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed opening test db: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
	docConsumer := consumer.NewConsumer(db, cfg)
	q := queue.NewQueue(db, 1)
	defer q.Close()

	wsHub := api.NewWebSocketHub()
	defer wsHub.Close()

	docHandler := api.NewDocumentsHandler(db, cfg, docConsumer, q, wsHub)

	// Test malformed queries with special characters that typically break FTS5
	dangerousQueries := []string{
		`"unclosed quote`,
		`AND OR NOT`,
		`(*&^%$#@!)`,
		`:::***^^^`,
		`"`,
		`"""""`,
	}

	for _, dq := range dangerousQueries {
		reqURL := "/api/documents/?query=" + url.QueryEscape(dq)
		req := httptest.NewRequest("GET", reqURL, nil)
		rec := httptest.NewRecorder()

		docHandler.List(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected HTTP 200 for query %q, got status %d: %s", dq, rec.Code, rec.Body.String())
		}
	}
}
