package scanner

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ScannerInfo struct {
	Name                 string   `json:"name"`
	URL                  string   `json:"url"`
	MakeAndModel         string   `json:"make_and_model"`
	Status               string   `json:"status"`
	SupportedColorModes  []string `json:"supported_color_modes"`
	SupportedResolutions []int    `json:"supported_resolutions"`
	SupportedSources     []string `json:"supported_sources"`
	SupportedFormats     []string `json:"supported_formats"`
}

type ScanOptions struct {
	ScannerURL string `json:"scanner_url"`
	Resolution int    `json:"resolution"` // e.g. 200, 300, 600
	ColorMode  string `json:"color_mode"` // "RGB24", "Grayscale8", "BlackAndWhite1"
	Source     string `json:"source"`     // "Platen" (flatbed), "Feeder" (ADF)
	Format     string `json:"format"`     // "application/pdf" or "image/jpeg"
}

type ESCLClient struct {
	httpClient *http.Client
}

func NewESCLClient() *ESCLClient {
	return &ESCLClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// CleanBaseURL ensures the eSCL URL ends without trailing slashes and validates the scheme
func CleanBaseURL(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid scheme: only http and https are allowed")
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid host")
	}
	base := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/eSCL"
	} else if !strings.HasSuffix(path, "/eSCL") && !strings.Contains(path, "eSCL") {
		path = path + "/eSCL"
	}
	return base + path, nil
}

// QueryCapabilities queries an eSCL scanner at the specified base URL
func (c *ESCLClient) QueryCapabilities(rawURL string) (*ScannerInfo, error) {
	baseURL, err := CleanBaseURL(rawURL)
	if err != nil {
		return nil, err
	}

	capsURL := baseURL + "/ScannerCapabilities"
	resp, err := c.httpClient.Get(capsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to scanner at %s: %w", capsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scanner returned HTTP %d on capabilities query", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	info := &ScannerInfo{
		URL:                  baseURL,
		Name:                 "Network eSCL Scanner",
		Status:               "Idle",
		SupportedColorModes:  []string{"RGB24", "Grayscale8", "BlackAndWhite1"},
		SupportedResolutions: []int{150, 200, 300, 600},
		SupportedSources:     []string{"Platen", "Feeder"},
		SupportedFormats:     []string{"application/pdf", "image/jpeg"},
	}

	// Simple XML parsing for MakeAndModel if present
	var xmlData struct {
		XMLName      xml.Name `xml:"ScannerCapabilities"`
		MakeAndModel string   `xml:"MakeAndModel"`
	}
	if err := xml.Unmarshal(body, &xmlData); err == nil && xmlData.MakeAndModel != "" {
		info.MakeAndModel = xmlData.MakeAndModel
		info.Name = xmlData.MakeAndModel
	} else {
		info.MakeAndModel = "Generic eSCL Scanner"
	}

	return info, nil
}

// PerformScan sends a ScanSettings XML to /eSCL/ScanJobs and downloads the resulting document
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

	// Standard eSCL XML payload
	settingsXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<scan:ScanSettings xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:Version>2.0</pwg:Version>
  <scan:InputSource>%s</scan:InputSource>
  <scan:ColorMode>%s</scan:ColorMode>
  <scan:XResolution>%d</scan:XResolution>
  <scan:YResolution>%d</scan:YResolution>
  <scan:DocumentFormat>%s</scan:DocumentFormat>
</scan:ScanSettings>`, opts.Source, opts.ColorMode, opts.Resolution, opts.Resolution, opts.Format)

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

	// If relative path in Location header, resolve against baseURL
	if !strings.HasPrefix(jobLocation, "http://") && !strings.HasPrefix(jobLocation, "https://") {
		u, _ := url.Parse(baseURL)
		if strings.HasPrefix(jobLocation, "/") {
			jobLocation = fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, jobLocation)
		} else {
			jobLocation = fmt.Sprintf("%s/%s", baseURL, jobLocation)
		}
	}

	// Download next document
	nextDocURL := strings.TrimRight(jobLocation, "/") + "/NextDocument"
	docResp, err := c.httpClient.Get(nextDocURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download scanned document: %w", err)
	}
	defer docResp.Body.Close()

	if docResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("failed to fetch document from %s (status %d)", nextDocURL, docResp.StatusCode)
	}

	data, err := io.ReadAll(docResp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("error reading scanned bytes: %w", err)
	}

	return data, opts.Format, nil
}

