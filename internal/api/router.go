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
	"paperless-go/internal/queue"
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

	// CORS configuration for local apps, mobile apps, and extensions
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRFToken"},
		ExposedHeaders:   []string{"Link", "Content-Disposition"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Use(AuthMiddleware(authSvc))

	// Initialize handlers
	authH := NewAuthHandler(db, authSvc)
	uiSettingsH := NewUISettingsHandler(db)
	configH := NewConfigHandler(db)
	docsH := NewDocumentsHandler(db, cfg, consumer, q)
	tagsH := NewTagsHandler(db)
	corrsH := NewCorrespondentsHandler(db)
	docTypesH := NewDocumentTypesHandler(db)
	storPathsH := NewStoragePathsHandler(db)
	customFieldsH := NewCustomFieldsHandler(db)
	savedViewsH := NewSavedViewsHandler(db)
	tasksH := NewTasksHandler(db)
	statsH := NewStatisticsHandler(db)
	usersH := NewUsersHandler(db)

	// API subrouter
	apiRouter := func(r chi.Router) {
		r.Post("/token/", authH.GetToken)
		r.Get("/ui_settings/", uiSettingsH.Get)
		r.Post("/ui_settings/", uiSettingsH.Post)
		r.Get("/config/", configH.Get)
		r.Patch("/config/", configH.Patch)
		r.Get("/statistics/", statsH.Get)
		r.Get("/status/", statsH.GetStatus)

		// Documents
		r.Route("/documents", func(r chi.Router) {
			r.Get("/", docsH.List)
			r.Post("/post_document/", docsH.PostDocument)
			r.Post("/bulk_edit/", docsH.BulkEdit)
			r.Get("/{id}/", docsH.Get)
			r.Put("/{id}/", docsH.Update)
			r.Patch("/{id}/", docsH.Update)
			r.Delete("/{id}/", docsH.Delete)
			r.Get("/{id}/preview/", docsH.Preview)
			r.Get("/{id}/download/", docsH.Download)
			r.Get("/{id}/thumb/", docsH.Thumb)
			r.Get("/{id}/metadata/", docsH.Metadata)
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
			r.Delete("/{id}/", customFieldsH.Delete)
		})

		// Saved Views
		r.Route("/saved_views", func(r chi.Router) {
			r.Get("/", savedViewsH.List)
			r.Post("/", savedViewsH.Create)
			r.Get("/{id}/", savedViewsH.Get)
			r.Delete("/{id}/", savedViewsH.Delete)
		})

		// Tasks
		r.Route("/tasks", func(r chi.Router) {
			r.Get("/", tasksH.List)
			r.Post("/acknowledge/", tasksH.Acknowledge)
		})

		// Users, Groups, Workflows, Logs
		r.Route("/users", func(r chi.Router) {
			r.Get("/", usersH.List)
		})
		r.Route("/groups", func(r chi.Router) {
			r.Get("/", usersH.ListGroups)
		})
		r.Route("/workflows", func(r chi.Router) {
			r.Get("/", usersH.ListWorkflows)
		})
		r.Route("/logs", func(r chi.Router) {
			r.Get("/", usersH.ListLogs)
		})
	}

	// Mount API at /api/
	r.Route("/api", apiRouter)

	// Mount API also under /{lang}/api/ for Angular base href compatibility
	r.Route("/{lang:[a-z]{2}-[A-Z]{2}}/api", apiRouter)

	// WebSocket routes
	r.HandleFunc("/ws/status/", wsHub.HandleWS)
	r.HandleFunc("/ws/", wsHub.HandleWS)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/ws/status/", wsHub.HandleWS)
	r.HandleFunc("/{lang:[a-z]{2}-[A-Z]{2}}/ws/", wsHub.HandleWS)

	// Authentication forms / redirects
	r.HandleFunc("/accounts/login/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	r.HandleFunc("/accounts/logout/", authH.Logout)

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
			targetFile := filepath.Join(langPath, relPath)
			if info, err := os.Stat(targetFile); err == nil && !info.IsDir() {
				http.ServeFile(w, r, targetFile)
				return
			}
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

	return r
}
