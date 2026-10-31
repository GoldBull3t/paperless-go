package crypto

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// AtomicCopy copies a file from src to dst using a temporary staging file and fsync,
// ensuring that dst is only created upon complete, non-corrupted write.
func AtomicCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer in.Close()

	return AtomicWrite(dst, in)
}

// AtomicWrite writes data from reader to dst atomically.
// It writes to a sibling ".tmp" file first, flushes dirty pages with fsync (Sync()),
// closes the handle, and atomically renames the file to dst.
func AtomicWrite(dst string, r io.Reader) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, filepath.Base(dst)+".atomic-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temporary staging file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup in case of error
	cleanTmp := true
	defer func() {
		if cleanTmp {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	// Write data
	if _, err := io.Copy(tmpFile, r); err != nil {
		return fmt.Errorf("failed to write staging data: %w", err)
	}

	// CRITICAL: Force operating system to flush dirty buffers to physical disk
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to fsync staging file to disk: %w", err)
	}

	// Close before renaming (especially required on Windows)
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close staging file: %w", err)
	}

	// Atomic replace
	// On Windows, os.Rename fails if dst exists, so we remove dst first if present
	_ = os.Remove(dst)
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("failed to atomically rename staging file to destination: %w", err)
	}

	cleanTmp = false
	return nil
}

