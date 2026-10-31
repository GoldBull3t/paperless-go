package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
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
	"paperless-go/internal/logger"
	"paperless-go/internal/queue"
)

func main() {
	// 1. Load configuration & initialize structured logging
	cfg := config.Load()
	logger.Setup(cfg.LogLevel, cfg.LogFormat)

	slog.Info("⚡ Starting paperless-go: Lightweight Paperless-ngx Engine",
		"data_dir", cfg.DataDir,
		"media_dir", cfg.MediaDir,
		"log_level", cfg.LogLevel,
		"log_format", cfg.LogFormat,
	)

	// Context for graceful shutdown of background services & maintenance routines
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	// 2. Open database & apply migrations
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		slog.Error("Database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Check for CLI subcommands
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "doctor", "--doctor", "-d":
			if !RunDoctor(db, cfg) {
				os.Exit(1)
			}
			return

		case "migrate-ngx", "migrate":
			sourceDir := "./export"
			if len(os.Args) > 2 {
				sourceDir = os.Args[2]
			}
			log.Printf("🚀 Starting 1-Click Migration from Paperless-ngx (%s)...", sourceDir)
			if err := exporter.Import(db, cfg, sourceDir); err != nil {
				log.Fatalf("Migration failed: %v", err)
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&count); err != nil {
				log.Printf("[WARN] Failed counting imported documents: %v", err)
			}
			log.Printf("✨ Migration completed successfully! %d document(s) imported and indexed in FTS5.", count)
			return

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
		slog.Error("Failed to initialize default admin", "error", err)
		os.Exit(1)
	}

	// 4. Start Continuous SQLite WAL & Query Planner Maintenance
	db.StartMaintenance(rootCtx, 1*time.Hour)

	// 5. Initialize In-Memory Queue & WebSocket Hub
	taskQueue := queue.NewQueue(db, cfg.OCRWorkerCount)
	defer taskQueue.Close()

	wsHub := api.NewWebSocketHub()
	defer wsHub.Close()
	taskQueue.SetBroadcaster(wsHub)

	// 6. Initialize Document Consumer & Watcher
	docConsumer := consumer.NewConsumer(db, cfg)
	watcher := consumer.NewWatcher(cfg.ConsumeDir, docConsumer, taskQueue)
	watcher.Start()
	defer watcher.Stop()

	// 7. Initialize HTTP Router
	router := api.NewRouter(cfg, db, authSvc, taskQueue, docConsumer, wsHub)

	// 8. Start HTTP Server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("🚀 paperless-go server listening", "addr", fmt.Sprintf("http://%s", addr), "ui", fmt.Sprintf("http://localhost:%d/", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server listen failed", "error", err)
			os.Exit(1)
		}
	}()

	// 9. Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down paperless-go gracefully...")
	rootCancel() // Terminate background maintenance and workers

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server shutdown error", "error", err)
	}
	slog.Info("paperless-go stopped successfully.")
}
