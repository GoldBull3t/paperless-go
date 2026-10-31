package scanner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestESCLClientWorkflow(t *testing.T) {
	// Create mock eSCL scanner server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eSCL/ScannerCapabilities":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerCapabilities xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:MakeAndModel>HP OfficeJet Pro Test</pwg:MakeAndModel>
  <scan:Manufacturer>HP</scan:Manufacturer>
  <scan:Model>OfficeJet Pro</scan:Model>
  <scan:Platen>
    <scan:PlatenInputCaps>
      <scan:SupportedResolutions>
        <scan:DiscreteResolutions>
          <scan:DiscreteResolution><scan:XResolution>200</scan:XResolution></scan:DiscreteResolution>
          <scan:DiscreteResolution><scan:XResolution>300</scan:XResolution></scan:DiscreteResolution>
        </scan:DiscreteResolutions>
      </scan:SupportedResolutions>
      <scan:ColorModes>
        <scan:ColorMode>RGB24</scan:ColorMode>
        <scan:ColorMode>Grayscale8</scan:ColorMode>
      </scan:ColorModes>
      <scan:DocumentFormats>
        <scan:DocumentFormat>application/pdf</scan:DocumentFormat>
      </scan:DocumentFormats>
    </scan:PlatenInputCaps>
  </scan:Platen>
</scan:ScannerCapabilities>`)

		case "/eSCL/ScannerStatus":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerStatus xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:State>Idle</pwg:State>
</scan:ScannerStatus>`)

		case "/eSCL/ScanJobs":
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Location", "/eSCL/ScanJobs/job123")
			w.WriteHeader(http.StatusCreated)

		case "/eSCL/ScanJobs/job123/NextDocument":
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("%PDF-1.4 mock scan test content"))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewESCLClient()

	// 1. Test QueryCapabilities
	info, err := client.QueryCapabilities(server.URL)
	if err != nil {
		t.Fatalf("QueryCapabilities failed: %v", err)
	}
	if info.MakeAndModel != "HP OfficeJet Pro Test" {
		t.Errorf("Expected MakeAndModel 'HP OfficeJet Pro Test', got %q", info.MakeAndModel)
	}
	if len(info.SupportedResolutions) != 2 || info.SupportedResolutions[0] != 200 || info.SupportedResolutions[1] != 300 {
		t.Errorf("Expected resolutions [200, 300], got %v", info.SupportedResolutions)
	}

	// 2. Test PerformScan
	opts := ScanOptions{
		ScannerURL: server.URL,
		Resolution: 300,
		ColorMode:  "RGB24",
		Source:     "Platen",
		Format:     "application/pdf",
	}

	data, format, err := client.PerformScan(opts)
	if err != nil {
		t.Fatalf("PerformScan failed: %v", err)
	}
	if format != "application/pdf" {
		t.Errorf("Expected format 'application/pdf', got %q", format)
	}
	if string(data) != "%PDF-1.4 mock scan test content" {
		t.Errorf("Unexpected scanned data content: %s", string(data))
	}
}

func TestESCLClient_FullCapabilitiesAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eSCL/ScannerCapabilities":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerCapabilities xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:MakeAndModel>Brother ADS-2700W</pwg:MakeAndModel>
  <scan:Manufacturer>Brother</scan:Manufacturer>
  <scan:Model>ADS-2700W</scan:Model>
  <scan:Adf>
    <scan:AdfInputCaps>
      <scan:AdfDuplex>1</scan:AdfDuplex>
      <scan:SupportedResolutions>
        <scan:DiscreteResolutions>
          <scan:DiscreteResolution><scan:XResolution>150</scan:XResolution></scan:DiscreteResolution>
          <scan:DiscreteResolution><scan:XResolution>300</scan:XResolution></scan:DiscreteResolution>
          <scan:DiscreteResolution><scan:XResolution>600</scan:XResolution></scan:DiscreteResolution>
        </scan:DiscreteResolutions>
      </scan:SupportedResolutions>
      <scan:ColorModes>
        <scan:ColorMode>RGB24</scan:ColorMode>
        <scan:ColorMode>Grayscale8</scan:ColorMode>
        <scan:ColorMode>BlackAndWhite1</scan:ColorMode>
      </scan:ColorModes>
      <scan:DocumentFormats>
        <scan:DocumentFormat>application/pdf</scan:DocumentFormat>
        <scan:DocumentFormat>image/jpeg</scan:DocumentFormat>
      </scan:DocumentFormats>
    </scan:AdfInputCaps>
  </scan:Adf>
