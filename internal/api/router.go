package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/oidc"
	"paperless-go/internal/queue"
	"paperless-go/internal/vault"
)

func NewRouter(
	cfg *config.Config,
	db *database.DB,
	authSvc *auth.Service,
	q *queue.Queue,
	consumer *consumer.Consumer,
	wsHub *WebSocketHub,
) *chi.Mux {
	r := chi.NewRouter()

	// Standard middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(SecurityHeadersMiddleware)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			IncrementRequestCount()
			next.ServeHTTP(w, r)
		})
	})

	// Secure CORS configuration
	var allowedOrigins []string
	if cfg.CorsAllowedHosts != "" {
		for _, host := range strings.Split(cfg.CorsAllowedHosts, ",") {
			if h := strings.TrimSpace(host); h != "" {
				allowedOrigins = append(allowedOrigins, h)
			}
		}
	}

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: allowedOrigins,
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			if len(allowedOrigins) == 0 {
				// By default, allow same-origin and local network clients
				if origin == "" || strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "https://localhost") {
					return true
				}
				return false
			}
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					return true
				}
			}
			return false
		},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRFToken"},
		ExposedHeaders:   []string{"Link", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Use(AuthMiddleware(authSvc))
	r.Use(CSRFMiddleware)

	// Initialize OIDC Client
	oidcClient := oidc.NewClient(oidc.Config{
		Enabled:      cfg.OIDCEnabled,
		ProviderName: cfg.OIDCProviderName,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		DiscoveryURL: cfg.OIDCDiscoveryURL,
		AuthURL:      cfg.OIDCAuthURL,
		TokenURL:     cfg.OIDCTokenURL,
		UserInfoURL:  cfg.OIDCUserInfoURL,
		RedirectURI:  cfg.OIDCRedirectURI,
	})

	// Initialize handlers
	authH := NewAuthHandler(db, authSvc, oidcClient)
	uiSettingsH := NewUISettingsHandler(db)
	configH := NewConfigHandler(db)
	docsH := NewDocumentsHandler(db, cfg, consumer, q, wsHub)
	tagsH := NewTagsHandler(db)
	corrsH := NewCorrespondentsHandler(db)
	docTypesH := NewDocumentTypesHandler(db)
	storPathsH := NewStoragePathsHandler(db)
	customFieldsH := NewCustomFieldsHandler(db)
	savedViewsH := NewSavedViewsHandler(db)
	tasksH := NewTasksHandler(db)
	statsH := NewStatisticsHandler(db)
	usersH := NewUsersHandler(db)
	workflowsH := NewWorkflowsHandler(db)
	searchH := NewSearchHandler(db, consumer)
	bulkH := NewBulkObjectsHandler(db)
	scanH := NewScanHandler(cfg, consumer, q)
	shareLinksH := NewShareLinksHandler(db, cfg)
	schemaH := NewSchemaHandler()
	mailH := NewMailHandler(db)
	systemH := NewSystemHandler(db, cfg)
	vaultMgr := vault.NewVaultManager(db)
	docsH.SetVaultManager(vaultMgr)
	vaultH := NewVaultHandler(vaultMgr)

	// API subrouter
	apiRouter := func(r chi.Router) {
		// Public auth endpoint
		r.Post("/token/", authH.GetToken)
		r.Get("/auth/oidc/login", authH.OIDCLogin)
		r.Get("/auth/oidc/callback", authH.OIDCCallback)
		r.Get("/auth/headless/*", authH.Headless)

		// Authenticated API endpoints
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth)

			r.Get("/profile/", authH.GetProfile)
			r.Patch("/profile/", authH.UpdateProfile)
			r.Post("/profile/generate_auth_token/", authH.GenerateAuthToken)
			r.Post("/profile/disconnect_social_account/", authH.DisconnectSocialAccount)
			r.Get("/profile/social_account_providers/", authH.GetSocialAccountProviders)
			r.Get("/profile/totp/", authH.GetTotpSettings)
			r.Post("/profile/totp/", authH.ActivateTotp)
			r.Delete("/profile/totp/", authH.DeactivateTotp)

			r.Get("/ui_settings/", uiSettingsH.Get)
			r.Post("/ui_settings/", uiSettingsH.Post)

			// Config
			r.Route("/config", func(r chi.Router) {
				r.Get("/", configH.Get)
				r.Patch("/", configH.Patch)
				r.Patch("/{id}/", configH.Patch)
			})
			r.Get("/remote_version/", configH.GetRemoteVersion)
			r.Get("/statistics/", statsH.Get)
			r.Get("/status/", statsH.GetStatus)

			// Search
			r.Get("/search/", searchH.Search)
			r.Get("/search/autocomplete/", searchH.Autocomplete)

			// Bulk edit generic objects (tags, correspondents, document types, etc.)
			r.Post("/bulk_edit_objects/", bulkH.BulkEditObjects)

			// Documents
			r.Route("/documents", func(r chi.Router) {
				r.Get("/", docsH.List)
				r.Post("/post_document/", docsH.PostDocument)
				r.Post("/bulk_edit/", docsH.BulkEdit)
				r.Post("/selection_data/", docsH.SelectionData)
				r.Get("/next_asn/", docsH.NextASN)
				r.Get("/check_duplicate/", docsH.CheckDuplicate)
				r.Post("/delete/", docsH.DeleteDocuments)
				r.Post("/reprocess/", docsH.ReprocessDocuments)
				r.Post("/bulk_download/", docsH.BulkDownload)
				r.Post("/rotate/", docsH.Rotate)
				r.Post("/merge/", docsH.Merge)
				r.Post("/merge_as_versions/", docsH.MergeAsVersions)
				r.Post("/edit_pdf/", docsH.EditPDF)
				r.Post("/remove_password/", docsH.RemovePassword)
				r.Post("/email/", docsH.EmailDocuments)
				r.Post("/email_document/", docsH.EmailDocuments)
				r.Post("/email_documents/", docsH.EmailDocuments)
				r.Post("/chat/", docsH.Chat)
				r.Post("/{id}/extract_metadata/", docsH.ExtractMetadata)
				r.Get("/{id}/summary/", docsH.Summary)
				r.Get("/{id}/", docsH.Get)
				r.Put("/{id}/", docsH.Update)
				r.Patch("/{id}/", docsH.Update)
				r.Delete("/{id}/", docsH.Delete)
				r.Get("/{id}/preview/", docsH.Preview)
				r.Get("/{id}/download/", docsH.Download)
				r.Get("/{id}/thumb/", docsH.Thumb)
				r.Get("/{id}/metadata/", docsH.Metadata)
				r.Get("/{id}/duplicates/", docsH.GetDuplicates)
				r.Get("/{id}/suggestions/", docsH.Suggestions)
				r.Get("/{id}/ai_suggestions/", docsH.AISuggestions)
				r.Get("/{id}/history/", docsH.History)
				r.Post("/{id}/email/", docsH.EmailDocument)
				r.Post("/{id}/email_document/", docsH.EmailDocument)
				r.Get("/{id}/share_links/", shareLinksH.GetForDocument)
				r.Post("/{id}/vault/", vaultH.ToggleDocumentVault)
				r.Get("/{id}/notes/", docsH.GetNotes)
				r.Post("/{id}/notes/", docsH.CreateNote)
				r.Delete("/{id}/notes/", docsH.DeleteNote)
				r.Delete("/{id}/notes/{note_id}/", docsH.DeleteNote)
			})

			// Trash
			r.Route("/trash", func(r chi.Router) {
				r.Get("/", docsH.TrashList)
				r.Post("/", docsH.TrashAction)
				r.Post("/restore/", docsH.TrashAction)
				r.Post("/purge/", docsH.TrashAction)
			})

			// Tags
			r.Route("/tags", func(r chi.Router) {
				r.Get("/", tagsH.List)
				r.Post("/", tagsH.Create)
				r.Get("/{id}/", tagsH.Get)
				r.Put("/{id}/", tagsH.Update)
				r.Patch("/{id}/", tagsH.Update)
				r.Delete("/{id}/", tagsH.Delete)
			})

			// Correspondents
			r.Route("/correspondents", func(r chi.Router) {
				r.Get("/", corrsH.List)
				r.Post("/", corrsH.Create)
				r.Get("/{id}/", corrsH.Get)
				r.Put("/{id}/", corrsH.Update)
				r.Patch("/{id}/", corrsH.Update)
				r.Delete("/{id}/", corrsH.Delete)
			})

			// Document Types
			r.Route("/document_types", func(r chi.Router) {
				r.Get("/", docTypesH.List)
				r.Post("/", docTypesH.Create)
				r.Get("/{id}/", docTypesH.Get)
				r.Put("/{id}/", docTypesH.Update)
				r.Patch("/{id}/", docTypesH.Update)
				r.Delete("/{id}/", docTypesH.Delete)
			})

			// Storage Paths
			r.Route("/storage_paths", func(r chi.Router) {
				r.Get("/", storPathsH.List)
				r.Post("/", storPathsH.Create)
				r.Get("/{id}/", storPathsH.Get)
				r.Put("/{id}/", storPathsH.Update)
				r.Patch("/{id}/", storPathsH.Update)
				r.Delete("/{id}/", storPathsH.Delete)
			})

			// Custom Fields
			r.Route("/custom_fields", func(r chi.Router) {
				r.Get("/", customFieldsH.List)
				r.Post("/", customFieldsH.Create)
				r.Get("/{id}/", customFieldsH.Get)
				r.Put("/{id}/", customFieldsH.Update)
				r.Patch("/{id}/", customFieldsH.Update)
				r.Delete("/{id}/", customFieldsH.Delete)
			})

			// Saved Views
			r.Route("/saved_views", func(r chi.Router) {
				r.Get("/", savedViewsH.List)
				r.Post("/", savedViewsH.Create)
				r.Get("/{id}/", savedViewsH.Get)
				r.Put("/{id}/", savedViewsH.Update)
				r.Patch("/{id}/", savedViewsH.Update)
				r.Delete("/{id}/", savedViewsH.Delete)
			})

			// Tasks
			r.Route("/tasks", func(r chi.Router) {
				r.Get("/", tasksH.List)
				r.Get("/status_counts/", tasksH.StatusCounts)
				r.Get("/active/", tasksH.Active)
				r.Post("/run/", tasksH.Run)
				r.Get("/summary/", tasksH.Summary)
				r.Post("/acknowledge/", tasksH.Acknowledge)
			})

			// Users, Groups, Workflows, Mail, ShareLinks, Logs, Scan
			r.Route("/users", func(r chi.Router) {
				r.Get("/", usersH.List)
				r.Post("/", usersH.Create)
				r.Get("/{id}/", usersH.Get)
				r.Put("/{id}/", usersH.Update)
				r.Patch("/{id}/", usersH.Update)
				r.Delete("/{id}/", usersH.Delete)
				r.Post("/{id}/deactivate_totp/", usersH.DeactivateUserTotp)
			})
			r.Route("/groups", func(r chi.Router) {
				r.Get("/", usersH.ListGroups)
				r.Post("/", usersH.CreateGroup)
				r.Get("/{id}/", usersH.GetGroup)
				r.Put("/{id}/", usersH.UpdateGroup)
				r.Patch("/{id}/", usersH.UpdateGroup)
				r.Delete("/{id}/", usersH.DeleteGroup)
			})
			r.Route("/workflows", func(r chi.Router) {
				r.Get("/", workflowsH.List)
				r.Post("/", workflowsH.Create)
				r.Post("/run/", workflowsH.RunAllWorkflows)
				r.Get("/{id}/", workflowsH.Get)
				r.Put("/{id}/", workflowsH.Update)
				r.Patch("/{id}/", workflowsH.Update)
				r.Delete("/{id}/", workflowsH.Delete)
				r.Post("/{id}/run/", workflowsH.RunWorkflow)
			})
			r.Route("/workflow_triggers", func(r chi.Router) {
				r.Get("/", workflowsH.ListTriggers)
				r.Post("/", workflowsH.CreateTrigger)
				r.Get("/{id}/", workflowsH.GetTrigger)
				r.Put("/{id}/", workflowsH.UpdateTrigger)
				r.Patch("/{id}/", workflowsH.UpdateTrigger)
				r.Delete("/{id}/", workflowsH.DeleteTrigger)
			})
			r.Route("/workflow_actions", func(r chi.Router) {
				r.Get("/", workflowsH.ListActions)
				r.Post("/", workflowsH.CreateAction)
				r.Get("/{id}/", workflowsH.GetAction)
				r.Put("/{id}/", workflowsH.UpdateAction)
				r.Patch("/{id}/", workflowsH.UpdateAction)
				r.Delete("/{id}/", workflowsH.DeleteAction)
			})
			r.Route("/mail_accounts", func(r chi.Router) {
				r.Get("/", mailH.ListAccounts)
				r.Post("/", mailH.CreateAccount)
				r.Get("/{id}/", mailH.GetAccount)
				r.Put("/{id}/", mailH.UpdateAccount)
				r.Patch("/{id}/", mailH.UpdateAccount)
				r.Delete("/{id}/", mailH.DeleteAccount)
			})
			r.Route("/mail_rules", func(r chi.Router) {
				r.Get("/", mailH.ListRules)
				r.Post("/", mailH.CreateRule)
				r.Get("/{id}/", mailH.GetRule)
				r.Put("/{id}/", mailH.UpdateRule)
				r.Patch("/{id}/", mailH.UpdateRule)
				r.Delete("/{id}/", mailH.DeleteRule)
			})
			r.Route("/processed_mail", func(r chi.Router) {
				r.Get("/", mailH.ListProcessedMail)
				r.Delete("/{id}/", mailH.DeleteProcessedMail)
			})
			r.Route("/share_links", func(r chi.Router) {
				r.Get("/", shareLinksH.List)
				r.Post("/", shareLinksH.Create)
				r.Get("/{id}/", shareLinksH.Get)
				r.Delete("/{id}/", shareLinksH.Delete)
			})
			r.Route("/share_link_bundles", func(r chi.Router) {
				r.Get("/", shareLinksH.ListBundles)
				r.Post("/", shareLinksH.CreateBundle)
				r.Get("/{id}/", shareLinksH.GetBundle)
				r.Delete("/{id}/", shareLinksH.DeleteBundle)
			})
			r.Route("/logs", func(r chi.Router) {
				r.Get("/", usersH.ListLogs)
				r.Get("/{log}/", usersH.GetLog)
			})

			// eSCL Network Scanning (Feature #19)
			r.Route("/scan", func(r chi.Router) {
				r.Get("/scanners/", scanH.GetScanners)
				r.Post("/scanners/test/", scanH.TestScanner)
				r.Post("/scan/", scanH.TriggerScan)
			})

			// Secure Cryptographic Vault (Feature #7)
			r.Route("/vault", func(r chi.Router) {
				r.Get("/status/", vaultH.Status)
				r.Post("/setup/", vaultH.Setup)
				r.Post("/unlock/", vaultH.Unlock)
				r.Post("/lock/", vaultH.Lock)
			})

			// System Integrity, Status, Diagnostics & Online Zero-Downtime Backup
			r.Get("/system/status/", systemH.SystemStatus)
			r.Get("/system/diagnostics/", systemH.SystemDiagnostics)
			r.Get("/system/integrity/", docsH.SystemIntegrityCheck)
			r.Get("/system/backup/", docsH.SystemBackup)

			// OpenAPI Schema & Interactive Documentation (Swagger UI & ReDoc)
			r.Get("/schema/", schemaH.GetSchema)
			r.Get("/schema/swagger-ui/", schemaH.SwaggerUI)
			r.Get("/schema/redoc/", schemaH.Redoc)
		})
	}

	// Mount API at /api/
	r.Route("/api", apiRouter)

	// Mount API also under /{lang}/api/ for Angular base href compatibility
	r.Route("/{lang:[a-z]{2}-[A-Z]{2}}/api", apiRouter)

	// Network Scanner Web Portal (UI Feature)
	r.Get("/scan", scanH.ScanPage)
	r.Get("/scan/", scanH.ScanPage)
	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}/scan", scanH.ScanPage)
	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}/scan/", scanH.ScanPage)

	// Public share links (Feature #3)
	r.Get("/share/{slug}", shareLinksH.PublicAccess)
	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}/share/{slug}", shareLinksH.PublicAccess)

	// WebSocket routes
	r.HandleFunc("/ws/status/", wsHub.HandleWS)
	r.HandleFunc("/ws/", wsHub.HandleWS)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/ws/status/", wsHub.HandleWS)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/ws/", wsHub.HandleWS)

	// Live Prometheus Metrics (/metrics)
	r.Get("/metrics", systemH.PrometheusMetrics)

	// Intercept legacy Django admin routes and redirect to home (no Django in paperless-go)
	adminRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}
	r.HandleFunc("/admin", adminRedirect)
	r.HandleFunc("/admin/", adminRedirect)
	r.HandleFunc("/admin/*", adminRedirect)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/admin", adminRedirect)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/admin/", adminRedirect)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/admin/*", adminRedirect)

	// Authentication forms / redirects
	r.HandleFunc("/accounts/login/", authH.LoginPage)
	r.HandleFunc("/accounts/logout/", authH.Logout)
	r.Get("/accounts/oidc/login", authH.OIDCLogin)
	r.Get("/accounts/oidc/callback", authH.OIDCCallback)
	r.Get("/api/auth/oidc/login", authH.OIDCLogin)
	r.Get("/api/auth/oidc/callback", authH.OIDCCallback)

	// Static assets (/static/*)
	staticFS := http.FileServer(http.Dir(cfg.StaticDir))
	r.Handle("/static/*", http.StripPrefix("/static/", staticFS))

	// Localized Angular Frontend (/en-US/, /pt-PT/, etc.)
	frontendDir := filepath.Join(cfg.StaticDir, "frontend")
	frontendHandler := func(w http.ResponseWriter, r *http.Request) {
		lang := chi.URLParam(r, "lang")
		if lang == "" {
			lang = "en-US"
		}
		langPath := filepath.Join(frontendDir, lang)
		if _, err := os.Stat(langPath); err != nil {
			lang = "en-US"
			langPath = filepath.Join(frontendDir, lang)
		}

		relPath := chi.URLParam(r, "*")
		if relPath != "" {
			cleanRel := filepath.Clean("/" + relPath)
			targetFile := filepath.Join(langPath, cleanRel)
			cleanLang := filepath.Clean(langPath)
			if strings.HasPrefix(filepath.Clean(targetFile), cleanLang) {
				if info, err := os.Stat(targetFile); err == nil && !info.IsDir() {
					http.ServeFile(w, r, targetFile)
					return
				}
			}
		}

		// If unauthenticated user requests the app shell (index.html), redirect to login page (matches Django LoginRequiredMixin)
		user := GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/accounts/login/?next="+r.URL.RequestURI(), http.StatusFound)
			return
		}

		// Otherwise serve index.html for Angular pushState routing
		indexPath := filepath.Join(langPath, "index.html")
		http.ServeFile(w, r, indexPath)
	}

	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}", func(w http.ResponseWriter, r *http.Request) {
		lang := chi.URLParam(r, "lang")
		http.Redirect(w, r, "/"+lang+"/", http.StatusMovedPermanently)
	})
	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}/", frontendHandler)
	r.Get("/{lang:[a-z]{2}-[A-Z]{2}}/*", frontendHandler)

	// Root redirect: detect language preference and redirect to /{lang}/
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			http.Redirect(w, r, "/accounts/login/?next=/", http.StatusFound)
			return
		}

		targetLang := cfg.DefaultLanguage
		if cookie, err := r.Cookie("django_language"); err == nil && cookie.Value != "" {
			targetLang = cookie.Value
		} else {
			accept := r.Header.Get("Accept-Language")
			if strings.Contains(strings.ToLower(accept), "pt") {
				targetLang = "pt-PT"
			}
		}

		// Ensure chosen language exists, otherwise fallback to en-US
		if _, err := os.Stat(filepath.Join(frontendDir, targetLang)); err != nil {
			targetLang = "en-US"
		}

		http.Redirect(w, r, fmt.Sprintf("/%s/", targetLang), http.StatusFound)
	})

	// Custom 404 Not Found handler: returns JSON for API, styled responsive page for browsers
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail": "Not found."}`))
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		html404 := `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Page Not Found - Paperless-ngx</title>
	<link rel="icon" type="image/x-icon" href="/static/paperless/img/favicon.ico">
	<link rel="stylesheet" href="/static/bootstrap.min.css">
	<style>
		:root {
			--bg-app: #f4f6f8;
			--bg-card: #ffffff;
			--text-main: #212529;
			--text-muted: #6c757d;
			--border-color: #dee2e6;
			--primary-color: #17541f;
			--primary-hover: #124018;
		}
		@media (prefers-color-scheme: dark) {
			:root {
				--bg-app: #121315;
				--bg-card: #242529;
				--text-main: #e2e8f0;
				--text-muted: #94a3b8;
				--border-color: #34373d;
				--primary-color: #1f7a2d;
				--primary-hover: #279939;
			}
		}
		body {
			background-color: var(--bg-app);
			color: var(--text-main);
			display: flex;
			align-items: center;
			justify-content: center;
			height: 100vh;
			margin: 0;
			font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
		}
		.card-box {
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			padding: 2.75rem 2rem;
			text-align: center;
			max-width: 440px;
			box-shadow: 0 10px 25px rgba(0,0,0,0.08);
		}
		.btn-primary {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
			font-weight: 500;
		}
		.btn-primary:hover {
			background-color: var(--primary-hover);
			border-color: var(--primary-hover);
		}
	</style>
</head>
<body>
	<div class="card-box">
		<h1 class="display-3 fw-bold text-muted mb-2">404</h1>
		<h2 class="h4 mb-2">Page Not Found</h2>
		<p class="text-muted small mb-4">The page or resource you requested could not be found on this Paperless instance.</p>
		<a href="/" class="btn btn-primary px-4">&larr; Return to Dashboard</a>
	</div>
</body>
</html>`
		_, _ = w.Write([]byte(html404))
	})

	return r
}
