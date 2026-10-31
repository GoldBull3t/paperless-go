package vault

import (
	"os"
	"path/filepath"
	"testing"

	"paperless-go/internal/database"
)

func TestVaultManager(t *testing.T) {
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

	vm := NewVaultManager(db)

	// 1. Initial status: not enabled, locked
	enabled, unlocked, _, _, err := vm.Status()
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if enabled || unlocked {
		t.Errorf("Expected vault disabled and locked initially, got enabled=%v, unlocked=%v", enabled, unlocked)
	}

	// 2. Setup master password
	masterPass := "SuperSecretMasterKey2026!"
	if err := vm.Setup(masterPass, 30); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Setup automatically unlocks for configured session
	if !vm.IsUnlocked() {
		t.Errorf("Expected vault to be unlocked immediately after setup")
	}

	// 3. Test Lock
	vm.Lock()
	if vm.IsUnlocked() {
		t.Errorf("Expected vault to be locked after calling Lock()")
	}

	// 4. Test Unlock with bad password
	ok, _, err := vm.Unlock("wrongpassword")
	if ok || err == nil {
		t.Errorf("Expected Unlock to fail with wrong password, got ok=%v, err=%v", ok, err)
	}

	// 5. Test Unlock with correct password
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

	// 6. Test Document Vault Protection Toggle
	res, err := db.Exec("INSERT INTO documents (title, original_filename, checksum, mime_type) VALUES ('Passport', 'pass.pdf', 'chk1', 'application/pdf')")
	if err != nil {
		t.Fatalf("Failed to insert document: %v", err)
	}
	docID64, _ := res.LastInsertId()
	docID := int(docID64)

	isVault, err := vm.ToggleDocument(docID)
	if err != nil {
		t.Fatalf("ToggleDocument failed: %v", err)
	}
	if !isVault {
		t.Errorf("Expected document to be marked as vault-protected, got false")
	}

	// Verify status count
	_, _, _, vaultCount, err := vm.Status()
	if err != nil {
		t.Fatalf("Status query failed: %v", err)
	}
	if vaultCount != 1 {
		t.Errorf("Expected 1 vault document, got %d", vaultCount)
	}

	// Toggle back
	isVault2, err := vm.ToggleDocument(docID)
	if err != nil {
		t.Fatalf("ToggleDocument back failed: %v", err)
	}
	if isVault2 {
		t.Errorf("Expected document to be unmarked from vault, got true")
	}
}