</scan:ScannerCapabilities>`)

		case "/eSCL/ScannerStatus":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerStatus xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:State>Idle</pwg:State>
  <scan:AdfState>ScannerAdfLoaded</scan:AdfState>
</scan:ScannerStatus>`)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewESCLClient()
	info, err := client.QueryCapabilities(server.URL)
	if err != nil {
		t.Fatalf("QueryCapabilities failed: %v", err)
	}

	if info.MakeAndModel != "Brother ADS-2700W" {
		t.Errorf("Expected 'Brother ADS-2700W', got %q", info.MakeAndModel)
	}
	if !info.AdfDuplex {
		t.Errorf("Expected AdfDuplex=true for duplex ADF scanner")
	}
	if info.Status != "Idle" {
		t.Errorf("Expected status 'Idle', got %q", info.Status)
	}
	if info.AdfState != "ScannerAdfLoaded" {
		t.Errorf("Expected AdfState 'ScannerAdfLoaded', got %q", info.AdfState)
	}

	st, err := client.QueryStatus(server.URL)
	if err != nil {
		t.Fatalf("QueryStatus failed: %v", err)
	}
	if !st.Ready || !st.AdfLoaded {
		t.Errorf("Expected Ready=true and AdfLoaded=true, got %+v", st)
	}
}

func TestESCLClient_MultiPageADFScan(t *testing.T) {
	var pageCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eSCL/ScanJobs":
			w.Header().Set("Location", "/eSCL/ScanJobs/adf_job_001")
			w.WriteHeader(http.StatusCreated)

		case "/eSCL/ScanJobs/adf_job_001/NextDocument":
			count := atomic.AddInt32(&pageCount, 1)
			if count <= 2 {
				w.Header().Set("Content-Type", "application/pdf")
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "%%PDF-1.4 Mock Page %d content", count)
			} else {
				// ADF feeder empty
				w.WriteHeader(http.StatusNotFound)
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewESCLClient()
	opts := ScanOptions{
		ScannerURL: server.URL,
		Source:     "Feeder",
		Format:     "application/pdf",
		Duplex:     true,
	}

	data, format, err := client.PerformScan(opts)
	if err != nil {
		t.Fatalf("PerformScan failed: %v", err)
	}
	if format != "application/pdf" {
		t.Errorf("Expected 'application/pdf', got %q", format)
	}
	if len(data) == 0 {
		t.Errorf("Expected scanned bytes, got empty")
	}
}

func TestCleanBaseURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		hasErr   bool
	}{
		{"192.168.1.100", "http://192.168.1.100/eSCL", false},
		{"http://192.168.1.100:8080/", "http://192.168.1.100:8080/eSCL", false},
		{"https://scanner.local/eSCL", "https://scanner.local/eSCL", false},
		{"https://scanner.local/eSCL/", "https://scanner.local/eSCL", false},
		{"ftp://scanner.local", "", true},
		{"", "", true},
	}

	for _, tc := range tests {
		actual, err := CleanBaseURL(tc.input)
		if tc.hasErr {
			if err == nil {
				t.Errorf("CleanBaseURL(%q) expected error, got nil", tc.input)
			}
		} else {
			if err != nil {
				t.Errorf("CleanBaseURL(%q) unexpected error: %v", tc.input, err)
			}
			if actual != tc.expected {
				t.Errorf("CleanBaseURL(%q) = %q; want %q", tc.input, actual, tc.expected)
			}
		}
	}
}
