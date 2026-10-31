package consumer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDFProcessor handles native, pure-Go PDF operations without external dependencies
type PDFProcessor struct{}

func NewPDFProcessor() *PDFProcessor {
	return &PDFProcessor{}
}

func (p *PDFProcessor) safeInPlace(inFile, outFile string, op func(in, out string) error) error {
	if outFile == "" || filepath.Clean(inFile) == filepath.Clean(outFile) {
		dir := filepath.Dir(inFile)
		tmpFile, err := os.CreateTemp(dir, "pdfcpu_*.pdf")
		if err != nil {
			return fmt.Errorf("failed to create temporary pdf: %w", err)
		}
		tmpPath := tmpFile.Name()
		_ = tmpFile.Close()
		defer os.Remove(tmpPath)

		if err := op(inFile, tmpPath); err != nil {
			return err
		}
		return os.Rename(tmpPath, inFile)
	}
	return op(inFile, outFile)
}

// RotatePDF rotates pages in a PDF file by the specified degrees (90, 180, 270)
func (p *PDFProcessor) RotatePDF(inFile, outFile string, degrees int, pages []string) error {
	degrees = ((degrees % 360) + 360) % 360
	if degrees == 0 {
		return nil
	}
	return p.safeInPlace(inFile, outFile, func(in, out string) error {
		conf := model.NewDefaultConfiguration()
		return api.RotateFile(in, out, degrees, pages, conf)
	})
}

// MergePDFs combines multiple PDF files into a single destination file
func (p *PDFProcessor) MergePDFs(inFiles []string, outFile string) error {
	conf := model.NewDefaultConfiguration()
	return api.MergeCreateFile(inFiles, outFile, false, conf)
}

// RemovePages removes specified page numbers (e.g. ["1", "3"]) from a PDF
func (p *PDFProcessor) RemovePages(inFile, outFile string, pages []string) error {
	if len(pages) == 0 {
		return nil
	}
	return p.safeInPlace(inFile, outFile, func(in, out string) error {
		conf := model.NewDefaultConfiguration()
		return api.RemovePagesFile(in, out, pages, conf)
	})
}

// DecryptPDF removes password encryption from a PDF
func (p *PDFProcessor) DecryptPDF(inFile, outFile string, password string) error {
	return p.safeInPlace(inFile, outFile, func(in, out string) error {
		conf := model.NewDefaultConfiguration()
		conf.UserPW = password
		conf.OwnerPW = password
		return api.DecryptFile(in, out, conf)
	})
}

// TrimPDF creates a new PDF containing only the selected pages (e.g. ["1", "2"] or ["1-3"])
func (p *PDFProcessor) TrimPDF(inFile, outFile string, selectedPages []string) error {
	conf := model.NewDefaultConfiguration()
	return api.TrimFile(inFile, outFile, selectedPages, conf)
}

// GetPageCount returns the physical number of pages in a PDF file
func (p *PDFProcessor) GetPageCount(filePath string) (int, error) {
	return api.PageCountFile(filePath)
}
