package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

func setupConcurrencySuite(t *testing.T) (*database.DB, *config.Config, *DocumentsHandler, *WebSocketHub, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "paperless-concurrency-*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "concurrency.db")
	db, err := database.Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed opening db: %v", err)
	}

	cfg := &config.Config{
		DataDir:  tmpDir,
		MediaDir: tmpDir,
	}
	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "archive"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "documents", "thumbnails"), 0755)

	q := queue.NewQueue(db, 2)
	c := consumer.NewConsumer(db, cfg)
	ws := NewWebSocketHub()
	handler := NewDocumentsHandler(db, cfg, c, q, ws)

	return db, cfg, handler, ws, tmpDir
}

// TestConcurrency_SimultaneousReadsAndWrites valida alto paralelismo com 50 goroutines
// realizando operações simultâneas de leitura, escrita e broadcast de WebSocket.
func TestConcurrency_SimultaneousReadsAndWrites(t *testing.T) {
	db, _, handler, ws, tmpDir := setupConcurrencySuite(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()
	defer ws.Close()

	// Pre-create initial documents
	for i := 1; i <= 10; i++ {
		_, err := db.Exec(`
			INSERT INTO documents (id, title, content, original_filename, checksum, mime_type, page_count)
			VALUES (?, ?, ?, ?, ?, 'application/pdf', 1)
		`, i, fmt.Sprintf("Document %d", i), "Concurrency text content", fmt.Sprintf("file_%d.pdf", i), fmt.Sprintf("chk_%d", i))
		if err != nil {
			t.Fatalf("failed inserting doc %d: %v", i, err)
		}
	}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		gid := g
		go func() {
			defer wg.Done()
			if gid%2 == 0 {
				// Reader
				req := httptest.NewRequest("GET", "/api/documents/", nil)
				rec := httptest.NewRecorder()
				handler.List(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("expected 200 OK for List, got %d", rec.Code)
				}
			} else {
				// Writer (Bulk edit tags or correspondent)
				docID := (gid % 10) + 1
				body, _ := json.Marshal(map[string]interface{}{
					"documents": []int{docID},
					"method":    "set_correspondent",
					"parameters": map[string]interface{}{
						"correspondent": nil,
					},
				})
				req := httptest.NewRequest("POST", "/api/documents/bulk_edit/", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.BulkEdit(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("expected 200 OK for BulkEdit, got %d: %s", rec.Code, rec.Body.String())
				}
			}
			// WebSocket broadcast concurrently
			ws.BroadcastDocumentsDeleted()
		}()
	}

	wg.Wait()
}

// TestTransaction_AtomicRollbackOnFailure comprova que uma transação multi-tabelas
// que falhe reverte todas as modificações anteriores, mantendo integridade atômica.
func TestTransaction_AtomicRollbackOnFailure(t *testing.T) {
	db, _, handler, ws, tmpDir := setupConcurrencySuite(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()
	defer ws.Close()
	_ = handler

	// Insert test document and tag
	_, err := db.Exec(`
		INSERT INTO documents (id, title, content, original_filename, checksum, mime_type)
		VALUES (999, 'Original Title', 'Original Content', 'orig.pdf', 'chk999', 'application/pdf')
	`)
	if err != nil {
		t.Fatalf("failed inserting initial document: %v", err)
	}
	_, err = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (999, 'Original Title', 'Original Content')")
	if err != nil {
		t.Fatalf("failed inserting initial fts: %v", err)
	}

	// Tentar atualizar com custom field inválido que causa erro no parse ou mockando WithTx
	ctx := context.Background()
	txErr := db.WithTx(ctx, func(tx *sql.Tx) error {
		// 1. Modifica documents
		_, err := tx.Exec("UPDATE documents SET title = 'Corrupted Title' WHERE id = 999")
		if err != nil {
			return err
		}
		// 2. Modifica documents_fts
		_, err = tx.Exec("UPDATE documents_fts SET title = 'Corrupted Title' WHERE rowid = 999")
		if err != nil {
			return err
		}
		// 3. Força falha (simulando quebra de constraint)
		return fmt.Errorf("simulated constraint abort in middle of multi-table write")
	})

	if txErr == nil {
		t.Fatal("expected transaction error, got nil")
	}

	// Verificar se o documento e o FTS retornaram intactos
	var title string
	err = db.QueryRow("SELECT title FROM documents WHERE id = 999").Scan(&title)
	if err != nil {
		t.Fatalf("failed reading document: %v", err)
	}
	if title != "Original Title" {
		t.Fatalf("expected atomic rollback to preserve 'Original Title', got '%s'", title)
	}

	var ftsTitle string
	err = db.QueryRow("SELECT title FROM documents_fts WHERE rowid = 999").Scan(&ftsTitle)
	if err != nil {
		t.Fatalf("failed reading documents_fts: %v", err)
	}
	if ftsTitle != "Original Title" {
		t.Fatalf("expected fts atomic rollback to preserve 'Original Title', got '%s'", ftsTitle)
	}
}

// TestConcurrency_WebSocketHubBroadcastStress estressa o hub com conexões concorrentes
func TestConcurrency_WebSocketHubBroadcastStress(t *testing.T) {
	hub := NewWebSocketHub()
	defer hub.Close()

	const readers = 30
	var wg sync.WaitGroup
	wg.Add(readers)

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				hub.BroadcastDocumentsDeleted()
				time.Sleep(1 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
}
