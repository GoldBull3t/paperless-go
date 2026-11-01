package metrics

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

var startTime = time.Now()

type Collector struct {
	db  *database.DB
	cfg *config.Config
}

func NewCollector(db *database.DB, cfg *config.Config) *Collector {
	return &Collector{db: db, cfg: cfg}
}

func (c *Collector) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// 1. Uptime
		uptime := time.Since(startTime).Seconds()
		fmt.Fprintf(w, "# HELP paperless_uptime_seconds Process uptime in seconds\n")
		fmt.Fprintf(w, "# TYPE paperless_uptime_seconds gauge\n")
		fmt.Fprintf(w, "paperless_uptime_seconds %.2f\n\n", uptime)

		// 2. Documents metrics
		var docTotal, docTrash, docVault int
		_ = c.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL").Scan(&docTotal)
		_ = c.db.QueryRow("SELECT COUNT(*) FROM documents WHERE deleted_at IS NOT NULL").Scan(&docTrash)
		_ = c.db.QueryRow("SELECT COUNT(*) FROM documents WHERE is_vault = 1").Scan(&docVault)

		fmt.Fprintf(w, "# HELP paperless_documents_total Total documents count by category\n")
		fmt.Fprintf(w, "# TYPE paperless_documents_total gauge\n")
		fmt.Fprintf(w, "paperless_documents_total{type=\"active\"} %d\n", docTotal)
		fmt.Fprintf(w, "paperless_documents_total{type=\"trash\"} %d\n", docTrash)
		fmt.Fprintf(w, "paperless_documents_total{type=\"vault\"} %d\n\n", docVault)

		// 3. Queue Tasks metrics
		var tasksSuccess, tasksFailed, tasksPending int
		_ = c.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'SUCCESS'").Scan(&tasksSuccess)
		_ = c.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status = 'FAILURE'").Scan(&tasksFailed)
		_ = c.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status IN ('PENDING', 'STARTED')").Scan(&tasksPending)

		fmt.Fprintf(w, "# HELP paperless_queue_tasks_total Cumulative task completions by status\n")
		fmt.Fprintf(w, "# TYPE paperless_queue_tasks_total counter\n")
		fmt.Fprintf(w, "paperless_queue_tasks_total{status=\"success\"} %d\n", tasksSuccess)
		fmt.Fprintf(w, "paperless_queue_tasks_total{status=\"failure\"} %d\n\n", tasksFailed)

		fmt.Fprintf(w, "# HELP paperless_queue_pending_tasks Tasks currently in queue\n")
		fmt.Fprintf(w, "# TYPE paperless_queue_pending_tasks gauge\n")
		fmt.Fprintf(w, "paperless_queue_pending_tasks %d\n\n", tasksPending)

		// 4. Mail processed
		var mailCount int
		_ = c.db.QueryRow("SELECT COUNT(*) FROM processed_mail").Scan(&mailCount)
		fmt.Fprintf(w, "# HELP paperless_mail_processed_total Cumulative emails ingested\n")
		fmt.Fprintf(w, "# TYPE paperless_mail_processed_total counter\n")
		fmt.Fprintf(w, "paperless_mail_processed_total %d\n\n", mailCount)

		// 5. Go Runtime & Memory Metrics
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Fprintf(w, "# HELP paperless_goroutines Number of running goroutines\n")
		fmt.Fprintf(w, "# TYPE paperless_goroutines gauge\n")
		fmt.Fprintf(w, "paperless_goroutines %d\n\n", runtime.NumGoroutine())

		fmt.Fprintf(w, "# HELP paperless_mem_alloc_bytes Bytes allocated and not yet freed\n")
		fmt.Fprintf(w, "# TYPE paperless_mem_alloc_bytes gauge\n")
		fmt.Fprintf(w, "paperless_mem_alloc_bytes %d\n\n", m.Alloc)

		// 6. Database and Storage Disk Space
		if c.cfg != nil {
			if fi, err := os.Stat(c.cfg.DatabasePath); err == nil {
				fmt.Fprintf(w, "# HELP paperless_db_size_bytes SQLite database file size\n")
				fmt.Fprintf(w, "# TYPE paperless_db_size_bytes gauge\n")
				fmt.Fprintf(w, "paperless_db_size_bytes %d\n\n", fi.Size())
			}
			walPath := c.cfg.DatabasePath + "-wal"
			if fi, err := os.Stat(walPath); err == nil {
				fmt.Fprintf(w, "# HELP paperless_db_wal_bytes SQLite WAL file size\n")
				fmt.Fprintf(w, "# TYPE paperless_db_wal_bytes gauge\n")
				fmt.Fprintf(w, "paperless_db_wal_bytes %d\n\n", fi.Size())
			}
		}
	}
}

