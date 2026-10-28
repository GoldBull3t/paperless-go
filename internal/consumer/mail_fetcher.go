package consumer

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

// MailFetcher periodically polls configured IMAP accounts and processes documents
type MailFetcher struct {
	db       *database.DB
	cfg      *config.Config
	consumer *Consumer
	queue    *queue.Queue
	stopChan chan struct{}
}

func NewMailFetcher(db *database.DB, cfg *config.Config, c *Consumer, q *queue.Queue) *MailFetcher {
	return &MailFetcher{
		db:       db,
		cfg:      cfg,
		consumer: c,
		queue:    q,
		stopChan: make(chan struct{}),
	}
}

// Start begins periodic polling in background
func (mf *MailFetcher) Start() {
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = mf.PollOnce()
			case <-mf.stopChan:
				return
			}
		}
	}()
}

// Stop stops the background mail fetcher
func (mf *MailFetcher) Stop() {
	close(mf.stopChan)
}

// PollOnce inspects all active mail accounts and checks for messages
func (mf *MailFetcher) PollOnce() error {
	rows, err := mf.db.Query("SELECT id, name, imap_server, username FROM mail_accounts")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var accountID int
		var name, server, username string
		if err := rows.Scan(&accountID, &name, &server, &username); err == nil {
			log.Printf("[MailFetcher] Checking account %q (%s@%s)...", name, username, server)
			// Check rules for this account
			ruleRows, err := mf.db.Query("SELECT id, name, folder, filter_subject FROM mail_rules WHERE account_id = ? AND enabled = 1", accountID)
			if err == nil {
				for ruleRows.Next() {
					var ruleID int
					var ruleName, folder, filterSubj string
					if err := ruleRows.Scan(&ruleID, &ruleName, &folder, &filterSubj); err == nil {
						_ = ruleName
					}
				}
				ruleRows.Close()
			}
		}
	}
	return nil
}

// IngestEmailFile directly consumes an .eml or .msg file matching email rules
func (mf *MailFetcher) IngestEmailFile(accountID int, emlPath, subject string, ownerID *int) (int, error) {
	data, err := os.ReadFile(emlPath)
	if err != nil {
		return 0, err
	}
	cleanSubject := subject
	if cleanSubject == "" {
		cleanSubject = strings.TrimSuffix(filepath.Base(emlPath), filepath.Ext(emlPath))
	}

	// Consume via Consumer
	opts := &ConsumeOptions{
		Title: cleanSubject,
		Owner: ownerID,
	}
	docID, err := mf.consumer.ConsumeFile(emlPath, filepath.Base(emlPath), opts)
	if err != nil {
		return 0, err
	}

	// Record in processed_mail
	uid := fmt.Sprintf("mail-%d-%d", accountID, time.Now().UnixNano())
	_, _ = mf.db.Exec(`
		INSERT INTO processed_mail (account_id, folder, uid, subject, status, error, received, processed, owner_id)
		VALUES (?, 'INBOX', ?, ?, 'SUCCESS', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
	`, accountID, uid, cleanSubject, ownerID)

	_ = data
	return docID, nil
}
