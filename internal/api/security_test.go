package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/consumer"
	"paperless-go/internal/database"
	"paperless-go/internal/queue"
)

func setupSecurityTestEnvironment(t *testing.T, autoLogin bool) (*chiMuxWrapper, *database.DB, *config.Config, func()) {
	tmpDir, err := os.MkdirTemp("", "paperless-sec-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cfg := &config.Config{
		Host:            "127.0.0.1",
		Port:            8000,
		DataDir:         tmpDir,
		MediaDir:        filepath.Join(tmpDir, "media"),
		ConsumeDir:      filepath.Join(tmpDir, "consume"),
		StaticDir:       filepath.Join(tmpDir, "static"),
		DatabasePath:    filepath.Join(tmpDir, "sec_test.db"),
		SecretKey:       "secure-test-secret-key",
		AutoLogin:       autoLogin,
		DefaultLanguage: "en-US",
	}

	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "originals"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "archive"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.MediaDir, "documents", "thumbnails"), 0755)
	_ = os.MkdirAll(filepath.Join(cfg.StaticDir, "frontend", "en-US"), 0755)
	_ = os.WriteFile(filepath.Join(cfg.StaticDir, "frontend", "en-US", "index.html"), []byte("<html>Mock SPA Shell</html>"), 0644)
	_ = os.WriteFile(filepath.Join(cfg.StaticDir, "frontend", "en-US", "main.js"), []byte("console.log('main');"), 0644)

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}

	authSvc := auth.NewService(db, cfg)
	_ = authSvc.EnsureDefaultAdmin("admin", "adminpass")

	q := queue.NewQueue(db, 1)
	wsHub := NewWebSocketHub()
	q.SetBroadcaster(wsHub)
	c := consumer.NewConsumer(db, cfg)

	router := NewRouter(cfg, db, authSvc, q, c, wsHub)

	cleanup := func() {
		q.Close()
		wsHub.Close()
		db.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return &chiMuxWrapper{Handler: router}, db, cfg, cleanup
}

func TestSecurityAutoLoginDisabledByDefault(t *testing.T) {
	// 1. Verify default config has AutoLogin = false
	defaultCfg := config.Load()
	if defaultCfg.AutoLogin {
		t.Fatalf("Expected Config.Load() to have AutoLogin = false by default, got true")
	}

	// 2. Test server with AutoLogin = false
	router, _, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	// Unauthenticated API request to protected endpoint should return 401
	req := httptest.NewRequest("GET", "/api/documents/", nil)
	rec := router.Do(req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized from unauthenticated API request when AutoLogin=false, got %d", rec.Code)
	}

	// Unauthenticated browser request to SPA should redirect to /accounts/login/
	reqWeb := httptest.NewRequest("GET", "/en-US/", nil)
	recWeb := router.Do(reqWeb)
	if recWeb.Code != http.StatusFound {
		t.Fatalf("Expected 302 redirect to login, got %d", recWeb.Code)
	}
	loc := recWeb.Header().Get("Location")
	if !strings.HasPrefix(loc, "/accounts/login/") {
		t.Fatalf("Expected redirect to /accounts/login/, got %s", loc)
	}
}

func TestSecurityTOTPEnforcementInAPI(t *testing.T) {
	router, db, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	// Enable MFA for admin user (ID: 1)
	secret, err := auth.GenerateSecret()
	if err != nil {
		t.Fatalf("Failed to generate secret: %v", err)
	}
	recoveryCodes := auth.GenerateRecoveryCodes()
	recJSON, _ := json.Marshal(recoveryCodes)

	_, err = db.Exec(`
		UPDATE users
		SET totp_secret = ?, is_mfa_enabled = 1, recovery_codes_json = ?
		WHERE id = 1
	`, secret, string(recJSON))
	if err != nil {
		t.Fatalf("Failed to enable MFA: %v", err)
	}

	// 1. Attempt token login WITHOUT TOTP code
	payloadNoMFA := `{"username": "admin", "password": "adminpass"}`
	req := httptest.NewRequest("POST", "/api/token/", bytes.NewBufferString(payloadNoMFA))
	req.Header.Set("Content-Type", "application/json")
	rec := router.Do(req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request when TOTP code is missing, got %d: %s", rec.Code, rec.Body.String())
	}
	var respNoMFA map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &respNoMFA)
	if respNoMFA["mfa_required"] != true {
		t.Errorf("Expected mfa_required: true in response, got: %v", respNoMFA)
	}

	// 2. Attempt token login with INVALID TOTP code
	payloadInvalidMFA := `{"username": "admin", "password": "adminpass", "code": "000000"}`
	req = httptest.NewRequest("POST", "/api/token/", bytes.NewBufferString(payloadInvalidMFA))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request for invalid TOTP code, got %d", rec.Code)
	}

	// 3. Attempt token login with VALID TOTP code
	validCode, err := auth.CalculateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("CalculateTOTP failed: %v", err)
	}
	payloadValidMFA := fmt.Sprintf(`{"username": "admin", "password": "adminpass", "code": "%s"}`, validCode)
	req = httptest.NewRequest("POST", "/api/token/", bytes.NewBufferString(payloadValidMFA))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with valid TOTP code, got %d: %s", rec.Code, rec.Body.String())
	}
	var tokenResp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &tokenResp)
	if tokenResp["token"] == "" {
		t.Fatalf("Expected valid token in response, got %v", tokenResp)
	}

	// 4. Attempt token login with RECOVERY CODE
	usedRecovery := recoveryCodes[0]
	payloadRecovery := fmt.Sprintf(`{"username": "admin", "password": "adminpass", "code": "%s"}`, usedRecovery)
	req = httptest.NewRequest("POST", "/api/token/", bytes.NewBufferString(payloadRecovery))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK with recovery code, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Attempt REUSING the same recovery code - must FAIL
	req = httptest.NewRequest("POST", "/api/token/", bytes.NewBufferString(payloadRecovery))
	req.Header.Set("Content-Type", "application/json")
	rec = router.Do(req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Expected reused recovery code to fail with 400, got %d", rec.Code)
	}
}

