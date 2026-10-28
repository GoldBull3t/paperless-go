package consumer

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var cleanPathRegex = regexp.MustCompile(`[^\w\s\-_./]`)

// FormatStoragePath formats a storage path template string using document metadata.
// Supports placeholders like {title}, {correspondent}, {document_type}, {created}, {created_year},
// {created_month}, {created_day}, {added}, {added_year}, {asn}.
// Also supports Jinja2 style {{ correspondent }}.
func FormatStoragePath(templateStr string, title, correspondent, docType string, created, added time.Time, asn *int) string {
	if strings.TrimSpace(templateStr) == "" {
		return ""
	}

	res := templateStr

	// Replace values
	replacements := map[string]string{
		"title":              title,
		"correspondent":      correspondent,
		"document_type":      docType,
		"created":            created.Format("2006-01-02"),
		"created_year":       created.Format("2006"),
		"created_year_short": created.Format("06"),
		"created_month":      created.Format("01"),
		"created_month_name": created.Format("January"),
		"created_day":        created.Format("02"),
		"added":              added.Format("2006-01-02"),
		"added_year":         added.Format("2006"),
		"added_month":        added.Format("01"),
		"added_day":          added.Format("02"),
	}

	if asn != nil {
		replacements["asn"] = fmt.Sprintf("%07d", *asn)
	} else {
		replacements["asn"] = "none"
	}

	for k, v := range replacements {
		if v == "" {
			v = "none"
		}
		// Standard {key}
		res = strings.ReplaceAll(res, "{"+k+"}", v)
		// Jinja2 {{ key }} or {{key}}
		res = strings.ReplaceAll(res, "{{ "+k+" }}", v)
		res = strings.ReplaceAll(res, "{{"+k+"}}", v)
	}

	// Clean path to prevent path traversal
	res = strings.ReplaceAll(res, "\\", "/")
	parts := strings.Split(res, "/")
	var cleanParts []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || p == "." || p == ".." {
			continue
		}
		// Sanitize characters
		p = cleanPathRegex.ReplaceAllString(p, "_")
		if p != "" {
			cleanParts = append(cleanParts, p)
		}
	}

	return filepath.Join(cleanParts...)
}

