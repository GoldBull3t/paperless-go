package consumer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBarcodeSplitter(t *testing.T) {
	splitter := NewBarcodeSplitter()
	if splitter == nil {
		t.Fatal("NewBarcodeSplitter returned nil")
	}

	tmpDir, err := os.MkdirTemp("", "splitter_test_*")
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
	pdfPath := filepath.Join(tmpDir, "single_page.pdf")
	if err := os.WriteFile(pdfPath, []byte(minPDF), 0644); err != nil {
		t.Fatal(err)
	}

	// 1-page PDF cannot have separators
	segments, wasSplit, err := splitter.SplitPDFBySeparators(pdfPath, "PATCHT", "ASN")
	if err != nil {
		t.Fatalf("SplitPDFBySeparators failed: %v", err)
	}
	if wasSplit {
		t.Errorf("Expected wasSplit to be false for 1-page PDF")
	}
	if len(segments) != 0 {
		t.Errorf("Expected 0 segments, got %d", len(segments))
	}
}
