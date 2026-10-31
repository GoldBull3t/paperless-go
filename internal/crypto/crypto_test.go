package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicOperations(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.txt")
	dst := filepath.Join(tmpDir, "target.txt")

	testData := []byte("Strict data integrity test string 1234567890")
	if err := os.WriteFile(src, testData, 0644); err != nil {
		t.Fatalf("Failed to create source file: %v", err)
	}

	if err := AtomicCopy(src, dst); err != nil {
		t.Fatalf("AtomicCopy failed: %v", err)
	}

	readBack, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("Failed to read dst: %v", err)
	}
	if !bytes.Equal(testData, readBack) {
		t.Fatalf("Data mismatch: expected %q, got %q", string(testData), string(readBack))
	}
}

func TestEncryptionAtRest(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "secret_invoice.pdf")
	enc := filepath.Join(tmpDir, "secret_invoice.pdf.enc")
	dec := filepath.Join(tmpDir, "secret_invoice.pdf.dec")

	secretKey := "super-secure-homelab-key-999"
	secretData := []byte("CONFIDENTIAL: Bank Account IBAN PT5000000000000000000000 Balance 99999 EUR")

	if err := os.WriteFile(src, secretData, 0600); err != nil {
		t.Fatalf("Failed to write secret file: %v", err)
	}

	// 1. Encrypt
	if err := EncryptFile(src, enc, secretKey); err != nil {
		t.Fatalf("Encryption failed: %v", err)
	}

	// Verify file is marked as encrypted
	isEnc, err := IsEncryptedFile(enc)
	if err != nil || !isEnc {
		t.Fatalf("Expected file to be recognized as encrypted, got isEnc=%v, err=%v", isEnc, err)
	}

	// Verify ciphertext does NOT contain plaintext
	encBytes, _ := os.ReadFile(enc)
	if bytes.Contains(encBytes, []byte("CONFIDENTIAL")) {
		t.Fatal("Ciphertext contains plaintext string!")
	}

	// 2. Decrypt
	if err := DecryptFile(enc, dec, secretKey); err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}

	decBytes, _ := os.ReadFile(dec)
	if !bytes.Equal(decBytes, secretData) {
		t.Fatalf("Decrypted content mismatch: expected %q, got %q", string(secretData), string(decBytes))
	}

	// 3. OpenDecryptedStream
	stream, err := OpenDecryptedStream(enc, secretKey)
	if err != nil {
		t.Fatalf("OpenDecryptedStream failed: %v", err)
	}
	defer stream.Close()

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(stream)
	if !bytes.Equal(buf.Bytes(), secretData) {
		t.Fatalf("Stream decryption mismatch")
	}

	// 4. Verify integrity
	hash, err := CalculateSHA256(src)
	if err != nil {
		t.Fatalf("CalculateSHA256 failed: %v", err)
	}
	valid, err := VerifyFileIntegrity(enc, hash, secretKey)
	if err != nil || !valid {
		t.Fatalf("VerifyFileIntegrity failed: valid=%v, err=%v", valid, err)
	}
}

func TestSecureShredding(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "tax_return.pdf")

	data := []byte("Sensitive Tax Return Data with SSN 123-45-6789")
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := ShredFile(target); err != nil {
		t.Fatalf("ShredFile failed: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("Expected file to be deleted after shredding, got err=%v", err)
	}
}

