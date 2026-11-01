package consumer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestMailFetcher_ProcessEmailSecurity(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "mail_fetcher_test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("Open DB failed: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		MediaDir:  tmpDir,
		SecretKey: "mail-test-secret-key-32byteslong!",
	}
	c := NewConsumer(db, cfg)
	mf := NewMailFetcher(db, cfg, c, nil)

	// Insert test mail account and mail rule to satisfy foreign keys
	_, err = db.Exec(`
		INSERT INTO mail_accounts (id, name, imap_server, imap_port, imap_security, username, password)
		VALUES (10, 'Test Invoices Account', 'imap.test.local', 993, 3, 'invoices@trusted-supplier.com', 'pwd')
	`)
	if err != nil {
		t.Fatalf("Failed to insert test mail account: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO mail_rules (id, name, account_id, enabled, folder)
		VALUES (1, 'Test Invoices Rule', 10, 1, 'INBOX')
	`)
	if err != nil {
		t.Fatalf("Failed to insert test mail rule: %v", err)
	}

	// Create raw MIME email with a genuine PDF attachment
	rawEML := fmt.Sprintf(`From: Accounting <invoices@trusted-supplier.com>
To: Paperless <archive@my-organization.org>
Subject: Invoice March 2026 #9944
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="boundary123"

--boundary123
Content-Type: text/plain; charset="utf-8"

Please find your monthly invoice attached.

--boundary123
Content-Type: application/pdf
Content-Disposition: attachment; filename="Invoice_March_2026.pdf"

%%PDF-1.4
1 0 obj
<< /Type /Catalog >>
endobj
trailer
<< /Root 1 0 R >>
%%%%EOF

--boundary123--
`)

	// 1. Authorized Sender: should ingest successfully
	filterFrom := "invoices@trusted-supplier.com"
	ruleID := 1
	docIDs, err := mf.ProcessEmailMessage(10, &ruleID, "INBOX", "uid-1001", []byte(rawEML), filterFrom, "Invoice", nil)
	if err != nil {
		t.Fatalf("ProcessEmailMessage failed: %v", err)
	}
	if len(docIDs) != 1 {
		t.Fatalf("Expected 1 document ingested, got %d", len(docIDs))
	}

	// Verify logged in processed_mail
	var status string
	err = db.QueryRow("SELECT status FROM processed_mail WHERE uid = 'uid-1001'").Scan(&status)
	if err != nil || status != "SUCCESS" {
		t.Errorf("Expected processed_mail status SUCCESS, got %q (err: %v)", status, err)
	}

	// 2. Unauthorized Sender: should be rejected by whitelist
	unauthorizedEML := strings.Replace(rawEML, "invoices@trusted-supplier.com", "spammer@phishing-attack.com", 1)
	docIDs2, err := mf.ProcessEmailMessage(10, &ruleID, "INBOX", "uid-1002", []byte(unauthorizedEML), filterFrom, "Invoice", nil)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if len(docIDs2) != 0 {
		t.Fatalf("Expected 0 documents ingested from unauthorized sender, got %d", len(docIDs2))
	}

	// 3. Malware Attachment Blocked: email with .exe attachment
	malwareEML := fmt.Sprintf(`From: Accounting <invoices@trusted-supplier.com>
To: Paperless <archive@my-organization.org>
Subject: Urgent Payment Details
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="boundaryMalware"

--boundaryMalware
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="invoice_details.exe"

MZ malicious executable payload...
--boundaryMalware--
`)
	docIDs3, err := mf.ProcessEmailMessage(10, &ruleID, "INBOX", "uid-1003", []byte(malwareEML), filterFrom, "Urgent", nil)
	if err != nil {
		t.Fatalf("Expected no error on malware drop, got: %v", err)
	}
	if len(docIDs3) != 0 {
		t.Fatalf("Expected executable attachment to be dropped, got %d docs", len(docIDs3))
	}
}

func TestMailFetcher_PlaintextNonLocalhostRejected(t *testing.T) {
	mf := NewMailFetcher(nil, &config.Config{SecretKey: "secret"}, nil, nil)

	acc := mailAccountRecord{
		id:       1,
		name:     "Insecure Public Account",
		server:   "imap.public-server.com",
		port:     143,
		security: 1, // Plaintext
		username: "user@public-server.com",
		password: "plain-password",
	}

	err := mf.PollAccount(acc)
	if err == nil || !strings.Contains(err.Error(), "security violation") {
		t.Fatalf("Expected security violation error when using plaintext IMAP on public server, got: %v", err)
	}
}
