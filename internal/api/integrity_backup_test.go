package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/crypto"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

func setupIntegrityTestEnv(t *testing.T, encryption bool) (*chiMuxWrapper, *database.DB, *consumer.Consumer, *config.Config, string) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_integrity.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	cfg := &config.Config{
		DataDir:          tmpDir,
		MediaDir:         tmpDir,
		ConsumeDir:       tmpDir,
		StaticDir:        tmpDir,
		SecretKey:        "test-super-secret-key-12345",
		EncryptionAtRest: encryption,
		EncryptionKey:    "test-super-secret-key-12345",
	}

	authSvc := auth.NewService(db, cfg)
	_ = authSvc.EnsureDefaultAdmin("admin", "admin")

	q := queue.NewQueue(db, 1)
	c := consumer.NewConsumer(db, cfg)

	wsHub := NewWebSocketHub()
	router := NewRouter(cfg, db, authSvc, q, c, wsHub)

	var token string
	_ = db.QueryRow("SELECT auth_token FROM users WHERE username = 'admin'").Scan(&token)

	return &chiMuxWrapper{Handler: router}, db, c, cfg, token
}

func TestAtomicIngestionAndRollback(t *testing.T) {
	_, db, c, cfg, _ := setupIntegrityTestEnv(t, false)
	defer db.Close()

	// Create valid text document
	tmpFile := filepath.Join(cfg.ConsumeDir, "tax_record.txt")
	_ = os.WriteFile(tmpFile, []byte("Confidential Tax Record 2026"), 0644)

	docID, err := c.ConsumeFile(tmpFile, "tax_record.txt", nil)
	if err != nil {
		t.Fatalf("ConsumeFile failed: %v", err)
	}
	if docID <= 0 {
		t.Fatalf("Expected valid docID > 0, got %d", docID)
	}

	// Verify file exists on disk
	expectedFile := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d.txt", docID))
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("Expected original file to exist at %s: %v", expectedFile, err)
	}

	// Verify SHA-256 matches
	var dbChecksum string
	_ = db.QueryRow("SELECT checksum FROM documents WHERE id = ?", docID).Scan(&dbChecksum)
	diskChecksum, _ := crypto.CalculateSHA256(expectedFile)
	if dbChecksum != diskChecksum {
		t.Fatalf("Checksum mismatch: DB=%s, Disk=%s", dbChecksum, diskChecksum)
	}
}

func TestEncryptionAtRestFlow(t *testing.T) {
	app, db, c, cfg, token := setupIntegrityTestEnv(t, true)
	defer db.Close()

	// Ingest confidential document
	tmpFile := filepath.Join(cfg.ConsumeDir, "bank_statement.txt")
	secretContent := "CONFIDENTIAL BANK STATEMENT: IBAN PT501234567890 BALANCE 50000 EUR"
	_ = os.WriteFile(tmpFile, []byte(secretContent), 0644)

	docID, err := c.ConsumeFile(tmpFile, "bank_statement.txt", nil)
	if err != nil {
		t.Fatalf("ConsumeFile with encryption failed: %v", err)
	}

	// Verify physical file on disk IS ENCRYPTED
	diskFile := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d.txt", docID))
	isEnc, err := crypto.IsEncryptedFile(diskFile)
	if err != nil || !isEnc {
		t.Fatalf("Expected file on disk to be encrypted, got isEnc=%v, err=%v", isEnc, err)
	}

	diskBytes, _ := os.ReadFile(diskFile)
	if bytes.Contains(diskBytes, []byte("CONFIDENTIAL BANK STATEMENT")) {
		t.Fatal("Disk file contains plaintext in violation of Encryption at Rest!")
	}

	// Verify transparent decryption via API download
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/documents/%d/download/", docID), nil)
	req.Header.Set("Authorization", "Token "+token)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on download, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != secretContent {
		t.Fatalf("Decrypted stream mismatch: got %q, expected %q", rec.Body.String(), secretContent)
	}
}

func TestSystemIntegrityCheckEndpoint(t *testing.T) {
	app, db, c, cfg, token := setupIntegrityTestEnv(t, false)
	defer db.Close()

	tmpFile := filepath.Join(cfg.ConsumeDir, "contract.txt")
	_ = os.WriteFile(tmpFile, []byte("Legal Contract Document"), 0644)
	docID, _ := c.ConsumeFile(tmpFile, "contract.txt", nil)

	// Call /api/system/integrity/
	req := httptest.NewRequest("GET", "/api/system/integrity/", nil)
	req.Header.Set("Authorization", "Token "+token)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Healthy        bool `json:"healthy"`
		TotalChecked   int  `json:"total_checked"`
		CorruptedCount int  `json:"corrupted_count"`
		MissingCount   int  `json:"missing_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse integrity response: %v", err)
	}
	if !resp.Healthy || resp.TotalChecked != 1 || resp.CorruptedCount != 0 {
		t.Fatalf("Expected healthy system with 1 doc, got %+v", resp)
	}

	// Now intentionally corrupt the file on disk (simulate bit rot)
	diskFile := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d.txt", docID))
	_ = os.WriteFile(diskFile, []byte("Corrupted corrupted data"), 0644)

	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req)

	var resp2 struct {
		Healthy        bool `json:"healthy"`
		CorruptedCount int  `json:"corrupted_count"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2.Healthy || resp2.CorruptedCount != 1 {
		t.Fatalf("Expected bit rot detection, got %+v", resp2)
	}
}

func TestSystemBackupEndpoint(t *testing.T) {
	app, db, c, cfg, token := setupIntegrityTestEnv(t, false)
	defer db.Close()

	tmpFile := filepath.Join(cfg.ConsumeDir, "receipt.txt")
	_ = os.WriteFile(tmpFile, []byte("Receipt 100 EUR"), 0644)
	_, _ = c.ConsumeFile(tmpFile, "receipt.txt", nil)

	req := httptest.NewRequest("GET", "/api/system/backup/", nil)
	req.Header.Set("Authorization", "Token "+token)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 on backup, got %d: %s", rec.Code, rec.Body.String())
	}

	zipBytes := rec.Body.Bytes()
	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Failed to open backup zip archive: %v", err)
	}

	foundDB := false
	for _, f := range zipReader.File {
		if f.Name == "paperless.db" {
			foundDB = true
			break
		}
	}
	if !foundDB {
		t.Fatal("Backup zip does not contain paperless.db snapshot!")
	}
}

