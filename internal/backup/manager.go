package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

type Manager struct {
	db  *database.DB
	cfg *config.Config
}

func NewManager(db *database.DB, cfg *config.Config) *Manager {
	return &Manager{db: db, cfg: cfg}
}

// CreateSnapshot creates an immediate zero-downtime backup and trims expired snapshots
func (m *Manager) CreateSnapshot() (string, error) {
	if err := os.MkdirAll(m.cfg.BackupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().UTC().Format("2006-01-02_15-04-05")
	dstName := fmt.Sprintf("paperless-backup-%s.db", timestamp)
	dstPath := filepath.Join(m.cfg.BackupDir, dstName)

	if err := m.db.BackupTo(dstPath); err != nil {
		return "", fmt.Errorf("VACUUM INTO backup failed: %w", err)
	}

	slog.Info("Automated database backup created successfully", "path", dstPath)

	// Clean up snapshots older than retention days
	m.cleanupExpiredSnapshots()

	return dstPath, nil
}

func (m *Manager) cleanupExpiredSnapshots() {
	if m.cfg.BackupRetentionDays <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -m.cfg.BackupRetentionDays)
	entries, err := os.ReadDir(m.cfg.BackupDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "paperless-backup-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			targetPath := filepath.Join(m.cfg.BackupDir, entry.Name())
			if err := os.Remove(targetPath); err == nil {
				slog.Info("Removed expired backup snapshot", "file", entry.Name())
			}
		}
	}
}

// Start launches the scheduled background backup loop
func (m *Manager) Start(ctx context.Context) {
	if !m.cfg.BackupEnabled {
		slog.Info("Automated scheduled backups are disabled")
		return
	}

	intervalHours := m.cfg.BackupIntervalHours
	if intervalHours <= 0 {
		intervalHours = 24
	}

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)

	go func() {
		defer ticker.Stop()
		slog.Info("Automated backup scheduler active",
			"interval_hours", intervalHours,
			"retention_days", m.cfg.BackupRetentionDays,
			"dir", m.cfg.BackupDir,
		)

		for {
			select {
			case <-ticker.C:
				if _, err := m.CreateSnapshot(); err != nil {
					slog.Error("Scheduled automated backup failed", "error", err)
				}
			case <-ctx.Done():
				slog.Info("Automated backup scheduler stopped gracefully")
				return
			}
		}
	}()
}

