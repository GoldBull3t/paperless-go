package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/crypto"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
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

func TestVault_AdvancedEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_adv_vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir}
	originalsDir := filepath.Join(tmpDir, "documents", "originals")
	_ = os.MkdirAll(originalsDir, 0755)

	vm := vault.NewVaultManager(db, tmpDir)
	vaultH := NewVaultHandler(vm, tmpDir)
	docsH := NewDocumentsHandler(db, cfg, nil, nil, nil)
	docsH.SetVaultManager(vm)

	adminUser := &models.User{ID: 1, Username: "admin", IsSuperuser: true}
	ctxWithAdmin := context.WithValue(context.Background(), UserContextKey, adminUser)

	plainText := "TOP SECRET GOVERNMENT CLEARANCE DOCUMENT #4432"
	h := sha256.Sum256([]byte(plainText))
	checksum := hex.EncodeToString(h[:])

	// Insert Doc 1
	res, err := db.Exec("INSERT INTO documents (title, original_filename, checksum, mime_type) VALUES ('Secret Contract', 'secret.pdf', ?, 'application/pdf')", checksum)
	if err != nil {
		t.Fatal(err)
	}
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	docPath := filepath.Join(originalsDir, fmt.Sprintf("%07d.pdf", docID))
	_ = os.WriteFile(docPath, []byte(plainText), 0644)

	// 1. Setup Vault & get Recovery Key
	setupPayload := `{"master_password": "InitialMasterPass2026!", "auto_lock_minutes": 15}`
	reqSetup := httptest.NewRequest("POST", "/api/vault/setup/", bytes.NewBufferString(setupPayload)).WithContext(ctxWithAdmin)
	wSetup := httptest.NewRecorder()
	vaultH.Setup(wSetup, reqSetup)

	if wSetup.Code != http.StatusOK {
		t.Fatalf("Setup failed: %d - %s", wSetup.Code, wSetup.Body.String())
	}

	var setupResp map[string]interface{}
	_ = json.Unmarshal(wSetup.Body.Bytes(), &setupResp)
	recoveryKey, ok := setupResp["recovery_key"].(string)
	if !ok || len(recoveryKey) == 0 {
		t.Fatalf("Expected recovery_key in setup response: %v", setupResp)
	}

	// 2. Toggle Document to Vault
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", docID))
	reqToggle := httptest.NewRequest("POST", fmt.Sprintf("/api/documents/%d/vault/", docID), nil).WithContext(context.WithValue(ctxWithAdmin, chi.RouteCtxKey, rctx))
	wToggle := httptest.NewRecorder()
	vaultH.ToggleDocumentVault(wToggle, reqToggle)

	if wToggle.Code != http.StatusOK {
		t.Fatalf("ToggleDocumentVault failed: %d - %s", wToggle.Code, wToggle.Body.String())
	}

	// Verify file on disk is physically encrypted with Vault Magic Header
	isEnc, _ := crypto.IsVaultEncryptedFile(docPath)
	if !isEnc {
		t.Fatalf("Expected file to be encrypted with PPLX:VAULT:1")
	}

	// 3. Lock Vault -> Download & Preview must return 423 Locked
	reqLock := httptest.NewRequest("POST", "/api/vault/lock/", nil).WithContext(ctxWithAdmin)
	wLock := httptest.NewRecorder()
	vaultH.Lock(wLock, reqLock)

	reqDl := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/download/", docID), nil).WithContext(context.WithValue(ctxWithAdmin, chi.RouteCtxKey, rctx))
	wDl := httptest.NewRecorder()
	docsH.Download(wDl, reqDl)
	if wDl.Code != http.StatusLocked {
		t.Errorf("Expected 423 StatusLocked on download when vault is locked, got: %d", wDl.Code)
	}

	reqPrev := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/preview/", docID), nil).WithContext(context.WithValue(ctxWithAdmin, chi.RouteCtxKey, rctx))
	wPrev := httptest.NewRecorder()
	docsH.Preview(wPrev, reqPrev)
	if wPrev.Code != http.StatusLocked {
		t.Errorf("Expected 423 StatusLocked on preview when vault is locked, got: %d", wPrev.Code)
	}

	// 4. Change Master Password
	chgPayload := `{"old_master_password": "InitialMasterPass2026!", "new_master_password": "NewRotatedMasterPass2027!"}`
	reqChg := httptest.NewRequest("POST", "/api/vault/change_password/", bytes.NewBufferString(chgPayload)).WithContext(ctxWithAdmin)
	wChg := httptest.NewRecorder()
	vaultH.ChangePassword(wChg, reqChg)
	if wChg.Code != http.StatusOK {
		t.Fatalf("ChangePassword failed: %d - %s", wChg.Code, wChg.Body.String())
	}

	// 5. Unlock with New Password and Download Document
	unlockPayload := `{"master_password": "NewRotatedMasterPass2027!"}`
	reqUnl := httptest.NewRequest("POST", "/api/vault/unlock/", bytes.NewBufferString(unlockPayload)).WithContext(ctxWithAdmin)
	wUnl := httptest.NewRecorder()
	vaultH.Unlock(wUnl, reqUnl)
	if wUnl.Code != http.StatusOK {
		t.Fatalf("Unlock with new password failed: %d - %s", wUnl.Code, wUnl.Body.String())
	}

	wDl2 := httptest.NewRecorder()
	docsH.Download(wDl2, reqDl)
	if wDl2.Code != http.StatusOK {
		t.Fatalf("Download after unlock failed: %d - %s", wDl2.Code, wDl2.Body.String())
	}
	if wDl2.Body.String() != plainText {
		t.Errorf("Decrypted download content mismatch: got %q", wDl2.Body.String())
	}

	// 6. Recover Vault with Emergency Recovery Key
	vm.Lock()
	recPayload := fmt.Sprintf(`{"recovery_key": %q, "new_master_password": "EmergencyRecoveredPass2028!"}`, recoveryKey)
	reqRec := httptest.NewRequest("POST", "/api/vault/recover/", bytes.NewBufferString(recPayload)).WithContext(ctxWithAdmin)
	wRec := httptest.NewRecorder()
	vaultH.Recover(wRec, reqRec)
	if wRec.Code != http.StatusOK {
		t.Fatalf("Recover failed: %d - %s", wRec.Code, wRec.Body.String())
	}

	// 7. Rotate VMEK Key
	rotPayload := `{"master_password": "EmergencyRecoveredPass2028!"}`
	reqRot := httptest.NewRequest("POST", "/api/vault/rotate_key/", bytes.NewBufferString(rotPayload)).WithContext(ctxWithAdmin)
	wRot := httptest.NewRecorder()
	vaultH.RotateKey(wRot, reqRot)
	if wRot.Code != http.StatusOK {
		t.Fatalf("RotateKey failed: %d - %s", wRot.Code, wRot.Body.String())
	}

	var rotResp map[string]interface{}
	_ = json.Unmarshal(wRot.Body.Bytes(), &rotResp)
	if rotResp["reencrypted_documents"].(float64) != 1 {
		t.Errorf("Expected reencrypted_documents=1, got: %v", rotResp)
	}

	// Document still decrypts flawlessly with the new rotated VMEK
	wDl3 := httptest.NewRecorder()
	docsH.Download(wDl3, reqDl)
	if wDl3.Code != http.StatusOK || wDl3.Body.String() != plainText {
		t.Fatalf("Download failed after VMEK key rotation: %d, body=%q", wDl3.Code, wDl3.Body.String())
	}
}
