package exporter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

type ManifestItem struct {
	Model  string                 `json:"model"`
	PK     int                    `json:"pk"`
	Fields map[string]interface{} `json:"fields"`
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func calculateChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Export dumps database metadata and document files into a Paperless-compatible backup directory.
func Export(db *database.DB, cfg *config.Config, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create export directory: %w", err)
	}

	var manifest []ManifestItem

	// 1. Tags
	rowsT, err := db.Query("SELECT id, name, slug, color, text_color, match, matching_algorithm, is_insensitive, is_inbox_tag, owner_id FROM tags")
	if err == nil {
		defer rowsT.Close()
		for rowsT.Next() {
			var id, algo int
			var name, slug, color, textColor, match string
			var insensitive, inboxTag bool
			var owner *int
			if err := rowsT.Scan(&id, &name, &slug, &color, &textColor, &match, &algo, &insensitive, &inboxTag, &owner); err == nil {
				manifest = append(manifest, ManifestItem{
					Model: "documents.tag",
					PK:    id,
					Fields: map[string]interface{}{
						"name":               name,
						"slug":               slug,
						"color":              color,
						"text_color":         textColor,
						"match":              match,
						"matching_algorithm": algo,
						"is_insensitive":     insensitive,
						"is_inbox_tag":       inboxTag,
						"owner":              owner,
					},
				})
			}
		}
	}

	// 2. Correspondents
	rowsC, err := db.Query("SELECT id, name, slug, match, matching_algorithm, is_insensitive, owner_id FROM correspondents")
	if err == nil {
		defer rowsC.Close()
		for rowsC.Next() {
			var id, algo int
			var name, slug, match string
			var insensitive bool
			var owner *int
			if err := rowsC.Scan(&id, &name, &slug, &match, &algo, &insensitive, &owner); err == nil {
				manifest = append(manifest, ManifestItem{
					Model: "documents.correspondent",
					PK:    id,
					Fields: map[string]interface{}{
						"name":               name,
						"slug":               slug,
						"match":              match,
						"matching_algorithm": algo,
						"is_insensitive":     insensitive,
						"owner":              owner,
					},
				})
			}
		}
	}

	// 3. Document Types
	rowsDT, err := db.Query("SELECT id, name, slug, match, matching_algorithm, is_insensitive, owner_id FROM document_types")
	if err == nil {
		defer rowsDT.Close()
		for rowsDT.Next() {
			var id, algo int
			var name, slug, match string
			var insensitive bool
			var owner *int
			if err := rowsDT.Scan(&id, &name, &slug, &match, &algo, &insensitive, &owner); err == nil {
				manifest = append(manifest, ManifestItem{
					Model: "documents.documenttype",
					PK:    id,
					Fields: map[string]interface{}{
						"name":               name,
						"slug":               slug,
						"match":              match,
						"matching_algorithm": algo,
						"is_insensitive":     insensitive,
						"owner":              owner,
					},
				})
			}
		}
	}

	// 4. Storage Paths
	rowsSP, err := db.Query("SELECT id, name, path, match, matching_algorithm, is_insensitive, owner_id FROM storage_paths")
	if err == nil {
		defer rowsSP.Close()
		for rowsSP.Next() {
			var id, algo int
			var name, path, match string
			var insensitive bool
			var owner *int
			if err := rowsSP.Scan(&id, &name, &path, &match, &algo, &insensitive, &owner); err == nil {
				manifest = append(manifest, ManifestItem{
					Model: "documents.storagepath",
					PK:    id,
					Fields: map[string]interface{}{
						"name":               name,
						"path":               path,
						"match":              match,
						"matching_algorithm": algo,
						"is_insensitive":     insensitive,
						"owner":              owner,
					},
				})
			}
		}
	}

	// 5. Custom Fields
	rowsCF, err := db.Query("SELECT id, name, data_type, extra_data FROM custom_fields")
	if err == nil {
		defer rowsCF.Close()
		for rowsCF.Next() {
			var id int
			var name, dataType, extraData string
			if err := rowsCF.Scan(&id, &name, &dataType, &extraData); err == nil {
				var extra map[string]interface{}
				_ = json.Unmarshal([]byte(extraData), &extra)
				manifest = append(manifest, ManifestItem{
					Model: "documents.customfield",
					PK:    id,
					Fields: map[string]interface{}{
						"name":       name,
						"data_type":  dataType,
						"extra_data": extra,
					},
				})
			}
		}
	}

	// 6. Documents & files
	rowsD, err := db.Query(`
		SELECT id, title, content, correspondent_id, document_type_id, storage_path_id,
		       created, modified, added, archive_serial_number, original_filename, archive_filename,
		       checksum, mime_type, page_count, owner_id
		FROM documents WHERE deleted_at IS NULL
	`)
	if err == nil {
		defer rowsD.Close()
		for rowsD.Next() {
			var id, pageCount int
			var title, content, origName, checksum, mimeType string
			var corrID, docTypeID, storPathID, asn, owner *int
			var archName *string
			var created, modified, added time.Time

			err := rowsD.Scan(
				&id, &title, &content, &corrID, &docTypeID, &storPathID,
				&created, &modified, &added, &asn, &origName, &archName,
				&checksum, &mimeType, &pageCount, &owner,
			)
			if err == nil {
				// Copy original file
				ext := filepath.Ext(origName)
				srcOrig := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext))
				destOrig := filepath.Join(targetDir, "originals", fmt.Sprintf("%07d%s", id, ext))
				_ = copyFile(srcOrig, destOrig)

				// Copy archive file if exists
				srcArch := filepath.Join(cfg.MediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", id))
				destArch := filepath.Join(targetDir, "archive", fmt.Sprintf("%07d.pdf", id))
				if _, err := os.Stat(srcArch); err == nil {
					_ = copyFile(srcArch, destArch)
				}

				// Copy thumbnail
				srcThumb := filepath.Join(cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", id))
				destThumb := filepath.Join(targetDir, "thumbnails", fmt.Sprintf("%07d.png", id))
				if _, err := os.Stat(srcThumb); err == nil {
					_ = copyFile(srcThumb, destThumb)
				}

				// Document tags
				var tags []int
				rowsDTags, err := db.Query("SELECT tag_id FROM document_tags WHERE document_id = ?", id)
				if err == nil {
					for rowsDTags.Next() {
						var tid int
						if err := rowsDTags.Scan(&tid); err == nil {
							tags = append(tags, tid)
						}
					}
					rowsDTags.Close()
				}
				if tags == nil {
					tags = []int{}
				}

				manifest = append(manifest, ManifestItem{
					Model: "documents.document",
					PK:    id,
					Fields: map[string]interface{}{
						"title":                 title,
						"content":               content,
						"correspondent":         corrID,
						"document_type":         docTypeID,
						"storage_path":          storPathID,
						"created":               created.Format(time.RFC3339),
						"modified":              modified.Format(time.RFC3339),
						"added":                 added.Format(time.RFC3339),
						"archive_serial_number": asn,
						"original_filename":     origName,
						"archive_filename":      archName,
						"checksum":              checksum,
						"mime_type":             mimeType,
						"page_count":            pageCount,
						"owner":                 owner,
						"tags":                  tags,
					},
				})
			}
		}
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	manifestPath := filepath.Join(targetDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0644); err != nil {
		return fmt.Errorf("failed to write manifest.json: %w", err)
	}

	return nil
}

// ImportReport contains statistics and validation findings from an import or simulation run
type ImportReport struct {
	TotalItems          int
	TagsCount           int
	CorrespondentsCount int
	DocTypesCount       int
	StoragePathsCount   int
	CustomFieldsCount   int
	DocumentsCount      int
	NotesCount          int
	MissingFiles        []string
	ChecksumErrors      []string
	Warnings            []string
	DryRun              bool
}

// Import restores documents and metadata from a backup directory.
func Import(db *database.DB, cfg *config.Config, sourceDir string) error {
	_, err := ImportWithDryRun(db, cfg, sourceDir, false)
	return err
}

// ImportWithDryRun parses manifest.json, validates file existence, integrity, and relations.
// When dryRun is true, it performs a complete simulation without mutating database or media files.
func ImportWithDryRun(db *database.DB, cfg *config.Config, sourceDir string, dryRun bool) (*ImportReport, error) {
	manifestPath := filepath.Join(sourceDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("manifest.json not found in %s: %w", sourceDir, err)
	}

	var items []ManifestItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("failed to parse manifest.json: %w", err)
	}

	report := &ImportReport{
		TotalItems: len(items),
		DryRun:     dryRun,
	}

	// In dry-run mode, validate all items and files without mutating database
	if dryRun {
		for _, item := range items {
			switch item.Model {
			case "documents.tag":
				report.TagsCount++
			case "documents.correspondent":
				report.CorrespondentsCount++
			case "documents.documenttype":
				report.DocTypesCount++
			case "documents.storagepath":
				report.StoragePathsCount++
			case "documents.customfield":
				report.CustomFieldsCount++
			case "documents.note":
				report.NotesCount++
			case "documents.document":
				report.DocumentsCount++
				origName, _ := item.Fields["original_filename"].(string)
				expectedChecksum, _ := item.Fields["checksum"].(string)
				ext := filepath.Ext(origName)

				srcOrig := filepath.Join(sourceDir, "originals", fmt.Sprintf("%07d%s", item.PK, ext))
				if _, err := os.Stat(srcOrig); err != nil {
					altOrig := filepath.Join(sourceDir, fmt.Sprintf("%07d%s", item.PK, ext))
					if _, err := os.Stat(altOrig); err != nil {
						report.MissingFiles = append(report.MissingFiles, fmt.Sprintf("Doc #%d (%s): original file missing", item.PK, origName))
					} else {
						srcOrig = altOrig
					}
				}

				if _, err := os.Stat(srcOrig); err == nil && expectedChecksum != "" {
					actualSum, err := calculateChecksum(srcOrig)
					if err != nil {
						report.ChecksumErrors = append(report.ChecksumErrors, fmt.Sprintf("Doc #%d (%s): failed reading file for checksum: %v", item.PK, origName, err))
					} else if actualSum != expectedChecksum {
						report.ChecksumErrors = append(report.ChecksumErrors, fmt.Sprintf("Doc #%d (%s): checksum mismatch (expected %s, got %s)", item.PK, origName, expectedChecksum, actualSum))
					}
				}

				var existingID int
				if err := db.QueryRow("SELECT id FROM documents WHERE checksum = ? OR id = ?", expectedChecksum, item.PK).Scan(&existingID); err == nil {
					report.Warnings = append(report.Warnings, fmt.Sprintf("Doc #%d (%s): matches existing document #%d in database", item.PK, origName, existingID))
				}
			}
		}
		return report, nil
	}

	for _, item := range items {
		switch item.Model {
		case "documents.tag":
			name, _ := item.Fields["name"].(string)
			slug, _ := item.Fields["slug"].(string)
			color, _ := item.Fields["color"].(string)
			textColor, _ := item.Fields["text_color"].(string)
			match, _ := item.Fields["match"].(string)
			algo := 0
			if a, ok := item.Fields["matching_algorithm"].(float64); ok {
				algo = int(a)
			}
			ins := true
			if i, ok := item.Fields["is_insensitive"].(bool); ok {
				ins = i
			}
			inbox := false
			if in, ok := item.Fields["is_inbox_tag"].(bool); ok {
				inbox = in
			}
			if _, err := db.Exec(`
				INSERT INTO tags (id, name, slug, color, text_color, match, matching_algorithm, is_insensitive, is_inbox_tag)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, slug = excluded.slug
			`, item.PK, name, slug, color, textColor, match, algo, ins, inbox); err != nil {
				log.Printf("[WARN] Exporter failed importing tag %d (%s): %v", item.PK, name, err)
			}

		case "documents.correspondent":
			name, _ := item.Fields["name"].(string)
			slug, _ := item.Fields["slug"].(string)
			match, _ := item.Fields["match"].(string)
			algo := 0
			if a, ok := item.Fields["matching_algorithm"].(float64); ok {
				algo = int(a)
			}
			ins := true
			if i, ok := item.Fields["is_insensitive"].(bool); ok {
				ins = i
			}
			if _, err := db.Exec(`
				INSERT INTO correspondents (id, name, slug, match, matching_algorithm, is_insensitive)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, slug = excluded.slug
			`, item.PK, name, slug, match, algo, ins); err != nil {
				log.Printf("[WARN] Exporter failed importing correspondent %d (%s): %v", item.PK, name, err)
			}

		case "documents.documenttype":
			name, _ := item.Fields["name"].(string)
			slug, _ := item.Fields["slug"].(string)
			match, _ := item.Fields["match"].(string)
			algo := 0
			if a, ok := item.Fields["matching_algorithm"].(float64); ok {
				algo = int(a)
			}
			ins := true
			if i, ok := item.Fields["is_insensitive"].(bool); ok {
				ins = i
			}
			if _, err := db.Exec(`
				INSERT INTO document_types (id, name, slug, match, matching_algorithm, is_insensitive)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, slug = excluded.slug
			`, item.PK, name, slug, match, algo, ins); err != nil {
				log.Printf("[WARN] Exporter failed importing document_type %d (%s): %v", item.PK, name, err)
			}

		case "documents.storagepath":
			name, _ := item.Fields["name"].(string)
			path, _ := item.Fields["path"].(string)
			match, _ := item.Fields["match"].(string)
			algo := 0
			if a, ok := item.Fields["matching_algorithm"].(float64); ok {
				algo = int(a)
			}
			ins := true
			if i, ok := item.Fields["is_insensitive"].(bool); ok {
				ins = i
			}
			if _, err := db.Exec(`
				INSERT INTO storage_paths (id, name, path, match, matching_algorithm, is_insensitive)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, path = excluded.path
			`, item.PK, name, path, match, algo, ins); err != nil {
				log.Printf("[WARN] Exporter failed importing storage_path %d (%s): %v", item.PK, name, err)
			}

		case "documents.customfield":
			name, _ := item.Fields["name"].(string)
			dataType, _ := item.Fields["data_type"].(string)
			extraBytes, _ := json.Marshal(item.Fields["extra_data"])
			if _, err := db.Exec(`
				INSERT INTO custom_fields (id, name, data_type, extra_data)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET name = excluded.name, data_type = excluded.data_type, extra_data = excluded.extra_data
			`, item.PK, name, dataType, string(extraBytes)); err != nil {
				log.Printf("[WARN] Exporter failed importing custom_field %d (%s): %v", item.PK, name, err)
			}

		case "documents.customfieldinstance":
			var fieldID, docID int
			if f, ok := item.Fields["field"].(float64); ok {
				fieldID = int(f)
			}
			if d, ok := item.Fields["document"].(float64); ok {
				docID = int(d)
			}
			var valStr, valDate *string
			var valNum *float64
			var valBool *bool
			if v, ok := item.Fields["value_text"].(string); ok {
				valStr = &v
			} else if v, ok := item.Fields["value_url"].(string); ok {
				valStr = &v
			}
			if v, ok := item.Fields["value_date"].(string); ok {
				valDate = &v
			}
			if v, ok := item.Fields["value_int"].(float64); ok {
				valNum = &v
			} else if v, ok := item.Fields["value_float"].(float64); ok {
				valNum = &v
			} else if v, ok := item.Fields["value_monetary"].(float64); ok {
				valNum = &v
			}
			if v, ok := item.Fields["value_bool"].(bool); ok {
				valBool = &v
			}
			if fieldID > 0 && docID > 0 {
				_, _ = db.Exec(`
					INSERT INTO document_custom_fields (document_id, custom_field_id, value_text, value_bool, value_date, value_number)
					VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT(document_id, custom_field_id) DO UPDATE SET value_text = excluded.value_text, value_bool = excluded.value_bool, value_date = excluded.value_date, value_number = excluded.value_number
				`, docID, fieldID, valStr, valBool, valDate, valNum)
			}

		case "documents.note":
			var docID int
			if d, ok := item.Fields["document"].(float64); ok {
				docID = int(d)
			}
			noteText, _ := item.Fields["note"].(string)
			var uID *int
			if u, ok := item.Fields["user"].(float64); ok && int(u) > 0 {
				ui := int(u)
				uID = &ui
			}
			createdTime := time.Now()
			if crStr, ok := item.Fields["created"].(string); ok && crStr != "" {
				if t, err := time.Parse(time.RFC3339, crStr); err == nil {
					createdTime = t
				} else if t, err := time.Parse("2006-01-02T15:04:05Z", crStr); err == nil {
					createdTime = t
				}
			}
			if docID > 0 && noteText != "" {
				_, _ = db.Exec(`
					INSERT INTO document_notes (document_id, note, created, user_id)
					VALUES (?, ?, ?, ?)
				`, docID, noteText, createdTime, uID)
			}

		case "documents.document":
			title, _ := item.Fields["title"].(string)
			content, _ := item.Fields["content"].(string)
			origName, _ := item.Fields["original_filename"].(string)
			checksum, _ := item.Fields["checksum"].(string)
			mimeType, _ := item.Fields["mime_type"].(string)
			pageCount := 1
			if pc, ok := item.Fields["page_count"].(float64); ok {
				pageCount = int(pc)
			}

			var corrID, docTypeID, storPathID, asn, ownerID *int
			if c, ok := item.Fields["correspondent"].(float64); ok && int(c) > 0 {
				ci := int(c)
				corrID = &ci
			}
			if dt, ok := item.Fields["document_type"].(float64); ok && int(dt) > 0 {
				dti := int(dt)
				docTypeID = &dti
			}
			if sp, ok := item.Fields["storage_path"].(float64); ok && int(sp) > 0 {
				spi := int(sp)
				storPathID = &spi
			}
			if a, ok := item.Fields["archive_serial_number"].(float64); ok && int(a) > 0 {
				ai := int(a)
				asn = &ai
			}
			if o, ok := item.Fields["owner"].(float64); ok && int(o) > 0 {
				oi := int(o)
				ownerID = &oi
			}

			createdTime := time.Now()
			if crStr, ok := item.Fields["created"].(string); ok && crStr != "" {
				if t, err := time.Parse(time.RFC3339, crStr); err == nil {
					createdTime = t
				} else if t, err := time.Parse("2006-01-02T15:04:05Z", crStr); err == nil {
					createdTime = t
				} else if t, err := time.Parse("2006-01-02", crStr); err == nil {
					createdTime = t
				}
			}

			// Restore files with error logging
			ext := filepath.Ext(origName)
			srcOrig := filepath.Join(sourceDir, "originals", fmt.Sprintf("%07d%s", item.PK, ext))
			destOrig := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", item.PK, ext))
			if err := copyFile(srcOrig, destOrig); err != nil {
				log.Printf("[WARN] Failed restoring original file for doc %d: %v", item.PK, err)
			}

			srcArch := filepath.Join(sourceDir, "archive", fmt.Sprintf("%07d.pdf", item.PK))
			destArch := filepath.Join(cfg.MediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", item.PK))
			_ = copyFile(srcArch, destArch)

			srcThumb := filepath.Join(sourceDir, "thumbnails", fmt.Sprintf("%07d.png", item.PK))
			destThumb := filepath.Join(cfg.MediaDir, "documents", "thumbnails", fmt.Sprintf("%07d.png", item.PK))
			_ = copyFile(srcThumb, destThumb)

			// Atomic document persistence with tags and FTS index
			docTxErr := db.WithTx(context.Background(), func(tx *sql.Tx) error {
				if _, err := tx.Exec(`
					INSERT INTO documents (
						id, title, content, original_filename, checksum, mime_type, page_count,
						correspondent_id, document_type_id, storage_path_id, archive_serial_number, owner_id,
						created, modified, added
					)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
					ON CONFLICT(id) DO UPDATE SET
						title = excluded.title,
						content = excluded.content,
						correspondent_id = excluded.correspondent_id,
						document_type_id = excluded.document_type_id,
						storage_path_id = excluded.storage_path_id,
						archive_serial_number = excluded.archive_serial_number,
						owner_id = excluded.owner_id
				`, item.PK, title, content, origName, checksum, mimeType, pageCount, corrID, docTypeID, storPathID, asn, ownerID, createdTime); err != nil {
					return fmt.Errorf("failed inserting document %d: %w", item.PK, err)
				}

				if _, err := tx.Exec("INSERT OR REPLACE INTO documents_fts (rowid, title, content) VALUES (?, ?, ?)", item.PK, title, content); err != nil {
					return fmt.Errorf("failed inserting fts index for doc %d: %w", item.PK, err)
				}

				if tagList, ok := item.Fields["tags"].([]interface{}); ok {
					for _, tidVal := range tagList {
						if tid, ok := tidVal.(float64); ok {
							if _, err := tx.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", item.PK, int(tid)); err != nil {
								return fmt.Errorf("failed inserting tag %d for doc %d: %w", int(tid), item.PK, err)
							}
						}
					}
				}
				return nil
			})
			if docTxErr != nil {
				log.Printf("[ERROR] Exporter transaction failed for document %d: %v", item.PK, docTxErr)
			}
		}
	}

	return report, nil
}

