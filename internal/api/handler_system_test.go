package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestSystemEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sys_test_*")
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

	h := NewSystemHandler(db, cfg)

	// 1. Test SystemStatus (/api/system/status/)
	reqStatus := httptest.NewRequest("GET", "/api/system/status/", nil)
	wStatus := httptest.NewRecorder()
	h.SystemStatus(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SystemStatus, got %d: %s", wStatus.Code, wStatus.Body.String())
	}

	var statusMap map[string]interface{}
	if err := json.Unmarshal(wStatus.Body.Bytes(), &statusMap); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}
	if statusMap["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %v", statusMap["status"])
	}

	// 2. Test PrometheusMetrics (/metrics)
	reqMetrics := httptest.NewRequest("GET", "/metrics", nil)
	wMetrics := httptest.NewRecorder()
	h.PrometheusMetrics(wMetrics, reqMetrics)

	if wMetrics.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from PrometheusMetrics, got %d: %s", wMetrics.Code, wMetrics.Body.String())
	}
	bodyStr := wMetrics.Body.String()
	if !strings.Contains(bodyStr, "paperless_go_info") {
		t.Errorf("expected metrics to contain 'paperless_go_info', got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "paperless_go_memory_alloc_bytes") {
		t.Errorf("expected metrics to contain 'paperless_go_memory_alloc_bytes', got: %s", bodyStr)
	}

	// 3. Test SystemDiagnostics (/api/system/diagnostics/)
	reqDiag := httptest.NewRequest("GET", "/api/system/diagnostics/", nil)
	wDiag := httptest.NewRecorder()
	h.SystemDiagnostics(wDiag, reqDiag)

	if wDiag.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SystemDiagnostics, got %d: %s", wDiag.Code, wDiag.Body.String())
	}
}
