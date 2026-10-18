package consumer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

type Consumer struct {
	db  *database.DB
	cfg *config.Config
}

func NewConsumer(db *database.DB, cfg *config.Config) *Consumer {
	return &Consumer{db: db, cfg: cfg}
}

type ConsumeOptions struct {
	Title         string
	Created       *time.Time
	Correspondent *int
	DocumentType  *int
	Tags          []int
}

func (c *Consumer) ConsumeFile(srcPath, originalFileName string, opts *ConsumeOptions) (int, error) {
	// 1. Calculate SHA256 checksum
	checksum, err := calculateChecksum(srcPath)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Check if already consumed
	var existingID int
	err = c.db.QueryRow("SELECT id FROM documents WHERE checksum = ?", checksum).Scan(&existingID)
	if err == nil {
		return existingID, fmt.Errorf("document with checksum %s already exists (ID: %d)", checksum, existingID)
	}

	// 2. Detect mime type
	ext := strings.ToLower(filepath.Ext(originalFileName))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		if ext == ".pdf" {
			mimeType = "application/pdf"
		} else {
			mimeType = "application/octet-stream"
		}
	}

	// 3. Extract text
	content, _ := ExtractText(srcPath, mimeType)

	// 4. Determine Title
	title := originalFileName
	if opts != nil && opts.Title != "" {
		title = opts.Title
	} else {
		// Clean filename as default title
		title = strings.TrimSuffix(originalFileName, ext)
	}

	// 5. Determine Created date
	created := time.Now()
	if opts != nil && opts.Created != nil {
		created = *opts.Created
	}

	// 6. Matching rules evaluation
	var correspondentID *int
	var documentTypeID *int
	var storagePathID *int
	matchedTags := make(map[int]bool)

	if opts != nil {
		correspondentID = opts.Correspondent
		documentTypeID = opts.DocumentType
		for _, t := range opts.Tags {
			matchedTags[t] = true
		}
	}

	// Auto-match tags
	c.matchTags(content, title, matchedTags)

	// Auto-match correspondent if not set
	if correspondentID == nil {
		correspondentID = c.matchCorrespondent(content, title)
	}

	// Auto-match document type if not set
	if documentTypeID == nil {
		documentTypeID = c.matchDocumentType(content, title)
	}

	// Auto-match storage path
	storagePathID = c.matchStoragePath(content, title)

	// Ensure Inbox tag is applied if no tags matched and an inbox tag exists
	var inboxTagID int
	if err := c.db.QueryRow("SELECT id FROM tags WHERE is_inbox_tag = 1 LIMIT 1").Scan(&inboxTagID); err == nil {
		matchedTags[inboxTagID] = true
	}

	// 7. Insert Document record
	res, err := c.db.Exec(`
		INSERT INTO documents (
			title, content, correspondent_id, document_type_id, storage_path_id,
			created, modified, added, original_filename, checksum, mime_type, page_count
		) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?, ?, 1)
	`, title, content, correspondentID, documentTypeID, storagePathID, created, originalFileName, checksum, mimeType)
	if err != nil {
		return 0, fmt.Errorf("failed to insert document: %w", err)
	}

	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	// 8. Associate tags
	for tagID := range matchedTags {
		_, _ = c.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", docID, tagID)
	}

	// 9. Store original file
	destFileName := fmt.Sprintf("%07d%s", docID, ext)
	destPath := filepath.Join(c.cfg.MediaDir, "documents", "originals", destFileName)

	if err := copyFile(srcPath, destPath); err != nil {
		log.Printf("Warning: failed to copy original file to %s: %v", destPath, err)
	}

	// 10. Generate and store thumbnail
	thumbFileName := fmt.Sprintf("%07d.png", docID)
	thumbPath := filepath.Join(c.cfg.MediaDir, "documents", "thumbnails", thumbFileName)
	_ = GenerateThumbnail(destPath, thumbPath, mimeType)

	return docID, nil
}

func (c *Consumer) matchTags(content, title string, matchedTags map[int]bool) {
	rows, err := c.db.Query("SELECT id, name, match, matching_algorithm, is_insensitive FROM tags WHERE match != ''")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, algo int
		var name, match string
		var insensitive bool
		if err := rows.Scan(&id, &name, &match, &algo, &insensitive); err == nil {
			if matchesRule(content, title, match, algo, insensitive) {
				matchedTags[id] = true
			}
		}
	}
}

func (c *Consumer) matchCorrespondent(content, title string) *int {
	rows, err := c.db.Query("SELECT id, match, matching_algorithm, is_insensitive FROM correspondents WHERE match != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var id, algo int
		var match string
		var insensitive bool
		if err := rows.Scan(&id, &match, &algo, &insensitive); err == nil {
			if matchesRule(content, title, match, algo, insensitive) {
				return &id
			}
		}
	}
	return nil
}

func (c *Consumer) matchDocumentType(content, title string) *int {
	rows, err := c.db.Query("SELECT id, match, matching_algorithm, is_insensitive FROM document_types WHERE match != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var id, algo int
		var match string
		var insensitive bool
		if err := rows.Scan(&id, &match, &algo, &insensitive); err == nil {
			if matchesRule(content, title, match, algo, insensitive) {
				return &id
			}
		}
	}
	return nil
}

func (c *Consumer) matchStoragePath(content, title string) *int {
	rows, err := c.db.Query("SELECT id, match, matching_algorithm, is_insensitive FROM storage_paths WHERE match != ''")
	if err != nil {
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var id, algo int
		var match string
		var insensitive bool
		if err := rows.Scan(&id, &match, &algo, &insensitive); err == nil {
			if matchesRule(content, title, match, algo, insensitive) {
				return &id
			}
		}
	}
	return nil
}

func matchesRule(content, title, match string, algo int, insensitive bool) bool {
	text := content + " " + title
	if insensitive {
		text = strings.ToLower(text)
		match = strings.ToLower(match)
	}

	switch algo {
	case 1: // Any word
		words := strings.Fields(match)
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false
	case 2: // All words
		words := strings.Fields(match)
		for _, w := range words {
			if !strings.Contains(text, w) {
				return false
			}
		}
		return len(words) > 0
	case 3: // Exact match
		return strings.Contains(text, match)
	case 4: // Regular expression
		re, err := regexp.Compile(match)
		if err == nil {
			return re.MatchString(text)
		}
	}
	return false
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

func copyFile(src, dst string) error {
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
