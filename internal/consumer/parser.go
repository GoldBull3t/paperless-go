package consumer

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	textObjRegex = regexp.MustCompile(`\((.*?)\)\s*Tj|\[(.*?)\]\s*TJ`)
	streamRegex  = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)

	// Regex for date extraction in invoices and documents
	isoDateRegex   = regexp.MustCompile(`\b(19\d\d|20\d\d)[-/.](0?[1-9]|1[0-2])[-/.](0?[1-9]|[12]\d|3[01])\b`)
	euroDateRegex  = regexp.MustCompile(`\b(0?[1-9]|[12]\d|3[01])[-/.](0?[1-9]|1[0-2])[-/.](19\d\d|20\d\d)\b`)
	namedDateRegex = regexp.MustCompile(`(?i)\b(0?[1-9]|[12]\d|3[01])\s+(?:de\s+)?(janeiro|fevereiro|março|marco|abril|maio|junho|julho|agosto|setembro|outubro|novembro|dezembro|jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\s+(?:de\s+)?(19\d\d|20\d\d)\b`)
)

var ptMonths = map[string]time.Month{
	"janeiro":   time.January,
	"jan":       time.January,
	"fevereiro": time.February,
	"feb":       time.February,
	"março":     time.March,
	"marco":     time.March,
	"mar":       time.March,
	"abril":     time.April,
	"apr":       time.April,
	"maio":      time.May,
	"may":       time.May,
	"junho":     time.June,
	"jun":       time.June,
	"julho":     time.July,
	"jul":       time.July,
	"agosto":    time.August,
	"aug":       time.August,
	"setembro":  time.September,
	"sep":       time.September,
	"outubro":   time.October,
	"oct":       time.October,
	"novembro":  time.November,
	"nov":       time.November,
	"dezembro":  time.December,
	"dec":       time.December,
}

// ExtractText extracts text using pdftotext, native fallback, office parser, or OCR
func ExtractText(filePath, mimeType string) (string, error) {
	// 0. Check for Office documents (.docx, .xlsx, .pptx, .odt, .ods)
	if IsOfficeDocument(filePath, mimeType) {
		text, err := ExtractOfficeText(filePath)
		if err == nil && len(text) > 0 {
			return text, nil
		}
	}

	if strings.Contains(mimeType, "pdf") {
		// 1. Try poppler's pdftotext first (most accurate for PDF standard encodings and fonts)
		if _, err := exec.LookPath("pdftotext"); err == nil {
			cmd := exec.Command("pdftotext", "-layout", filePath, "-")
			out, err := cmd.Output()
			if err == nil {
				text := strings.TrimSpace(string(out))
				if isLegitimateText(text) {
					return text, nil
				}
			}
		}

		// 2. Native PDF text extraction
		text, err := extractNativePDFText(filePath)
		if err == nil && isLegitimateText(text) {
			return text, nil
		}

		// 3. Fallback: OCR rendering using pdftoppm + tesseract if text is not embedded (scanned PDF)
		if ocrText, err := ocrScannedPDF(filePath); err == nil && len(ocrText) > 0 {
			return ocrText, nil
		}

		if text != "" {
			return text, nil
		}
	} else if strings.HasPrefix(mimeType, "image/") {
		// Run OCR directly on image files
		if _, err := exec.LookPath("tesseract"); err == nil {
			cmd := exec.Command("tesseract", filePath, "stdout", "-l", "por+eng")
			out, err := cmd.Output()
			if err == nil {
				return strings.TrimSpace(string(out)), nil
			}
		}
	} else if strings.HasPrefix(mimeType, "text/") {
		content, err := os.ReadFile(filePath)
		if err == nil {
			return string(content), nil
		}
	}

	return "", nil
}

