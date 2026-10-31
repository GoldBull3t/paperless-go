package consumer

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

// RenameAllDocuments iterates through all active documents in the database
// and evaluates their target storage path formatting, moving the physical file if needed.
func RenameAllDocuments(db *database.DB, cfg *config.Config) (int, error) {
	rows, err := db.Query(`
		SELECT d.id, d.title, d.created, d.archive_serial_number, d.original_filename,
		       c.name, dt.name, sp.path
		FROM documents d
		LEFT JOIN correspondents c ON d.correspondent_id = c.id
		LEFT JOIN document_types dt ON d.document_type_id = dt.id
		LEFT JOIN storage_paths sp ON d.storage_path_id = sp.id
		WHERE d.deleted_at IS NULL
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	renamedCount := 0
	for rows.Next() {
		var id int
		var title, origName string
		var created time.Time
		var asnVal sql.NullInt64
		var corrName, dtName, spPath sql.NullString

		if err := rows.Scan(&id, &title, &created, &asnVal, &origName, &corrName, &dtName, &spPath); err != nil {
			continue
		}

		if spPath.Valid && spPath.String != "" {
			var asnPtr *int
			if asnVal.Valid {
				v := int(asnVal.Int64)
				asnPtr = &v
			}
			targetDir := FormatStoragePath(spPath.String, title, corrName.String, dtName.String, created, created, asnPtr)
			if targetDir != "" {
				ext := filepath.Ext(origName)
				if ext == "" {
					ext = ".pdf"
				}
				destDir := filepath.Join(cfg.MediaDir, "documents", "originals", targetDir)
				_ = os.MkdirAll(destDir, 0755)
				destPath := filepath.Join(destDir, fmt.Sprintf("%07d%s", id, ext))

				// Current flat path
				srcPath := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext))
				if _, err := os.Stat(srcPath); err == nil && srcPath != destPath {
					if err := os.Rename(srcPath, destPath); err == nil {
						renamedCount++
					}
				}
			}
		}
	}
	log.Printf("[Renamer] Completed document renaming. Reorganized %d files.", renamedCount)
	return renamedCount, nil
}
