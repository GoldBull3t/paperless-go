package consumer

import (
	"strings"
	"testing"
)

func TestValidateAttachment_SecurityControls(t *testing.T) {
	// 1. Rejects dangerous executable extensions
	_, err := ValidateAttachment("trojan.exe", []byte("MZ executable payload"))
	if err == nil || !strings.Contains(err.Error(), "security violation") {
		t.Errorf("Expected executable to be blocked, got: %v", err)
	}

	_, err = ValidateAttachment("script.sh", []byte("#!/bin/bash\nrm -rf /"))
	if err == nil || !strings.Contains(err.Error(), "security violation") {
		t.Errorf("Expected script to be blocked, got: %v", err)
	}

	// 2. Rejects double extension tricks
	_, err = ValidateAttachment("fatura.exe.pdf", []byte("%PDF-1.4 mock"))
	if err == nil || !strings.Contains(err.Error(), "double extension") {
		t.Errorf("Expected double extension to be blocked, got: %v", err)
	}

	// 3. Sanitizes path traversal attempts
	clean, err := ValidateAttachment("../../etc/secret_document.pdf", []byte("%PDF-1.4 payload"))
	if err != nil {
		t.Fatalf("Expected valid PDF to pass, got: %v", err)
	}
	if clean != "secret_document.pdf" {
		t.Errorf("Expected sanitized filename 'secret_document.pdf', got %q", clean)
	}

	// 4. Magic bytes validation: claims to be PDF but contains text
	_, err = ValidateAttachment("invoice.pdf", []byte("This is plain text pretending to be PDF"))
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Errorf("Expected signature mismatch for fake PDF, got: %v", err)
	}

	// 5. Valid PDF passes
	validPDF := []byte("%PDF-1.4 valid content")
	clean, err = ValidateAttachment("genuine_invoice.pdf", validPDF)
	if err != nil || clean != "genuine_invoice.pdf" {
		t.Errorf("Expected genuine PDF to pass, got clean=%q, err=%v", clean, err)
	}

	// 6. Valid PNG passes
	validPNG := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR...")
	clean, err = ValidateAttachment("receipt.png", validPNG)
	if err != nil || clean != "receipt.png" {
		t.Errorf("Expected genuine PNG to pass, got clean=%q, err=%v", clean, err)
	}
}

func TestMatchesSenderWhitelist(t *testing.T) {
	tests := []struct {
		name     string
		sender   string
		pattern  string
		expected bool
	}{
		{
			name:     "Blank pattern allows all",
			sender:   "anyone@example.com",
			pattern:  "",
			expected: true,
		},
		{
			name:     "Exact match",
			sender:   "invoices@supplier.com",
			pattern:  "invoices@supplier.com",
			expected: true,
		},
		{
			name:     "RFC 5322 formatted sender name with email",
			sender:   "Supplier Billing Dept <invoices@supplier.com>",
			pattern:  "invoices@supplier.com",
			expected: true,
		},
		{
			name:     "Domain wildcard",
			sender:   "receipts@accounting.company.com",
			pattern:  "@company.com, @supplier.com",
			expected: false,
		},
		{
			name:     "Domain match",
			sender:   "user@supplier.com",
			pattern:  "@supplier.com",
			expected: true,
		},
		{
			name:     "Unauthorized sender rejected",
			sender:   "attacker@phishing-attack.net",
			pattern:  "invoices@supplier.com, @trusted-domain.org",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MatchesSenderWhitelist(tc.sender, tc.pattern)
			if got != tc.expected {
				t.Errorf("MatchesSenderWhitelist(%q, %q) = %v, want %v", tc.sender, tc.pattern, got, tc.expected)
			}
		})
	}
}

