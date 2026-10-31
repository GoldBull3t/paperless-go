package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

		case "createsuperuser":
			authSvc := auth.NewService(db, cfg)
			user := "admin"
			pass := ""
			email := "admin@paperless.local"
			if len(os.Args) > 2 {
				user = os.Args[2]
			}
			if len(os.Args) > 3 {
				pass = os.Args[3]
			}
			if len(os.Args) > 4 {
				email = os.Args[4]
			}
			if pass == "" {
				pass = auth.GenerateRandomPassword(16)
			}
			if err := authSvc.CreateSuperuser(user, pass, email); err != nil {
				log.Fatalf("Failed creating superuser: %v", err)
			}
			fmt.Printf("✨ Superuser created successfully! Username: %s | Password: %s | Email: %s\n", user, pass, email)
			return

		case "migrate-ngx", "migrate":
			dryRun := false
			sourceDir := "./export"
			for _, arg := range os.Args[2:] {
				if arg == "--dry-run" || arg == "-n" {
					dryRun = true
				} else if !strings.HasPrefix(arg, "-") {
					sourceDir = arg
				}
			}
			if dryRun {
				log.Printf("🔍 Running SIMULATION (--dry-run) of Paperless-ngx migration from %s (NO files or DB records will be changed)...", sourceDir)
			} else {
				log.Printf("🚀 Starting 1-Click Migration from Paperless-ngx (%s)...", sourceDir)
			}
			report, err := exporter.ImportWithDryRun(db, cfg, sourceDir, dryRun)
			if err != nil {
				log.Fatalf("Migration failed: %v", err)
			}
			fmt.Println("--------------------------------------------------")
			fmt.Printf("📦 Total Manifest Items:    %d\n", report.TotalItems)
			fmt.Printf("📄 Documents Detected:      %d\n", report.DocumentsCount)
			fmt.Printf("🏷️  Tags Detected:           %d\n", report.TagsCount)
			fmt.Printf("🏢 Correspondents Detected: %d\n", report.CorrespondentsCount)
			fmt.Printf("📁 Document Types Detected: %d\n", report.DocTypesCount)
			fmt.Printf("💾 Storage Paths Detected:  %d\n", report.StoragePathsCount)
			if len(report.MissingFiles) > 0 {
				fmt.Printf("⚠️  Missing Files (%d):\n", len(report.MissingFiles))
				for _, mf := range report.MissingFiles {
					fmt.Printf("   - %s\n", mf)
				}
			}
			if len(report.ChecksumErrors) > 0 {
				fmt.Printf("❌ Checksum Errors (%d):\n", len(report.ChecksumErrors))
				for _, ce := range report.ChecksumErrors {
					fmt.Printf("   - %s\n", ce)
				}
			}
			fmt.Println("--------------------------------------------------")
			if dryRun {
				fmt.Println("✨ Dry-run simulation finished! Zero changes were made to your database.")
			} else {
				fmt.Println("✨ Migration completed successfully! All documents and tags are now active.")
			}
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

	// 3. Initialize Auth Service & admin account
	authSvc := auth.NewService(db, cfg)
	adminUser := cfg.AdminUser
	if adminUser == "" {
		adminUser = "admin"
	}
	created, genPass, err := authSvc.EnsureAdminUser(adminUser, cfg.AdminPassword, cfg.AdminEmail)
	if err != nil {
		slog.Error("Failed to initialize admin account", "error", err)
		os.Exit(1)
	}
	if created && genPass != "" {
		fmt.Println("=======================================================================")
		fmt.Println("🔐 SECURITY NOTICE: Generated initial administrator credentials:")
		fmt.Printf("   Username: %s\n", adminUser)
		fmt.Printf("   Password: %s\n", genPass)
		fmt.Println("   Please store this password securely or set PAPERLESS_ADMIN_PASSWORD.")
		fmt.Println("=======================================================================")
	}

	// 4. Start Continuous SQLite WAL & Query Planner Maintenance
	db.StartMaintenance(rootCtx, 1*time.Hour)

	// 5. Initialize In-Memory Queue & Document Consumer
	taskQueue := queue.NewQueue(db, cfg.OCRWorkerCount)
	defer taskQueue.Close()

	docConsumer := consumer.NewConsumer(db, cfg)

	// Register persistent task handlers
	consumeHandler := func(ctx context.Context, taskID string, payloadJSON string) (int, error) {
		var p struct {
			FilePath   string `json:"file_path"`
			FileName   string `json:"file_name"`
			ConsumeDir string `json:"consume_dir"`
		}
		if err := json.Unmarshal([]byte(payloadJSON), &p); err != nil {
			return 0, fmt.Errorf("failed unmarshaling consume payload: %w", err)
		}
		if p.FilePath != "" {
			defer os.Remove(p.FilePath)
		}
		return docConsumer.ConsumeFileWithFallback(p.FilePath, p.FileName, p.ConsumeDir)
	}
	taskQueue.RegisterHandler("consume_document", consumeHandler)
	taskQueue.RegisterHandler("file_handling", consumeHandler)

	// Recover any pending or interrupted tasks from database
	recoveredTasks := taskQueue.RecoverInterruptedTasks()
	if recoveredTasks > 0 {
		slog.Info("Recovered interrupted tasks from previous run", "count", recoveredTasks)
	}

	wsHub := api.NewWebSocketHub()
	defer wsHub.Close()
	taskQueue.SetBroadcaster(wsHub)

	// 6. Initialize Watcher
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
