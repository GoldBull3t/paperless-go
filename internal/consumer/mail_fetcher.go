package consumer

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
	"paperless-go/internal/config"
	"paperless-go/internal/crypto"
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

type mailAccountRecord struct {
	id                int
	name              string
	server            string
	port              int
	security          int // 1=None, 2=StartTLS, 3=SSL/TLS
	username          string
	password          string
	characterSet      string
	authType          string
	oauthClientID     string
	oauthClientSecret string
	oauthRefreshToken string
	oauthTokenURL     string
	ownerID           *int
}

type mailRuleRecord struct {
	id            int
	name          string
	folder        string
	filterFrom    string
	filterSubject string
	filterBody    string
	action        int // 1=MarkRead, 2=Flag, 3=Move, 4=Delete
	actionParam   string
	ownerID       *int
}

// PollOnce inspects all active mail accounts and checks for messages
func (mf *MailFetcher) PollOnce() error {
	rows, err := mf.db.Query(`
		SELECT id, name, imap_server, imap_port, imap_security, username, password, character_set,
		       auth_type, oauth_client_id, oauth_client_secret, oauth_refresh_token, oauth_token_url, owner_id
		FROM mail_accounts
	`)
	if err != nil {
		return fmt.Errorf("failed querying mail accounts: %w", err)
	}
	defer rows.Close()

	var accounts []mailAccountRecord
	for rows.Next() {
		var a mailAccountRecord
		var owner sql.NullInt64
		if err := rows.Scan(
			&a.id, &a.name, &a.server, &a.port, &a.security, &a.username, &a.password, &a.characterSet,
			&a.authType, &a.oauthClientID, &a.oauthClientSecret, &a.oauthRefreshToken, &a.oauthTokenURL, &owner,
		); err == nil {
			if owner.Valid {
				v := int(owner.Int64)
				a.ownerID = &v
			}
			accounts = append(accounts, a)
		}
	}

	for _, acc := range accounts {
		if err := mf.PollAccount(acc); err != nil {
			log.Printf("[MailFetcher] Error polling account %q (%s): %v", acc.name, acc.server, err)
		}
	}

	return nil
}

// PollAccount connects securely to a configured IMAP account and evaluates rules
func (mf *MailFetcher) PollAccount(acc mailAccountRecord) error {
	secret := ""
	if mf.cfg != nil {
		secret = mf.cfg.SecretKey
	}

	// 1. Decrypt password in memory
	plainPass, err := crypto.DecryptString(acc.password, secret)
	if err != nil {
		return fmt.Errorf("failed decrypting mail account credentials: %w", err)
	}

	// 2. Security validation: disallow plaintext connection across public network
	isLocalhost := acc.server == "localhost" || acc.server == "127.0.0.1" || acc.server == "::1"
	if acc.security == 1 && !isLocalhost {
		return fmt.Errorf("security violation: plaintext IMAP (no encryption) is strictly prohibited on non-localhost server %q", acc.server)
	}

	serverAddr := fmt.Sprintf("%s:%d", acc.server, acc.port)
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: acc.server,
	}

	options := &imapclient.Options{
		TLSConfig: tlsConfig,
	}

	var client *imapclient.Client
	switch acc.security {
	case 3: // Direct SSL/TLS (typically port 993)
		client, err = imapclient.DialTLS(serverAddr, options)
		if err != nil {
			return fmt.Errorf("failed connecting to IMAP TLS (%s): %w", serverAddr, err)
		}
		defer client.Close()
		return mf.processClientMail(client, acc, plainPass)
	case 2: // StartTLS (typically port 143 upgraded to TLS)
		client, err = imapclient.DialStartTLS(serverAddr, options)
		if err != nil {
			return fmt.Errorf("failed connecting to IMAP StartTLS (%s): %w", serverAddr, err)
		}
		defer client.Close()
		return mf.processClientMail(client, acc, plainPass)
	case 1: // Plaintext (only allowed for local mock tests)
		client, err = imapclient.DialInsecure(serverAddr, options)
		if err != nil {
			return fmt.Errorf("failed connecting to IMAP (%s): %w", serverAddr, err)
		}
		defer client.Close()
		return mf.processClientMail(client, acc, plainPass)
	default:
		return fmt.Errorf("unknown imap_security mode %d", acc.security)
	}
}

