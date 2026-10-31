package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Host             string
	Port             int
	DataDir          string
	MediaDir         string
	ConsumeDir       string
	StaticDir        string
	DatabasePath     string
	SecretKey        string
	OCRMode          string // "skip", "auto", "force"
	OCRWorkerCount   int
	AutoTagging      bool
	DefaultLanguage  string
	AutoLogin        bool
	CorsAllowedHosts string
	LogLevel         string // "debug", "info", "warn", "error"
	LogFormat        string // "text", "json"

	// AI Configuration (1. A, 1. B, 1. C)
	AIEnabled          bool
	AIProvider         string // "ollama", "openai", "gemini"
	AIAPIURL           string
	AIAPIKey           string
	AIModel            string
	AIEmbeddingModel   string
	AIExtractOnConsume bool

	// Initial Admin Credentials (Optional - if omitted, a secure random password is generated on first boot)
	AdminUser     string
	AdminPassword string
	AdminEmail    string

	// Security & Integrity Configuration
	EncryptionAtRest bool
	EncryptionKey    string

	// OIDC / SSO Configuration (4. A)
	OIDCEnabled      bool
	OIDCProviderName string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCDiscoveryURL string
	OIDCAuthURL      string
	OIDCTokenURL     string
	OIDCUserInfoURL  string
	OIDCRedirectURI  string
	OIDCAutoCreate   bool
}

