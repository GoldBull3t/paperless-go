package scanner

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

type ScannerInfo struct {
	ID                   int      `json:"id,omitempty"`
	Name                 string   `json:"name"`
	URL                  string   `json:"url"`
	MakeAndModel         string   `json:"make_and_model"`
	Manufacturer         string   `json:"manufacturer,omitempty"`
	Model                string   `json:"model,omitempty"`
	SerialNumber         string   `json:"serial_number,omitempty"`
	Status               string   `json:"status"`
	AdfState             string   `json:"adf_state,omitempty"`
	AdfDuplex            bool     `json:"adf_duplex"`
	SupportedColorModes  []string `json:"supported_color_modes"`
	SupportedResolutions []int    `json:"supported_resolutions"`
	SupportedSources     []string `json:"supported_sources"`
	SupportedFormats     []string `json:"supported_formats"`
}

type ScannerStatusInfo struct {
	State     string `json:"state"`      // "Idle", "Processing", "Testing", "Stopped"
	AdfState  string `json:"adf_state"`   // "ScannerAdfEmpty", "ScannerAdfLoaded", "ScannerAdfJam"
	AdfLoaded bool   `json:"adf_loaded"`
	Ready     bool   `json:"ready"`
}

type ScanOptions struct {
	ScannerURL string `json:"scanner_url"`
	Resolution int    `json:"resolution"` // e.g. 150, 200, 300, 600
	ColorMode  string `json:"color_mode"` // "RGB24", "Grayscale8", "BlackAndWhite1"
	Source     string `json:"source"`     // "Platen", "Feeder"
	Duplex     bool   `json:"duplex"`     // Duplex two-sided ADF scan
	Format     string `json:"format"`     // "application/pdf" or "image/jpeg"
}

type ESCLClient struct {
	httpClient *http.Client
}

func NewESCLClient() *ESCLClient {
	return &ESCLClient{
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

// CleanBaseURL ensures the eSCL URL ends without trailing slashes and normalizes the /eSCL endpoint path.
func CleanBaseURL(rawURL string) (string, error) {
	clean := strings.TrimSpace(rawURL)
	if clean == "" {
		return "", fmt.Errorf("scanner URL cannot be empty")
	}
	if !strings.Contains(clean, "://") {
		clean = "http://" + clean
	}

	u, err := url.Parse(clean)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid scheme: only http and https are supported")
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid host in URL %q", rawURL)
	}

	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/eSCL"
	} else if !strings.HasSuffix(path, "/eSCL") && !strings.Contains(strings.ToLower(path), "escl") {
		path = path + "/eSCL"
	}
	return base + path, nil
}

type rawCapabilitiesXML struct {
	XMLName      xml.Name `xml:"ScannerCapabilities"`
	Version      string   `xml:"Version"`
	MakeAndModel string   `xml:"MakeAndModel"`
	Manufacturer string   `xml:"Manufacturer"`
	Model        string   `xml:"Model"`
	SerialNumber string   `xml:"SerialNumber"`
	Platen       *struct {
		PlatenInputCaps struct {
			SupportedResolutions struct {
				DiscreteResolutions []struct {
					XResolution int `xml:"XResolution"`
				} `xml:"DiscreteResolutions>DiscreteResolution"`
			} `xml:"SupportedResolutions"`
			ColorModes      []string `xml:"ColorModes>ColorMode"`
			DocumentFormats []string `xml:"DocumentFormats>DocumentFormat"`
		} `xml:"PlatenInputCaps"`
	} `xml:"Platen"`
	Adf *struct {
		AdfInputCaps struct {
			AdfDuplex            int `xml:"AdfDuplex"`
			SupportedResolutions struct {
				DiscreteResolutions []struct {
					XResolution int `xml:"XResolution"`
				} `xml:"DiscreteResolutions>DiscreteResolution"`
			} `xml:"SupportedResolutions"`
			ColorModes      []string `xml:"ColorModes>ColorMode"`
			DocumentFormats []string `xml:"DocumentFormats>DocumentFormat"`
		} `xml:"AdfInputCaps"`
	} `xml:"Adf"`
}

type rawStatusXML struct {
	XMLName  xml.Name `xml:"ScannerStatus"`
	State    string   `xml:"State"`
	AdfState string   `xml:"AdfState"`
}

