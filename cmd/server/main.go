package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paperless-go/internal/api"
	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

func main() {
	log.Println("==================================================")
	log.Println("⚡ paperless-go: Lightweight Paperless-ngx Engine")
	log.Println("==================================================")

	// 1. Load configuration
	cfg := config.Load()
	log.Printf("Loaded configuration. DataDir: %s, MediaDir: %s", cfg.DataDir, cfg.MediaDir)

	// 2. Open database & apply migrations
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer db.Close()

	// 3. Initialize Auth Service & default admin
	authSvc := auth.NewService(db, cfg)
	if err := authSvc.EnsureDefaultAdmin("admin", "admin"); err != nil {
		log.Fatalf("Failed to initialize default admin: %v", err)
	}

	// 4. Initialize In-Memory Queue & WebSocket Hub
	taskQueue := queue.NewQueue(db, cfg.OCRWorkerCount)
	defer taskQueue.Close()

	wsHub := api.NewWebSocketHub()
	defer wsHub.Close()
	taskQueue.SetBroadcaster(wsHub)

	// 5. Initialize Document Consumer & Watcher
	docConsumer := consumer.NewConsumer(db, cfg)
	watcher := consumer.NewWatcher(cfg.ConsumeDir, docConsumer, taskQueue)
	watcher.Start()
	defer watcher.Stop()

	// 6. Initialize HTTP Router
	router := api.NewRouter(cfg, db, authSvc, taskQueue, docConsumer, wsHub)

	// 7. Start HTTP Server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("🚀 paperless-go server listening on http://%s", addr)
		log.Printf("📱 Web UI available at http://localhost:%d/", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listen failed: %v", err)
		}
	}()

	// 8. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down paperless-go gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("paperless-go stopped successfully.")
}
