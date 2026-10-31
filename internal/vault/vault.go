package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
	"paperless-go/internal/crypto"
	"paperless-go/internal/database"
)

var (
	ErrVaultLocked        = errors.New("vault is locked")
	ErrVaultNotConfigured = errors.New("vault is not configured")
	ErrInvalidPassword    = errors.New("invalid master password")
	ErrInvalidRecoveryKey = errors.New("invalid recovery key")
)

type VaultManager struct {
	db              *database.DB
	mediaDir        string
	isUnlocked      bool
	unlockedUntil   time.Time
	autoLockMinutes int
	keyVersion      int
	vmek            []byte // in-memory unsealed Vault Master Encryption Key (zeroized on lock)
	mu              sync.RWMutex
}

func NewVaultManager(db *database.DB, mediaDir ...string) *VaultManager {
	vm := &VaultManager{
		db:              db,
		autoLockMinutes: 15,
		keyVersion:      1,
	}
	if len(mediaDir) > 0 {
		vm.mediaDir = mediaDir[0]
	}

	var mins, kv int
	if err := db.QueryRow("SELECT auto_lock_minutes, COALESCE(key_version, 1) FROM vault_config WHERE id = 1").Scan(&mins, &kv); err == nil {
		if mins > 0 {
			vm.autoLockMinutes = mins
		}
		if kv > 0 {
			vm.keyVersion = kv
		}
	}
	return vm
}

func (v *VaultManager) SetMediaDir(mediaDir string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mediaDir = mediaDir
}

// deriveKeys implements Argon2id key derivation with HKDF domain separation.
// It outputs two cryptographically independent 256-bit keys:
// 1. authKey: Used strictly for password verification (SHA-256 verifier hash).
// 2. encKey: Used strictly as the Key Encryption Key (KEK) to unwrap the VMEK.
func deriveKeys(masterPassword string, salt []byte) (authKey []byte, encKey []byte, err error) {
	// Argon2id: 3 iterations, 64MB memory, 2 threads, 32 bytes output
	masterKey := argon2.IDKey([]byte(masterPassword), salt, 3, 64*1024, 2, 32)
	defer zeroize(masterKey)

	authReader := hkdf.New(sha256.New, masterKey, nil, []byte("paperless-vault-auth-key-v1"))
	authKey = make([]byte, 32)
	if _, err := io.ReadFull(authReader, authKey); err != nil {
		return nil, nil, fmt.Errorf("failed deriving auth key: %w", err)
	}

	encReader := hkdf.New(sha256.New, masterKey, nil, []byte("paperless-vault-encryption-key-v1"))
	encKey = make([]byte, 32)
	if _, err := io.ReadFull(encReader, encKey); err != nil {
		return nil, nil, fmt.Errorf("failed deriving encryption key: %w", err)
	}

	return authKey, encKey, nil
}

func hashKey(key []byte) string {
	h := sha256.Sum256(key)
	return hex.EncodeToString(h[:])
}

func wrapKey(keyToWrap []byte, kek []byte) (string, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, keyToWrap, nil)
	combined := append(nonce, ciphertext...)
	return hex.EncodeToString(combined), nil
}

func unwrapKey(wrappedHex string, kek []byte) ([]byte, error) {
	combined, err := hex.DecodeString(wrappedHex)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(combined) < nonceSize {
		return nil, fmt.Errorf("invalid wrapped key payload length")
	}
	nonce := combined[:nonceSize]
	ciphertext := combined[nonceSize:]
	unwrapped, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to unwrap key: authentication failed: %w", err)
	}
	return unwrapped, nil
}

func generateRecoveryKey() (rawKey string, formattedKey string, salt []byte, err error) {
	rawBytes := make([]byte, 16) // 128-bit cryptographic entropy
	if _, err := rand.Read(rawBytes); err != nil {
		return "", "", nil, err
	}
	rawHex := strings.ToUpper(hex.EncodeToString(rawBytes))
	var parts []string
	for i := 0; i < len(rawHex); i += 4 {
		parts = append(parts, rawHex[i:i+4])
	}
	formattedKey = strings.Join(parts, "-")

	salt = make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", nil, err
	}
	return rawHex, formattedKey, salt, nil
}

