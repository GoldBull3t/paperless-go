package consumer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"paperless-go/internal/barcode"
)

// SplitDocumentSegment represents a partitioned document chunk created by a separator
type SplitDocumentSegment struct {
	FilePath  string
	StartPage int
	EndPage   int
	ASN       *int
}

// BarcodeSplitter handles automatic multi-page batch separation
type BarcodeSplitter struct {
	pdfProc *PDFProcessor
}

func NewBarcodeSplitter() *BarcodeSplitter {
	return &BarcodeSplitter{
		pdfProc: NewPDFProcessor(),
	}
}

// SplitPDFBySeparators scans a PDF for separator pages and partitions it into individual documents.
// If no separators are detected, it returns an empty slice and false.
func (s *BarcodeSplitter) SplitPDFBySeparators(pdfPath string, sepString string, asnPrefix string) ([]SplitDocumentSegment, bool, error) {
	totalCount, err := s.pdfProc.GetPageCount(pdfPath)
	if err != nil || totalCount <= 1 {
		return nil, false, nil
	}

	// 1. Try visual barcode & patch-code scanning via internal/barcode
	scanner := barcode.NewScanner(barcode.Config{
		Enabled:             true,
		SeparatorString:     sepString,
		EnableASN:           true,
		ASNPrefix:           asnPrefix,
		RemoveSeparatorPage: true,
	})

	if visualSlices, err := scanner.SplitPDFByBarcodes(context.Background(), pdfPath, filepath.Dir(pdfPath)); err == nil && len(visualSlices) > 1 {
		var segments []SplitDocumentSegment
		for _, sl := range visualSlices {
			segments = append(segments, SplitDocumentSegment{
				FilePath:  sl.FilePath,
				StartPage: sl.StartPage,
				EndPage:   sl.EndPage,
				ASN:       sl.ASN,
			})
		}
		return segments, true, nil
	}

	// 2. Fallback to textual separator matching in embedded PDF text
	var separatorPages []int
	pageASNs := make(map[int]*int)

	for p := 1; p <= totalCount; p++ {
		pageText := s.extractPageText(pdfPath, p)
		if DetectSeparatorBarcode(pageText, sepString) {
			separatorPages = append(separatorPages, p)
			if asn := DetectASNFromBarcode(pageText, asnPrefix); asn != nil {
				pageASNs[p] = asn
			}
		}
	}

	if len(separatorPages) == 0 {
		return nil, false, nil
	}

	type rangeInfo struct {
		start int
		end   int
		asn   *int
	}
	var ranges []rangeInfo

	curStart := 1
	var nextASN *int

	for _, sepPage := range separatorPages {
		if sepPage > curStart {
			ranges = append(ranges, rangeInfo{
				start: curStart,
				end:   sepPage - 1,
				asn:   nextASN,
			})
		}
		nextASN = pageASNs[sepPage]
		curStart = sepPage + 1
	}

	if curStart <= totalCount {
		ranges = append(ranges, rangeInfo{
			start: curStart,
			end:   totalCount,
			asn:   nextASN,
		})
	}

	if len(ranges) == 0 {
		return nil, false, nil
	}

	dir := filepath.Dir(pdfPath)
	var segments []SplitDocumentSegment

	for i, r := range ranges {
		outPath := filepath.Join(dir, fmt.Sprintf("split_part_%d_%s", i+1, filepath.Base(pdfPath)))
		var pages []string
		for p := r.start; p <= r.end; p++ {
			pages = append(pages, strconv.Itoa(p))
		}

		if err := s.pdfProc.TrimPDF(pdfPath, outPath, pages); err != nil {
			for _, seg := range segments {
				_ = os.Remove(seg.FilePath)
			}
			return nil, false, fmt.Errorf("failed to trim page range %d-%d: %w", r.start, r.end, err)
		}

		segments = append(segments, SplitDocumentSegment{
			FilePath:  outPath,
			StartPage: r.start,
			EndPage:   r.end,
			ASN:       r.asn,
		})
	}

	return segments, true, nil
}

func (s *BarcodeSplitter) extractPageText(pdfPath string, page int) string {
	dir := filepath.Dir(pdfPath)
	tmpPageFile := filepath.Join(dir, fmt.Sprintf("probe_%d_%s", page, filepath.Base(pdfPath)))
	defer os.Remove(tmpPageFile)

	if err := s.pdfProc.TrimPDF(pdfPath, tmpPageFile, []string{strconv.Itoa(page)}); err == nil {
		text, _ := ExtractText(tmpPageFile, "application/pdf")
		return text
	}
	return ""
}
