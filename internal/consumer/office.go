package consumer

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsOfficeDocument checks if the given file or MIME type corresponds to an Office format
func IsOfficeDocument(filePath, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp", ".doc", ".xls", ".ppt", ".rtf":
		return true
	}

	m := strings.ToLower(mimeType)
	return strings.Contains(m, "officedocument") ||
		strings.Contains(m, "msword") ||
		strings.Contains(m, "ms-excel") ||
		strings.Contains(m, "ms-powerpoint") ||
		strings.Contains(m, "oasis.opendocument")
}

// ExtractOfficeText extracts text from OpenXML (.docx, .xlsx, .pptx) and OpenDocument (.odt, .ods)
func ExtractOfficeText(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	// All modern Office formats (.docx, .xlsx, .pptx, .odt, .ods) are standard ZIP archives
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open office file as zip: %w", err)
	}
	defer zr.Close()

	var sb strings.Builder

	switch ext {
	case ".docx":
		for _, f := range zr.File {
			if f.Name == "word/document.xml" {
				text, err := extractXMLText(f, "w:t")
				if err == nil {
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
	case ".xlsx":
		for _, f := range zr.File {
			if f.Name == "xl/sharedStrings.xml" || strings.HasPrefix(f.Name, "xl/worksheets/sheet") {
				text, err := extractXMLText(f, "t", "v")
				if err == nil {
					sb.WriteString(text)
					sb.WriteString(" ")
				}
			}
		}
	case ".pptx":
		for _, f := range zr.File {
			if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
				text, err := extractXMLText(f, "a:t")
				if err == nil {
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
	case ".odt", ".ods", ".odp":
		for _, f := range zr.File {
			if f.Name == "content.xml" {
				text, err := extractXMLText(f, "text:p", "text:span", "table:table-cell")
				if err == nil {
					sb.WriteString(text)
					sb.WriteString("\n")
				}
			}
		}
	default:
		// Generic fallback: scan any XML files inside the archive for text
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, ".xml") {
				text, _ := extractXMLText(f)
				if text != "" {
					sb.WriteString(text)
					sb.WriteString(" ")
				}
			}
		}
	}

	res := strings.TrimSpace(sb.String())
	return res, nil
}

// extractXMLText parses an XML file inside a ZIP archive and extracts inner text
func extractXMLText(f *zip.File, targetElements ...string) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	var sb strings.Builder

	targets := make(map[string]bool)
	for _, t := range targetElements {
		targets[t] = true
		if idx := strings.Index(t, ":"); idx >= 0 {
			targets[t[idx+1:]] = true
		}
	}

	captureAll := len(targets) == 0
	inTarget := false

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			if captureAll || targets[elem.Name.Local] {
				inTarget = true
			}
		case xml.EndElement:
			if targets[elem.Name.Local] {
				inTarget = false
				sb.WriteString(" ")
			}
		case xml.CharData:
			if captureAll || inTarget {
				text := strings.TrimSpace(string(elem))
				if text != "" {
					sb.WriteString(text)
					sb.WriteString(" ")
				}
			}
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

// ConvertOfficeToPDF converts an office document into a PDF using headless LibreOffice if available
func ConvertOfficeToPDF(inputPath, outputPath string) bool {
	// Check for libreoffice or soffice in PATH
	bin := ""
	if p, err := exec.LookPath("libreoffice"); err == nil {
		bin = p
	} else if p, err := exec.LookPath("soffice"); err == nil {
		bin = p
	}

	if bin == "" {
		return false
	}

	outDir := filepath.Dir(outputPath)
	cmd := exec.Command(bin, "--headless", "--convert-to", "pdf", "--outdir", outDir, inputPath)
	if err := cmd.Run(); err != nil {
		return false
	}

	// LibreOffice outputs <base_name>.pdf in outDir
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	generatedPDF := filepath.Join(outDir, baseName+".pdf")

	if _, err := os.Stat(generatedPDF); err == nil {
		if generatedPDF != outputPath {
			_ = os.Rename(generatedPDF, outputPath)
		}
		return true
	}

	return false
}
