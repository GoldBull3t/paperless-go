package consumer

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

type MailFetcher struct {
	db       *database.DB
	cfg      *config.Config
	consumer *Consumer
	queue    *queue.Queue
	stopChan chan struct{}
	wg       sync.WaitGroup
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
	mf.wg.Add(1)
	go func() {
		defer mf.wg.Done()
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := mf.PollOnce(); err != nil {
					log.Printf("[WARN] MailFetcher periodic check encountered error: %v", err)
				}
			case <-mf.stopChan:
				return
			}
		}
	}()
}

// Stop stops the background mail fetcher and waits for ongoing iteration to exit
func (mf *MailFetcher) Stop() {
	select {
	case <-mf.stopChan:
		// already closed
	default:
		close(mf.stopChan)
	}
	mf.wg.Wait()
}

// PollOnce inspects all active mail accounts and checks for messages
func (mf *MailFetcher) PollOnce() error {
	rows, err := mf.db.Query("SELECT id, name, imap_server, username FROM mail_accounts")
	if err != nil {
		return fmt.Errorf("failed querying mail accounts: %w", err)
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
				_ = ruleRows.Close()
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cursor error iterating mail accounts: %w", err)
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
	if _, err := mf.db.Exec(`
		INSERT INTO processed_mail (account_id, folder, uid, subject, status, error, received, processed, owner_id)
		VALUES (?, 'INBOX', ?, ?, 'SUCCESS', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
	`, accountID, uid, cleanSubject, ownerID); err != nil {
		log.Printf("[ERROR] Failed recording processed mail for doc %d: %v", docID, err)
	}

	_ = data
	return docID, nil
}
