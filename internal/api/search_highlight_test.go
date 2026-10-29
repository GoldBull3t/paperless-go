package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

func TestFTS5Snippet(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fts_test_*")
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

	// Insert into documents_fts
	_, err = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (1, 'Electric Bill', 'The electricity consumption for November was high')")
	if err != nil {
		t.Fatalf("Failed to insert into documents_fts: %v", err)
	}

	var rowid int
	var snippet string
	err = db.QueryRow("SELECT rowid, snippet(documents_fts, 1, '<mark>', '</mark>', '...', 16) FROM documents_fts WHERE documents_fts MATCH 'electricity'").Scan(&rowid, &snippet)
	if err != nil {
		t.Fatalf("Failed to query snippet: %v", err)
	}

	if !strings.Contains(snippet, "<mark>electricity</mark>") {
		t.Errorf("Expected snippet to contain '<mark>electricity</mark>', got: %s", snippet)
	}
}

func TestPureGoPDFProcessor(t *testing.T) {
	proc := consumer.NewPDFProcessor()
	if proc == nil {
		t.Fatal("NewPDFProcessor returned nil")
	}

	tmpDir, err := os.MkdirTemp("", "pdf_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	minPDF := `%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>
endobj
xref
0 4
0000000000 65535 f 
0000000010 00000 n 
0000000060 00000 n 
0000000117 00000 n 
trailer
<< /Size 4 /Root 1 0 R >>
startxref
192
%%EOF
`
	pdfPath := filepath.Join(tmpDir, "test.pdf")
	if err := os.WriteFile(pdfPath, []byte(minPDF), 0644); err != nil {
		t.Fatal(err)
	}

	count, err := proc.GetPageCount(pdfPath)
	if err != nil {
		t.Fatalf("GetPageCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 page, got %d", count)
	}

	// Test rotation
	rotatedPath := filepath.Join(tmpDir, "rotated.pdf")
	if err := proc.RotatePDF(pdfPath, rotatedPath, 90, nil); err != nil {
		t.Fatalf("RotatePDF failed: %v", err)
	}

	rCount, err := proc.GetPageCount(rotatedPath)
	if err != nil {
		t.Fatalf("GetPageCount on rotated failed: %v", err)
	}
	if rCount != 1 {
		t.Errorf("Expected 1 page in rotated PDF, got %d", rCount)
	}

	// Test merge
	mergedPath := filepath.Join(tmpDir, "merged.pdf")
	if err := proc.MergePDFs([]string{pdfPath, rotatedPath}, mergedPath); err != nil {
		t.Fatalf("MergePDFs failed: %v", err)
	}

	mCount, err := proc.GetPageCount(mergedPath)
	if err != nil {
		t.Fatalf("GetPageCount on merged failed: %v", err)
	}
	if mCount != 2 {
		t.Errorf("Expected 2 pages in merged PDF, got %d", mCount)
	}

	// Test remove page
	removedPath := filepath.Join(tmpDir, "removed.pdf")
	if err := proc.RemovePages(mergedPath, removedPath, []string{"2"}); err != nil {
		t.Fatalf("RemovePages failed: %v", err)
	}
	remCount, err := proc.GetPageCount(removedPath)
	if err != nil {
		t.Fatalf("GetPageCount on removed failed: %v", err)
	}
	if remCount != 1 {
		t.Errorf("Expected 1 page after removing page 2, got %d", remCount)
	}
}

func TestVisualSearchHits(t *testing.T) {
	// 1. Fallback snippet generator
	content := "This is an important contract regarding the electricity supply for the year 2026."
	snippet := GenerateFallbackSnippet(content, "electricity", 10)
	if !strings.Contains(snippet, "<mark>electricity</mark>") {
		t.Errorf("Expected snippet to contain '<mark>electricity</mark>', got: %s", snippet)
	}

	// 2. BuildSearchHits integration
	tmpDir, err := os.MkdirTemp("", "hits_test_*")
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

	_, _ = db.Exec("INSERT INTO documents_fts (rowid, title, content) VALUES (1, 'Energy Bill', 'Monthly electricity bill is 45 euros')")

	doc := &models.Document{
		ID:      1,
		Title:   "Energy Bill",
		Content: "Monthly electricity bill is 45 euros",
	}

	BuildSearchHits(db, []*models.Document{doc}, "electricity")

	if doc.SearchHit == nil {
		t.Fatal("Expected doc.SearchHit to be populated, got nil")
	}

	hit, ok := doc.SearchHit.(*models.SearchHit)
	if !ok {
		t.Fatalf("Expected *models.SearchHit, got %T", doc.SearchHit)
	}

	if len(hit.Highlights) == 0 {
		t.Fatal("Expected highlights to have at least 1 item")
	}

	if !strings.Contains(hit.Highlights[0], "<mark>electricity</mark>") {
		t.Errorf("Expected highlight to contain '<mark>electricity</mark>', got: %s", hit.Highlights[0])
	}
}
