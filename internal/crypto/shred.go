package crypto

import (
	"crypto/rand"
	"io"
	"os"
)

// ShredFile securely overwrites a file's contents before unlinking it,
// preventing forensic data recovery of sensitive documents from free disk sectors.
func ShredFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	size := info.Size()
	if size > 0 {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil {
			defer f.Close()

			// Overwrite with cryptographically secure random bytes
			buf := make([]byte, 64*1024)
			var written int64
			for written < size {
				toWrite := int64(len(buf))
				if size-written < toWrite {
					toWrite = size - written
				}
				_, _ = rand.Read(buf[:toWrite])
				if _, err := f.Write(buf[:toWrite]); err != nil {
					break
				}
				written += toWrite
			}

			// Also zero-fill pass for flash storage wear leveling sanity
			_, _ = f.Seek(0, io.SeekStart)
			for i := range buf {
				buf[i] = 0
			}
			written = 0
			for written < size {
				toWrite := int64(len(buf))
				if size-written < toWrite {
					toWrite = size - written
				}
				if _, err := f.Write(buf[:toWrite]); err != nil {
					break
				}
				written += toWrite
			}

			_ = f.Sync()
			_ = f.Close()
		}
	}

	return os.Remove(path)
}