// QueryCapabilities queries an eSCL scanner and parses its full capabilities XML.
func (c *ESCLClient) QueryCapabilities(rawURL string) (*ScannerInfo, error) {
	baseURL, err := CleanBaseURL(rawURL)
	if err != nil {
		return nil, err
	}

	capsURL := baseURL + "/ScannerCapabilities"
	resp, err := c.httpClient.Get(capsURL)
	if err != nil {
		// Attempt HTTPS fallback if initial HTTP connection failed and port was default
		if strings.HasPrefix(baseURL, "http://") {
			altURL := "https://" + strings.TrimPrefix(baseURL, "http://") + "/ScannerCapabilities"
			altResp, altErr := c.httpClient.Get(altURL)
			if altErr == nil && altResp.StatusCode == http.StatusOK {
				resp = altResp
				baseURL = strings.TrimSuffix(altURL, "/ScannerCapabilities")
			} else {
				return nil, fmt.Errorf("failed to connect to scanner at %s: %w", capsURL, err)
			}
		} else {
			return nil, fmt.Errorf("failed to connect to scanner at %s: %w", capsURL, err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scanner returned HTTP %d on capabilities query (%s)", resp.StatusCode, capsURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var caps rawCapabilitiesXML
	_ = xml.Unmarshal(body, &caps)

	info := &ScannerInfo{
		URL:    baseURL,
		Status: "Ready",
	}

	if caps.MakeAndModel != "" {
		info.MakeAndModel = caps.MakeAndModel
		info.Name = caps.MakeAndModel
	} else if caps.Manufacturer != "" || caps.Model != "" {
		info.MakeAndModel = strings.TrimSpace(caps.Manufacturer + " " + caps.Model)
		info.Name = info.MakeAndModel
	} else {
		info.MakeAndModel = "Network Scanner (eSCL 2.0 / WSD Driverless)"
		info.Name = "Network Scanner (eSCL)"
	}
	info.Manufacturer = caps.Manufacturer
	info.Model = caps.Model
	info.SerialNumber = caps.SerialNumber

	// Sources
	var sources []string
	if caps.Platen != nil {
		sources = append(sources, "Platen")
	}
	if caps.Adf != nil {
		sources = append(sources, "Feeder")
		if caps.Adf.AdfInputCaps.AdfDuplex > 0 {
			info.AdfDuplex = true
		}
	}
	if len(sources) == 0 {
		sources = []string{"Platen", "Feeder"}
	}
	info.SupportedSources = sources

	// Resolutions
	resMap := make(map[int]bool)
	if caps.Platen != nil {
		for _, r := range caps.Platen.PlatenInputCaps.SupportedResolutions.DiscreteResolutions {
			if r.XResolution > 0 {
				resMap[r.XResolution] = true
			}
		}
	}
	if caps.Adf != nil {
		for _, r := range caps.Adf.AdfInputCaps.SupportedResolutions.DiscreteResolutions {
			if r.XResolution > 0 {
				resMap[r.XResolution] = true
			}
		}
	}
	for r := range resMap {
		info.SupportedResolutions = append(info.SupportedResolutions, r)
	}
	if len(info.SupportedResolutions) == 0 {
		info.SupportedResolutions = []int{150, 200, 300, 600}
	}
	sort.Ints(info.SupportedResolutions)

	// Color Modes
	cmMap := make(map[string]bool)
	if caps.Platen != nil {
		for _, cm := range caps.Platen.PlatenInputCaps.ColorModes {
			if cm != "" {
				cmMap[cm] = true
			}
		}
	}
	if caps.Adf != nil {
		for _, cm := range caps.Adf.AdfInputCaps.ColorModes {
			if cm != "" {
				cmMap[cm] = true
			}
		}
	}
	for cm := range cmMap {
		info.SupportedColorModes = append(info.SupportedColorModes, cm)
	}
	if len(info.SupportedColorModes) == 0 {
		info.SupportedColorModes = []string{"RGB24", "Grayscale8", "BlackAndWhite1"}
	}

	// Formats
	fmtMap := make(map[string]bool)
	if caps.Platen != nil {
		for _, df := range caps.Platen.PlatenInputCaps.DocumentFormats {
			if df != "" {
				fmtMap[df] = true
			}
		}
	}
	if caps.Adf != nil {
		for _, df := range caps.Adf.AdfInputCaps.DocumentFormats {
			if df != "" {
				fmtMap[df] = true
			}
		}
	}
	for df := range fmtMap {
		info.SupportedFormats = append(info.SupportedFormats, df)
	}
	if len(info.SupportedFormats) == 0 {
		info.SupportedFormats = []string{"application/pdf", "image/jpeg"}
	}

	// Query live status if available
	if st, err := c.QueryStatus(baseURL); err == nil {
		info.Status = st.State
		info.AdfState = st.AdfState
	}

	return info, nil
}

// QueryStatus queries /eSCL/ScannerStatus to check if scanner is Idle, Busy, or ADF is loaded.
func (c *ESCLClient) QueryStatus(rawURL string) (*ScannerStatusInfo, error) {
	baseURL, err := CleanBaseURL(rawURL)
	if err != nil {
		return nil, err
	}

	statusURL := baseURL + "/ScannerStatus"
	resp, err := c.httpClient.Get(statusURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status query returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var st rawStatusXML
	_ = xml.Unmarshal(body, &st)

	state := st.State
	if state == "" {
		state = "Idle"
	}

	adfLoaded := (st.AdfState == "ScannerAdfLoaded")
	ready := (strings.ToLower(state) == "idle" || strings.ToLower(state) == "ready")

	return &ScannerStatusInfo{
		State:     state,
		AdfState:  st.AdfState,
		AdfLoaded: adfLoaded,
		Ready:     ready,
	}, nil
}

// PerformScan creates an eSCL scan job and retrieves the scanned document (supporting multi-page ADF scans).
func (c *ESCLClient) PerformScan(opts ScanOptions) ([]byte, string, error) {
	baseURL, err := CleanBaseURL(opts.ScannerURL)
	if err != nil {
		return nil, "", err
	}

	if opts.Resolution <= 0 {
		opts.Resolution = 300
	}
	if opts.ColorMode == "" {
		opts.ColorMode = "RGB24"
	}
	if opts.Source == "" {
		opts.Source = "Platen"
	}
	if opts.Format == "" {
		opts.Format = "application/pdf"
	}

	duplexTag := ""
	if opts.Duplex && opts.Source == "Feeder" {
		duplexTag = "\n  <scan:Duplex>true</scan:Duplex>"
	}

	settingsXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<scan:ScanSettings xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:Version>2.0</pwg:Version>
  <scan:InputSource>%s</scan:InputSource>
  <scan:ColorMode>%s</scan:ColorMode>
  <scan:XResolution>%d</scan:XResolution>
  <scan:YResolution>%d</scan:YResolution>%s
  <scan:DocumentFormat>%s</scan:DocumentFormat>
</scan:ScanSettings>`, opts.Source, opts.ColorMode, opts.Resolution, opts.Resolution, duplexTag, opts.Format)

	jobsURL := baseURL + "/ScanJobs"
	req, err := http.NewRequest("POST", jobsURL, bytes.NewBufferString(settingsXML))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "text/xml")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initiate scan job: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("scanner rejected scan job (status %d): %s", resp.StatusCode, string(body))
	}

	jobLocation := resp.Header.Get("Location")
	if jobLocation == "" {
		jobLocation = jobsURL
	}

	// Resolve relative Location header against baseURL
	if !strings.HasPrefix(jobLocation, "http://") && !strings.HasPrefix(jobLocation, "https://") {
		u, _ := url.Parse(baseURL)
		if strings.HasPrefix(jobLocation, "/") {
			jobLocation = fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, jobLocation)
		} else {
			jobLocation = fmt.Sprintf("%s/%s", baseURL, jobLocation)
		}
	}

	nextDocURL := strings.TrimRight(jobLocation, "/") + "/NextDocument"

	// Fetch pages (handles single-page Platen and multi-page ADF Feeder)
	var pages [][]byte
	maxPages := 100
	if opts.Source == "Platen" {
		maxPages = 1
	}

	for pageNum := 1; pageNum <= maxPages; pageNum++ {
		docResp, err := c.httpClient.Get(nextDocURL)
		if err != nil {
			if len(pages) > 0 {
				break
			}
			return nil, "", fmt.Errorf("failed fetching scanned page: %w", err)
		}

		if docResp.StatusCode == http.StatusOK {
			data, err := io.ReadAll(docResp.Body)
			docResp.Body.Close()
			if err != nil {
				return nil, "", fmt.Errorf("failed reading page %d: %w", pageNum, err)
			}
			if len(data) > 0 {
				pages = append(pages, data)
			}
			if opts.Source == "Platen" {
				break
			}
		} else {
			docResp.Body.Close()
			// Scanner finished all pages in ADF
			break
		}
	}

	if len(pages) == 0 {
		return nil, "", fmt.Errorf("no document pages received from scanner (ADF may be empty)")
	}

	if len(pages) == 1 {
		return pages[0], opts.Format, nil
	}

	// Merge multi-page PDF scans using pure Go pdfcpu
	if strings.Contains(opts.Format, "pdf") {
		merged, err := mergePDFPages(pages)
		if err == nil {
			return merged, opts.Format, nil
		}
	}

	// If image/jpeg or merge failed, return the first page
	return pages[0], opts.Format, nil
}

func mergePDFPages(pages [][]byte) ([]byte, error) {
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages to merge")
	}
	if len(pages) == 1 {
		return pages[0], nil
	}

	tmpDir, err := os.MkdirTemp("", "escl_pdf_merge_*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	var inFiles []string
	for i, page := range pages {
		pagePath := filepath.Join(tmpDir, fmt.Sprintf("page_%03d.pdf", i+1))
		if err := os.WriteFile(pagePath, page, 0600); err != nil {
			return nil, err
		}
		inFiles = append(inFiles, pagePath)
	}

	outPath := filepath.Join(tmpDir, "merged.pdf")
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	if err := api.MergeCreateFile(inFiles, outPath, false, conf); err != nil {
		return nil, fmt.Errorf("pdfcpu merge error: %w", err)
	}

	return os.ReadFile(outPath)
}
