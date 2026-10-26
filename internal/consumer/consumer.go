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

	"context"
	"encoding/json"

	"paperless-go/internal/ai"
	"paperless-go/internal/classifier"
	"paperless-go/internal/compress"
	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
	"paperless-go/internal/workflow"
)

type Consumer struct {
	db         *database.DB
	cfg        *config.Config
	clf        *classifier.Classifier
	wfEngine   *workflow.Engine
	aiProvider ai.Provider
	ragSvc     *ai.RAGService
	extractor  *ai.ExtractorService
}

func NewConsumer(db *database.DB, cfg *config.Config) *Consumer {
	c := &Consumer{
		db:       db,
		cfg:      cfg,
		clf:      classifier.NewClassifier(db),
		wfEngine: workflow.NewEngine(db),
	}

	if cfg.AIEnabled {
		prov := ai.NewProvider(ai.ProviderConfig{
			Provider:       cfg.AIProvider,
			BaseURL:        cfg.AIAPIURL,
			APIKey:         cfg.AIAPIKey,
			Model:          cfg.AIModel,
			EmbeddingModel: cfg.AIEmbeddingModel,
		})
		c.aiProvider = prov
		c.ragSvc = ai.NewRAGService(prov)
		c.extractor = ai.NewExtractorService(prov)
	}

	return c
}

func (c *Consumer) SetAI(provider ai.Provider, rag *ai.RAGService, extractor *ai.ExtractorService) {
	c.aiProvider = provider
	c.ragSvc = rag
	c.extractor = extractor
}

func (c *Consumer) GetRAGService() *ai.RAGService {
	return c.ragSvc
}

func (c *Consumer) GetExtractorService() *ai.ExtractorService {
	return c.extractor
}

func (c *Consumer) GetAIProvider() ai.Provider {
	return c.aiProvider
}

func (c *Consumer) GetClassifier() *classifier.Classifier {
	return c.clf
}

func (c *Consumer) GetWorkflowEngine() *workflow.Engine {
	return c.wfEngine
}

type ConsumeOptions struct {
	Title               string
	Created             *time.Time
	Correspondent       *int
	DocumentType        *int
	StoragePath         *int
	ArchiveSerialNumber *int
	Tags                []int
	Owner               *int
}

type ErrDuplicateDocument struct {
	ExistingID    int
	ExistingTitle string
	Checksum      string
	FileName      string
}

func (e *ErrDuplicateDocument) Error() string {
	return fmt.Sprintf("Not consuming %s: It is a duplicate of %s (#%d)", e.FileName, e.ExistingTitle, e.ExistingID)
}

