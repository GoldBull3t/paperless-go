package api

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type LogRingBuffer struct {
	mu       sync.RWMutex
	maxLines int
	logs     map[string][]string
}

var GlobalLogBuffer = NewLogRingBuffer(1000)

func NewLogRingBuffer(maxLines int) *LogRingBuffer {
	buf := &LogRingBuffer{
		maxLines: maxLines,
		logs: map[string][]string{
			"paperless": {
				fmt.Sprintf("[%s] [System] Paperless-go engine initialized", time.Now().Format("2006-01-02 15:04:05")),
				fmt.Sprintf("[%s] [System] Database connection active", time.Now().Format("2006-01-02 15:04:05")),
				fmt.Sprintf("[%s] [Consumer] Document ingestion pipeline ready", time.Now().Format("2006-01-02 15:04:05")),
			},
			"mail": {
				fmt.Sprintf("[%s] [Mail] Mail fetcher service initialized", time.Now().Format("2006-01-02 15:04:05")),
			},
		},
	}
	return buf
}

func (b *LogRingBuffer) Add(channel, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	clean := strings.TrimSpace(line)
	if clean == "" {
		return
	}
	formatted := clean
	if !strings.HasPrefix(clean, "[") {
		formatted = fmt.Sprintf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), clean)
	}

	lines := b.logs[channel]
	lines = append(lines, formatted)
	if len(lines) > b.maxLines {
		lines = lines[len(lines)-b.maxLines:]
	}
	b.logs[channel] = lines
}

func (b *LogRingBuffer) Write(p []byte) (n int, err error) {
	line := string(p)
	b.Add("paperless", line)
	return len(p), nil
}

func (b *LogRingBuffer) Get(channel string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	lines, ok := b.logs[channel]
	if !ok || len(lines) == 0 {
		return []string{
			fmt.Sprintf("[%s] No log entries recorded for %s", time.Now().Format("2006-01-02 15:04:05"), channel),
		}
	}
	res := make([]string, len(lines))
	copy(res, lines)
	return res
}

func (b *LogRingBuffer) Channels() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	channels := make([]string, 0, len(b.logs))
	for k := range b.logs {
		channels = append(channels, k)
	}
	return channels
}

func init() {
	log.SetOutput(io.MultiWriter(os.Stderr, GlobalLogBuffer))
}