func Load() *Config {
	dataDir := getEnv("PAPERLESS_DATA_DIR", "./data")
	mediaDir := getEnv("PAPERLESS_MEDIA_DIR", "./media")
	consumeDir := getEnv("PAPERLESS_CONSUME_DIR", "./consume")
	staticDir := getEnv("PAPERLESS_STATIC_DIR", "./web/static")
	if _, err := os.Stat(staticDir); err != nil {
		if exe, err := os.Executable(); err == nil {
			candidate := filepath.Join(filepath.Dir(exe), "web", "static")
			if _, err := os.Stat(candidate); err == nil {
				staticDir = candidate
			}
		}
	}

	// Ensure directories exist
	_ = os.MkdirAll(dataDir, 0755)
	_ = os.MkdirAll(mediaDir, 0755)
	_ = os.MkdirAll(filepath.Join(mediaDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(mediaDir, "documents", "archive"), 0755)
	_ = os.MkdirAll(filepath.Join(mediaDir, "documents", "thumbnails"), 0755)
	_ = os.MkdirAll(consumeDir, 0755)

	dbPath := getEnv("PAPERLESS_DB_PATH", filepath.Join(dataDir, "paperless.db"))
	port := getEnvInt("PAPERLESS_PORT", 8000)
	host := getEnv("PAPERLESS_HOST", "0.0.0.0")
	secret := getEnv("PAPERLESS_SECRET_KEY", "paperless-go-super-secret-key-change-me")
	ocrMode := getEnv("PAPERLESS_OCR_MODE", "auto")
	ocrWorkers := getEnvInt("PAPERLESS_OCR_WORKERS", 1)
	defaultLang := getEnv("PAPERLESS_DEFAULT_LANGUAGE", "en-US")
	autoLogin := getEnv("PAPERLESS_AUTO_LOGIN", "false") == "true"
	corsAllowed := getEnv("PAPERLESS_CORS_ALLOWED_HOSTS", "")
	logLevel := getEnv("PAPERLESS_LOG_LEVEL", "info")
	logFormat := getEnv("PAPERLESS_LOG_FORMAT", "text")

	// AI Defaults & envs
	aiProvider := strings.ToLower(getEnv("PAPERLESS_AI_PROVIDER", "ollama"))
	aiEnabled := getEnv("PAPERLESS_AI_ENABLED", "false") == "true" || os.Getenv("PAPERLESS_AI_PROVIDER") != "" || os.Getenv("PAPERLESS_AI_API_KEY") != ""
	aiURL := getEnv("PAPERLESS_AI_API_URL", "")
	if aiURL == "" && aiProvider == "ollama" {
		aiURL = "http://localhost:11434"
	}
	aiKey := getEnv("PAPERLESS_AI_API_KEY", "")
	aiModel := getEnv("PAPERLESS_AI_MODEL", "")
	aiEmbModel := getEnv("PAPERLESS_AI_EMBEDDING_MODEL", "")
	aiExtractOnConsume := getEnv("PAPERLESS_AI_EXTRACT_ON_CONSUME", "true") == "true"

	// Admin Credentials
	adminUser := getEnv("PAPERLESS_ADMIN_USER", "")
	adminPass := getEnv("PAPERLESS_ADMIN_PASSWORD", "")
	adminEmail := getEnv("PAPERLESS_ADMIN_EMAIL", "admin@paperless.local")

	// Security & Encryption
	encAtRest := getEnv("PAPERLESS_ENCRYPTION_AT_REST", "false") == "true"
	encKey := getEnv("PAPERLESS_ENCRYPTION_KEY", secret)

	// OIDC Defaults & envs
	oidcClientID := getEnv("PAPERLESS_OIDC_CLIENT_ID", "")
	oidcEnabled := getEnv("PAPERLESS_OIDC_ENABLED", "false") == "true" || oidcClientID != ""
	oidcProviderName := getEnv("PAPERLESS_OIDC_PROVIDER_NAME", "SSO")
	oidcClientSecret := getEnv("PAPERLESS_OIDC_CLIENT_SECRET", "")
	oidcDiscovery := getEnv("PAPERLESS_OIDC_DISCOVERY_URL", "")
	oidcAuthURL := getEnv("PAPERLESS_OIDC_AUTH_URL", "")
	oidcTokenURL := getEnv("PAPERLESS_OIDC_TOKEN_URL", "")
	oidcUserInfoURL := getEnv("PAPERLESS_OIDC_USERINFO_URL", "")
	oidcRedirectURI := getEnv("PAPERLESS_OIDC_REDIRECT_URI", "")
	oidcAutoCreate := getEnv("PAPERLESS_OIDC_AUTO_CREATE_USER", "true") == "true"

	return &Config{
		Host:               host,
		Port:               port,
		DataDir:            dataDir,
		MediaDir:           mediaDir,
		ConsumeDir:         consumeDir,
		StaticDir:          staticDir,
		DatabasePath:       dbPath,
		SecretKey:          secret,
		OCRMode:            ocrMode,
		OCRWorkerCount:     ocrWorkers,
		AutoTagging:        getEnv("PAPERLESS_AUTO_TAGGING", "true") == "true",
		DefaultLanguage:    defaultLang,
		AutoLogin:          autoLogin,
		CorsAllowedHosts:   corsAllowed,
		LogLevel:           logLevel,
		LogFormat:          logFormat,
		EncryptionAtRest:   encAtRest,
		EncryptionKey:      encKey,
		AIEnabled:          aiEnabled,
		AIProvider:         aiProvider,
		AIAPIURL:           aiURL,
		AIAPIKey:           aiKey,
		AIModel:            aiModel,
		AIEmbeddingModel:   aiEmbModel,
		AIExtractOnConsume: aiExtractOnConsume,
		AdminUser:          adminUser,
		AdminPassword:      adminPass,
		AdminEmail:         adminEmail,
		OIDCEnabled:        oidcEnabled,
		OIDCProviderName:   oidcProviderName,
		OIDCClientID:       oidcClientID,
		OIDCClientSecret:   oidcClientSecret,
		OIDCDiscoveryURL:   oidcDiscovery,
		OIDCAuthURL:        oidcAuthURL,
		OIDCTokenURL:       oidcTokenURL,
		OIDCUserInfoURL:    oidcUserInfoURL,
		OIDCRedirectURI:    oidcRedirectURI,
		OIDCAutoCreate:     oidcAutoCreate,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