func (c *Consumer) ConsumeFile(srcPath, originalFileName string, opts *ConsumeOptions) (int, error) {
	// 1. Calculate SHA256 checksum
	checksum, err := CalculateChecksum(srcPath)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate checksum: %w", err)
	}

	// Check if already consumed (SHA-256 Deduplication)
	var existingID int
	var existingTitle string
	err = c.db.QueryRow("SELECT id, title FROM documents WHERE checksum = ?", checksum).Scan(&existingID, &existingTitle)
	if err == nil {
		return existingID, &ErrDuplicateDocument{
			ExistingID:    existingID,
			ExistingTitle: existingTitle,
			Checksum:      checksum,
			FileName:      originalFileName,
		}
	}

	// 2. Detect mime type (including Office formats)
	ext := strings.ToLower(filepath.Ext(originalFileName))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		switch ext {
		case ".pdf":
			mimeType = "application/pdf"
		case ".docx":
			mimeType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".xlsx":
			mimeType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		case ".pptx":
			mimeType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		case ".odt":
			mimeType = "application/vnd.oasis.opendocument.text"
		case ".ods":
			mimeType = "application/vnd.oasis.opendocument.spreadsheet"
		default:
			mimeType = "application/octet-stream"
		}
	}

	// 3. Extract text (supports PDF native, pdftotext, OCR multi-page, and Office OOXML)
	content, _ := ExtractText(srcPath, mimeType)

	// 4. Determine Title
	title := originalFileName
	if opts != nil && opts.Title != "" {
		title = opts.Title
	} else {
		// Clean filename as default title
		title = strings.TrimSuffix(originalFileName, ext)
	}

	// 5. Determine Created date (check user option, then scan document text, fallback to now)
	created := time.Now()
	if opts != nil && opts.Created != nil {
		created = *opts.Created
	} else if detectedDate := ExtractDocumentDate(content); detectedDate != nil {
		created = *detectedDate
	}

	// 6. Matching rules evaluation
	var correspondentID *int
	var documentTypeID *int
	var storagePathID *int
	var archiveSerialNumber *int
	var ownerID *int
	matchedTags := make(map[int]bool)

	if opts != nil {
		correspondentID = opts.Correspondent
		documentTypeID = opts.DocumentType
		storagePathID = opts.StoragePath
		archiveSerialNumber = opts.ArchiveSerialNumber
		ownerID = opts.Owner
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

	// Auto-match storage path if not set
	if storagePathID == nil {
		storagePathID = c.matchStoragePath(content, title)
	}

	// Machine learning vector classification fallback (Feature #6)
	if c.clf != nil {
		pred := c.clf.Classify(content + " " + title)
		if len(matchedTags) == 0 && len(pred.SuggestedTags) > 0 {
			for _, st := range pred.SuggestedTags {
				matchedTags[st] = true
			}
		}
		if correspondentID == nil && pred.SuggestedCorrespondent != nil {
			correspondentID = pred.SuggestedCorrespondent
		}
		if documentTypeID == nil && pred.SuggestedDocType != nil {
			documentTypeID = pred.SuggestedDocType
		}
		if storagePathID == nil && pred.SuggestedStoragePath != nil {
			storagePathID = pred.SuggestedStoragePath
		}
	}

	// Ensure Inbox tag is applied if no tags matched and an inbox tag exists
	var inboxTagID int
	if err := c.db.QueryRow("SELECT id FROM tags WHERE is_inbox_tag = 1 LIMIT 1").Scan(&inboxTagID); err == nil {
		matchedTags[inboxTagID] = true
	}

	// Detect accurate page count (Feature #3)
	pageCount := DetectPageCount(srcPath, mimeType)

	// 7. Insert Document record with Zstandard compression
	compressedContent := compress.CompressText(content)
	res, err := c.db.Exec(`
		INSERT INTO documents (
			title, content, correspondent_id, document_type_id, storage_path_id,
			created, modified, added, original_filename, checksum, mime_type, page_count, owner_id, archive_serial_number
		) VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?)
	`, title, compressedContent, correspondentID, documentTypeID, storagePathID, created, originalFileName, checksum, mimeType, pageCount, ownerID, archiveSerialNumber)
	if err != nil {
		return 0, fmt.Errorf("failed to insert document: %w", err)
	}

	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	// Index full uncompressed text in SQLite FTS5
	_, _ = c.db.Exec("INSERT OR REPLACE INTO documents_fts (rowid, title, content) VALUES (?, ?, ?)", docID, title, content)

	// 8. Associate tags
	for tagID := range matchedTags {
		_, _ = c.db.Exec("INSERT OR IGNORE INTO document_tags (document_id, tag_id) VALUES (?, ?)", docID, tagID)
	}

	// 8b. Execute Native Workflows (Feature #10)
	docObj := &models.Document{
		ID:               docID,
		Title:            title,
		OriginalFileName: originalFileName,
		Correspondent:    correspondentID,
		DocumentType:     documentTypeID,
		StoragePath:      storagePathID,
		Owner:            ownerID,
		PageCount:        pageCount,
		Created:          created,
		CreatedDate:      created.Format("2006-01-02"),
		MimeType:         mimeType,
	}
	for tagID := range matchedTags {
		docObj.Tags = append(docObj.Tags, tagID)
	}
	if c.wfEngine != nil {
		_ = c.wfEngine.ExecuteWorkflows(docObj, workflow.TriggerDocumentAdded, workflow.SourceConsumeFolder)
	}

	// Asynchronously update vector classifier model
	if c.clf != nil {
		go func() {
			_ = c.clf.Train()
		}()
	}

	// Asynchronously execute AI Embeddings & Metadata Extraction (Features 1.A, 1.B, 1.C)
	if c.aiProvider != nil && c.cfg.AIEnabled {
		go func(id int, docTitle, docContent string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			// 1. Generate & store embeddings for semantic vector search (1. C)
			embs, err := c.aiProvider.Embed(ctx, []string{docTitle + " " + docContent})
			if err == nil && len(embs) > 0 {
				if embJSON, err := ai.EncodeEmbedding(embs[0]); err == nil {
					_, _ = c.db.Exec(`
						INSERT OR REPLACE INTO document_embeddings (document_id, embedding_json, model, updated_at)
						VALUES (?, ?, ?, CURRENT_TIMESTAMP)
					`, id, embJSON, c.cfg.AIEmbeddingModel)
				}
			}

			// 2. Extract structured metadata (1. B)
			if c.extractor != nil && c.cfg.AIExtractOnConsume {
				meta, err := c.extractor.ExtractMetadata(ctx, docTitle, docContent)
				if err == nil && meta != nil {
					metaJSON, _ := json.Marshal(meta)
					_, _ = c.db.Exec("UPDATE documents SET extracted_metadata_json = ? WHERE id = ?", string(metaJSON), id)
				}
			}
		}(docID, title, content)
	}

	// 9. Store original file
	destFileName := fmt.Sprintf("%07d%s", docID, ext)
	destPath := filepath.Join(c.cfg.MediaDir, "documents", "originals", destFileName)
	_ = os.MkdirAll(filepath.Dir(destPath), 0755)

	if err := copyFile(srcPath, destPath); err != nil {
		log.Printf("Warning: failed to copy original file to %s: %v", destPath, err)
	}

	// 10. Attempt to generate searchable PDF/A archive version or convert Office to PDF (Features #1 & #3)
	archivePath := filepath.Join(c.cfg.MediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", docID))
	_ = os.MkdirAll(filepath.Dir(archivePath), 0755)
	archiveGenerated := false

	if strings.Contains(mimeType, "pdf") {
		archiveFileName := fmt.Sprintf("%07d.pdf", docID)
		if GenerateArchivePDF(destPath, archivePath) {
			_, _ = c.db.Exec("UPDATE documents SET archive_filename = ? WHERE id = ?", archiveFileName, docID)
			archiveGenerated = true
		}
	} else if IsOfficeDocument(destPath, mimeType) {
		archiveFileName := fmt.Sprintf("%07d.pdf", docID)
		if ConvertOfficeToPDF(destPath, archivePath) {
			_, _ = c.db.Exec("UPDATE documents SET archive_filename = ? WHERE id = ?", archiveFileName, docID)
			archiveGenerated = true
		}
	}

	// 11. Generate and store thumbnail
	thumbFileName := fmt.Sprintf("%07d.png", docID)
	thumbPath := filepath.Join(c.cfg.MediaDir, "documents", "thumbnails", thumbFileName)
	_ = os.MkdirAll(filepath.Dir(thumbPath), 0755)
	if archiveGenerated {
		_ = GenerateThumbnail(archivePath, thumbPath, "application/pdf")
	} else {
		_ = GenerateThumbnail(destPath, thumbPath, mimeType)
	}

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
	_ = rows.Err()
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
	_ = rows.Err()
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
	_ = rows.Err()
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
	_ = rows.Err()
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

func CalculateChecksum(path string) (string, error) {
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
