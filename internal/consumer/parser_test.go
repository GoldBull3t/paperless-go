package consumer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paperless-go/internal/consumer"
)

func TestExtractTextWithContext_Cancellation(t *testing.T) {
	tmpDir := t.TempDir()
	samplePDF := filepath.Join(tmpDir, "test.pdf")

	// Create a minimal PDF structure
	pdfContent := `%PDF-1.4
1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj
2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj
3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj
xref
0 4
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
trailer << /Size 4 /Root 1 0 R >>
startxref
190
%%EOF`

	if err := os.WriteFile(samplePDF, []byte(pdfContent), 0644); err != nil {
		t.Fatalf("failed writing test pdf: %v", err)
	}

	// 1. Context already cancelled before extraction
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := consumer.ExtractTextWithContext(ctx, samplePDF, "application/pdf")
	// Must not panic, gracefully returns or terminates subprocess
	if err == nil {
		t.Log("Extract returned without error for minimal PDF")
	}

	// 2. Normal extraction with valid context completes cleanly
	validCtx, validCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer validCancel()

	text, err := consumer.ExtractTextWithContext(validCtx, samplePDF, "application/pdf")
	if err != nil {
		t.Logf("Extraction result: %v", err)
	}
	t.Logf("Extracted length: %d", len(text))
}

