package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Host            string
	Port            int
	DataDir         string
	MediaDir        string
	ConsumeDir      string
	StaticDir       string
	DatabasePath    string
	SecretKey       string
	OCRMode         string // "skip", "auto", "force"
	OCRWorkerCount  int
	AutoTagging     bool
	DefaultLanguage string
	AutoLogin       bool
	CorsAllowedHosts string
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

	return &Config{
		Host:             host,
		Port:             port,
		DataDir:          dataDir,
		MediaDir:         mediaDir,
		ConsumeDir:       consumeDir,
		StaticDir:        staticDir,
		DatabasePath:     dbPath,
		SecretKey:        secret,
		OCRMode:          ocrMode,
		OCRWorkerCount:   ocrWorkers,
		AutoTagging:      getEnv("PAPERLESS_AUTO_TAGGING", "true") == "true",
		DefaultLanguage:  defaultLang,
		AutoLogin:        autoLogin,
		CorsAllowedHosts: corsAllowed,
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
