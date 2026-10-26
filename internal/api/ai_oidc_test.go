package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"paperless-go/internal/ai"
	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

// MockAIProvider for tests
type MockAIProvider struct{}

func (m *MockAIProvider) Name() string { return "MockAI" }

func (m *MockAIProvider) Chat(ctx context.Context, systemPrompt string, messages []ai.ChatMessage) (string, error) {
	lastMsg := messages[len(messages)-1].Content
	return "Mock AI response for query: " + lastMsg, nil
}

func (m *MockAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var res [][]float32
	for range texts {
		res = append(res, []float32{0.5, 0.5, 0.0})
	}
	return res, nil
}

func setupAITestEnv(t *testing.T) (*chiMuxWrapper, *database.DB, *consumer.Consumer, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_ai.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	cfg := &config.Config{
		DataDir:          tmpDir,
		MediaDir:         tmpDir,
		ConsumeDir:       tmpDir,
		StaticDir:        tmpDir,
		SecretKey:        "test-secret",
		AIEnabled:        true,
		AIProvider:       "mock",
		OIDCEnabled:      true,
		OIDCClientID:     "test-client",
		OIDCClientSecret: "test-secret",
		OIDCAuthURL:      "http://auth.example.com/login",
		OIDCTokenURL:     "http://auth.example.com/token",
		OIDCUserInfoURL:  "http://auth.example.com/userinfo",
		OIDCRedirectURI:  "http://localhost:8000/api/auth/oidc/callback",
		OIDCAutoCreate:   true,
	}

	authSvc := auth.NewService(db, cfg)
	_ = authSvc.EnsureDefaultAdmin("admin", "admin")

	q := queue.NewQueue(db, 1)
	c := consumer.NewConsumer(db, cfg)

	mockProv := &MockAIProvider{}
	c.SetAI(mockProv, ai.NewRAGService(mockProv), ai.NewExtractorService(mockProv))

	wsHub := NewWebSocketHub()
	router := NewRouter(cfg, db, authSvc, q, c, wsHub)

	// Get auth token
	var token string
	_ = db.QueryRow("SELECT auth_token FROM users WHERE username = 'admin'").Scan(&token)

	return &chiMuxWrapper{Handler: router}, db, c, token
}

func TestAIChatEndpoint(t *testing.T) {
	app, db, _, token := setupAITestEnv(t)
	defer db.Close()

	// Insert test document
	res, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Invoice EDP 2026', 'Total amount payable: 85.50 EUR for electricity', 'invoice.pdf', 'chk123', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	docID64, _ := res.LastInsertId()
	docID := int(docID64)
	_, _ = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (?, 'Invoice EDP 2026', 'Total amount payable: 85.50 EUR for electricity')", docID)

	// Call /api/documents/chat/
	chatPayload := map[string]interface{}{
		"message":   "What is the total amount?",
		"documents": []int{docID},
	}
	body, _ := json.Marshal(chatPayload)
	req := httptest.NewRequest("POST", "/api/documents/chat/", bytes.NewReader(body))
	req.Header.Set("Authorization", "Token "+token)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var chatResp struct {
		Message string `json:"message"`
		Sources []struct {
			ID int `json:"id"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &chatResp); err != nil {
		t.Fatalf("Failed to decode chat response: %v", err)
	}
	if len(chatResp.Sources) == 0 || chatResp.Sources[0].ID != docID {
		t.Fatalf("Expected source ID %d, got %+v", docID, chatResp.Sources)
	}
}

func TestAISummaryEndpoint(t *testing.T) {
	app, db, _, token := setupAITestEnv(t)
	defer db.Close()

	res, _ := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Service Contract', 'This agreement is between Party A and Party B for 12 months', 'contract.pdf', 'chk456', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/summary/", docID), nil)
	req.Header.Set("Authorization", "Token "+token)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sumResp struct {
		ID      int    `json:"id"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sumResp); err != nil {
		t.Fatalf("Failed to parse summary: %v", err)
	}
	if sumResp.ID != docID || sumResp.Summary == "" {
		t.Fatalf("Invalid summary response: %+v", sumResp)
	}
}

func TestSemanticSearchRanking(t *testing.T) {
	app, db, _, token := setupAITestEnv(t)
	defer db.Close()

	// Insert document that does NOT contain the search word "luz", but has semantic embedding
	res, _ := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, created)
		VALUES ('Fatura Eletricidade', 'Consumo mensal de kwh e potencia', 'luz.pdf', 'chk789', 'application/pdf', CURRENT_TIMESTAMP)
	`)
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	// Save embedding vector for docID [0.5, 0.5, 0.0] matching mock embedding
	embJSON, _ := ai.EncodeEmbedding([]float32{0.5, 0.5, 0.0})
	_, _ = db.Exec("INSERT INTO document_embeddings (document_id, embedding_json, model) VALUES (?, ?, 'mock')", docID, embJSON)

	// Search for "luz" (not in text, but will match through vector cosine similarity)
	req := httptest.NewRequest("GET", "/api/search/?query=luz", nil)
	req.Header.Set("Authorization", "Token "+token)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var searchRes struct {
		Documents []struct {
			ID int `json:"id"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searchRes); err != nil {
		t.Fatalf("Failed to unmarshal search response: %v", err)
	}
	if len(searchRes.Documents) == 0 || searchRes.Documents[0].ID != docID {
		t.Fatalf("Expected semantic search hit for docID %d, got %+v", docID, searchRes.Documents)
	}
}

func TestOIDCLoginRedirect(t *testing.T) {
	app, db, _, _ := setupAITestEnv(t)
	defer db.Close()

	req := httptest.NewRequest("GET", "/api/auth/oidc/login?next=/dashboard", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Expected 302 redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if location == "" {
		t.Fatal("Expected Location header in OIDC redirect")
	}

	// Verify state cookie is set
	cookies := rec.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "oidc_state" {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("Expected 'oidc_state' cookie to be set")
	}
}
