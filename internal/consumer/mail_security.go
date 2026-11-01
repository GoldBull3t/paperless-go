package consumer

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// MaxAttachmentSize enforces strict 35MB limit per attachment to prevent disk/OOM exhaustion
	MaxAttachmentSize = 35 * 1024 * 1024
)

var (
	// AllowedExtensions whitelist of document extensions allowed into Paperless
	AllowedExtensions = map[string]bool{
		".pdf":  true,
		".png":  true,
		".jpg":  true,
		".jpeg": true,
		".tiff": true,
		".tif":  true,
		".docx": true,
		".xlsx": true,
		".odt":  true,
		".ods":  true,
		".eml":  true,
		".txt":  true,
	}

	// DangerousExtensions blacklist of dangerous executable and script extensions
	DangerousExtensions = map[string]bool{
		".exe": true, ".bat": true, ".cmd": true, ".sh": true,
		".vbs": true, ".js": true, ".scr": true, ".jar": true,
		".msi": true, ".ps1": true, ".com": true, ".pif": true,
		".reg": true, ".hta": true, ".cpl": true, ".py": true,
	}
)

// SanitizeAttachmentFilename cleans and hardens an untrusted filename from an email attachment
func SanitizeAttachmentFilename(rawName string) string {
	clean := filepath.Base(rawName)
	clean = strings.ReplaceAll(clean, "\x00", "")
	clean = strings.ReplaceAll(clean, "/", "")
	clean = strings.ReplaceAll(clean, "\\", "")
	clean = strings.ReplaceAll(clean, "..", "")
	clean = strings.TrimSpace(clean)
	if clean == "" || clean == "." {
		clean = "attachment"
	}
	return clean
}

// ValidateAttachment performs strict security checks on an email or webhook attachment
func ValidateAttachment(filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("attachment is empty (0 bytes)")
	}

	if len(data) > MaxAttachmentSize {
		return "", fmt.Errorf("attachment size (%d bytes) exceeds strict limit of 35MB", len(data))
	}

	sanitizedName := SanitizeAttachmentFilename(filename)
	ext := strings.ToLower(filepath.Ext(sanitizedName))

	// 1. Check double extension tricks (e.g. invoice.pdf.exe)
	nameWithoutExt := strings.TrimSuffix(sanitizedName, ext)
	secondExt := strings.ToLower(filepath.Ext(nameWithoutExt))
	if secondExt != "" && DangerousExtensions[secondExt] {
		return "", fmt.Errorf("security violation: dangerous double extension detected in filename %q", sanitizedName)
	}

	if DangerousExtensions[ext] {
		return "", fmt.Errorf("security violation: dangerous executable extension %q rejected", ext)
	}

	if !AllowedExtensions[ext] {
		return "", fmt.Errorf("unsupported or unauthorized file extension %q", ext)
	}

	// 2. Magic bytes validation against claimed file type
	switch ext {
	case ".pdf":
		if len(data) < 4 || !bytes.HasPrefix(data, []byte("%PDF")) {
			return "", fmt.Errorf("file signature mismatch: expected valid PDF magic bytes, got invalid content")
		}
	case ".png":
		if len(data) < 8 || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
			return "", fmt.Errorf("file signature mismatch: expected valid PNG header")
		}
	case ".jpg", ".jpeg":
		if len(data) < 3 || !bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
			return "", fmt.Errorf("file signature mismatch: expected valid JPEG header")
		}
	case ".docx", ".xlsx":
		if len(data) < 4 || !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return "", fmt.Errorf("file signature mismatch: expected valid OOXML ZIP header")
		}
	}

	return sanitizedName, nil
}

// MatchesSenderWhitelist checks if an incoming sender email matches an authorized rule whitelist
func MatchesSenderWhitelist(fromAddress, filterPattern string) bool {
	cleanPattern := strings.TrimSpace(filterPattern)
	if cleanPattern == "" {
		return true // No whitelist restriction configured
	}

	sender := strings.ToLower(strings.TrimSpace(fromAddress))
	// Extract raw email if in "Name <email@domain.com>" format
	if start := strings.Index(sender, "<"); start != -1 {
		if end := strings.Index(sender[start:], ">"); end != -1 {
			sender = strings.TrimSpace(sender[start+1 : start+end])
		}
	}

	patterns := strings.FieldsFunc(cleanPattern, func(r rune) bool {
		return r == ',' || r == ';'
	})

	for _, p := range patterns {
		pat := strings.ToLower(strings.TrimSpace(p))
		if pat == "" {
			continue
		}
		// Exact match
		if sender == pat {
			return true
		}
		// Domain match, e.g. "@mycompany.com" or "*@mycompany.com"
		if strings.HasPrefix(pat, "*@") {
			pat = strings.TrimPrefix(pat, "*")
		}
		if strings.HasPrefix(pat, "@") && strings.HasSuffix(sender, pat) {
			return true
		}
	}

	return false
}

