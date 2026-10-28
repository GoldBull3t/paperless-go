package consumer

import (
	"regexp"
	"strconv"
	"strings"
)

// DetectASNFromBarcode extracts an Archive Serial Number (ASN) from barcode or OCR text
// given a configured prefix (e.g. "ASN" or "ASN-").
func DetectASNFromBarcode(text, prefix string) *int {
	if prefix == "" {
		prefix = "ASN"
	}
	escaped := regexp.QuoteMeta(prefix)
	// Match prefix followed by optional separator and 1-9 digits
	re := regexp.MustCompile(`(?i)` + escaped + `[-_:\s]?(\d+)`)
	matches := re.FindStringSubmatch(text)
	if len(matches) > 1 {
		if val, err := strconv.Atoi(matches[1]); err == nil && val > 0 {
			return &val
		}
	}
	return nil
}

// DetectSeparatorBarcode returns true if the text contains a patch/separator barcode value
// like "PATCHT", "---SEPARATOR---", or the configured barcode separation string.
func DetectSeparatorBarcode(text, separatorString string) bool {
	if separatorString == "" {
		separatorString = "PATCHT"
	}
	upperText := strings.ToUpper(text)
	upperSep := strings.ToUpper(separatorString)
	if strings.Contains(upperText, upperSep) {
		return true
	}
	// Also check common patch code values
	if strings.Contains(upperText, "PATCH T") || strings.Contains(upperText, "PATCH-T") || strings.Contains(upperText, "DOC_SEP") {
		return true
	}
	return false
}

// BarcodeResult represents detected barcodes and metadata on a document page
type BarcodeResult struct {
	Page        int
	Value       string
	IsASN       bool
	ASNValue    *int
	IsSeparator bool
}

// ParseBarcodesFromPageText analyzes text lines for barcode indicators
func ParseBarcodesFromPageText(pageNumber int, text, asnPrefix, sepString string) []BarcodeResult {
	var results []BarcodeResult
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		isSep := DetectSeparatorBarcode(trimmed, sepString)
		asn := DetectASNFromBarcode(trimmed, asnPrefix)
		if isSep || asn != nil {
			results = append(results, BarcodeResult{
				Page:        pageNumber,
				Value:       trimmed,
				IsASN:       asn != nil,
				ASNValue:    asn,
				IsSeparator: isSep,
			})
		}
	}
	return results
}
