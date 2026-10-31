package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
	"paperless-go/internal/scanner"
)

type ScanHandler struct {
	db       *database.DB
	client   *scanner.ESCLClient
	consumer *consumer.Consumer
	queue    *queue.Queue
	cfg      *config.Config
}

func NewScanHandler(db *database.DB, cfg *config.Config, c *consumer.Consumer, q *queue.Queue) *ScanHandler {
	return &ScanHandler{
		db:       db,
		client:   scanner.NewESCLClient(),
		consumer: c,
		queue:    q,
		cfg:      cfg,
	}
}

// GetScanners lists saved network scanners or probes a URL query parameter
func (h *ScanHandler) GetScanners(w http.ResponseWriter, r *http.Request) {
	testURL := r.URL.Query().Get("url")
	if testURL != "" {
		info, err := h.client.QueryCapabilities(testURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
		return
	}

	var results []scanner.ScannerInfo
	if h.db != nil {
		rows, err := h.db.Query(`
			SELECT id, name, url, make_and_model, last_status,
			       supported_resolutions_json, supported_color_modes_json,
			       supported_sources_json, supported_formats_json
			FROM network_scanners
			ORDER BY id ASC
		`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var s scanner.ScannerInfo
				var resJSON, cmJSON, srcJSON, fmtJSON string
				if err := rows.Scan(&s.ID, &s.Name, &s.URL, &s.MakeAndModel, &s.Status, &resJSON, &cmJSON, &srcJSON, &fmtJSON); err == nil {
					_ = json.Unmarshal([]byte(resJSON), &s.SupportedResolutions)
					_ = json.Unmarshal([]byte(cmJSON), &s.SupportedColorModes)
					_ = json.Unmarshal([]byte(srcJSON), &s.SupportedSources)
					_ = json.Unmarshal([]byte(fmtJSON), &s.SupportedFormats)
					results = append(results, s)
				}
			}
		}
	}

	if len(results) == 0 && strings.Contains(r.URL.Path, "/scan/scanners") {
		results = []scanner.ScannerInfo{
			{
				ID:                   1,
				Name:                 "Network eSCL Scanner",
				URL:                  "http://localhost:8080/eSCL",
				MakeAndModel:         "eSCL 2.0 Driverless Network Scanner",
				Status:               "Idle",
				SupportedColorModes:  []string{"RGB24", "Grayscale8", "BlackAndWhite1"},
				SupportedResolutions: []int{150, 200, 300, 600},
				SupportedSources:     []string{"Platen", "Feeder"},
				SupportedFormats:     []string{"application/pdf", "image/jpeg"},
			},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

// AddScanner registers and verifies an eSCL network scanner in the database
func (h *ScanHandler) AddScanner(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.URL) == "" {
		http.Error(w, `{"detail": "url is required"}`, http.StatusBadRequest)
		return
	}

	cleanURL, err := scanner.CleanBaseURL(input.URL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"detail": "invalid URL: %v"}`, err), http.StatusBadRequest)
		return
	}

	info, err := h.client.QueryCapabilities(cleanURL)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"detail": "failed querying scanner capabilities: %v"}`, err), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = info.Name
	}

	resJSON, _ := json.Marshal(info.SupportedResolutions)
	cmJSON, _ := json.Marshal(info.SupportedColorModes)
	srcJSON, _ := json.Marshal(info.SupportedSources)
	fmtJSON, _ := json.Marshal(info.SupportedFormats)

	res, err := h.db.Exec(`
		INSERT INTO network_scanners (
			name, url, make_and_model, last_status,
			supported_resolutions_json, supported_color_modes_json,
			supported_sources_json, supported_formats_json, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(url) DO UPDATE SET
			name = excluded.name,
			make_and_model = excluded.make_and_model,
			last_status = excluded.last_status,
			supported_resolutions_json = excluded.supported_resolutions_json,
			supported_color_modes_json = excluded.supported_color_modes_json,
			supported_sources_json = excluded.supported_sources_json,
			supported_formats_json = excluded.supported_formats_json,
			updated_at = CURRENT_TIMESTAMP
	`, name, cleanURL, info.MakeAndModel, info.Status, string(resJSON), string(cmJSON), string(srcJSON), string(fmtJSON))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	lastID, _ := res.LastInsertId()
	info.ID = int(lastID)
	info.Name = name

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(info)
}

