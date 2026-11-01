package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "paperless-db-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("failed to open database: %v", err)
	}
	return db, tmpDir
}

func TestWithTx_Commit(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "TestTag", "test-tag")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx returned unexpected error: %v", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'test-tag'").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count tags: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 tag committed, got %d", count)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()
	simulatedErr := errors.New("simulated failure")

	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "RollbackTag", "rollback-tag")
		if err != nil {
			return err
		}
		return simulatedErr
	})
	if !errors.Is(err, simulatedErr) {
		t.Fatalf("expected simulatedErr, got %v", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'rollback-tag'").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query tags: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 tags after rollback, got %d", count)
	}
}

func TestWithTx_RollbackOnPanic(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx := context.Background()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to be propagated")
		}

		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM tags WHERE slug = 'panic-tag'").Scan(&count)
		if err != nil {
			t.Fatalf("failed to query tags after panic: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected 0 tags after panic rollback, got %d", count)
		}
	}()

	_ = db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO tags (name, slug) VALUES (?, ?)", "PanicTag", "panic-tag")
		if err != nil {
			return err
		}
		panic("critical crash inside transaction")
	})
}

func TestDB_Maintenance(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Direct execution
	db.RunMaintenance()

	// Periodic maintenance startup & shutdown
	db.StartMaintenance(ctx, 50*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	cancel()
}

func TestVersionedMigrations(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	records, err := db.GetAppliedMigrations()
	if err != nil {
		t.Fatalf("GetAppliedMigrations failed: %v", err)
	}

	if len(records) < 8 {
		t.Fatalf("expected at least 8 applied migrations, got %d", len(records))
	}

	if records[0].Version != 1 || records[0].Name != "0001_initial_schema" {
		t.Errorf("unexpected migration 1: %+v", records[0])
	}
	if records[1].Version != 2 || records[1].Name != "0002_backward_compat_columns" {
		t.Errorf("unexpected migration 2: %+v", records[1])
	}
	if records[2].Version != 3 || records[2].Name != "0003_tasks_persistence_and_retries" {
		t.Errorf("unexpected migration 3: %+v", records[2])
	}
	if records[3].Version != 4 || records[3].Name != "0004_search_indexes_and_fts_sync" {
		t.Errorf("unexpected migration 4: %+v", records[3])
	}
	if records[6].Version != 7 || records[6].Name != "0007_mail_schema_parity_and_columns" {
		t.Errorf("unexpected migration 7: %+v", records[6])
	}
}

func TestMigration7_LegacyMailRulesUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_mail.db")

	// Step 1: Create an older database table structure simulating older paperless-go
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed creating raw db: %v", err)
	}
	_, err = rawDB.Exec(`
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT, applied_at DATETIME);
		INSERT INTO schema_migrations (version, name) VALUES (1, '0001_initial_schema');
		INSERT INTO schema_migrations (version, name) VALUES (2, '0002_backward_compat_columns');
		INSERT INTO schema_migrations (version, name) VALUES (3, '0003_tasks_persistence_and_retries');
		INSERT INTO schema_migrations (version, name) VALUES (4, '0004_search_indexes_and_fts_sync');
		INSERT INTO schema_migrations (version, name) VALUES (5, '0005_vault_envelope_encryption_and_rotation');
		INSERT INTO schema_migrations (version, name) VALUES (6, '0006_network_scanners');

		CREATE TABLE mail_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			server TEXT NOT NULL,
			port INTEGER DEFAULT 993,
			username TEXT NOT NULL,
			password TEXT NOT NULL
		);

		CREATE TABLE mail_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			account_id INTEGER REFERENCES mail_accounts(id) ON DELETE CASCADE,
			order_num INTEGER DEFAULT 0
		);

		INSERT INTO mail_accounts (id, name, server, port, username, password)
		VALUES (1, 'Legacy Account', 'imap.legacy.com', 993, 'legacy@test.com', 'pass123');

		INSERT INTO mail_rules (id, name, account_id, order_num)
		VALUES (1, 'Legacy Invoices Rule', 1, 0);
	`)
	if err != nil {
		t.Fatalf("failed populating legacy db: %v", err)
	}
	_ = rawDB.Close()

	// Step 2: Open with database.Open, which runs migrations (including 0007)
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("database.Open failed on legacy database: %v", err)
	}
	defer db.Close()

	// Step 3: Verify SELECT with 'enabled' and all other columns now works without 'no such column' error!
	var enabled bool
	var folder string
	var orderNum int
	err = db.QueryRow("SELECT enabled, folder, order_num FROM mail_rules WHERE id = 1").Scan(&enabled, &folder, &orderNum)
	if err != nil {
		t.Fatalf("SELECT enabled from upgraded mail_rules failed: %v", err)
	}
	if !enabled {
		t.Errorf("expected enabled default to be true (1), got %v", enabled)
	}
	if folder != "INBOX" {
		t.Errorf("expected default folder 'INBOX', got %q", folder)
	}

	// Verify mail_accounts had imap_server and imap_port migrated from server/port
	var imapServer string
	var imapPort int
	err = db.QueryRow("SELECT imap_server, imap_port FROM mail_accounts WHERE id = 1").Scan(&imapServer, &imapPort)
	if err != nil {
		t.Fatalf("SELECT imap_server from upgraded mail_accounts failed: %v", err)
	}
	if imapServer != "imap.legacy.com" || imapPort != 993 {
		t.Errorf("expected imapServer='imap.legacy.com' and imapPort=993, got %q, %d", imapServer, imapPort)
	}
}