// ExtractDocumentDate scans document text to find likely invoice / bill / contract date
func ExtractDocumentDate(text string) *time.Time {
	if text == "" {
		return nil
	}

	// 1. Match named month date (e.g. "12 de Outubro de 2024")
	if m := namedDateRegex.FindStringSubmatch(text); len(m) == 4 {
		day, _ := strconv.Atoi(m[1])
		monthName := strings.ToLower(m[2])
		year, _ := strconv.Atoi(m[3])
		if month, ok := ptMonths[monthName]; ok && year >= 1990 && year <= 2040 {
			t := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
			return &t
		}
	}

	// 2. Match European date (e.g. 15/03/2024 or 15.03.2024)
	if m := euroDateRegex.FindStringSubmatch(text); len(m) == 4 {
		day, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		year, _ := strconv.Atoi(m[3])
		if year >= 1990 && year <= 2040 && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			return &t
		}
	}

	// 3. Match ISO date (e.g. 2024-03-15)
	if m := isoDateRegex.FindStringSubmatch(text); len(m) == 4 {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if year >= 1990 && year <= 2040 && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
			return &t
		}
	}

	return nil
}

// GenerateArchivePDF attempts to produce a searchable PDF/A using ocrmypdf if available
func GenerateArchivePDF(inputPath, outputPath string) bool {
	mode := os.Getenv("PAPERLESS_OCR_MODE")
	if mode == "" {
		mode = "skip"
	}
	lang := os.Getenv("PAPERLESS_OCR_LANGUAGE")
	if lang == "" {
		lang = "por+eng"
	}
	clean := os.Getenv("PAPERLESS_OCR_CLEAN") != "none"
	deskew := os.Getenv("PAPERLESS_OCR_DESKEW") == "true" || os.Getenv("PAPERLESS_OCR_DESKEW") == "1"
	rotate := os.Getenv("PAPERLESS_OCR_ROTATE_PAGES") == "true" || os.Getenv("PAPERLESS_OCR_ROTATE_PAGES") == "1"

	return GenerateArchivePDFWithConfig(inputPath, outputPath, mode, lang, deskew, rotate, clean)
}

// GenerateArchivePDFWithConfig produces a searchable PDF/A honoring configurable parameters
func GenerateArchivePDFWithConfig(inputPath, outputPath, mode, lang string, deskew, rotate, clean bool) bool {
	if _, err := exec.LookPath("ocrmypdf"); err == nil {
		args := []string{}
		switch strings.ToLower(mode) {
		case "redo":
			args = append(args, "--redo-ocr")
		case "force":
			args = append(args, "--force-ocr")
		default:
			args = append(args, "--skip-text")
		}
		if clean {
			args = append(args, "--clean")
		}
		if deskew {
			args = append(args, "--deskew")
		}
		if rotate {
			args = append(args, "--rotate-pages")
		}
		if lang == "" {
			lang = "por+eng"
		}
		args = append(args, "-l", lang, inputPath, outputPath)

		cmd := exec.Command("ocrmypdf", args...)
		if err := cmd.Run(); err == nil {
			if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
				return true
			}
		}
	}
	return false
}

func isLegitimateText(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 20 {
		return false
	}
	printableCount := 0
	for _, r := range trimmed {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printableCount++
		}
	}
	ratio := float64(printableCount) / float64(len(trimmed))
	return ratio > 0.85
}

// DetectOCREngine checks if an external OCR engine is installed
func DetectOCREngine() string {
	if _, err := exec.LookPath("ocrmypdf"); err == nil {
		return "OCRmyPDF + Tesseract"
	}
	if _, err := exec.LookPath("tesseract"); err == nil {
		return "Tesseract OCR"
	}
	return "none"
}

// DetectPageCount detects the number of pages in a document
func DetectPageCount(filePath, mimeType string) int {
	if strings.Contains(mimeType, "pdf") {
		// 1. Pure Go PDF analysis via pdfcpu
		if count, err := NewPDFProcessor().GetPageCount(filePath); err == nil && count > 0 {
			return count
		}

		// 2. Try pdfinfo if available
		if _, err := exec.LookPath("pdfinfo"); err == nil {
			cmd := exec.Command("pdfinfo", filePath)
			if out, err := cmd.Output(); err == nil {
				re := regexp.MustCompile(`Pages:\s+(\d+)`)
				if m := re.FindStringSubmatch(string(out)); len(m) == 2 {
					if count, err := strconv.Atoi(m[1]); err == nil && count > 0 {
						return count
					}
				}
			}
		}

		// 3. Native PDF scan for /Type /Page
		data, err := os.ReadFile(filePath)
		if err == nil {
			pageRegex := regexp.MustCompile(`/Type\s*/Page\b`)
			matches := pageRegex.FindAll(data, -1)
			if len(matches) > 0 {
				return len(matches)
			}
		}
	}
	return 1
}

