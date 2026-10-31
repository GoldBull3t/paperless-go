package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUIPages_VaultAndScanAndAuth(t *testing.T) {
	router, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. GET /vault
	req := httptest.NewRequest("GET", "/vault", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from /vault, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Cryptographic Vault") || !strings.Contains(body, "Envelope Encryption") {
		t.Errorf("Expected vault page content, got: %s", body[:min(300, len(body))])
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("Expected text/html content-type, got: %s", rec.Header().Get("Content-Type"))
	}

	// 2. GET /en-US/vault
	req = httptest.NewRequest("GET", "/en-US/vault", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from /en-US/vault, got %d", rec.Code)
	}

	// 3. GET /scan
	req = httptest.NewRequest("GET", "/scan", nil)
	rec = router.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from /scan, got %d", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, "eSCL Driverless Network Scanner") || !strings.Contains(body, "scannerSelect") {
		t.Errorf("Expected scanner page content, got: %s", body[:min(300, len(body))])
	}

	// 4. GET /accounts/login/ (unauthenticated)
	secRouter, _, _, secCleanup := setupSecurityTestEnvironment(t, false)
	defer secCleanup()
	req = httptest.NewRequest("GET", "/accounts/login/", nil)
	rec = secRouter.Do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 from /accounts/login/, got %d", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, "/vault") || !strings.Contains(body, "/scan") {
		t.Errorf("Expected footer navigation links in login page, got: %s", body[:min(300, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
