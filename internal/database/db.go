package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

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

// WithTx executes the provided function within an atomic transaction.
// It automatically handles rollback in case of error or panic, and commits on success.
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault})
	if err != nil {
		return fmt.Errorf("failed to begin atomic database transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil && rbErr != sql.ErrTxDone {
			return fmt.Errorf("transaction execution failed: %v (rollback failed: %w)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// BackupTo creates a frozen, 100% consistent online backup using SQLite's VACUUM INTO,
// safely capturing database state with zero downtime and no transaction lock contention.
func (db *DB) BackupTo(dstPath string) error {
	if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove existing backup file before VACUUM: %w", err)
	}
	_, err := db.Exec("VACUUM INTO ?", dstPath)
	return err
}

func addColumnIfNotExists(db *DB, table, column, colDef string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("failed to inspect table info for %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("failed to scan table info for %s: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return nil // column already exists
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cursor error inspecting table info for %s: %w", table, err)
	}

	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, colDef))
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return fmt.Errorf("failed to add column %s to %s: %w", column, table, err)
	}
	return nil
}

func (db *DB) migrate() error {
	_, err := db.Exec(SchemaSQL)
	if err != nil {
		return err
	}
	// Safe backward compatibility for pre-existing databases with strict error checking
	columnsToAdd := []struct {
		table  string
		column string
		def    string
	}{
		{"documents", "deleted_at", "DATETIME"},
		{"documents", "archive_filename", "TEXT"},
		{"documents", "page_count", "INTEGER DEFAULT 1"},
		{"documents", "owner_id", "INTEGER"},
		{"documents", "root_document_id", "INTEGER"},
		{"users", "totp_secret", "TEXT DEFAULT ''"},
		{"users", "is_mfa_enabled", "BOOLEAN DEFAULT 0"},
		{"users", "recovery_codes_json", "TEXT DEFAULT '[]'"},
		{"users", "oidc_sub", "TEXT"},
		{"documents", "extracted_metadata_json", "TEXT DEFAULT '{}'"},
		{"documents", "is_vault", "INTEGER DEFAULT 0"},
	}

	for _, col := range columnsToAdd {
		if err := addColumnIfNotExists(db, col.table, col.column, col.def); err != nil {
			return fmt.Errorf("migration error adding %s.%s: %w", col.table, col.column, err)
		}
	}

	// Ensure unique index for oidc_sub without violating SQLite ALTER TABLE constraints
	_, err = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_sub ON users(oidc_sub) WHERE oidc_sub IS NOT NULL")
	if err != nil {
		return fmt.Errorf("failed to create idx_users_oidc_sub: %w", err)
	}

	log.Println("Database schema migrated successfully (SQLite WAL + FTS5 + AI Embeddings)")
	return nil
}
