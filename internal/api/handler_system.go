package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

var (
	startTime      = time.Now()
	requestCounter uint64
	requestMu      sync.Mutex
)

// IncrementRequestCount counts incoming HTTP requests for metrics
func IncrementRequestCount() {
	requestMu.Lock()
	requestCounter++
	requestMu.Unlock()
}

type SystemHandler struct {
	db  *database.DB
	cfg *config.Config
}

func NewSystemHandler(db *database.DB, cfg *config.Config) *SystemHandler {
	return &SystemHandler{db: db, cfg: cfg}
}

// SystemStatus (/api/system/status/) returns live system health and performance statistics
func (h *SystemHandler) SystemStatus(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var docCount, tagCount, corrCount, typeCount int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL").Scan(&docCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM correspondents").Scan(&corrCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM document_types").Scan(&typeCount)

	var integrity string
	_ = h.db.QueryRow("PRAGMA quick_check").Scan(&integrity)
	if integrity == "" {
		integrity = "ok"
	}

	uptimeSec := int(time.Since(startTime).Seconds())

	status := map[string]interface{}{
		"status":                "healthy",
		"version":               "3.3.3",
		"engine":                "paperless-go",
		"uptime_seconds":        uptimeSec,
		"uptime_human":          time.Since(startTime).Truncate(time.Second).String(),
		"go_version":            runtime.Version(),
		"os_arch":               fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		"goroutines":            runtime.NumGoroutine(),
		"memory_alloc_mb":       float64(m.Alloc) / 1024 / 1024,
		"memory_sys_mb":         float64(m.Sys) / 1024 / 1024,
		"memory_heap_in_use_mb": float64(m.HeapInuse) / 1024 / 1024,
		"gc_cycles":             m.NumGC,
		"database": map[string]interface{}{
			"engine":    "SQLite WAL + FTS5",
			"integrity": integrity,
		},
		"counts": map[string]int{
			"documents":      docCount,
			"tags":           tagCount,
			"correspondents": corrCount,
			"document_types": typeCount,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// SystemDiagnostics (/api/system/diagnostics/) provides in-depth inspection for sysadmins
func (h *SystemHandler) SystemDiagnostics(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user != nil && !user.IsSuperuser {
		http.Error(w, `{"detail": "Only superusers can access full diagnostics"}`, http.StatusForbidden)
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Check disk directories
	origDir := filepath.Join(h.cfg.MediaDir, "documents", "originals")
	thumbDir := filepath.Join(h.cfg.MediaDir, "documents", "thumbnails")

	origWritable := testDirWritable(origDir)
	thumbWritable := testDirWritable(thumbDir)

	var fullIntegrity string
	_ = h.db.QueryRow("PRAGMA integrity_check(1)").Scan(&fullIntegrity)

	diag := map[string]interface{}{
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"version":        "3.3.3",
		"go_runtime":     runtime.Version(),
		"num_cpu":        runtime.NumCPU(),
		"num_goroutines": runtime.NumGoroutine(),
		"memory": map[string]interface{}{
			"alloc_bytes":       m.Alloc,
			"total_alloc_bytes": m.TotalAlloc,
			"sys_bytes":         m.Sys,
			"heap_alloc_bytes":  m.HeapAlloc,
			"num_gc":            m.NumGC,
		},
		"storage": map[string]interface{}{
			"data_dir":           h.cfg.DataDir,
			"media_dir":          h.cfg.MediaDir,
			"database_path":      h.cfg.DatabasePath,
			"originals_writable": origWritable,
			"thumbnails_writable": thumbWritable,
		},
		"database": map[string]interface{}{
			"integrity_check": fullIntegrity,
			"wal_mode":        true,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(diag)
}

// PrometheusMetrics (/metrics) exports standard Prometheus metrics
func (h *SystemHandler) PrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var docCount, tagCount, corrCount int
	_ = h.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL").Scan(&docCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount)
	_ = h.db.QueryRow("SELECT COUNT(*) FROM correspondents").Scan(&corrCount)

	uptimeSec := time.Since(startTime).Seconds()

	requestMu.Lock()
	reqs := requestCounter
	requestMu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	out := fmt.Sprintf(`# HELP paperless_go_info Information about Paperless-Go instance
# TYPE paperless_go_info gauge
paperless_go_info{version="3.3.3",go="%s",os="%s",arch="%s"} 1

# HELP paperless_go_uptime_seconds Server uptime in seconds
# TYPE paperless_go_uptime_seconds counter
paperless_go_uptime_seconds %.2f

# HELP paperless_go_requests_total Total number of HTTP requests processed
# TYPE paperless_go_requests_total counter
paperless_go_requests_total %d

# HELP paperless_go_goroutines Current number of goroutines
# TYPE paperless_go_goroutines gauge
paperless_go_goroutines %d

# HELP paperless_go_memory_alloc_bytes Bytes allocated and still in use
# TYPE paperless_go_memory_alloc_bytes gauge
paperless_go_memory_alloc_bytes %d

# HELP paperless_go_memory_sys_bytes Total bytes of memory obtained from OS
# TYPE paperless_go_memory_sys_bytes gauge
paperless_go_memory_sys_bytes %d

# HELP paperless_go_documents_total Total number of active documents
# TYPE paperless_go_documents_total gauge
paperless_go_documents_total %d

# HELP paperless_go_tags_total Total number of tags
# TYPE paperless_go_tags_total gauge
paperless_go_tags_total %d

# HELP paperless_go_correspondents_total Total number of correspondents
# TYPE paperless_go_correspondents_total gauge
paperless_go_correspondents_total %d
`, runtime.Version(), runtime.GOOS, runtime.GOARCH, uptimeSec, reqs, runtime.NumGoroutine(), m.Alloc, m.Sys, docCount, tagCount, corrCount)

	_, _ = w.Write([]byte(out))
}

func testDirWritable(dir string) bool {
	_ = os.MkdirAll(dir, 0755)
	tmp, err := os.CreateTemp(dir, ".test_write_*")
	if err != nil {
		return false
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpName)
	return true
}