func TestFTS5_AutomaticTriggersSync(t *testing.T) {
	db, tmpDir := setupTestDB(t)
	defer os.RemoveAll(tmpDir)
	defer db.Close()

	// 1. Insert document into 'documents'
	res, err := db.Exec(`
		INSERT INTO documents (title, content, original_filename, checksum, mime_type)
		VALUES ('Fatura Eletricidade', 'Consumo mensal de 150 kWh no valor de 45 EUR', 'fatura.pdf', 'chk123', 'application/pdf')
	`)
	if err != nil {
		t.Fatalf("failed inserting test document: %v", err)
	}
	docID, _ := res.LastInsertId()

	// Verify trigger automatically inserted into documents_fts
	var ftsTitle, ftsContent string
	err = db.QueryRow("SELECT title, content FROM documents_fts WHERE rowid = ?", docID).Scan(&ftsTitle, &ftsContent)
	if err != nil {
		t.Fatalf("trigger documents_ai failed to populate documents_fts: %v", err)
	}
	if ftsTitle != "Fatura Eletricidade" {
		t.Errorf("expected fts title 'Fatura Eletricidade', got %q", ftsTitle)
	}

	// 2. Update document title and content
	_, err = db.Exec("UPDATE documents SET title = 'Fatura EDP 2026', content = 'Novo valor 50 EUR' WHERE id = ?", docID)
	if err != nil {
		t.Fatalf("failed updating document: %v", err)
	}

	// Verify trigger documents_au updated documents_fts
	err = db.QueryRow("SELECT title, content FROM documents_fts WHERE rowid = ?", docID).Scan(&ftsTitle, &ftsContent)
	if err != nil {
		t.Fatalf("failed querying documents_fts after update: %v", err)
	}
	if ftsTitle != "Fatura EDP 2026" || ftsContent != "Novo valor 50 EUR" {
		t.Errorf("expected updated fts values, got title=%q, content=%q", ftsTitle, ftsContent)
	}

	// 3. Delete document
	_, err = db.Exec("DELETE FROM documents WHERE id = ?", docID)
	if err != nil {
		t.Fatalf("failed deleting document: %v", err)
	}

	// Verify trigger documents_ad removed from documents_fts
	var dummy int
	err = db.QueryRow("SELECT rowid FROM documents_fts WHERE rowid = ?", docID).Scan(&dummy)
	if err == nil {
		t.Errorf("expected row to be deleted from documents_fts, but it still exists")
	}
}