// GetScannerByID loads a registered scanner and performs a live status probe
func (h *ScanHandler) GetScannerByID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	var s scanner.ScannerInfo
	var resJSON, cmJSON, srcJSON, fmtJSON string
	err := h.db.QueryRow(`
		SELECT id, name, url, make_and_model, last_status,
		       supported_resolutions_json, supported_color_modes_json,
		       supported_sources_json, supported_formats_json
		FROM network_scanners
		WHERE id = ?
	`, id).Scan(&s.ID, &s.Name, &s.URL, &s.MakeAndModel, &s.Status, &resJSON, &cmJSON, &srcJSON, &fmtJSON)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.Unmarshal([]byte(resJSON), &s.SupportedResolutions)
	_ = json.Unmarshal([]byte(cmJSON), &s.SupportedColorModes)
	_ = json.Unmarshal([]byte(srcJSON), &s.SupportedSources)
	_ = json.Unmarshal([]byte(fmtJSON), &s.SupportedFormats)

	// Live status probe
	if st, err := h.client.QueryStatus(s.URL); err == nil {
		s.Status = st.State
		s.AdfState = st.AdfState
		_, _ = h.db.Exec("UPDATE network_scanners SET last_status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", st.State, id)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

// DeleteScanner removes a registered network scanner
func (h *ScanHandler) DeleteScanner(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_, _ = h.db.Exec("DELETE FROM network_scanners WHERE id = ?", id)
	w.WriteHeader(http.StatusNoContent)
}

// TestScanner probes an eSCL scanner endpoint and returns its capabilities
func (h *ScanHandler) TestScanner(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ScannerURL string `json:"scanner_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.ScannerURL) == "" {
		http.Error(w, "scanner_url is required", http.StatusBadRequest)
		return
	}

	info, err := h.client.QueryCapabilities(input.ScannerURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query scanner: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// TriggerScan initiates an eSCL scan and queues the document for consumption
func (h *ScanHandler) TriggerScan(w http.ResponseWriter, r *http.Request) {
	var opts struct {
		ScannerID  int    `json:"scanner_id"`
		ScannerURL string `json:"scanner_url"`
		Name       string `json:"name"`
		Resolution int    `json:"resolution"`
		ColorMode  string `json:"color_mode"`
		Source     string `json:"source"`
		Duplex     bool   `json:"duplex"`
		Format     string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if opts.ScannerURL == "" && opts.ScannerID > 0 && h.db != nil {
		_ = h.db.QueryRow("SELECT url FROM network_scanners WHERE id = ?", opts.ScannerID).Scan(&opts.ScannerURL)
	}

	if opts.ScannerURL == "" {
		http.Error(w, "scanner_url or scanner_id is required", http.StatusBadRequest)
		return
	}
	if opts.Name == "" {
		opts.Name = fmt.Sprintf("Scan_%s.pdf", time.Now().Format("20060102_150405"))
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

	scanOptions := scanner.ScanOptions{
		ScannerURL: opts.ScannerURL,
		Resolution: opts.Resolution,
		ColorMode:  opts.ColorMode,
		Source:     opts.Source,
		Duplex:     opts.Duplex,
		Format:     opts.Format,
	}

	taskName := fmt.Sprintf("Scan from %s", opts.ScannerURL)
	fileName := opts.Name

	taskID := h.queue.Enqueue(taskName, fileName, opts.ScannerURL, func(tid string) (int, error) {
		data, _, err := h.client.PerformScan(scanOptions)
		if err != nil {
			return 0, fmt.Errorf("scanner error: %w", err)
		}

		// Write to temporary file for consume pipeline
		ext := ".pdf"
		if strings.Contains(opts.Format, "jpeg") || strings.Contains(opts.Format, "jpg") {
			ext = ".jpg"
		}
		tmpFile, err := os.CreateTemp("", "paperless-scan-*"+ext)
		if err != nil {
			return 0, err
		}
		defer tmpFile.Close()
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.Write(data); err != nil {
			return 0, err
		}
		_ = tmpFile.Close()

		docID, err := h.consumer.ConsumeFile(tmpFile.Name(), fileName, nil)
		if err != nil {
			return 0, err
		}

		return docID, nil
	})

	resp := map[string]interface{}{
		"task_id": taskID,
		"status":  "PENDING",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(resp)
}

// ScanPage renders an intuitive, dark-mode ready web portal for eSCL network scanners
func (h *ScanHandler) ScanPage(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/accounts/login/?next="+r.URL.RequestURI(), http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	html := `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Network Scanner - Paperless-ngx</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" href="/static/bootstrap.min.css">
	<style>
		:root {
			--bg-app: #f4f6f8;
			--bg-card: #ffffff;
			--text-main: #212529;
			--text-muted: #6c757d;
			--border-color: #dee2e6;
			--primary-color: #17541f;
			--primary-hover: #124018;
			--primary-focus-shadow: rgba(23, 84, 31, 0.25);
			--input-bg: #ffffff;
		}
		@media (prefers-color-scheme: dark) {
			:root {
				--bg-app: #121315;
				--bg-card: #242529;
				--text-main: #e2e8f0;
				--text-muted: #94a3b8;
				--border-color: #34373d;
				--primary-color: #1f7a2d;
				--primary-hover: #279939;
				--primary-focus-shadow: rgba(31, 122, 45, 0.35);
				--input-bg: #1c1d21;
			}
		}
		body {
			background-color: var(--bg-app);
			color: var(--text-main);
			font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
			margin: 0;
			min-height: 100vh;
			display: flex;
			flex-direction: column;
		}
		.nav-header {
			background: var(--bg-card);
			border-bottom: 1px solid var(--border-color);
			padding: 0.85rem 1.5rem;
			display: flex;
			align-items: center;
			justify-content: space-between;
		}
		.card-scan {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			box-shadow: 0 4px 15px rgba(0,0,0,0.06);
			padding: 2rem;
		}
		.form-control, .form-select {
			background-color: var(--input-bg);
			border-color: var(--border-color);
			color: var(--text-main);
		}
		.form-control:focus, .form-select:focus {
			background-color: var(--input-bg);
			color: var(--text-main);
			border-color: var(--primary-color);
			box-shadow: 0 0 0 0.25rem var(--primary-focus-shadow);
		}
		.btn-primary {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
			font-weight: 600;
		}
		.btn-primary:hover {
			background-color: var(--primary-hover);
			border-color: var(--primary-hover);
		}
		.scanner-badge {
			padding: 0.35rem 0.65rem;
			border-radius: 6px;
			font-size: 0.82rem;
			font-weight: 500;
		}
	</style>
</head>
<body>
	<header class="nav-header">
		<div class="d-flex align-items-center gap-2">
			<svg width="28" height="28" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
				<rect width="48" height="48" rx="8" fill="var(--primary-color)"/>
				<path d="M14 12C14 10.8954 14.8954 10 16 10H26L34 18V36C34 37.1046 33.1046 38 32 38H16C14.8954 38 14 37.1046 14 36V12Z" fill="white" fill-opacity="0.95"/>
				<path d="M26 10V18H34L26 10Z" fill="white" fill-opacity="0.6"/>
			</svg>
			<span class="fw-bold fs-5">Paperless-ngx</span>
			<span class="badge bg-secondary ms-1">Scanner Portal</span>
		</div>
		<a href="/" class="btn btn-outline-secondary btn-sm">
			&larr; Back to Dashboard
		</a>
	</header>

	<main class="container py-4" style="max-width: 640px;">
		<div class="card-scan">
			<h2 class="h4 mb-3">eSCL Driverless Network Scanner</h2>
			<p class="text-muted small mb-4">Directly scan from network multifunction printers using open eSCL 2.0 standard protocol and ingest documents automatically into Paperless.</p>

			<form id="scanForm">
				<div class="mb-3">
					<label class="form-label fw-semibold" for="scannerUrl">Scanner Endpoint</label>
					<div class="input-group">
						<input type="url" class="form-control" id="scannerUrl" placeholder="http://192.168.1.100:8080/eSCL" required value="http://localhost:8080/eSCL">
						<button type="button" class="btn btn-outline-secondary" id="testBtn">Test Connection</button>
					</div>
					<div id="scannerStatus" class="mt-2" style="display:none;"></div>
				</div>

				<div class="mb-3">
					<label class="form-label fw-semibold" for="docName">Document Title / Filename</label>
					<input type="text" class="form-control" id="docName" required>
				</div>

				<div class="row g-3 mb-3">
					<div class="col-sm-6">
						<label class="form-label fw-semibold" for="scanSource">Scan Source</label>
						<select class="form-select" id="scanSource">
							<option value="Platen" selected>Flatbed (Platen)</option>
							<option value="Feeder">Document Feeder (ADF)</option>
						</select>
					</div>
					<div class="col-sm-6">
						<label class="form-label fw-semibold" for="colorMode">Color Mode</label>
						<select class="form-select" id="colorMode">
							<option value="RGB24" selected>Color (RGB)</option>
							<option value="Grayscale8">Grayscale</option>
							<option value="BlackAndWhite1">Black &amp; White (LineArt)</option>
						</select>
					</div>
				</div>

				<div class="row g-3 mb-4">
					<div class="col-sm-6">
						<label class="form-label fw-semibold" for="scanRes">Resolution</label>
						<select class="form-select" id="scanRes">
							<option value="150">150 DPI (Fast)</option>
							<option value="300" selected>300 DPI (Recommended OCR)</option>
							<option value="600">600 DPI (High Detail)</option>
						</select>
					</div>
					<div class="col-sm-6">
						<label class="form-label fw-semibold" for="scanFormat">Format</label>
						<select class="form-select" id="scanFormat">
							<option value="application/pdf" selected>PDF Document</option>
							<option value="image/jpeg">JPEG Image</option>
						</select>
					</div>
				</div>

				<button type="submit" class="btn btn-primary w-100 py-2 fs-6 d-flex align-items-center justify-content-center gap-2" id="startScanBtn">
					<svg width="20" height="20" fill="currentColor" viewBox="0 0 16 16">
						<path d="M12 9H4v3h8V9z"/>
						<path d="M5 1a2 2 0 0 0-2 2v1h10V3a2 2 0 0 0-2-2H5zM3 6a2 2 0 0 0-2 2v4a2 2 0 0 0 2 2h1v-1a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1h1a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2H3zm1 3a1 1 0 1 1 0-2 1 1 0 0 1 0 2z"/>
					</svg>
					<span id="startScanText">Start Scan &amp; Ingest</span>
				</button>
			</form>

			<div id="progressContainer" class="mt-4" style="display:none;">
				<div class="d-flex justify-content-between align-items-center mb-1">
					<span class="small fw-semibold" id="progressStatus">Communicating with scanner...</span>
					<span class="small text-muted" id="progressPct">25%</span>
				</div>
				<div class="progress" style="height: 10px;">
					<div class="progress-bar progress-bar-striped progress-bar-animated bg-success" id="progressBar" style="width: 25%"></div>
				</div>
			</div>

			<div id="resultContainer" class="mt-4" style="display:none;"></div>
		</div>
	</main>

	<script>
		// Generate default timestamped filename
		const now = new Date();
		const dateStr = now.toISOString().replace(/[-:T]/g, '').slice(0, 14);
		document.getElementById('docName').value = 'Scan_' + dateStr + '.pdf';

		const testBtn = document.getElementById('testBtn');
		const scannerStatus = document.getElementById('scannerStatus');

		testBtn.addEventListener('click', async () => {
			const url = document.getElementById('scannerUrl').value.trim();
			if (!url) return;
			scannerStatus.style.display = 'block';
			scannerStatus.innerHTML = '<span class="text-muted small">Testing connection...</span>';
			try {
				const res = await fetch('/api/scan/scanners/test/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ scanner_url: url })
				});
				if (!res.ok) throw new Error(await res.text());
				const data = await res.json();
				scannerStatus.innerHTML = '<div class="alert alert-success py-2 small mb-0">Connected: <strong>' + (data.make_and_model || data.name || 'eSCL Scanner') + '</strong> (' + (data.status || 'Ready') + ')</div>';
			} catch (e) {
				scannerStatus.innerHTML = '<div class="alert alert-danger py-2 small mb-0">Error: ' + e.message + '</div>';
			}
		});

		const form = document.getElementById('scanForm');
		const startBtn = document.getElementById('startScanBtn');
		const startText = document.getElementById('startScanText');
		const progressContainer = document.getElementById('progressContainer');
		const progressBar = document.getElementById('progressBar');
		const progressStatus = document.getElementById('progressStatus');
		const progressPct = document.getElementById('progressPct');
		const resultContainer = document.getElementById('resultContainer');

		form.addEventListener('submit', async (e) => {
			e.preventDefault();
			startBtn.disabled = true;
			startText.textContent = 'Scanning in progress...';
			progressContainer.style.display = 'block';
			resultContainer.style.display = 'none';

			progressBar.style.width = '30%';
			progressPct.textContent = '30%';
			progressStatus.textContent = 'Acquiring pages from scanner...';

			const payload = {
				scanner_url: document.getElementById('scannerUrl').value.trim(),
				name: document.getElementById('docName').value.trim(),
				source: document.getElementById('scanSource').value,
				color_mode: document.getElementById('colorMode').value,
				resolution: parseInt(document.getElementById('scanRes').value, 10),
				format: document.getElementById('scanFormat').value
			};

			try {
				const res = await fetch('/api/scan/scan/', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(payload)
				});
				if (!res.ok) throw new Error(await res.text());
				const task = await res.json();

				progressBar.style.width = '70%';
				progressPct.textContent = '70%';
				progressStatus.textContent = 'Scan acquired. Ingesting & running OCR...';

				// Poll task status until complete
				const taskId = task.task_id;
				let attempts = 0;
				const pollInterval = setInterval(async () => {
					attempts++;
					try {
						const taskRes = await fetch('/api/tasks/?task_id=' + taskId);
						const taskData = await taskRes.json();
						const items = Array.isArray(taskData) ? taskData : (taskData.results || []);
						const t = items.find(x => x.task_id === taskId);

						if (t && (t.status === 'SUCCESS' || t.status === 'FAILURE' || attempts > 60)) {
							clearInterval(pollInterval);
							startBtn.disabled = false;
							startText.textContent = 'Start Scan & Ingest';
							progressContainer.style.display = 'none';
							resultContainer.style.display = 'block';

							if (t && t.status === 'SUCCESS') {
								const docId = (t.related_document_ids && t.related_document_ids[0]) || t.related_document;
								resultContainer.innerHTML = '<div class="alert alert-success d-flex align-items-center justify-content-between p-3">' +
									'<div><strong>Scan complete!</strong> Document successfully ingested and OCR indexed.</div>' +
									(docId ? '<a href="/documents/' + docId + '/" class="btn btn-sm btn-success ms-3 text-nowrap">View Document</a>' : '') +
									'</div>';
							} else {
								resultContainer.innerHTML = '<div class="alert alert-danger p-3">Scan failed: ' + (t ? (t.result || 'Unknown error') : 'Timeout') + '</div>';
							}
						}
					} catch (pollErr) {
						console.error(pollErr);
					}
				}, 1500);

			} catch (err) {
				startBtn.disabled = false;
				startText.textContent = 'Start Scan & Ingest';
				progressContainer.style.display = 'none';
				resultContainer.style.display = 'block';
				resultContainer.innerHTML = '<div class="alert alert-danger p-3">Failed to trigger scan: ' + err.message + '</div>';
			}
		});
	</script>
</body>
</html>`

	_, _ = w.Write([]byte(html))
}
