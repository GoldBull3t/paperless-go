package barcode

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/datamatrix"
	"github.com/makiuchi-d/gozxing/oned"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// BarcodeInfo represents a detected and sanitized barcode on a document page
type BarcodeInfo struct {
	Format      string   `json:"format"`       // e.g. "QR_CODE", "CODE_128", "CODE_39", "PATCH_CODE"
	Value       string   `json:"value"`        // Sanitized textual value
	Page        int      `json:"page"`         // 1-based page number
	IsSeparator bool     `json:"is_separator"` // True if matches separator pattern (e.g. PATCHT)
	ASN         *int     `json:"asn,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Title       string   `json:"title,omitempty"`
}

// Config configures barcode detection and batch splitting
type Config struct {
	Enabled             bool
	SeparatorString     string // default: "PATCHT"
	EnableASN           bool   // default: true
	ASNPrefix           string // default: "ASN"
	RemoveSeparatorPage bool   // default: true (drops standalone separator sheets)
}

func DefaultConfig() Config {
	return Config{
		Enabled:             true,
		SeparatorString:     "PATCHT",
		EnableASN:           true,
		ASNPrefix:           "ASN",
		RemoveSeparatorPage: true,
	}
}

// Scanner performs robust and secure 1D/2D barcode scanning
type Scanner struct {
	cfg Config
}

func NewScanner(cfg ...Config) *Scanner {
	c := DefaultConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &Scanner{cfg: c}
}

// SanitizeBarcodeText neutralizes hazardous injection payloads from untrusted barcodes
func SanitizeBarcodeText(raw string) string {
	if len(raw) > 1024 {
		raw = raw[:1024]
	}

	var sb strings.Builder
	for _, r := range raw {
		// Drop null bytes and non-printable control characters
		if r == 0 || (r < 32 && r != '\t' && r != '\n' && r != '\r') || (r >= 127 && r < 160) {
			continue
		}
		// Strip shell / path hazard characters
		if r == '\x00' {
			continue
		}
		sb.WriteRune(r)
	}

	res := strings.TrimSpace(sb.String())
	// Remove dangerous directory traversal patterns
	res = strings.ReplaceAll(res, "../", "")
	res = strings.ReplaceAll(res, "..\\", "")
	return res
}

// ParseBarcodeMetadata extracts ASN, tags, and title from a sanitized barcode value
func (s *Scanner) ParseBarcodeMetadata(cleanVal string) (asn *int, tags []string, title string, isSeparator bool) {
	upper := strings.ToUpper(cleanVal)

	// 1. Check if separator barcode (e.g. PATCHT, PATCH-T, SPLIT, SEPARATOR)
	sepKey := strings.ToUpper(s.cfg.SeparatorString)
	if upper == sepKey || upper == "PATCHT" || upper == "PATCH-T" || upper == "SPLIT" || upper == "SEPARATOR" {
		isSeparator = true
	}

	// 2. Check ASN pattern (e.g. ASN:1004, ASN-1004, ASN 1004, ASN1004, PAPERLESS-ASN:1004)
	if s.cfg.EnableASN {
		asnRegex := regexp.MustCompile(`(?i)(?:^|[\s_:-])(?:` + regexp.QuoteMeta(s.cfg.ASNPrefix) + `|PAPERLESS-ASN)[\s_:=-]*(\d+)\b`)
		if matches := asnRegex.FindStringSubmatch(cleanVal); len(matches) > 1 {
			if val, err := strconv.Atoi(matches[1]); err == nil && val > 0 {
				asn = &val
			}
		}
	}

	// 3. Check Tags pattern (e.g. TAG:Invoice or TAGS:Finance,Accounting)
	tagRegex := regexp.MustCompile(`(?i)(?:^|[\s_:-])TAGS?[\s_:=-]+([^:\r\n]+?)(?:\s+[A-Z0-9_-]+:|$)`)
	if matches := tagRegex.FindStringSubmatch(cleanVal); len(matches) > 1 {
		rawTags := strings.FieldsFunc(matches[1], func(r rune) bool {
			return r == ',' || r == ';'
		})
		for _, t := range rawTags {
			cleanTag := strings.TrimSpace(t)
			if cleanTag != "" {
				tags = append(tags, cleanTag)
			}
		}
	}

	// 4. Check Title pattern (e.g. TITLE:Document Title)
	titleRegex := regexp.MustCompile(`(?i)(?:^|[\s_:-])TITLE[\s_:=-]+([^\r\n]+)`)
	if matches := titleRegex.FindStringSubmatch(cleanVal); len(matches) > 1 {
		title = strings.TrimSpace(matches[1])
	}

	return asn, tags, title, isSeparator
}

// ScanImage inspects a Go image.Image for 1D/2D barcodes using pure-Go ZXing
func (s *Scanner) ScanImage(img image.Image, pageNum int) ([]BarcodeInfo, error) {
	if img == nil {
		return nil, fmt.Errorf("nil image provided")
	}

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, fmt.Errorf("failed converting image to bitmap: %w", err)
	}

	var results []BarcodeInfo

	// Primary 1D/2D readers: QR Code, Code 128 (most common for Patch-T), Code 39, DataMatrix, EAN 13
	readers := []struct {
		name   string
		reader gozxing.Reader
	}{
		{"QR_CODE", qrcode.NewQRCodeReader()},
		{"CODE_128", oned.NewCode128Reader()},
		{"CODE_39", oned.NewCode39Reader()},
		{"DATA_MATRIX", datamatrix.NewDataMatrixReader()},
		{"EAN_13", oned.NewEAN13Reader()},
	}

	for _, r := range readers {
		res, err := r.reader.Decode(bmp, nil)
		if err == nil && res != nil {
			clean := SanitizeBarcodeText(res.GetText())
			if clean != "" {
				asn, tags, title, isSep := s.ParseBarcodeMetadata(clean)
				results = append(results, BarcodeInfo{
					Format:      r.name,
					Value:       clean,
					Page:        pageNum,
					IsSeparator: isSep,
					ASN:         asn,
					Tags:        tags,
					Title:       title,
				})
				// Found barcode on this page
				break
			}
		}
	}

	return results, nil
}

// SplitResult describes a document slice generated from a batch scan
type SplitResult struct {
	FilePath    string       // Path to the sliced PDF file
	StartPage   int          // 1-based start page in original
	EndPage     int          // 1-based end page in original
	Barcodes    []BarcodeInfo // Barcodes found on pages in this slice
	ASN         *int         // Extracted ASN if found
	Tags        []string     // Extracted tags
	Title       string       // Extracted title
}

// SplitPDFByBarcodes analyzes a multi-page PDF, identifies separator pages, and slices the PDF
func (s *Scanner) SplitPDFByBarcodes(ctx context.Context, pdfPath, outDir string) ([]SplitResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// 1. Check page count via pdfcpu
	pageCount, err := api.PageCountFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("failed counting PDF pages: %w", err)
	}

	if pageCount <= 1 {
		// Single page cannot be split
		return nil, nil
	}

	// 2. Render each page to temporary images or inspect embedded scans
	tmpDir, err := os.MkdirTemp("", "barcode-split-*")
	if err != nil {
		return nil, fmt.Errorf("failed creating temp dir for page scans: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pageBarcodes := make(map[int][]BarcodeInfo)
	separatorPages := make(map[int]bool)

	// Check if pdftoppm is available for rendering
	if _, err := exec.LookPath("pdftoppm"); err == nil {
		// Render pages at 150 DPI
		prefix := filepath.Join(tmpDir, "page")
		cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", "150", "-f", "1", "-l", strconv.Itoa(pageCount), pdfPath, prefix)
		_ = cmd.Run()

		// Read rendered page files
		for p := 1; p <= pageCount; p++ {
			// Try possible pdftoppm naming conventions (page-1.png, page-01.png, page-001.png)
			candidates := []string{
				fmt.Sprintf("%s-%d.png", prefix, p),
				fmt.Sprintf("%s-%02d.png", prefix, p),
				fmt.Sprintf("%s-%03d.png", prefix, p),
				fmt.Sprintf("%s-%04d.png", prefix, p),
			}
			for _, c := range candidates {
				if f, err := os.Open(c); err == nil {
					img, _, err := image.Decode(f)
					_ = f.Close()
					if err == nil {
						bcs, _ := s.ScanImage(img, p)
						if len(bcs) > 0 {
							pageBarcodes[p] = bcs
							for _, b := range bcs {
								if b.IsSeparator {
									separatorPages[p] = true
									break
								}
							}
						}
					}
					break
				}
			}
		}
	} else {
		// Fallback: extract embedded images per page using pdfcpu
		for p := 1; p <= pageCount; p++ {
			pageDir := filepath.Join(tmpDir, fmt.Sprintf("page_%d", p))
			_ = os.MkdirAll(pageDir, 0755)
			if err := api.ExtractImagesFile(pdfPath, pageDir, []string{strconv.Itoa(p)}, model.NewDefaultConfiguration()); err == nil {
				entries, _ := os.ReadDir(pageDir)
				for _, e := range entries {
					lower := strings.ToLower(e.Name())
					if strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".tif") || strings.HasSuffix(lower, ".tiff") {
						imgPath := filepath.Join(pageDir, e.Name())
						if f, err := os.Open(imgPath); err == nil {
							img, _, err := image.Decode(f)
							_ = f.Close()
							if err == nil {
								bcs, _ := s.ScanImage(img, p)
								if len(bcs) > 0 {
									pageBarcodes[p] = append(pageBarcodes[p], bcs...)
									for _, b := range bcs {
										if b.IsSeparator {
											separatorPages[p] = true
											break
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// If no separator pages found, no split needed
	if len(separatorPages) == 0 {
		return nil, nil
	}

	// 3. Calculate page ranges for document slices
	type docRange struct {
		start int
		end   int
		pages []int
	}

	var ranges []docRange
	currentStart := 1
	var currentPages []int

	for p := 1; p <= pageCount; p++ {
		if separatorPages[p] {
			// If we have accumulated pages before this separator, close the previous document
			if len(currentPages) > 0 {
				ranges = append(ranges, docRange{
					start: currentStart,
					end:   p - 1,
					pages: currentPages,
				})
				currentPages = nil
			}
			// If separator page is a standalone sheet to drop, skip it
			if s.cfg.RemoveSeparatorPage {
				currentStart = p + 1
				continue
			} else {
				// Keep separator as first page of new document
				currentStart = p
				currentPages = append(currentPages, p)
			}
		} else {
			currentPages = append(currentPages, p)
		}
	}

	if len(currentPages) > 0 {
		ranges = append(ranges, docRange{
			start: currentStart,
			end:   pageCount,
			pages: currentPages,
		})
	}

	if len(ranges) <= 1 {
		// Only one document produced, no split needed
		return nil, nil
	}

	// 4. Generate sliced PDFs via pdfcpu TrimFile
	if outDir == "" {
		outDir = filepath.Dir(pdfPath)
	}

	baseName := strings.TrimSuffix(filepath.Base(pdfPath), filepath.Ext(pdfPath))
	var results []SplitResult

	for i, r := range ranges {
		if len(r.pages) == 0 {
			continue
		}

		outFileName := fmt.Sprintf("%s_part%d.pdf", baseName, i+1)
		outFilePath := filepath.Join(outDir, outFileName)

		// Build page selection string, e.g. ["1", "2", "3"]
		var pageStrings []string
		for _, p := range r.pages {
			pageStrings = append(pageStrings, strconv.Itoa(p))
		}

		err := api.TrimFile(pdfPath, outFilePath, pageStrings, model.NewDefaultConfiguration())
		if err != nil {
			return nil, fmt.Errorf("failed slicing PDF pages %v: %w", pageStrings, err)
		}

		// Gather metadata for this slice from barcodes found within its page range
		var sliceBarcodes []BarcodeInfo
		var sliceASN *int
		var sliceTags []string
		sliceTitle := ""

		for _, p := range r.pages {
			if bcs, ok := pageBarcodes[p]; ok {
				for _, b := range bcs {
					sliceBarcodes = append(sliceBarcodes, b)
					if sliceASN == nil && b.ASN != nil {
						sliceASN = b.ASN
					}
					if len(b.Tags) > 0 {
						sliceTags = append(sliceTags, b.Tags...)
					}
					if sliceTitle == "" && b.Title != "" {
						sliceTitle = b.Title
					}
				}
			}
		}

		results = append(results, SplitResult{
			FilePath:  outFilePath,
			StartPage: r.start,
			EndPage:   r.end,
			Barcodes:  sliceBarcodes,
			ASN:       sliceASN,
			Tags:      sliceTags,
			Title:     sliceTitle,
		})
	}

	return results, nil
}
