package consumer

import (
	"archive/zip"
	"os"
	"strings"
	"testing"
)

func TestExtractOfficeTextDocx(t *testing.T) {
	// Create a mock .docx file (which is a zip file with word/document.xml)
	tmpFile, err := os.CreateTemp("", "test-doc-*.docx")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	zw := zip.NewWriter(tmpFile)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("Failed to create entry in zip: %v", err)
	}

	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:r>
        <w:t>Invoice for Consulting Services</w:t>
      </w:r>
    </w:p>
    <w:p>
      <w:r>
        <w:t>Amount due: 500 EUR</w:t>
      </w:r>
    </w:p>
  </w:body>
</w:document>`

	_, _ = w.Write([]byte(docXML))
	_ = zw.Close()
	_ = tmpFile.Close()

	if !IsOfficeDocument(tmpFile.Name(), "") {
		t.Errorf("Expected IsOfficeDocument to return true for .docx")
	}

	text, err := ExtractOfficeText(tmpFile.Name())
	if err != nil {
		t.Fatalf("ExtractOfficeText failed: %v", err)
	}

	if !strings.Contains(text, "Invoice for Consulting Services") || !strings.Contains(text, "Amount due: 500 EUR") {
		t.Errorf("Unexpected extracted text: %q", text)
	}
}

func TestExtractOfficeTextXlsx(t *testing.T) {
	// Create a mock .xlsx file (zip with xl/sharedStrings.xml)
	tmpFile, err := os.CreateTemp("", "test-sheet-*.xlsx")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	zw := zip.NewWriter(tmpFile)
	w, err := zw.Create("xl/sharedStrings.xml")
	if err != nil {
		t.Fatalf("Failed to create entry in zip: %v", err)
	}

	sheetXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <si><t>Q1 Budget</t></si>
  <si><t>Total Revenue</t></si>
  <si><t>450000</t></si>
</sst>`

	_, _ = w.Write([]byte(sheetXML))
	_ = zw.Close()
	_ = tmpFile.Close()

	if !IsOfficeDocument(tmpFile.Name(), "") {
		t.Errorf("Expected IsOfficeDocument to return true for .xlsx")
	}

	text, err := ExtractOfficeText(tmpFile.Name())
	if err != nil {
		t.Fatalf("ExtractOfficeText failed: %v", err)
	}

	if !strings.Contains(text, "Q1 Budget") || !strings.Contains(text, "Total Revenue") {
		t.Errorf("Unexpected extracted text from spreadsheet: %q", text)
	}
}

