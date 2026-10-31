package consumer

import (
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
	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestConsumerBatchBarcodeSeparation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_barcode.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		MediaDir:         tmpDir,
		EnableBarcodes:   true,
		BarcodeSeparator: "PATCHT",
		BarcodeEnableASN: true,
		BarcodeASNPrefix: "ASN",
	}

	c := NewConsumer(db, cfg)

	// Insert test tag "Taxes"
	_, _ = db.Exec("INSERT INTO tags (name, slug, color, text_color) VALUES ('Taxes', 'taxes', '#00ff00', '#000000')")

	// 1. Page 1: Content
	p1Path := filepath.Join(tmpDir, "p1.png")
	img1 := image.NewRGBA(image.Rect(0, 0, 200, 200))
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

	// 3. Page 3: Second document with QR code (ASN:5432 TAG:Taxes TITLE:Tax Declaration 2026)
	qrBytes, err := qrcode.Encode("ASN:5432 TAG:Taxes TITLE:Tax Declaration 2026", qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("Failed encoding QR: %v", err)
	}
	p3Path := filepath.Join(tmpDir, "p3.png")
	_ = os.WriteFile(p3Path, qrBytes, 0644)

	// Combine into a 3-page scanned batch PDF
	batchPDF := filepath.Join(tmpDir, "ScannedBatch.pdf")
	err = api.ImportImagesFile([]string{p1Path, p2Path, p3Path}, batchPDF, nil, model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("Failed creating test batch PDF: %v", err)
	}

	// Consume the batch PDF
	docID, err := c.ConsumeFileWithContext(context.Background(), batchPDF, "ScannedBatch.pdf", nil)
	if err != nil {
		t.Fatalf("ConsumeFileWithContext failed: %v", err)
	}
	if docID == 0 {
		t.Fatalf("Expected non-zero document ID returned")
	}

	// Check total documents created in DB (should be 2)
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&count); err != nil || count != 2 {
		t.Fatalf("Expected 2 documents created from batch, got %d (err: %v)", count, err)
	}

	// Verify Part 2 has ASN 5432 and Title "Tax Declaration 2026"
	var p2Title string
	var p2ASN int
	err = db.QueryRow("SELECT title, archive_serial_number FROM documents WHERE archive_serial_number = 5432").Scan(&p2Title, &p2ASN)
	if err != nil {
		t.Fatalf("Failed finding document with ASN 5432: %v", err)
	}
	if p2Title != "Tax Declaration 2026" {
		t.Errorf("Expected title 'Tax Declaration 2026', got %q", p2Title)
	}
}
