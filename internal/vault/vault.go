package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"paperless-go/internal/database"
)

type VaultManager struct {
	db              *database.DB
	isUnlocked      bool
	unlockedUntil   time.Time
	autoLockMinutes int
	mu              sync.RWMutex
}

func NewVaultManager(db *database.DB) *VaultManager {
	vm := &VaultManager{
		db:              db,
		autoLockMinutes: 15,
	}
	// Load auto_lock_minutes if configured
	var mins int
	if err := db.QueryRow("SELECT auto_lock_minutes FROM vault_config WHERE id = 1").Scan(&mins); err == nil && mins > 0 {
		vm.autoLockMinutes = mins
	}
	return vm
}

func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 1, 64*1024, 2, 32)
}

func hashKey(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}

// Setup initializes or changes the master password for the secure vault
func (v *VaultManager) Setup(masterPassword string, autoLockMinutes int) error {
	if len(masterPassword) < 6 {
		return fmt.Errorf("master password must be at least 6 characters")
	}
	if autoLockMinutes <= 0 {
		autoLockMinutes = 15
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate cryptographic salt: %w", err)
	}

	key := deriveKey(masterPassword, salt)
	verifier := hashKey(key)
	saltHex := hex.EncodeToString(salt)

	_, err := v.db.Exec(`
		INSERT INTO vault_config (id, is_enabled, salt_hex, verifier_hash, auto_lock_minutes, updated_at)
		VALUES (1, 1, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			is_enabled = 1,
			salt_hex = excluded.salt_hex,
			verifier_hash = excluded.verifier_hash,
			auto_lock_minutes = excluded.auto_lock_minutes,
			updated_at = CURRENT_TIMESTAMP
	`, saltHex, verifier, autoLockMinutes)
	if err != nil {
		return fmt.Errorf("failed to persist vault configuration: %w", err)
	}

	v.mu.Lock()
	v.autoLockMinutes = autoLockMinutes
	v.isUnlocked = true
	v.unlockedUntil = time.Now().Add(time.Duration(autoLockMinutes) * time.Minute)
	v.mu.Unlock()

	return nil
}

// Unlock verifies the master password and unlocks the vault for the configured duration
func (v *VaultManager) Unlock(masterPassword string) (bool, int, error) {
	var saltHex, verifier string
	var mins int
	err := v.db.QueryRow("SELECT salt_hex, verifier_hash, auto_lock_minutes FROM vault_config WHERE id = 1 AND is_enabled = 1").Scan(&saltHex, &verifier, &mins)
	if err == sql.ErrNoRows {
		return false, 0, fmt.Errorf("vault is not configured")
	} else if err != nil {
		return false, 0, err
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false, 0, fmt.Errorf("invalid salt stored in vault")
	}

	key := deriveKey(masterPassword, salt)
	computedVerifier := hashKey(key)

	if computedVerifier != verifier {
		return false, 0, fmt.Errorf("invalid master password")
	}

	if mins <= 0 {
		mins = 15
	}

	v.mu.Lock()
	v.autoLockMinutes = mins
	v.isUnlocked = true
	v.unlockedUntil = time.Now().Add(time.Duration(mins) * time.Minute)
	remSec := int(time.Until(v.unlockedUntil).Seconds())
	v.mu.Unlock()

	return true, remSec, nil
}

// Lock immediately locks the secure vault
func (v *VaultManager) Lock() {
	v.mu.Lock()
	v.isUnlocked = false
	v.unlockedUntil = time.Time{}
	v.mu.Unlock()
}

// IsUnlocked returns whether the vault is currently accessible
func (v *VaultManager) IsUnlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.isUnlocked {
		return false
	}
	if time.Now().After(v.unlockedUntil) {
		v.isUnlocked = false
		v.unlockedUntil = time.Time{}
		return false
	}
	return true
}

// Status returns current configuration and lock state of the vault
func (v *VaultManager) Status() (bool, bool, int, int, error) {
	var enabled bool
	var mins int
	err := v.db.QueryRow("SELECT is_enabled, auto_lock_minutes FROM vault_config WHERE id = 1").Scan(&enabled, &mins)
	if err == sql.ErrNoRows {
		return false, false, 0, 0, nil
	} else if err != nil {
		return false, false, 0, 0, err
	}

	var vaultDocCount int
	_ = v.db.QueryRow("SELECT COUNT(*) FROM documents WHERE is_vault = 1 AND deleted_at IS NULL").Scan(&vaultDocCount)

	unlocked := v.IsUnlocked()
	remainingSeconds := 0
	if unlocked {
		v.mu.RLock()
		remainingSeconds = int(time.Until(v.unlockedUntil).Seconds())
		v.mu.RUnlock()
		if remainingSeconds < 0 {
			remainingSeconds = 0
		}
	}

	return enabled, unlocked, remainingSeconds, vaultDocCount, nil
}

// ToggleDocument toggles a document's vault status between protected (1) and normal (0)
func (v *VaultManager) ToggleDocument(docID int) (bool, error) {
	var currentVault int
	err := v.db.QueryRow("SELECT is_vault FROM documents WHERE id = ?", docID).Scan(&currentVault)
	if err != nil {
		return false, err
	}

	newVault := 1
	if currentVault == 1 {
		newVault = 0
	}

	_, err = v.db.Exec("UPDATE documents SET is_vault = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newVault, docID)
	if err != nil {
		return false, err
	}

	return newVault == 1, nil
}