func deriveRecoveryKey(cleanRecoveryKey string, salt []byte) []byte {
	normalized := strings.ToUpper(strings.ReplaceAll(cleanRecoveryKey, "-", ""))
	return argon2.IDKey([]byte(normalized), salt, 3, 64*1024, 2, 32)
}

func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Setup initializes master password, generates random VMEK and recovery key,
// and saves envelope-encrypted keys into vault_config.
func (v *VaultManager) Setup(masterPassword string, autoLockMinutes int) (string, error) {
	if len(masterPassword) < 6 {
		return "", fmt.Errorf("master password must be at least 6 characters")
	}
	if autoLockMinutes <= 0 {
		autoLockMinutes = 15
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed generating cryptographic salt: %w", err)
	}

	authKey, encKey, err := deriveKeys(masterPassword, salt)
	if err != nil {
		return "", err
	}
	defer zeroize(authKey)
	defer zeroize(encKey)

	verifier := hashKey(authKey)
	saltHex := hex.EncodeToString(salt)

	// Generate 256-bit Vault Master Encryption Key (VMEK)
	vmek := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, vmek); err != nil {
		return "", fmt.Errorf("failed generating VMEK: %w", err)
	}

	wrappedVMEK, err := wrapKey(vmek, encKey)
	if err != nil {
		return "", fmt.Errorf("failed wrapping VMEK: %w", err)
	}

	// Generate emergency recovery key
	rawRecKey, formattedRecKey, recSalt, err := generateRecoveryKey()
	if err != nil {
		return "", fmt.Errorf("failed generating recovery key: %w", err)
	}
	derivedRecKey := deriveRecoveryKey(rawRecKey, recSalt)
	defer zeroize(derivedRecKey)

	wrappedRecVMEK, err := wrapKey(vmek, derivedRecKey)
	if err != nil {
		return "", fmt.Errorf("failed wrapping VMEK with recovery key: %w", err)
	}

	_, err = v.db.Exec(`
		INSERT INTO vault_config (
			id, is_enabled, salt_hex, verifier_hash, wrapped_key_hex,
			key_version, recovery_salt_hex, recovery_wrapped_key_hex,
			auto_lock_minutes, updated_at
		)
		VALUES (1, 1, ?, ?, ?, 1, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			is_enabled = 1,
			salt_hex = excluded.salt_hex,
			verifier_hash = excluded.verifier_hash,
			wrapped_key_hex = excluded.wrapped_key_hex,
			key_version = excluded.key_version,
			recovery_salt_hex = excluded.recovery_salt_hex,
			recovery_wrapped_key_hex = excluded.recovery_wrapped_key_hex,
			auto_lock_minutes = excluded.auto_lock_minutes,
			updated_at = CURRENT_TIMESTAMP
	`, saltHex, verifier, wrappedVMEK, hex.EncodeToString(recSalt), wrappedRecVMEK, autoLockMinutes)
	if err != nil {
		return "", fmt.Errorf("failed to persist vault configuration: %w", err)
	}

	v.mu.Lock()
	v.autoLockMinutes = autoLockMinutes
	v.keyVersion = 1
	if v.vmek != nil {
		zeroize(v.vmek)
	}
	v.vmek = vmek
	v.isUnlocked = true
	v.unlockedUntil = time.Now().Add(time.Duration(autoLockMinutes) * time.Minute)
	v.mu.Unlock()

	return formattedRecKey, nil
}

