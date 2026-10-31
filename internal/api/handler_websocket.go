package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"paperless-go/internal/models"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for SPA and local networks
	},
}

type WebSocketHub struct {
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
	quit    chan struct{}
}

func NewWebSocketHub() *WebSocketHub {
	hub := &WebSocketHub{
		clients: make(map[*websocket.Conn]bool),
		quit:    make(chan struct{}),
	}
	go hub.heartbeatLoop()
	return hub
}

func (h *WebSocketHub) heartbeatLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	heartbeatMsg, _ := json.Marshal(map[string]interface{}{
		"type": "heartbeat",
		"data": nil,
	})

	for {
		select {
		case <-ticker.C:
			h.broadcastBytes(heartbeatMsg)
		case <-h.quit:
			return
		}
	}
}

func (h *WebSocketHub) Close() {
	close(h.quit)
}

func (h *WebSocketHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	h.mu.Lock()
	h.clients[conn] = true
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
	}()

	// Read loop to keep alive until client disconnects
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (h *WebSocketHub) broadcastBytes(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for conn := range h.clients {
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}
}

func (h *WebSocketHub) BroadcastTaskUpdate(task models.Task) {
	// Status in Paperless-ngx enum: STARTED, WORKING, SUCCESS, FAILED
	status := "WORKING"
	switch task.Status {
	case "STARTED":
		status = "STARTED"
	case "SUCCESS":
		status = "SUCCESS"
	case "FAILURE":
		status = "FAILED"
	}

	var docID *int
	if len(task.RelatedDocumentIDs) > 0 {
		docID = &task.RelatedDocumentIDs[0]
	} else if task.RelatedDocument != nil {
		if id, err := strconv.Atoi(*task.RelatedDocument); err == nil {
			docID = &id
		}
	}

	msgText := ""
	if task.Result != nil {
		msgText = *task.Result
	}

	// 1. Angular status_update envelope
	progressUpdate := map[string]interface{}{
		"type": "status_update",
		"data": map[string]interface{}{
			"task_id":          task.TaskID,
			"filename":         task.TaskFileName,
			"current_progress": 100,
			"max_progress":     100,
			"message":          msgText,
			"document_id":      docID,
			"status":           status,
			"owner_id":         nil,
		},
	}
	if data, err := json.Marshal(progressUpdate); err == nil {
		h.broadcastBytes(data)
	}

	// 2. If completed, notify document updated
	if status == "SUCCESS" && docID != nil {
		docUpdate := map[string]interface{}{
			"type": "document_updated",
			"data": map[string]interface{}{
				"id": *docID,
			},
		}
		if data, err := json.Marshal(docUpdate); err == nil {
			h.broadcastBytes(data)
		}
	}
}

func (h *WebSocketHub) BroadcastDocumentsDeleted() {
	delMsg := map[string]interface{}{
		"type": "documents_deleted",
		"data": nil,
	}
	if data, err := json.Marshal(delMsg); err == nil {
		h.broadcastBytes(data)
	}
}