func (mf *MailFetcher) processClientMail(client *imapclient.Client, acc mailAccountRecord, plainPass string) error {
	// 1. Authenticate (Password or OAuth2/XOAUTH2)
	if strings.EqualFold(acc.authType, "oauth2") {
		plainSecret, _ := crypto.DecryptString(acc.oauthClientSecret, mf.cfg.SecretKey)
		plainRefresh, _ := crypto.DecryptString(acc.oauthRefreshToken, mf.cfg.SecretKey)
		accessToken, err := RefreshOAuth2Token(acc.oauthTokenURL, acc.oauthClientID, plainSecret, plainRefresh)
		if err != nil {
			return fmt.Errorf("OAuth2 token refresh failed for %s@%s: %w", acc.username, acc.server, err)
		}
		saslClient := NewXOAuth2Client(acc.username, accessToken)
		if err := client.Authenticate(saslClient); err != nil {
			return fmt.Errorf("IMAP XOAUTH2 authentication failed for %s@%s: %w", acc.username, acc.server, err)
		}
	} else {
		if err := client.Login(acc.username, plainPass).Wait(); err != nil {
			return fmt.Errorf("IMAP authentication failed for %s@%s: %w", acc.username, acc.server, err)
		}
	}
	defer client.Logout()

	// 2. Fetch rules for this account
	ruleRows, err := mf.db.Query(`
		SELECT id, name, folder, filter_from, filter_subject, filter_body, action, action_parameter, owner_id
		FROM mail_rules WHERE account_id = ? AND enabled = 1
	`, acc.id)
	if err != nil {
		return err
	}
	defer ruleRows.Close()

	var rules []mailRuleRecord
	for ruleRows.Next() {
		var r mailRuleRecord
		var owner sql.NullInt64
		if err := ruleRows.Scan(&r.id, &r.name, &r.folder, &r.filterFrom, &r.filterSubject, &r.filterBody, &r.action, &r.actionParam, &owner); err == nil {
			if owner.Valid {
				v := int(owner.Int64)
				r.ownerID = &v
			}
			rules = append(rules, r)
		}
	}

	// 3. Process rules per folder
	for _, rule := range rules {
		folder := rule.folder
		if folder == "" {
			folder = "INBOX"
		}

		selectedMbox, err := client.Select(folder, nil).Wait()
		if err != nil {
			log.Printf("[MailFetcher] Could not select folder %q: %v", folder, err)
			continue
		}

		if selectedMbox.NumMessages == 0 {
			continue
		}

		searchCriteria := &imap.SearchCriteria{
			NotFlag: []imap.Flag{imap.FlagSeen},
		}
		searchData, err := client.Search(searchCriteria, nil).Wait()
		if err != nil {
			continue
		}

		seqSet := searchData.AllSeqNums()
		if len(seqSet) == 0 {
			continue
		}

		fetchOptions := &imap.FetchOptions{
			UID:          true,
			Envelope:     true,
			BodySection: []*imap.FetchItemBodySection{{}},
		}

		fetchCmd := client.Fetch(imap.SeqSetNum(seqSet...), fetchOptions)
		for {
			msg := fetchCmd.Next()
			if msg == nil {
				break
			}

			var bodyBytes []byte
			var msgUID string

			for {
				item := msg.Next()
				if item == nil {
					break
				}
				switch item := item.(type) {
				case imapclient.FetchItemDataUID:
					msgUID = fmt.Sprintf("%d", item.UID)
				case imapclient.FetchItemDataBodySection:
					b, err := io.ReadAll(item.Literal)
					if err == nil {
						bodyBytes = b
					}
				}
			}

			if len(bodyBytes) == 0 {
				continue
			}

			// Check if already processed
			var alreadyProcessed int
			_ = mf.db.QueryRow("SELECT COUNT(*) FROM processed_mail WHERE account_id = ? AND folder = ? AND uid = ?", acc.id, folder, msgUID).Scan(&alreadyProcessed)
			if alreadyProcessed > 0 {
				continue
			}

			docIDs, err := mf.ProcessEmailMessage(acc.id, &rule.id, folder, msgUID, bodyBytes, rule.filterFrom, rule.filterSubject, rule.ownerID)
			if err != nil {
				log.Printf("[MailFetcher] Error processing message %s: %v", msgUID, err)
			} else if len(docIDs) > 0 {
				log.Printf("[MailFetcher] Ingested %d documents from email UID %s", len(docIDs), msgUID)
			}
		}
	}

	return nil
}

