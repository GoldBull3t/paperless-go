package vault

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless-go/internal/crypto"
	"paperless-go/internal/database"
)

func TestVaultManager_FullLifecycleAndEnvelopeEncryption(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vault_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mediaDir := filepath.Join(tmpDir, "media")
	originalsDir := filepath.Join(mediaDir, "documents", "originals")
	thumbnailsDir := filepath.Join(mediaDir, "documents", "thumbnails")
	_ = os.MkdirAll(originalsDir, 0755)
	_ = os.MkdirAll(thumbnailsDir, 0755)

	vm := NewVaultManager(db, mediaDir)

	// 1. Initial status: not enabled, locked
	enabled, unlocked, _, _, err := vm.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if enabled || unlocked {
		t.Errorf("Expected vault disabled and locked initially, got enabled=%v, unlocked=%v", enabled, unlocked)
	}

	// 2. Setup master password & get emergency recovery key
	masterPass := "SuperSecretMasterKey2026!"
	recoveryKey, err := vm.Setup(masterPass, 30)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if len(recoveryKey) == 0 || !strings.Contains(recoveryKey, "-") {
		t.Fatalf("Expected formatted recovery key, got: %q", recoveryKey)
	}

	// Setup automatically unlocks
	if !vm.IsUnlocked() {
		t.Errorf("Expected vault to be unlocked immediately after setup")
	}

	// 3. Test Physical Document Encryption on Disk when Toggled to Vault
	res, err := db.Exec("INSERT INTO documents (title, original_filename, checksum, mime_type) VALUES ('Passport', 'pass.pdf', 'chk1', 'application/pdf')")
	if err != nil {
		t.Fatalf("Failed to insert document: %v", err)
	}
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	docFilePath := filepath.Join(originalsDir, fmt.Sprintf("%07d.pdf", docID))
	plainContent := "CONFIDENTIAL IDENTITY PASSPORT DATA #998877"
	if err := os.WriteFile(docFilePath, []byte(plainContent), 0644); err != nil {
		t.Fatalf("Failed writing test doc: %v", err)
	}

	isVault, err := vm.ToggleDocument(docID)
	if err != nil {
		t.Fatalf("ToggleDocument failed: %v", err)
	}
	if !isVault {
		t.Errorf("Expected document to be marked as vault-protected, got false")
	}

	// Verify file on disk is physically encrypted with Vault Magic Header
	isEncrypted, err := crypto.IsVaultEncryptedFile(docFilePath)
	if err != nil || !isEncrypted {
		t.Fatalf("Expected file on disk to be vault-encrypted with PPLX:VAULT:1 header, isEncrypted=%v, err=%v", isEncrypted, err)
	}

	rawDiskData, _ := os.ReadFile(docFilePath)
	if strings.Contains(string(rawDiskData), plainContent) {
		t.Errorf("Security flaw: Plaintext leaked inside vault-encrypted file on disk!")
	}

	// 4. Test Stream Decryption when Unlocked
	stream, err := vm.OpenDecryptedStream(docFilePath)
	if err != nil {
		t.Fatalf("OpenDecryptedStream failed: %v", err)
	}
	decryptedBytes, _ := io.ReadAll(stream)
	stream.Close()
	if string(decryptedBytes) != plainContent {
		t.Errorf("Decrypted content mismatch: expected %q, got %q", plainContent, string(decryptedBytes))
	}

	// 5. Test Lock and Security Denials
	vm.Lock()
	if vm.IsUnlocked() {
		t.Errorf("Expected vault to be locked after calling Lock()")
	}

	_, err = vm.OpenDecryptedStream(docFilePath)
	if err != ErrVaultLocked {
		t.Errorf("Expected ErrVaultLocked when opening stream on locked vault, got %v", err)
	}

	// 6. Test Unlock with bad password
	ok, _, err := vm.Unlock("wrongpassword")
	if ok || err == nil {
		t.Errorf("Expected Unlock to fail with wrong password, got ok=%v, err=%v", ok, err)
	}

	// 7. Test Unlock with correct password
	ok, remSec, err := vm.Unlock(masterPass)
	if !ok || err != nil {
		t.Fatalf("Unlock with valid password failed: %v", err)
	}
	if remSec <= 0 {
		t.Errorf("Expected remaining seconds > 0, got %d", remSec)
	}
	if !vm.IsUnlocked() {
		t.Errorf("Expected vault to be unlocked after valid Unlock")
	}

	// 8. Test Master Password Change (Re-wrapping VMEK)
	newPass := "EvenMoreSecretPassphrase2027#!"
	if err := vm.ChangeMasterPassword(masterPass, newPass); err != nil {
		t.Fatalf("ChangeMasterPassword failed: %v", err)
	}

	vm.Lock()
	// Old password must fail
	ok, _, _ = vm.Unlock(masterPass)
	if ok {
		t.Errorf("Old master password should no longer work after change")
	}
	// New password must succeed
	ok, _, err = vm.Unlock(newPass)
	if !ok || err != nil {
		t.Fatalf("Unlock with new master password failed: %v", err)
	}

	// Stream decryption still works with re-wrapped key
	s2, err := vm.OpenDecryptedStream(docFilePath)
	if err != nil {
		t.Fatalf("Decryption after password change failed: %v", err)
	}
	dBytes2, _ := io.ReadAll(s2)
	s2.Close()
	if string(dBytes2) != plainContent {
		t.Errorf("Content mismatch after password change: %q", string(dBytes2))
	}

	// 9. Test Emergency Recovery Key
	vm.Lock()
	recoveredPass := "RecoveredPasswordAfterLostPass2028!"
	if err := vm.Recover(recoveryKey, recoveredPass); err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if !vm.IsUnlocked() {
		t.Errorf("Expected vault to be unlocked after successful recovery")
	}

	// 10. Test Key Rotation (Re-encrypting all vault documents on physical disk)
	reencryptedCount, err := vm.RotateVaultKey(recoveredPass, mediaDir)
	if err != nil {
		t.Fatalf("RotateVaultKey failed: %v", err)
	}
	if reencryptedCount != 1 {
		t.Errorf("Expected 1 document re-encrypted, got %d", reencryptedCount)
	}

	// Verify document is still completely intact after VMEK rotation
	s3, err := vm.OpenDecryptedStream(docFilePath)
	if err != nil {
		t.Fatalf("OpenDecryptedStream failed after key rotation: %v", err)
	}
	dBytes3, _ := io.ReadAll(s3)
	s3.Close()
	if string(dBytes3) != plainContent {
		t.Errorf("Content corrupted after key rotation: %q", string(dBytes3))
	}

	// 11. Test Untoggle from Vault (decrypts physical file back to standard)
	isVault2, err := vm.ToggleDocument(docID)
	if err != nil {
		t.Fatalf("ToggleDocument back failed: %v", err)
	}
	if isVault2 {
		t.Errorf("Expected document to be unmarked from vault, got true")
	}

	// Physical file on disk is back to plaintext
	isStillEnc, _ := crypto.IsVaultEncryptedFile(docFilePath)
	if isStillEnc {
		t.Errorf("Expected file to be decrypted on untoggle, but still has vault magic header")
	}
	finalBytes, _ := os.ReadFile(docFilePath)
	if string(finalBytes) != plainContent {
		t.Errorf("Expected plaintext content on untoggled disk file: got %q", string(finalBytes))
	}
}

func TestVault_AutoLockExpiration(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_autolock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	vm := NewVaultManager(db)
	_, err = vm.Setup("MasterKey2026!", 1)
	if err != nil {
		t.Fatal(err)
	}

	if !vm.IsUnlocked() {
		t.Errorf("Expected vault unlocked after setup")
	}

	// Artificially expire the unlock duration
	vm.mu.Lock()
	vm.unlockedUntil = time.Now().Add(-1 * time.Second)
	vm.mu.Unlock()

	// Calling IsUnlocked should detect expiration, zeroize and lock
	if vm.IsUnlocked() {
		t.Errorf("Expected vault to auto-lock after expiration time")
	}
}
