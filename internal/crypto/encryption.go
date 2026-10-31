package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	MagicHeader       = []byte("PPLX:ENC:1\n")
	ErrInvalidHeader  = errors.New("file is not encrypted or header is invalid")
	ErrDecryptionFail = errors.New("decryption failed: incorrect key or corrupted file")
)

// DeriveKey derives a consistent 32-byte AES-256 key from any secret string using SHA-256
func DeriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// IsEncryptedFile checks if the file begins with the Paperless encryption magic header
func IsEncryptedFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	header := make([]byte, len(MagicHeader))
	n, err := io.ReadFull(f, header)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return false, err
	}
	if n < len(MagicHeader) {
		return false, nil
	}
	return bytes.Equal(header, MagicHeader), nil
}

// EncryptFile encrypts src to dst using AES-256-GCM atomically
func EncryptFile(src, dst, secret string) error {
	inData, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file for encryption: %w", err)
	}

	key := DeriveKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	// Encrypt with nonce as prefix
	ciphertext := gcm.Seal(nil, nonce, inData, nil)

	var buf bytes.Buffer
	buf.Write(MagicHeader)
	buf.Write(nonce)
	buf.Write(ciphertext)

	return AtomicWrite(dst, &buf)
}

// DecryptFile decrypts src to dst using AES-256-GCM atomically
func DecryptFile(src, dst, secret string) error {
	inData, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	headerLen := len(MagicHeader)
	if len(inData) < headerLen || !bytes.Equal(inData[:headerLen], MagicHeader) {
		return ErrInvalidHeader
	}

	key := DeriveKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	nonceSize := gcm.NonceSize()
	if len(inData) < headerLen+nonceSize {
		return ErrInvalidHeader
	}

	nonce := inData[headerLen : headerLen+nonceSize]
	ciphertext := inData[headerLen+nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return ErrDecryptionFail
	}

	return AtomicWrite(dst, bytes.NewReader(plaintext))
}

// OpenDecryptedStream returns an io.ReadCloser for the file.
// If the file is encrypted, it transparently decrypts it in memory.
// If not encrypted, it returns a normal os.File reader.
func OpenDecryptedStream(path, secret string) (io.ReadCloser, error) {
	encrypted, err := IsEncryptedFile(path)
	if err != nil {
		return nil, err
	}

	if !encrypted {
		return os.Open(path)
	}

	inData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	key := DeriveKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	headerLen := len(MagicHeader)
	nonceSize := gcm.NonceSize()
	if len(inData) < headerLen+nonceSize {
		return nil, ErrInvalidHeader
	}

	nonce := inData[headerLen : headerLen+nonceSize]
	ciphertext := inData[headerLen+nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFail
	}

	return io.NopCloser(bytes.NewReader(plaintext)), nil
}