// SanityCheck inspects database consistency, file integrity, checksums, and returns warning messages.
func SanityCheck(db *database.DB, cfg *config.Config) ([]string, error) {
	var issues []string

	// 1. Check for documents without original file
	rows, err := db.Query("SELECT id, original_filename, checksum FROM documents WHERE deleted_at IS NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var origName, expectedChecksum string
		if err := rows.Scan(&id, &origName, &expectedChecksum); err == nil {
			ext := filepath.Ext(origName)
			origPath := filepath.Join(cfg.MediaDir, "documents", "originals", fmt.Sprintf("%07d%s", id, ext))
			info, err := os.Stat(origPath)
			if err != nil || info.Size() == 0 {
				issues = append(issues, fmt.Sprintf("Document #%d (%s): original file missing from %s", id, origName, origPath))
			} else if expectedChecksum != "" {
				actualSum, _ := calculateChecksum(origPath)
				if actualSum != "" && actualSum != expectedChecksum {
					issues = append(issues, fmt.Sprintf("Document #%d (%s): checksum mismatch (expected %s, got %s)", id, origName, expectedChecksum, actualSum))
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sanity check document cursor error: %w", err)
	}

	// 2. Check for orphaned tags in document_tags
	var orphanTags int
	if err := db.QueryRow("SELECT COUNT(*) FROM document_tags WHERE tag_id NOT IN (SELECT id FROM tags)").Scan(&orphanTags); err != nil {
		log.Printf("[WARN] Failed checking orphaned tags in sanity check: %v", err)
	}
	if orphanTags > 0 {
		issues = append(issues, fmt.Sprintf("Found %d orphaned document_tags references with non-existent tag_id", orphanTags))
	}

	return issues, nil
}

