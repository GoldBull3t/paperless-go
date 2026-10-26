package crypto

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// CalculateSHA256 returns the hex-encoded SHA-256 hash of a file
func CalculateSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyFileIntegrity checks if the file on disk matches its expected SHA-256 hash.
// If secretKey is provided and the file is encrypted at rest, it verifies the plaintext integrity.
func VerifyFileIntegrity(filePath, expectedHash, secretKey string) (bool, error) {
	stream, err := OpenDecryptedStream(filePath, secretKey)
	if err != nil {
		return false, fmt.Errorf("failed to open stream for verification: %w", err)
	}
	defer stream.Close()

	h := sha256.New()
	if _, err := io.Copy(h, stream); err != nil {
		return false, fmt.Errorf("failed to read stream for hash: %w", err)
	}

	actualHash := hex.EncodeToString(h.Sum(nil))
	return actualHash == expectedHash, nil
}

