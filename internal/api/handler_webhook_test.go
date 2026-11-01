package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

func TestWebhook_SecurityAndIngestion(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_webhook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{
		MediaDir:              tmpDir,
		DataDir:               tmpDir,
		WebhookIngestionToken: "super-secure-webhook-token-9944",
	}
	c := consumer.NewConsumer(db, cfg)
	q := queue.NewQueue(db, 2)
	defer q.Close()

	docsH := NewDocumentsHandler(db, cfg, c, q, nil)

	r := chi.NewRouter()
	r.Post("/api/ingest/webhook/", docsH.PostDocumentWebhook)

	createMultipartReq := func(field, filename string, content []byte, authHeader, xToken string) *http.Request {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile(field, filename)
		_, _ = part.Write(content)
		_ = writer.WriteField("title", "Scanned Receipt")
		_ = writer.Close()

		req := httptest.NewRequest("POST", "/api/ingest/webhook/", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if xToken != "" {
			req.Header.Set("X-Ingestion-Token", xToken)
		}
		return req
	}

	validPDF := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")

	// 1. Missing Token -> 401 Unauthorized
	{
		req := createMultipartReq("document", "doc.pdf", validPDF, "", "")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 Unauthorized for missing token, got %d", rr.Code)
		}
	}

	// 2. Invalid Token -> 401 Unauthorized
	{
		req := createMultipartReq("document", "doc.pdf", validPDF, "Bearer wrong-token", "")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 Unauthorized for invalid token, got %d", rr.Code)
		}
	}

	// 3. Security Check: Dangerous Executable Extension -> 400 Bad Request
	{
		req := createMultipartReq("document", "receipt.exe", []byte("MZ\x90\x00\x03\x00"), "Bearer super-secure-webhook-token-9944", "")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for .exe payload, got %d", rr.Code)
		}
	}

	// 4. Security Check: Double Extension Spoofing -> 400 Bad Request
	{
		req := createMultipartReq("document", "invoice.pdf.bat", []byte("@echo off"), "Bearer super-secure-webhook-token-9944", "")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("Expected 400 Bad Request for double extension, got %d", rr.Code)
		}
	}

	// 5. Success with Bearer Token & field "document"
	{
		req := createMultipartReq("document", "scanned_receipt.pdf", validPDF, "Bearer super-secure-webhook-token-9944", "")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("Expected 201 Created for valid document upload, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}
		if resp["status"] != "queued" || resp["task_id"] == nil || resp["task_id"] == "" {
			t.Errorf("Unexpected response structure: %+v", resp)
		}
	}

	// 6. Success with X-Ingestion-Token & field "file"
	{
		req := createMultipartReq("file", "mobile_capture.pdf", validPDF, "", "super-secure-webhook-token-9944")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("Expected 201 Created for valid file upload with X-Ingestion-Token, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}
		if resp["status"] != "queued" {
			t.Errorf("Expected queued status, got: %+v", resp)
		}
	}

	// 7. Token not configured in config -> 403 Forbidden
	{
		cfgNoToken := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
		docsNoToken := NewDocumentsHandler(db, cfgNoToken, c, q, nil)
		rNoToken := chi.NewRouter()
		rNoToken.Post("/api/ingest/webhook/", docsNoToken.PostDocumentWebhook)

		req := createMultipartReq("document", "doc.pdf", validPDF, "Bearer whatever", "")
		rr := httptest.NewRecorder()
		rNoToken.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("Expected 403 Forbidden when token not configured, got %d", rr.Code)
		}
	}
}

