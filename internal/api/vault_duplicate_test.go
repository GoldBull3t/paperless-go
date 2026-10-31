package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/vault"
)

func TestVaultAndDuplicateAPI(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vault_api_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{
		DataDir:      tmpDir,
		MediaDir:     tmpDir,
		DatabasePath: dbPath,
	}

	vm := vault.NewVaultManager(db)
	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)
	docsH.SetVaultManager(vm)
	vaultH := NewVaultHandler(vm)

	// 1. Insert 2 near-duplicate documents
	doc1Text := "Fatura de Energia Eletrica EDP 2026 Mes de Outubro Total 90 EUR"
	doc2Text := "Fatura de Energia Eletrica EDP 2026 Mes de Novembro Total 90 EUR"

	res1, _ := db.Exec("INSERT INTO documents (title, content, original_filename, checksum, mime_type) VALUES ('EDP Out', ?, 'out.pdf', 'chk1', 'application/pdf')", doc1Text)
	id1, _ := res1.LastInsertId()
	_, _ = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (?, 'EDP Out', ?)", id1, doc1Text)

	res2, _ := db.Exec("INSERT INTO documents (title, content, original_filename, checksum, mime_type) VALUES ('EDP Nov', ?, 'nov.pdf', 'chk2', 'application/pdf')", doc2Text)
	id2, _ := res2.LastInsertId()
	_, _ = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (?, 'EDP Nov', ?)", id2, doc2Text)

	// 2. Test GetDuplicates endpoint
	reqDup := httptest.NewRequest("GET", "/api/documents/1/duplicates/", nil)
	wDup := httptest.NewRecorder()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "1")
	reqDup = reqDup.WithContext(context.WithValue(reqDup.Context(), chi.RouteCtxKey, rctx))

	docsH.GetDuplicates(wDup, reqDup)
	if wDup.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GetDuplicates, got %d: %s", wDup.Code, wDup.Body.String())
	}

	// 3. Test Vault Setup API
	setupBody := bytes.NewBufferString(`{"master_password": "MyMasterKey2026!", "auto_lock_minutes": 15}`)
	reqSetup := httptest.NewRequest("POST", "/api/vault/setup/", setupBody)
	wSetup := httptest.NewRecorder()
	vaultH.Setup(wSetup, reqSetup)

	if wSetup.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from Vault Setup, got %d: %s", wSetup.Code, wSetup.Body.String())
	}

	// 4. Test Vault Status API
	reqStatus := httptest.NewRequest("GET", "/api/vault/status/", nil)
	wStatus := httptest.NewRecorder()
	vaultH.Status(wStatus, reqStatus)

	var stMap map[string]interface{}
	_ = json.Unmarshal(wStatus.Body.Bytes(), &stMap)
	if stMap["enabled"] != true {
		t.Errorf("expected vault enabled: true, got %v", stMap["enabled"])
	}
	if stMap["unlocked"] != true {
		t.Errorf("expected vault unlocked: true right after setup, got %v", stMap["unlocked"])
	}

	// 5. Toggle Document Vault protection
	reqToggle := httptest.NewRequest("POST", "/api/documents/1/vault/", nil)
	wToggle := httptest.NewRecorder()
	reqToggle = reqToggle.WithContext(context.WithValue(reqToggle.Context(), chi.RouteCtxKey, rctx))
	vaultH.ToggleDocumentVault(wToggle, reqToggle)

	if wToggle.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from ToggleDocumentVault, got %d: %s", wToggle.Code, wToggle.Body.String())
	}

	// 6. Lock Vault and test protection
	reqLock := httptest.NewRequest("POST", "/api/vault/lock/", nil)
	wLock := httptest.NewRecorder()
	vaultH.Lock(wLock, reqLock)

	// Document 1 is vault protected and vault is locked -> Get should return 423 Locked
	reqGet := httptest.NewRequest("GET", "/api/documents/1/", nil)
	wGet := httptest.NewRecorder()
	reqGet = reqGet.WithContext(context.WithValue(reqGet.Context(), chi.RouteCtxKey, rctx))
	docsH.Get(wGet, reqGet)

	if wGet.Code != http.StatusLocked {
		t.Errorf("expected 423 StatusLocked when accessing vault protected document, got %d", wGet.Code)
	}

	// 7. Unlock Vault with Master Password
	unlockBody := bytes.NewBufferString(`{"master_password": "MyMasterKey2026!"}`)
	reqUnlock := httptest.NewRequest("POST", "/api/vault/unlock/", unlockBody)
	wUnlock := httptest.NewRecorder()
	vaultH.Unlock(wUnlock, reqUnlock)

	if wUnlock.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from Vault Unlock, got %d: %s", wUnlock.Code, wUnlock.Body.String())
	}

	// Access document 1 again -> should return 200 OK
	wGet2 := httptest.NewRecorder()
	docsH.Get(wGet2, reqGet)
	if wGet2.Code != http.StatusOK {
		t.Errorf("expected 200 OK after vault unlocked, got %d: %s", wGet2.Code, wGet2.Body.String())
	}
}
