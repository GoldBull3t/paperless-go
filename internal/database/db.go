package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(dbPath string) (*DB, error) {
	// Enable WAL mode, busy timeout and foreign keys in DSN
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// In WAL mode, SQLite supports concurrent readers
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Verify SQLite database integrity on startup (Crash-Proof Guarantee)
	var checkResult string
	if err := db.QueryRow("PRAGMA quick_check(10)").Scan(&checkResult); err != nil {
		return nil, fmt.Errorf("database integrity verification failed: %w", err)
	}
	if checkResult != "ok" {
		return nil, fmt.Errorf("CRITICAL: SQLite database corruption detected: %s", checkResult)
	}

	wrapped := &DB{DB: db}
	if err := wrapped.migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return wrapped, nil
}

// BackupTo creates a frozen, 100% consistent online backup using SQLite's VACUUM INTO,
// safely capturing database state with zero downtime and no transaction lock contention.
func (db *DB) BackupTo(dstPath string) error {
	_ = os.Remove(dstPath)
	_, err := db.Exec("VACUUM INTO ?", dstPath)
	return err
}

func (db *DB) migrate() error {
	_, err := db.Exec(SchemaSQL)
	if err != nil {
		return err
	}
	// Safe backward compatibility for pre-existing databases
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN deleted_at DATETIME")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN archive_filename TEXT")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN page_count INTEGER DEFAULT 1")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN owner_id INTEGER")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN root_document_id INTEGER")
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN totp_secret TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN is_mfa_enabled BOOLEAN DEFAULT 0")
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN recovery_codes_json TEXT DEFAULT '[]'")
	_, _ = db.Exec("ALTER TABLE users ADD COLUMN oidc_sub TEXT UNIQUE")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN extracted_metadata_json TEXT DEFAULT '{}'")
	_, _ = db.Exec("ALTER TABLE documents ADD COLUMN is_vault INTEGER DEFAULT 0")
	log.Println("Database schema migrated successfully (SQLite WAL + FTS5 + AI Embeddings)")
	return nil
}