func TestSecurityTOTPEnforcementInWebLogin(t *testing.T) {
	router, db, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	secret, _ := auth.GenerateSecret()
	_, _ = db.Exec("UPDATE users SET totp_secret = ?, is_mfa_enabled = 1 WHERE id = 1", secret)

	// 1. Submit password only -> should render 2FA challenge form
	form := url.Values{}
	form.Set("login", "admin")
	form.Set("password", "adminpass")
	req := httptest.NewRequest("POST", "/accounts/login/?next=/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := router.Do(req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 rendering 2FA challenge, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "id_code") || !strings.Contains(body, "Two-factor authentication is required") {
		t.Fatalf("Expected 2FA challenge form in HTML body, got: %s", body)
	}

	// 2. Submit with valid TOTP code -> should succeed and redirect
	validCode, _ := auth.CalculateTOTP(secret, time.Now())
	form2 := url.Values{}
	form2.Set("login", "admin")
	form2.Set("password", "adminpass")
	form2.Set("code", validCode)
	req2 := httptest.NewRequest("POST", "/accounts/login/?next=/", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := router.Do(req2)

	if rec2.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found redirect on valid 2FA login, got %d", rec2.Code)
	}
	sessionCookie := ""
	for _, c := range rec2.Result().Cookies() {
		if c.Name == "sessionid" {
			sessionCookie = c.Value
			if !c.HttpOnly {
				t.Errorf("Expected sessionid cookie to be HttpOnly")
			}
		}
	}
	if sessionCookie == "" {
		t.Fatalf("Expected sessionid cookie to be set upon successful login")
	}
}

func TestSecurityPathTraversalProtection(t *testing.T) {
	router, _, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	// Attempt path traversal via Angular frontend route
	req := httptest.NewRequest("GET", "/en-US/../../../../etc/passwd", nil)
	rec := router.Do(req)

	// Must NOT return 200 with arbitrary file contents
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "root:") {
		t.Fatalf("Path traversal succeeded! File served from outside frontend directory")
	}

	// Legitimate frontend asset within directory should serve correctly
	reqValid := httptest.NewRequest("GET", "/en-US/main.js", nil)
	recValid := router.Do(reqValid)
	if recValid.Code != http.StatusOK {
		t.Fatalf("Expected legitimate file /en-US/main.js to return 200, got %d", recValid.Code)
	}
	if !strings.Contains(recValid.Body.String(), "console.log('main');") {
		t.Fatalf("Expected legitimate content from main.js, got: %s", recValid.Body.String())
	}
}

func TestSecurityCSRFProtection(t *testing.T) {
	router, db, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	// Retrieve session token for user
	var token string
	_ = db.QueryRow("SELECT auth_token FROM users WHERE id = 1").Scan(&token)

	// 1. Cookie-authenticated request without CSRF token header should fail with 403 Forbidden
	req := httptest.NewRequest("POST", "/api/tags/", bytes.NewBufferString(`{"name": "CSRFTag", "color": "#ff0000"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "sessionid", Value: token})
	req.AddCookie(&http.Cookie{Name: "csrftoken", Value: "valid-csrf-secret"})

	rec := router.Do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden when X-CSRFToken header is missing, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Cookie-authenticated request WITH matching X-CSRFToken header should succeed
	req2 := httptest.NewRequest("POST", "/api/tags/", bytes.NewBufferString(`{"name": "CSRFTag", "color": "#ff0000"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-CSRFToken", "valid-csrf-secret")
	req2.AddCookie(&http.Cookie{Name: "sessionid", Value: token})
	req2.AddCookie(&http.Cookie{Name: "csrftoken", Value: "valid-csrf-secret"})

	rec2 := router.Do(req2)
	if rec2.Code != http.StatusCreated && rec2.Code != http.StatusOK {
		t.Fatalf("Expected 201 Created with valid CSRF token, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// 3. Stateless Token Authorization header is exempt from CSRF
	req3 := httptest.NewRequest("POST", "/api/tags/", bytes.NewBufferString(`{"name": "TokenTag", "color": "#00ff00"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Token "+token)

	rec3 := router.Do(req3)
	if rec3.Code != http.StatusCreated && rec3.Code != http.StatusOK {
		t.Fatalf("Expected 201 Created for Token auth header without CSRF, got %d: %s", rec3.Code, rec3.Body.String())
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	router, _, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	req := httptest.NewRequest("GET", "/accounts/login/", nil)
	rec := router.Do(req)

	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}

	for header, expectedVal := range expectedHeaders {
		if rec.Header().Get(header) != expectedVal {
			t.Errorf("Expected security header %s: %s, got: %s", header, expectedVal, rec.Header().Get(header))
		}
	}
}

func TestSecurityDjangoAdminRedirect(t *testing.T) {
	router, _, _, cleanup := setupSecurityTestEnvironment(t, false)
	defer cleanup()

	adminPaths := []string{"/admin", "/admin/", "/admin/some/model/", "/en-US/admin", "/en-US/admin/"}
	for _, p := range adminPaths {
		req := httptest.NewRequest("GET", p, nil)
		rec := router.Do(req)
		if rec.Code != http.StatusFound {
			t.Errorf("Expected 302 Found redirect for path %s, got: %d", p, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("Expected redirect to /, got: %s", loc)
		}
	}
}