// Unlock verifies master password, unseals the VMEK into memory, and resets the auto-lock window.
func (v *VaultManager) Unlock(masterPassword string) (bool, int, error) {
	var saltHex, verifier, wrappedKeyHex string
	var mins, keyVersion int
	err := v.db.QueryRow(`
		SELECT salt_hex, verifier_hash, COALESCE(wrapped_key_hex, ''), auto_lock_minutes, COALESCE(key_version, 1)
		FROM vault_config
		WHERE id = 1 AND is_enabled = 1
	`).Scan(&saltHex, &verifier, &wrappedKeyHex, &mins, &keyVersion)
	if err == sql.ErrNoRows {
		return false, 0, ErrVaultNotConfigured
	} else if err != nil {
		return false, 0, err
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false, 0, fmt.Errorf("invalid salt stored in vault")
	}

	authKey, encKey, err := deriveKeys(masterPassword, salt)
	if err != nil {
		return false, 0, err
	}
	defer zeroize(authKey)
	defer zeroize(encKey)

	computedVerifier := hashKey(authKey)
	if computedVerifier != verifier {
		return false, 0, ErrInvalidPassword
	}

	var vmek []byte
	if wrappedKeyHex != "" {
		vmek, err = unwrapKey(wrappedKeyHex, encKey)
		if err != nil {
			return false, 0, fmt.Errorf("failed to unseal VMEK: %w", err)
		}
	} else {
		// Legacy vault upgrade: generate new VMEK and wrap it
		vmek = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, vmek); err != nil {
			return false, 0, err
		}
		newWrapped, err := wrapKey(vmek, encKey)
		if err == nil {
			_, _ = v.db.Exec("UPDATE vault_config SET wrapped_key_hex = ?, key_version = 1 WHERE id = 1", newWrapped)
		}
	}

	if mins <= 0 {
		mins = 15
	}

	v.mu.Lock()
	v.autoLockMinutes = mins
	v.keyVersion = keyVersion
	if v.vmek != nil {
		zeroize(v.vmek)
	}
	v.vmek = vmek
	v.isUnlocked = true
	v.unlockedUntil = time.Now().Add(time.Duration(mins) * time.Minute)
	remSec := int(time.Until(v.unlockedUntil).Seconds())
	v.mu.Unlock()

	return true, remSec, nil
}

// Lock immediately locks the secure vault and securely zeroes out unsealed key material in memory.
func (v *VaultManager) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lockLocked()
}

func (v *VaultManager) lockLocked() {
	if v.vmek != nil {
		zeroize(v.vmek)
		v.vmek = nil
	}
	v.isUnlocked = false
	v.unlockedUntil = time.Time{}
}

// IsUnlocked returns whether the vault is currently accessible and auto-locks on expiration.
func (v *VaultManager) IsUnlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.isUnlocked {
		return false
	}
	if time.Now().After(v.unlockedUntil) {
		v.lockLocked()
		return false
	}
	return true
}

// GetActiveVMEK returns a copy of the active in-memory VMEK if unlocked.
func (v *VaultManager) GetActiveVMEK() ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if !v.isUnlocked || time.Now().After(v.unlockedUntil) || len(v.vmek) == 0 {
		return nil, ErrVaultLocked
	}
	cp := make([]byte, len(v.vmek))
	copy(cp, v.vmek)
	return cp, nil
}

// OpenDecryptedStream returns an io.ReadCloser for a document file protected by the vault.
func (v *VaultManager) OpenDecryptedStream(path string) (io.ReadCloser, error) {
	vmek, err := v.GetActiveVMEK()
	if err != nil {
		return nil, err
	}
	defer zeroize(vmek)

	return crypto.OpenVaultDecryptedStream(path, vmek)
}

