package consumer_test

import (
	"os"
	"path/filepath"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
)

func TestConsumeFileWithFallback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed opening test db: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		MediaDir: filepath.Join(tmpDir, "media"),
		DataDir:  tmpDir,
	}
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "thumbnails"), 0755)

	consumeDir := filepath.Join(tmpDir, "consume")
	_ = os.MkdirAll(consumeDir, 0755)

	c := consumer.NewConsumer(db, cfg)

	// Test 1: First ingestion succeeds
	sampleFile := filepath.Join(tmpDir, "sample.txt")
	sampleContent := []byte("Invoice ABC-987 for Acme Corp")
	if err := os.WriteFile(sampleFile, sampleContent, 0644); err != nil {
		t.Fatalf("failed writing sample file: %v", err)
	}

	docID, err := c.ConsumeFileWithFallback(sampleFile, "sample.txt", consumeDir)
	if err != nil {
		t.Fatalf("expected successful ingestion, got: %v", err)
	}
	if docID <= 0 {
		t.Fatalf("expected docID > 0, got %d", docID)
	}

	// Test 2: Ingesting duplicate file moves copy to .duplicates folder
	sampleFile2 := filepath.Join(tmpDir, "sample2.txt")
	if err := os.WriteFile(sampleFile2, sampleContent, 0644); err != nil {
		t.Fatalf("failed writing sample2 file: %v", err)
	}

	dupDocID, err := c.ConsumeFileWithFallback(sampleFile2, "sample2.txt", consumeDir)
	if err == nil {
		t.Fatalf("expected duplicate error, got nil")
	}
	if dupDocID != docID {
		t.Errorf("expected duplicate docID %d, got %d", docID, dupDocID)
	}

	dupFile := filepath.Join(consumeDir, ".duplicates", "sample2.txt")
	if _, err := os.Stat(dupFile); os.IsNotExist(err) {
		t.Errorf("expected duplicate file to be preserved in %s", dupFile)
	}
}

