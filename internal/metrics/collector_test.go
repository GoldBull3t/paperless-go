package metrics

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestMetrics_PrometheusFormat(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "metrics_test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{DatabasePath: dbPath}
	c := NewCollector(db, cfg)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()

	c.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()
	expectedMetrics := []string{
		"paperless_uptime_seconds",
		"paperless_documents_total{type=\"active\"}",
		"paperless_queue_tasks_total{status=\"success\"}",
		"paperless_queue_pending_tasks",
		"paperless_mail_processed_total",
		"paperless_goroutines",
		"paperless_mem_alloc_bytes",
		"paperless_db_size_bytes",
	}

	for _, m := range expectedMetrics {
		if !strings.Contains(body, m) {
			t.Errorf("Missing expected metric: %s in output:\n%s", m, body)
		}
	}
}

