package api

import (
	"encoding/json"
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

func TestVisualDuplicate_PerceptualHashAPI(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_phash.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
	c := consumer.NewConsumer(db, cfg)
	q := queue.NewQueue(db, 2)
	defer q.Close()

	docsH := NewDocumentsHandler(db, cfg, c, q, nil)

	r := chi.NewRouter()
	r.Get("/api/documents/check_duplicate/", docsH.CheckDuplicate)

	// Insert existing document with a known pHash
	samplePHash := "0000ffff0000ffff"
	_, err = db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type, phash)
		VALUES ('Original Invoice', 'Sample invoice text', 'invoice.pdf', 'chk_abc123', 'application/pdf', ?)
	`, samplePHash)
	if err != nil {
		t.Fatalf("Failed inserting document: %v", err)
	}

	// 1. Query with identical pHash (distance 0)
	req1 := httptest.NewRequest("GET", "/api/documents/check_duplicate/?phash="+samplePHash, nil)
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr1.Code)
	}
	var resp1 map[string]interface{}
	_ = json.Unmarshal(rr1.Body.Bytes(), &resp1)
	if resp1["duplicate"] != true || resp1["visual_duplicate"] != true {
		t.Errorf("Expected visual duplicate detection, got %+v", resp1)
	}

	// 2. Query with slightly degraded pHash (Hamming distance = 1 bit flipped)
	// Flipping 1 bit: "0000ffff0000ffff" -> "0000ffff0000fffe"
	nearPHash := "0000ffff0000fffe"
	req2 := httptest.NewRequest("GET", "/api/documents/check_duplicate/?phash="+nearPHash, nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)

	var resp2 map[string]interface{}
	_ = json.Unmarshal(rr2.Body.Bytes(), &resp2)
	if resp2["duplicate"] != true || resp2["visual_duplicate"] != true {
		t.Errorf("Expected visual duplicate detection with distance 1, got %+v", resp2)
	}

	// 3. Query with completely different pHash (distance >> 5)
	diffPHash := "ffff0000ffff0000"
	req3 := httptest.NewRequest("GET", "/api/documents/check_duplicate/?phash="+diffPHash, nil)
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)

	var resp3 map[string]interface{}
	_ = json.Unmarshal(rr3.Body.Bytes(), &resp3)
	if resp3["duplicate"] != false {
		t.Errorf("Expected duplicate=false for different image, got %+v", resp3)
	}
}

