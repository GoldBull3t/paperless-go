package scanner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestESCLClientWorkflow(t *testing.T) {
	// Create mock eSCL scanner server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eSCL/ScannerCapabilities":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<ScannerCapabilities><MakeAndModel>HP OfficeJet Pro Test</MakeAndModel></ScannerCapabilities>`)

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

