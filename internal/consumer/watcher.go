package consumer

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"paperless-go/internal/queue"
)

type Watcher struct {
	consumeDir string
	consumer   *Consumer
	queue      *queue.Queue
	quit       chan struct{}
}

func NewWatcher(consumeDir string, c *Consumer, q *queue.Queue) *Watcher {
	return &Watcher{
		consumeDir: consumeDir,
		consumer:   c,
		queue:      q,
		quit:       make(chan struct{}),
	}
}

func (w *Watcher) Start() {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				w.scanConsumeDir()
			case <-w.quit:
				ticker.Stop()
				return
			}
		}
	}()
	log.Printf("[Watcher] Monitoring consume folder: %s", w.consumeDir)
}

func (w *Watcher) Stop() {
	close(w.quit)
}

func (w *Watcher) scanConsumeDir() {
	entries, err := os.ReadDir(w.consumeDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || stringsHasPrefix(entry.Name(), ".") {
			continue
		}

		filePath := filepath.Join(w.consumeDir, entry.Name())
		info1, err := entry.Info()
		if err != nil || info1.Size() == 0 {
			continue
		}

		// Ensure file has finished being written by checking size and mod time stability
		initialSize := info1.Size()
		initialMod := info1.ModTime()
		time.Sleep(500 * time.Millisecond)
		info2, err := os.Stat(filePath)
		if err != nil || info2.Size() != initialSize || !info2.ModTime().Equal(initialMod) {
			// File is still being actively written, will pick it up on next tick
			continue
		}

		fileName := entry.Name()
		// Create a copy in temp folder for consumer to process safely
		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("paperless-consume-%d-%s", time.Now().UnixNano(), fileName))
		if err := copyFile(filePath, tmpPath); err != nil {
			continue
		}

		// Remove from consume folder now that safe temporary copy is secured
		_ = os.Remove(filePath)
		log.Printf("[Watcher] Queued drop-in document: %s", fileName)

		w.queue.Enqueue("Consume document", fileName, tmpPath, func(tid string) (int, error) {
			defer os.Remove(tmpPath)
			docID, err := w.consumer.ConsumeFile(tmpPath, fileName, nil)
			if err != nil {
				var dupErr *ErrDuplicateDocument
				if errors.As(err, &dupErr) {
					// Duplicate detected: move to .duplicates/ or remove if configured
					if os.Getenv("PAPERLESS_CONSUMER_DELETE_DUPLICATES") != "true" {
						dupDir := filepath.Join(w.consumeDir, ".duplicates")
						_ = os.MkdirAll(dupDir, 0755)
						_ = copyFile(tmpPath, filepath.Join(dupDir, fileName))
					}
					return docID, err
				}

				// If other failure, preserve failed file in consume/.failed/ so user doesn't lose original
				failedDir := filepath.Join(w.consumeDir, ".failed")
				_ = os.MkdirAll(failedDir, 0755)
				_ = copyFile(tmpPath, filepath.Join(failedDir, fileName))
				return 0, err
			}
			return docID, nil
		})
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
}
