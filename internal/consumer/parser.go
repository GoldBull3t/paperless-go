package consumer

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	textObjRegex = regexp.MustCompile(`\((.*?)\)\s*Tj|\[(.*?)\]\s*TJ`)
	streamRegex  = regexp.MustCompile(`(?s)stream\r?\n(.*?)\r?\nendstream`)
)

// ExtractText tries native pure-Go extraction first, then falls back to pdftotext/tesseract if needed
func ExtractText(filePath, mimeType string) (string, error) {
	if strings.Contains(mimeType, "pdf") {
		// 1. Try native PDF text extraction
		text, err := extractNativePDFText(filePath)
		if err == nil && len(strings.TrimSpace(text)) > 20 {
			return text, nil
		}

		// 2. Try pdftotext CLI if installed
		if _, err := exec.LookPath("pdftotext"); err == nil {
			cmd := exec.Command("pdftotext", filePath, "-")
			out, err := cmd.Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 10 {
				return string(out), nil
			}
		}

		// 3. Fallback to Tesseract OCR CLI if available
		if _, err := exec.LookPath("tesseract"); err == nil {
			cmd := exec.Command("tesseract", filePath, "stdout", "-l", "por+eng", "--oem", "1")
			out, err := cmd.Output()
			if err == nil && len(strings.TrimSpace(string(out))) > 0 {
				return string(out), nil
			}
		}

		if text != "" {
			return text, nil
		}
	} else if strings.HasPrefix(mimeType, "image/") {
		// Run OCR on images if tesseract is available
		if _, err := exec.LookPath("tesseract"); err == nil {
			cmd := exec.Command("tesseract", filePath, "stdout", "-l", "por+eng")
			out, err := cmd.Output()
			if err == nil {
				return string(out), nil
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

		// Find (text) Tj and [(text)] TJ
		matches := textObjRegex.FindAllSubmatch(decompressed, -1)
		for _, m := range matches {
			if len(m[1]) > 0 {
				sb.Write(cleanPDFString(m[1]))
				sb.WriteString(" ")
			} else if len(m[2]) > 0 {
				// Parse array of strings e.g. [(Hello) -10 (World)]
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
		return data // might not be compressed
	}
	defer r.Close()

	out, err := io.ReadAll(r)
	if err != nil {
		return data
	}
	return out
}

func cleanPDFString(s []byte) []byte {
	// Unescape standard PDF octal or escaped chars: \( \) \\
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