func ocrScannedPDF(filePath string) (string, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return "", fmt.Errorf("pdftoppm not found")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		return "", fmt.Errorf("tesseract not found")
	}

	tmpDir := os.TempDir()
	prefix := fmt.Sprintf("paperless-ocr-%d", time.Now().UnixNano())
	tmpImgPrefix := filepath.Join(tmpDir, prefix)

	// Clean up all rendered page images on exit
	defer func() {
		matches, _ := filepath.Glob(tmpImgPrefix + "-*.png")
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}()

	// Render all pages (up to 100 pages) at 150 DPI
	renderCmd := exec.Command("pdftoppm", "-png", "-r", "150", "-f", "1", "-l", "100", filePath, tmpImgPrefix)
	if err := renderCmd.Run(); err != nil {
		return "", err
	}

	pageFiles, err := filepath.Glob(tmpImgPrefix + "-*.png")
	if err != nil || len(pageFiles) == 0 {
		return "", fmt.Errorf("no page images rendered by pdftoppm")
	}

	// Sort pages in natural numeric order (1, 2, 3...)
	sort.Slice(pageFiles, func(i, j int) bool {
		return extractPageNum(pageFiles[i]) < extractPageNum(pageFiles[j])
	})

	var fullText strings.Builder
	for idx, pageImg := range pageFiles {
		ocrCmd := exec.Command("tesseract", pageImg, "stdout", "-l", "por+eng")
		out, err := ocrCmd.Output()
		if err == nil {
			pageText := strings.TrimSpace(string(out))
			if pageText != "" {
				if idx > 0 {
					fullText.WriteString("\n\n")
				}
				fullText.WriteString(pageText)
			}
		}
	}

	return strings.TrimSpace(fullText.String()), nil
}

func extractPageNum(filename string) int {
	base := filepath.Base(filename)
	parts := strings.Split(strings.TrimSuffix(base, filepath.Ext(base)), "-")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if num, err := strconv.Atoi(last); err == nil {
			return num
		}
	}
	return 0
}

func extractNativePDFText(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	streams := streamRegex.FindAllSubmatch(data, -1)

	for _, s := range streams {
		if len(s) < 2 {
			continue
		}
		rawStream := s[1]
		decompressed := decompressStream(rawStream)

		matches := textObjRegex.FindAllSubmatch(decompressed, -1)
		for _, m := range matches {
			if len(m[1]) > 0 {
				sb.Write(cleanPDFString(m[1]))
				sb.WriteString(" ")
			} else if len(m[2]) > 0 {
				sb.WriteString(parseTJArray(string(m[2])))
				sb.WriteString(" ")
			}
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

func decompressStream(data []byte) []byte {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return data
	}
	defer r.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		return data
	}
	return out
}

func cleanPDFString(s []byte) []byte {
	res := bytes.ReplaceAll(s, []byte(`\(`), []byte(`(`))
	res = bytes.ReplaceAll(res, []byte(`\)`), []byte(`)`))
	res = bytes.ReplaceAll(res, []byte(`\\`), []byte(`\`))
	return res
}

func parseTJArray(s string) string {
	var sb strings.Builder
	inParen := false
	var cur strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' && (i == 0 || s[i-1] != '\\') {
			inParen = true
			cur.Reset()
		} else if c == ')' && (i == 0 || s[i-1] != '\\') {
			inParen = false
			sb.WriteString(cur.String())
		} else if inParen {
			cur.WriteByte(c)
		}
	}
	return sb.String()
}
