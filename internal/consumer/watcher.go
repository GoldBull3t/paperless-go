package consumer

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"paperless-go/internal/queue"
)

type Watcher struct {
	consumeDir string
	consumer   *Consumer
	queue      *queue.Queue
	quit       chan struct{}
	wg         sync.WaitGroup
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
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				w.scanConsumeDir()
			case <-w.quit:
				return
			}
		}
	}()
	log.Printf("[Watcher] Monitoring consume folder: %s", w.consumeDir)
}

func (w *Watcher) Stop() {
	select {
	case <-w.quit:
		// already closed
	default:
		close(w.quit)
	}
	w.wg.Wait()
}

func (w *Watcher) scanConsumeDir() {
	entries, err := os.ReadDir(w.consumeDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[WARN] Watcher unable to read consume directory %s: %v", w.consumeDir, err)
		}
		return
	}

	for _, entry := range entries {
		select {
		case <-w.quit:
			return
		default:
		}

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

		select {
		case <-time.After(500 * time.Millisecond):
		case <-w.quit:
			return
		}

		info2, err := os.Stat(filePath)
		if err != nil || info2.Size() != initialSize || !info2.ModTime().Equal(initialMod) {
			// File is still being actively written, will pick it up on next tick
			continue
		}

		fileName := entry.Name()
		// Create a copy in temp folder for consumer to process safely
		tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("paperless-consume-%d-%s", time.Now().UnixNano(), fileName))
		if err := copyFile(filePath, tmpPath); err != nil {
			log.Printf("[WARN] Watcher failed copying consume file %s: %v", fileName, err)
			continue
		}

		// Remove from consume folder now that safe temporary copy is secured
		if err := os.Remove(filePath); err != nil {
			log.Printf("[WARN] Watcher failed removing original consumed file %s: %v", fileName, err)
		}
		log.Printf("[Watcher] Queued drop-in document: %s", fileName)

		payload := map[string]string{
			"file_path":   tmpPath,
			"file_name":   fileName,
			"consume_dir": w.consumeDir,
		}
		if _, err := w.queue.EnqueuePersistent("consume_document", "Consume document", fileName, tmpPath, payload); err != nil {
			log.Printf("[WARN] Watcher failed enqueuing persistent consume task for %s: %v", fileName, err)
			// Fallback to in-memory execution if persistent enqueue failed
			w.queue.Enqueue("Consume document", fileName, tmpPath, func(tid string) (int, error) {
				defer os.Remove(tmpPath)
				return w.consumer.ConsumeFileWithFallback(tmpPath, fileName, w.consumeDir)
			})
		}
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[0:len(prefix)] == prefix
}
