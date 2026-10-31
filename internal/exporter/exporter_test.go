package exporter

import (
	"os"
	"path/filepath"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestImportWithDryRun(t *testing.T) {
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

	// Create an export directory with mock manifest and files
	exportDir := filepath.Join(tmpDir, "mock_export")
	_ = os.MkdirAll(filepath.Join(exportDir, "originals"), 0755)

	// Write mock original file
	mockDocContent := "Invoice 12345 total 100 EUR"
	mockDocPath := filepath.Join(exportDir, "originals", "0000001.pdf")
	if err := os.WriteFile(mockDocPath, []byte(mockDocContent), 0644); err != nil {
		t.Fatalf("failed writing mock doc: %v", err)
	}
	checksum, _ := calculateChecksum(mockDocPath)

	manifestJSON := `[
		{
			"model": "documents.tag",
			"pk": 1,
			"fields": {
				"name": "Finance",
				"slug": "finance",
				"color": "#ff0000",
				"text_color": "#ffffff",
				"match": "invoice",
				"matching_algorithm": 1,
				"is_insensitive": true,
				"is_inbox_tag": false
			}
		},
		{
			"model": "documents.correspondent",
			"pk": 1,
			"fields": {
				"name": "Acme Corp",
				"slug": "acme-corp",
				"match": "acme",
				"matching_algorithm": 1,
				"is_insensitive": true
			}
		},
		{
			"model": "documents.document",
			"pk": 1,
			"fields": {
				"title": "Acme Invoice",
				"content": "Invoice 12345 total 100 EUR",
				"original_filename": "invoice.pdf",
				"checksum": "` + checksum + `",
				"mime_type": "application/pdf",
				"page_count": 1,
				"correspondent": 1,
				"tags": [1]
			}
		}
	]`
	if err := os.WriteFile(filepath.Join(exportDir, "manifest.json"), []byte(manifestJSON), 0644); err != nil {
		t.Fatalf("failed writing manifest.json: %v", err)
	}

	// 1. Run simulation (dryRun == true)
	report, err := ImportWithDryRun(db, cfg, exportDir, true)
	if err != nil {
		t.Fatalf("ImportWithDryRun (dryRun) failed: %v", err)
	}
	if !report.DryRun {
		t.Fatalf("expected DryRun = true")
	}
	if report.TotalItems != 3 {
		t.Fatalf("expected 3 total items, got %d", report.TotalItems)
	}
	if report.DocumentsCount != 1 || report.TagsCount != 1 || report.CorrespondentsCount != 1 {
		t.Fatalf("counts mismatch: docs=%d tags=%d corrs=%d", report.DocumentsCount, report.TagsCount, report.CorrespondentsCount)
	}
	if len(report.MissingFiles) > 0 {
		t.Fatalf("expected 0 missing files, got %v", report.MissingFiles)
	}
	if len(report.ChecksumErrors) > 0 {
		t.Fatalf("expected 0 checksum errors, got %v", report.ChecksumErrors)
	}

	// Ensure database was NOT modified in dry-run
	var docCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount); err != nil || docCount != 0 {
		t.Fatalf("expected 0 documents in DB after dry run, got %d", docCount)
	}

	// 2. Run actual import (dryRun == false)
	realReport, err := ImportWithDryRun(db, cfg, exportDir, false)
	if err != nil {
		t.Fatalf("ImportWithDryRun (real) failed: %v", err)
	}
	if realReport.DryRun {
		t.Fatalf("expected DryRun = false")
	}

	// Verify database was populated
	if err := db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount); err != nil || docCount != 1 {
		t.Fatalf("expected 1 document in DB after real import, got %d", docCount)
	}
	var tagCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM tags WHERE name = 'Finance'").Scan(&tagCount); err != nil || tagCount != 1 {
		t.Fatalf("expected 1 tag 'Finance' in DB, got %d", tagCount)
	}
}
