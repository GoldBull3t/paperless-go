package barcode

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/skip2/go-qrcode"
)

func TestSanitizeBarcodeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Clean simple text",
			input:    "ASN:12345",
			expected: "ASN:12345",
		},
		{
			name:     "Null byte injection",
			input:    "ASN:12345\x00-- DROP TABLE documents;",
			expected: "ASN:12345-- DROP TABLE documents;",
		},
		{
			name:     "Path traversal attempt",
			input:    "../../etc/passwd",
			expected: "etc/passwd",
		},
		{
			name:     "Windows path traversal attempt",
			input:    "..\\..\\Windows\\System32",
			expected: "Windows\\System32",
		},
		{
			name:     "Non-printable control characters",
			input:    "DOC\x01\x02\x03\x1f-PAYLOAD",
			expected: "DOC-PAYLOAD",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeBarcodeText(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeBarcodeText(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestParseBarcodeMetadata(t *testing.T) {
	scanner := NewScanner()

	// 1. Separator barcode
	_, _, _, isSep := scanner.ParseBarcodeMetadata("PATCHT")
	if !isSep {
		t.Errorf("Expected PATCHT to be recognized as separator")
	}

	_, _, _, isSep = scanner.ParseBarcodeMetadata("SPLIT")
	if !isSep {
		t.Errorf("Expected SPLIT to be recognized as separator")
	}

	// 2. ASN barcode
	asn, _, _, _ := scanner.ParseBarcodeMetadata("ASN:4502")
	if asn == nil || *asn != 4502 {
		t.Errorf("Expected ASN 4502, got %v", asn)
	}

	asn, _, _, _ = scanner.ParseBarcodeMetadata("PAPERLESS-ASN:9801")
	if asn == nil || *asn != 9801 {
		t.Errorf("Expected ASN 9801, got %v", asn)
	}

	// 3. Tags and Title
	_, tags, title, _ := scanner.ParseBarcodeMetadata("TAGS:Invoice,Accounting;Tax TITLE:Fiscal Receipt March")
	if len(tags) != 3 {
		t.Errorf("Expected 3 tags, got %d: %v", len(tags), tags)
	}
	if title != "Fiscal Receipt March" {
		t.Errorf("Expected title 'Fiscal Receipt March', got %q", title)
	}
}

func TestScanImage_QRCode(t *testing.T) {
	scanner := NewScanner()

	// Generate QR Code PNG in memory
	qrContent := "ASN:7722 TAG:Finance TITLE:Audited Report"
	pngBytes, err := qrcode.Encode(qrContent, qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("Failed generating QR code: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("Failed decoding PNG: %v", err)
	}

	results, err := scanner.ScanImage(img, 1)
	if err != nil {
		t.Fatalf("ScanImage returned error: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected at least 1 barcode, got 0")
	}

	b := results[0]
	if b.Format != "QR_CODE" {
		t.Errorf("Expected QR_CODE, got %s", b.Format)
	}
	if b.ASN == nil || *b.ASN != 7722 {
		t.Errorf("Expected ASN 7722, got %v", b.ASN)
	}
	if len(b.Tags) == 0 || b.Tags[0] != "Finance" {
		t.Errorf("Expected tag 'Finance', got %v", b.Tags)
	}
	if b.Title != "Audited Report" {
		t.Errorf("Expected title 'Audited Report', got %q", b.Title)
	}
}

func TestScanImage_Code128(t *testing.T) {
	scanner := NewScanner()

	// Generate Code 128 barcode in memory
	writer := oned.NewCode128Writer()
	bitMatrix, err := writer.Encode("PATCHT", gozxing.BarcodeFormat_CODE_128, 300, 100, nil)
	if err != nil {
		t.Fatalf("Failed encoding Code 128: %v", err)
	}

	// Convert BitMatrix to RGBA Image
	width := bitMatrix.GetWidth()
	height := bitMatrix.GetHeight()
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if bitMatrix.Get(x, y) {
				rgba.Set(x, y, color.Black)
			} else {
				rgba.Set(x, y, color.White)
			}
		}
	}

	results, err := scanner.ScanImage(rgba, 1)
	if err != nil {
		t.Fatalf("ScanImage returned error: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected at least 1 barcode decoded, got 0")
	}

	b := results[0]
	if b.Value != "PATCHT" {
		t.Errorf("Expected value 'PATCHT', got %q", b.Value)
	}
	if !b.IsSeparator {
		t.Errorf("Expected Code 128 with PATCHT to be marked as separator")
	}
}

func TestSplitPDFByBarcodes_Trimming(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 3 minimal PNG images
	p1Path := filepath.Join(tmpDir, "p1.png")
	p2Path := filepath.Join(tmpDir, "p2.png")
	p3Path := filepath.Join(tmpDir, "p3.png")

	img1 := image.NewRGBA(image.Rect(0, 0, 100, 100))
	f1, _ := os.Create(p1Path)
	_ = png.Encode(f1, img1)
	_ = f1.Close()

	f2, _ := os.Create(p2Path)
	_ = png.Encode(f2, img1)
	_ = f2.Close()

	f3, _ := os.Create(p3Path)
	_ = png.Encode(f3, img1)
	_ = f3.Close()

	// Create 3-page PDF via pdfcpu ImportImagesFile
	dummyPDF := filepath.Join(tmpDir, "batch_test.pdf")
	err := api.ImportImagesFile([]string{p1Path, p2Path, p3Path}, dummyPDF, nil, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("Failed creating test 3-page PDF: %v", err)
	}

	// Verify page count
	count, err := api.PageCountFile(dummyPDF)
	if err != nil || count != 3 {
		t.Fatalf("Expected 3 pages, got %d (err: %v)", count, err)
	}

	// Verify trimming page 1 and page 2-3 into separate slices
	slice1 := filepath.Join(tmpDir, "slice1.pdf")
	slice2 := filepath.Join(tmpDir, "slice2.pdf")

	if err := api.TrimFile(dummyPDF, slice1, []string{"1"}, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("TrimFile slice1 failed: %v", err)
	}
	if err := api.TrimFile(dummyPDF, slice2, []string{"2-3"}, model.NewDefaultConfiguration()); err != nil {
		t.Fatalf("TrimFile slice2 failed: %v", err)
	}

	c1, _ := api.PageCountFile(slice1)
	c2, _ := api.PageCountFile(slice2)

	if c1 != 1 {
		t.Errorf("Expected slice1 to have 1 page, got %d", c1)
	}
	if c2 != 2 {
		t.Errorf("Expected slice2 to have 2 pages, got %d", c2)
	}
}

func TestSplitPDFByBarcodes_WithSeparators(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Page 1: Normal document page
	p1Path := filepath.Join(tmpDir, "p1.png")
	img1 := image.NewRGBA(image.Rect(0, 0, 300, 300))
	f1, _ := os.Create(p1Path)
	_ = png.Encode(f1, img1)
	_ = f1.Close()

	// 2. Page 2: Separator barcode page (Code 128 PATCHT)
	writer := oned.NewCode128Writer()
	bitMatrix, err := writer.Encode("PATCHT", gozxing.BarcodeFormat_CODE_128, 300, 100, nil)
	if err != nil {
		t.Fatalf("Failed encoding Code 128: %v", err)
	}
	sepImg := image.NewRGBA(image.Rect(0, 0, bitMatrix.GetWidth(), bitMatrix.GetHeight()))
	for y := 0; y < bitMatrix.GetHeight(); y++ {
		for x := 0; x < bitMatrix.GetWidth(); x++ {
			if bitMatrix.Get(x, y) {
				sepImg.Set(x, y, color.Black)
			} else {
				sepImg.Set(x, y, color.White)
			}
		}
	}
	p2Path := filepath.Join(tmpDir, "p2.png")
	f2, _ := os.Create(p2Path)
	_ = png.Encode(f2, sepImg)
	_ = f2.Close()

	// 3. Page 3: Second document with QR code (ASN:9010 TAG:Contract)
	qrBytes, err := qrcode.Encode("ASN:9010 TAG:Contract", qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("Failed encoding QR: %v", err)
	}
	p3Path := filepath.Join(tmpDir, "p3.png")
	_ = os.WriteFile(p3Path, qrBytes, 0644)

	// Combine all 3 pages into a single batch PDF
	batchPDF := filepath.Join(tmpDir, "scanned_batch.pdf")
	err = api.ImportImagesFile([]string{p1Path, p2Path, p3Path}, batchPDF, nil, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("Failed creating test batch PDF: %v", err)
	}

	scanner := NewScanner()
	slices, err := scanner.SplitPDFByBarcodes(context.Background(), batchPDF, tmpDir)
	if err != nil {
		t.Fatalf("SplitPDFByBarcodes failed: %v", err)
	}

	if len(slices) != 2 {
		t.Fatalf("Expected 2 sliced documents, got %d", len(slices))
	}

	// Slice 1: from Page 1 (1 page)
	s1Pages, _ := api.PageCountFile(slices[0].FilePath)
	if s1Pages != 1 {
		t.Errorf("Expected slice 1 to have 1 page, got %d", s1Pages)
	}

	// Slice 2: from Page 3 (1 page, dropped Page 2 separator)
	s2Pages, _ := api.PageCountFile(slices[1].FilePath)
	if s2Pages != 1 {
		t.Errorf("Expected slice 2 to have 1 page, got %d", s2Pages)
	}

	if slices[1].ASN == nil || *slices[1].ASN != 9010 {
		t.Errorf("Expected slice 2 ASN to be 9010, got %v", slices[1].ASN)
	}
	if len(slices[1].Tags) == 0 || slices[1].Tags[0] != "Contract" {
		t.Errorf("Expected slice 2 tag 'Contract', got %v", slices[1].Tags)
	}
}