// ProcessEmailMessage parses raw RFC822 email bytes, applies whitelist filters, validates attachments, and ingests documents
func (mf *MailFetcher) ProcessEmailMessage(accountID int, ruleID *int, folder, uid string, rawEML []byte, filterFrom, filterSubj string, ownerID *int) ([]int, error) {
	parsedMsg, err := mail.ReadMessage(bytes.NewReader(rawEML))
	if err != nil {
		return nil, fmt.Errorf("failed parsing raw email: %w", err)
	}

	fromHdr := parsedMsg.Header.Get("From")
	subjectHdr := parsedMsg.Header.Get("Subject")

	// 1. Whitelist Sender Verification
	if filterFrom != "" && !MatchesSenderWhitelist(fromHdr, filterFrom) {
		log.Printf("[SECURITY] Email from %q rejected: not matching authorized sender whitelist %q", fromHdr, filterFrom)
		return nil, nil
	}

	// 2. Subject Filter Verification
	if filterSubj != "" && !strings.Contains(strings.ToLower(subjectHdr), strings.ToLower(filterSubj)) {
		return nil, nil
	}

	// 3. Parse Attachments using go-message/mail
	mr, err := gomail.CreateReader(bytes.NewReader(rawEML))
	if err != nil {
		return nil, fmt.Errorf("failed creating mail body reader: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "mail-ingest-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	var ingestedDocIDs []int

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch h := part.Header.(type) {
		case *gomail.AttachmentHeader:
			filename, _ := h.Filename()
			if filename == "" {
				filename = "email_attachment.pdf"
			}

			payload, err := io.ReadAll(io.LimitReader(part.Body, MaxAttachmentSize+1024))
			if err != nil {
				continue
			}

			// Validate through strict security sandbox
			safeFilename, err := ValidateAttachment(filename, payload)
			if err != nil {
				log.Printf("[SECURITY] Mail attachment %q dropped: %v", filename, err)
				continue
			}

			tmpPath := filepath.Join(tmpDir, safeFilename)
			if err := os.WriteFile(tmpPath, payload, 0600); err != nil {
				continue
			}

			docTitle := subjectHdr
			if docTitle == "" {
				docTitle = strings.TrimSuffix(safeFilename, filepath.Ext(safeFilename))
			}

			opts := &ConsumeOptions{
				Title: docTitle,
				Owner: ownerID,
			}

			docID, err := mf.consumer.ConsumeFileWithContext(context.Background(), tmpPath, safeFilename, opts)
			if err == nil && docID > 0 {
				ingestedDocIDs = append(ingestedDocIDs, docID)
			}
		}
	}

	// 4. Record into processed_mail audit log
	status := "SUCCESS"
	if len(ingestedDocIDs) == 0 {
		status = "IGNORED_NO_ATTACHMENTS"
	}

	if _, err := mf.db.Exec(`
		INSERT INTO processed_mail (account_id, rule_id, folder, uid, subject, status, error, received, processed, owner_id)
		VALUES (?, ?, ?, ?, ?, ?, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?)
	`, accountID, ruleID, folder, uid, subjectHdr, status, ownerID); err != nil {
		log.Printf("[ERROR] Failed recording processed_mail: %v", err)
	}

	return ingestedDocIDs, nil
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

	opts := &ConsumeOptions{
		Title: cleanSubject,
		Owner: ownerID,
	}
	docID, err := mf.consumer.ConsumeFile(emlPath, filepath.Base(emlPath), opts)
	if err != nil {
		return 0, err
	}

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
