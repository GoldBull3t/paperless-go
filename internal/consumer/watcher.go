package consumer

import (
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
		// Ensure file has stabilized (not actively being copied)
		info1, err := entry.Info()
		if err != nil {
			continue
		}
		time.Sleep(500 * time.Millisecond)
		info2, err := os.Stat(filePath)
		if err != nil || info1.Size() != info2.Size() {
			continue // still being written
		}

		fileName := entry.Name()
		log.Printf("[Watcher] Ingesting drop-in document: %s", fileName)

		// Create a copy in temp folder for consumer to process safely
		tmpPath := filepath.Join(os.TempDir(), "paperless-consume-"+fileName)
		if err := copyFile(filePath, tmpPath); err != nil {
			continue
		}
		_ = os.Remove(filePath) // Remove from consume folder once captured

		w.queue.Enqueue("Consume document", fileName, tmpPath, func(tid string) (int, error) {
			defer os.Remove(tmpPath)
			return w.consumer.ConsumeFile(tmpPath, fileName, nil)
		})
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
}