// Status returns current configuration and lock state of the vault.
func (v *VaultManager) Status() (bool, bool, int, int, error) {
	var enabled bool
	var mins, kv int
	err := v.db.QueryRow("SELECT is_enabled, auto_lock_minutes, COALESCE(key_version, 1) FROM vault_config WHERE id = 1").Scan(&enabled, &mins, &kv)
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

// ChangeMasterPassword rotates the master password by unsealing the VMEK,
// deriving new authentication and encryption keys, and re-wrapping the VMEK.
// Physical document files on disk do NOT need re-encryption as the VMEK remains consistent.
func (v *VaultManager) ChangeMasterPassword(oldPassword, newPassword string) error {
	if len(newPassword) < 6 {
		return fmt.Errorf("new master password must be at least 6 characters")
	}

	vmek, err := v.GetActiveVMEK()
	if err != nil {
		// Attempt unlock with old password if locked
		ok, _, uErr := v.Unlock(oldPassword)
		if !ok || uErr != nil {
			return fmt.Errorf("failed to authenticate with old master password: %w", uErr)
		}
		vmek, err = v.GetActiveVMEK()
		if err != nil {
			return err
		}
	}
	defer zeroize(vmek)

	newSalt := make([]byte, 16)
	if _, err := rand.Read(newSalt); err != nil {
		return err
	}

	newAuthKey, newEncKey, err := deriveKeys(newPassword, newSalt)
	if err != nil {
		return err
	}
	defer zeroize(newAuthKey)
	defer zeroize(newEncKey)

	newVerifier := hashKey(newAuthKey)
	newWrappedVMEK, err := wrapKey(vmek, newEncKey)
	if err != nil {
		return fmt.Errorf("failed re-wrapping VMEK: %w", err)
	}

	_, err = v.db.Exec(`
		UPDATE vault_config
		SET salt_hex = ?, verifier_hash = ?, wrapped_key_hex = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, hex.EncodeToString(newSalt), newVerifier, newWrappedVMEK)
	if err != nil {
		return fmt.Errorf("failed updating vault configuration: %w", err)
	}

	return nil
}

// Recover unseals the VMEK using the Emergency Recovery Key and sets a new master password.
func (v *VaultManager) Recover(recoveryKey, newMasterPassword string) error {
	if len(newMasterPassword) < 6 {
		return fmt.Errorf("new master password must be at least 6 characters")
	}

	var recSaltHex, recWrappedKeyHex string
	var mins int
	err := v.db.QueryRow(`
		SELECT recovery_salt_hex, recovery_wrapped_key_hex, auto_lock_minutes
		FROM vault_config
		WHERE id = 1 AND is_enabled = 1
	`).Scan(&recSaltHex, &recWrappedKeyHex, &mins)
	if err != nil {
		return fmt.Errorf("failed retrieving recovery config: %w", err)
	}

	recSalt, err := hex.DecodeString(recSaltHex)
	if err != nil {
		return fmt.Errorf("invalid recovery salt")
	}

	derivedRecKey := deriveRecoveryKey(recoveryKey, recSalt)
	defer zeroize(derivedRecKey)

	vmek, err := unwrapKey(recWrappedKeyHex, derivedRecKey)
	if err != nil {
		return ErrInvalidRecoveryKey
	}
	defer zeroize(vmek)

	// Derive new master password keys
	newSalt := make([]byte, 16)
	if _, err := rand.Read(newSalt); err != nil {
		return err
	}
	newAuthKey, newEncKey, err := deriveKeys(newMasterPassword, newSalt)
	if err != nil {
		return err
	}
	defer zeroize(newAuthKey)
	defer zeroize(newEncKey)

	newVerifier := hashKey(newAuthKey)
	newWrappedVMEK, err := wrapKey(vmek, newEncKey)
	if err != nil {
		return fmt.Errorf("failed wrapping VMEK: %w", err)
	}

	// Re-wrap with existing recovery key
	newWrappedRecVMEK, err := wrapKey(vmek, derivedRecKey)
	if err != nil {
		return fmt.Errorf("failed re-wrapping recovery key: %w", err)
	}

	_, err = v.db.Exec(`
		UPDATE vault_config
		SET salt_hex = ?, verifier_hash = ?, wrapped_key_hex = ?, recovery_wrapped_key_hex = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, hex.EncodeToString(newSalt), newVerifier, newWrappedVMEK, newWrappedRecVMEK)
	if err != nil {
		return fmt.Errorf("failed saving recovered vault: %w", err)
	}

	v.mu.Lock()
	if v.vmek != nil {
		zeroize(v.vmek)
	}
	v.vmek = make([]byte, len(vmek))
	copy(v.vmek, vmek)
	v.isUnlocked = true
	v.unlockedUntil = time.Now().Add(time.Duration(mins) * time.Minute)
	v.mu.Unlock()

	return nil
}

// RotateVaultKey generates a new VMEK, re-encrypts all physical documents marked as is_vault = 1
// from vmek_v1 to vmek_v2, and updates vault_config.
func (v *VaultManager) RotateVaultKey(masterPassword string, mediaDir string) (int, error) {
	ok, _, err := v.Unlock(masterPassword)
	if !ok || err != nil {
		return 0, fmt.Errorf("authentication failed: %w", err)
	}

	oldVMEK, err := v.GetActiveVMEK()
	if err != nil {
		return 0, err
	}
	defer zeroize(oldVMEK)

	// Generate new VMEK_v2
	newVMEK := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newVMEK); err != nil {
		return 0, fmt.Errorf("failed generating new VMEK: %w", err)
	}
	defer zeroize(newVMEK)

	// Retrieve salt and recovery salt to re-wrap newVMEK
	var saltHex, recSaltHex string
	var currentKeyVersion int
	err = v.db.QueryRow("SELECT salt_hex, recovery_salt_hex, COALESCE(key_version, 1) FROM vault_config WHERE id = 1").Scan(&saltHex, &recSaltHex, &currentKeyVersion)
	if err != nil {
		return 0, err
	}

	salt, _ := hex.DecodeString(saltHex)
	_, encKey, err := deriveKeys(masterPassword, salt)
	if err != nil {
		return 0, err
	}
	defer zeroize(encKey)

	newWrappedVMEK, err := wrapKey(newVMEK, encKey)
	if err != nil {
		return 0, err
	}

	// Re-encrypt all vault-protected document files on physical disk
	rows, err := v.db.Query("SELECT id, original_filename, archive_filename FROM documents WHERE is_vault = 1")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type docFile struct {
		id       int
		origName string
		archName sql.NullString
	}
	var docsToRotate []docFile
	for rows.Next() {
		var df docFile
		if err := rows.Scan(&df.id, &df.origName, &df.archName); err == nil {
			docsToRotate = append(docsToRotate, df)
		}
	}
	rows.Close()

	reencryptedCount := 0
	newKeyVersion := currentKeyVersion + 1

	for _, df := range docsToRotate {
		ext := filepath.Ext(df.origName)
		origPath := filepath.Join(mediaDir, "documents", "originals", fmt.Sprintf("%07d%s", df.id, ext))
		if _, err := os.Stat(origPath); err == nil {
			isEnc, _ := crypto.IsVaultEncryptedFile(origPath)
			if isEnc {
				tmpDecrypted := origPath + ".rekey_tmp"
				if err := crypto.DecryptVaultFile(origPath, tmpDecrypted, oldVMEK); err == nil {
					_ = crypto.EncryptVaultFile(tmpDecrypted, origPath, newVMEK)
					_ = crypto.ShredFile(tmpDecrypted)
				}
			}
		}

		if df.archName.Valid && df.archName.String != "" {
			archPath := filepath.Join(mediaDir, "documents", "archive", fmt.Sprintf("%07d.pdf", df.id))
			if _, err := os.Stat(archPath); err == nil {
				isEnc, _ := crypto.IsVaultEncryptedFile(archPath)
				if isEnc {
					tmpDecrypted := archPath + ".rekey_tmp"
					if err := crypto.DecryptVaultFile(archPath, tmpDecrypted, oldVMEK); err == nil {
						_ = crypto.EncryptVaultFile(tmpDecrypted, archPath, newVMEK)
						_ = crypto.ShredFile(tmpDecrypted)
					}
				}
			}
		}

		_, _ = v.db.Exec("UPDATE documents SET vault_key_version = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newKeyVersion, df.id)
		reencryptedCount++
	}

	// Save new wrapped key and increment key version
	_, err = v.db.Exec(`
		UPDATE vault_config
		SET wrapped_key_hex = ?, key_version = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, newWrappedVMEK, newKeyVersion)
	if err != nil {
		return reencryptedCount, fmt.Errorf("failed persisting rotated key: %w", err)
	}

	v.mu.Lock()
	if v.vmek != nil {
		zeroize(v.vmek)
	}
	v.vmek = make([]byte, len(newVMEK))
	copy(v.vmek, newVMEK)
	v.keyVersion = newKeyVersion
	v.mu.Unlock()

	return reencryptedCount, nil
}

// ToggleDocument toggles a document's vault status and physically encrypts/decrypts the files on disk.
func (v *VaultManager) ToggleDocument(docID int, mediaDir ...string) (bool, error) {
	if !v.IsUnlocked() {
		return false, ErrVaultLocked
	}

	vmek, err := v.GetActiveVMEK()
	if err != nil {
		return false, err
	}
	defer zeroize(vmek)

	var currentVault int
	var origName string
	var archName sql.NullString
	err = v.db.QueryRow("SELECT is_vault, original_filename, archive_filename FROM documents WHERE id = ?", docID).Scan(&currentVault, &origName, &archName)
	if err != nil {
		return false, err
	}

	dir := v.mediaDir
	if len(mediaDir) > 0 && mediaDir[0] != "" {
		dir = mediaDir[0]
	}

	newVault := 1
	if currentVault == 1 {
		newVault = 0
	}

	// Physically encrypt or decrypt files on disk if mediaDir is available
	if dir != "" {
		ext := filepath.Ext(origName)
		origPath := filepath.Join(dir, "documents", "originals", fmt.Sprintf("%07d%s", docID, ext))

		if newVault == 1 {
			// Enclosing into vault: Encrypt with AES-256-GCM
			if _, err := os.Stat(origPath); err == nil {
				isEnc, _ := crypto.IsVaultEncryptedFile(origPath)
				if !isEnc {
					_ = crypto.EncryptVaultFile(origPath, origPath, vmek)
				}
			}
			if archName.Valid && archName.String != "" {
				archPath := filepath.Join(dir, "documents", "archive", fmt.Sprintf("%07d.pdf", docID))
				if _, err := os.Stat(archPath); err == nil {
					isEnc, _ := crypto.IsVaultEncryptedFile(archPath)
					if !isEnc {
						_ = crypto.EncryptVaultFile(archPath, archPath, vmek)
					}
				}
			}
		} else {
			// Removing from vault: Decrypt back to plaintext / standard storage
			if _, err := os.Stat(origPath); err == nil {
				isEnc, _ := crypto.IsVaultEncryptedFile(origPath)
				if isEnc {
					_ = crypto.DecryptVaultFile(origPath, origPath, vmek)
				}
			}
			if archName.Valid && archName.String != "" {
				archPath := filepath.Join(dir, "documents", "archive", fmt.Sprintf("%07d.pdf", docID))
				if _, err := os.Stat(archPath); err == nil {
					isEnc, _ := crypto.IsVaultEncryptedFile(archPath)
					if isEnc {
						_ = crypto.DecryptVaultFile(archPath, archPath, vmek)
					}
				}
			}
		}
	}

	_, err = v.db.Exec("UPDATE documents SET is_vault = ?, modified = CURRENT_TIMESTAMP WHERE id = ?", newVault, docID)
	if err != nil {
		return false, err
	}

	return newVault == 1, nil
}
