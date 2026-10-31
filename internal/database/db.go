package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

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

// StartMaintenance initiates periodic background WAL checkpointing and query planner optimization.
func (db *DB) StartMaintenance(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 1 * time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				db.RunMaintenance()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// RunMaintenance executes WAL checkpointing and statistics optimization
func (db *DB) RunMaintenance() {
	var busy, logCount, checkpointed int
	err := db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logCount, &checkpointed)
	if err != nil {
		slog.Warn("SQLite WAL checkpoint maintenance failed", "err", err)
	} else {
		slog.Debug("SQLite WAL checkpoint completed", "busy", busy, "log_frames", logCount, "checkpointed_frames", checkpointed)
	}

	if _, err := db.Exec("PRAGMA optimize"); err != nil {
		slog.Warn("SQLite optimizer maintenance failed", "err", err)
	}
}

func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "busy")
}

// WithTx executes the provided function within an atomic transaction with automatic retry on busy locks.
// It automatically handles rollback in case of error or panic, and commits on success.
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	var tx *sql.Tx
	var err error

	// Retry up to 3 times on transient SQLite busy locks with jittered backoff
	for attempt := 0; attempt < 3; attempt++ {
		tx, err = db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelDefault})
		if err == nil {
			break
		}
		if isBusyError(err) && attempt < 2 {
			backoff := time.Duration(40*(attempt+1))*time.Millisecond + time.Duration(rand.Intn(20))*time.Millisecond
			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
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

// MigrationRecord represents an applied database schema migration
type MigrationRecord struct {
	Version   int
	Name      string
	AppliedAt time.Time
}

// GetAppliedMigrations returns all migration records ordered by version
func (db *DB) GetAppliedMigrations() ([]MigrationRecord, error) {
	rows, err := db.Query("SELECT version, name, applied_at FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []MigrationRecord
	for rows.Next() {
		var rec MigrationRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.AppliedAt); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (db *DB) migrate() error {
	// 1. Ensure version tracking table exists
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed creating schema_migrations table: %w", err)
	}

	migrations := []struct {
		version int
		name    string
		run     func(db *DB) error
	}{
		{
			version: 1,
			name:    "0001_initial_schema",
			run: func(db *DB) error {
				_, err := db.Exec(SchemaSQL)
				return err
			},
		},
		{
			version: 2,
			name:    "0002_backward_compat_columns",
			run: func(db *DB) error {
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
				_, err = db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_sub ON users(oidc_sub) WHERE oidc_sub IS NOT NULL")
				return err
			},
		},
		{
			version: 3,
			name:    "0003_tasks_persistence_and_retries",
			run: func(db *DB) error {
				taskColumns := []struct {
					table  string
					column string
					def    string
				}{
					{"tasks", "payload_json", "TEXT DEFAULT '{}'"},
					{"tasks", "attempt_count", "INTEGER DEFAULT 0"},
					{"tasks", "max_attempts", "INTEGER DEFAULT 3"},
				}
				for _, col := range taskColumns {
					if err := addColumnIfNotExists(db, col.table, col.column, col.def); err != nil {
						return fmt.Errorf("migration error adding %s.%s: %w", col.table, col.column, err)
					}
				}
				return nil
			},
		},
		{
			version: 4,
			name:    "0004_search_indexes_and_fts_sync",
			run: func(db *DB) error {
				statements := []string{
					"CREATE INDEX IF NOT EXISTS idx_documents_checksum ON documents(checksum)",
					"CREATE INDEX IF NOT EXISTS idx_documents_storage_path ON documents(storage_path_id)",
					"CREATE INDEX IF NOT EXISTS idx_documents_owner ON documents(owner_id)",
					"CREATE INDEX IF NOT EXISTS idx_documents_vault ON documents(is_vault)",
					"CREATE INDEX IF NOT EXISTS idx_document_tags_tag_doc ON document_tags(tag_id, document_id)",
					"CREATE INDEX IF NOT EXISTS idx_document_custom_fields_field ON document_custom_fields(field_id, document_id)",
					"CREATE INDEX IF NOT EXISTS idx_document_notes_doc ON document_notes(document_id)",
					`CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
						INSERT OR REPLACE INTO documents_fts (rowid, title, content) VALUES (new.id, new.title, new.content);
					END`,
					`CREATE TRIGGER IF NOT EXISTS documents_au AFTER UPDATE OF title, content ON documents BEGIN
						UPDATE documents_fts SET title = new.title, content = new.content WHERE rowid = new.id;
					END`,
				}
				for _, stmt := range statements {
					if _, err := db.Exec(stmt); err != nil {
						return fmt.Errorf("migration 4 statement failed (%s): %w", stmt, err)
					}
				}
				return nil
			},
		},
	}

	for _, m := range migrations {
		var exists int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed checking migration status for version %d: %w", m.version, err)
		}
		if exists > 0 {
			continue
		}

		slog.Info("Applying database migration", "version", m.version, "name", m.name)
		if err := m.run(db); err != nil {
			return fmt.Errorf("failed running migration %d (%s): %w", m.version, m.name, err)
		}

		if _, err := db.Exec("INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, CURRENT_TIMESTAMP)", m.version, m.name); err != nil {
			return fmt.Errorf("failed recording migration %d in schema_migrations: %w", m.version, err)
		}
	}

	log.Println("Database schema migrated successfully (Versioned Migrations + SQLite WAL + FTS5)")
	return nil
}
