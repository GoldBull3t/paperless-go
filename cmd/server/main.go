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
	"paperless-go/internal/exporter"
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

	// Check for CLI subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "export":
			targetDir := "./export"
			if len(os.Args) > 2 {
				targetDir = os.Args[2]
			}
			log.Printf("Exporting documents to %s...", targetDir)
			if err := exporter.Export(db, cfg, targetDir); err != nil {
				log.Fatalf("Export failed: %v", err)
			}
			log.Println("Export completed successfully!")
			return

		case "import":
			if len(os.Args) < 3 {
				log.Fatal("Usage: paperless-go import <source_directory>")
			}
			sourceDir := os.Args[2]
			log.Printf("Importing documents from %s...", sourceDir)
			if err := exporter.Import(db, cfg, sourceDir); err != nil {
				log.Fatalf("Import failed: %v", err)
			}
			log.Println("Import completed successfully!")
			return

		case "check", "sanity_check":
			log.Println("Running sanity check...")
			issues, err := exporter.SanityCheck(db, cfg)
			if err != nil {
				log.Fatalf("Sanity check failed: %v", err)
			}
			if len(issues) == 0 {
				log.Println("All sanity checks passed! Zero integrity issues detected.")
			} else {
				log.Printf("Found %d issue(s):", len(issues))
				for _, issue := range issues {
					log.Printf(" - %s", issue)
				}
			}
			return

		case "rename", "document_renamer":
			log.Println("Running document renamer (reorganizing files according to storage paths)...")
			count, err := consumer.RenameAllDocuments(db, cfg)
			if err != nil {
				log.Fatalf("Renamer failed: %v", err)
			}
			log.Printf("Renamer completed successfully! %d file(s) reorganized.", count)
			return
		}
	}

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
