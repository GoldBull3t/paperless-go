package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
	"paperless-go/internal/scanner"
)

func TestNetworkScannersAPI_CRUDAndTriggerScan(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_scanner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
	c := consumer.NewConsumer(db, cfg)
	q := queue.NewQueue(db, 2)
	defer q.Close()

	scanH := NewScanHandler(db, cfg, c, q)

	// Mock eSCL scanner
	mockScanner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eSCL/ScannerCapabilities":
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<scan:ScannerCapabilities xmlns:scan="http://schemas.hp.com/imaging/escl/2011/05/03" xmlns:pwg="http://www.pwg.org/schemas/2010/12/sm">
  <pwg:MakeAndModel>Office Brother ADS-1700W</pwg:MakeAndModel>
  <scan:Manufacturer>Brother</scan:Manufacturer>
  <scan:Model>ADS-1700W</scan:Model>
  <scan:Adf>
    <scan:AdfInputCaps>
      <scan:AdfDuplex>1</scan:AdfDuplex>
      <scan:SupportedResolutions>
        <scan:DiscreteResolutions>
          <scan:DiscreteResolution><scan:XResolution>300</scan:XResolution></scan:DiscreteResolution>
          <scan:DiscreteResolution><scan:XResolution>600</scan:XResolution></scan:DiscreteResolution>
        </scan:DiscreteResolutions>
      </scan:SupportedResolutions>
      <scan:ColorModes>
        <scan:ColorMode>RGB24</scan:ColorMode>
        <scan:ColorMode>Grayscale8</scan:ColorMode>
      </scan:ColorModes>
      <scan:DocumentFormats>
        <scan:DocumentFormat>application/pdf</scan:DocumentFormat>
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

		case "/eSCL/ScanJobs":
			w.Header().Set("Location", "/eSCL/ScanJobs/job_xyz")
			w.WriteHeader(http.StatusCreated)

		case "/eSCL/ScanJobs/job_xyz/NextDocument":
			w.Header().Set("Content-Type", "application/pdf")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("%PDF-1.4 Mock scanned network invoice document"))

		default:
			http.NotFound(w, r)
		}
	}))
	defer mockScanner.Close()

	r := chi.NewRouter()
	r.Route("/api/scanners", func(r chi.Router) {
		r.Get("/", scanH.GetScanners)
		r.Post("/", scanH.AddScanner)
		r.Post("/test/", scanH.TestScanner)
		r.Get("/{id}/", scanH.GetScannerByID)
		r.Delete("/{id}/", scanH.DeleteScanner)
	})
	r.Post("/api/scan/", scanH.TriggerScan)

	// 1. Initial list of scanners should be empty
	reqList := httptest.NewRequest("GET", "/api/scanners/", nil)
	wList := httptest.NewRecorder()
	r.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", wList.Code)
	}
	var scannersList []scanner.ScannerInfo
	_ = json.Unmarshal(wList.Body.Bytes(), &scannersList)
	if len(scannersList) != 0 {
		t.Errorf("Expected 0 scanners initially, got %d", len(scannersList))
	}

	// 2. Register/Add a new network scanner via POST /api/scanners/
	addBody := fmt.Sprintf(`{"url": %q, "name": "Department Scanner"}`, mockScanner.URL)
	reqAdd := httptest.NewRequest("POST", "/api/scanners/", bytes.NewBufferString(addBody))
	reqAdd.Header.Set("Content-Type", "application/json")
	wAdd := httptest.NewRecorder()
	r.ServeHTTP(wAdd, reqAdd)

	if wAdd.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created from AddScanner, got %d: %s", wAdd.Code, wAdd.Body.String())
	}

	var addedScanner scanner.ScannerInfo
	_ = json.Unmarshal(wAdd.Body.Bytes(), &addedScanner)
	if addedScanner.ID <= 0 || addedScanner.Name != "Department Scanner" {
		t.Errorf("Unexpected added scanner response: %+v", addedScanner)
	}
	if addedScanner.MakeAndModel != "Office Brother ADS-1700W" {
		t.Errorf("Expected MakeAndModel 'Office Brother ADS-1700W', got %q", addedScanner.MakeAndModel)
	}

	// 3. Query scanner by ID via GET /api/scanners/{id}/
	reqGet := httptest.NewRequest("GET", fmt.Sprintf("/api/scanners/%d/", addedScanner.ID), nil)
	wGet := httptest.NewRecorder()
	r.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from GetScannerByID, got %d", wGet.Code)
	}
	var gotScanner scanner.ScannerInfo
	_ = json.Unmarshal(wGet.Body.Bytes(), &gotScanner)
	if gotScanner.Status != "Idle" || gotScanner.AdfState != "ScannerAdfLoaded" {
		t.Errorf("Expected live status probe 'Idle'/'ScannerAdfLoaded', got: %+v", gotScanner)
	}

	// 4. Trigger scan using scanner_id
	scanBody := fmt.Sprintf(`{"scanner_id": %d, "name": "ScannedDoc.pdf", "resolution": 300, "source": "Feeder"}`, addedScanner.ID)
	reqScan := httptest.NewRequest("POST", "/api/scan/", bytes.NewBufferString(scanBody))
	reqScan.Header.Set("Content-Type", "application/json")
	wScan := httptest.NewRecorder()
	r.ServeHTTP(wScan, reqScan)

	if wScan.Code != http.StatusAccepted {
		t.Fatalf("Expected 202 Accepted from TriggerScan, got %d: %s", wScan.Code, wScan.Body.String())
	}

	var scanTaskResp map[string]interface{}
	_ = json.Unmarshal(wScan.Body.Bytes(), &scanTaskResp)
	if scanTaskResp["task_id"] == "" || scanTaskResp["status"] != "PENDING" {
		t.Errorf("Unexpected scan trigger response: %v", scanTaskResp)
	}

	// 5. Delete scanner via DELETE /api/scanners/{id}/
	reqDel := httptest.NewRequest("DELETE", fmt.Sprintf("/api/scanners/%d/", addedScanner.ID), nil)
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, reqDel)
	if wDel.Code != http.StatusNoContent {
		t.Errorf("Expected 204 No Content from DeleteScanner, got %d", wDel.Code)
	}
}

