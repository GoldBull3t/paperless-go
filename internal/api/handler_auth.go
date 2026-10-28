package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"paperless-go/internal/auth"
	"paperless-go/internal/database"
	"paperless-go/internal/oidc"
)

type AuthHandler struct {
	db          *database.DB
	authSvc     *auth.Service
	rateLimiter *auth.RateLimiter
	oidcClient  *oidc.Client
}

func NewAuthHandler(db *database.DB, authSvc *auth.Service, oidcClient *oidc.Client) *AuthHandler {
	return &AuthHandler{
		db:          db,
		authSvc:     authSvc,
		rateLimiter: auth.NewRateLimiter(5, 5*time.Minute, 5*time.Minute),
		oidcClient:  oidcClient,
	}
}

type TokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
	OTP      string `json:"otp"`
}

type TokenResponse struct {
	Token string `json:"token"`
}

func (h *AuthHandler) getClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		return strings.TrimSpace(realIP)
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (h *AuthHandler) GetToken(w http.ResponseWriter, r *http.Request) {
	clientIP := h.getClientIP(r)
	if allowed, remaining := h.rateLimiter.Allow(clientIP); !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"non_field_errors": []string{fmt.Sprintf("Too many failed login attempts. Please wait %d seconds.", int(remaining.Seconds()))},
		})
		return
	}

	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"non_field_errors": []string{"Unable to log in with provided credentials."},
		})
		return
	}

	var id int
	var hash, token, totpSecret, recoveryJSON string
	var isMfa bool
	err := h.db.QueryRow(`
		SELECT id, password_hash, auth_token, is_mfa_enabled, totp_secret, recovery_codes_json
		FROM users WHERE username = ? AND is_active = 1
	`, req.Username).Scan(&id, &hash, &token, &isMfa, &totpSecret, &recoveryJSON)

	if err != nil || !h.authSvc.CheckPassword(hash, req.Password) {
		if err != nil {
			auth.DummyBcryptCompare(req.Password)
		}
		h.rateLimiter.RecordFailure(clientIP)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"non_field_errors": []string{"Unable to log in with provided credentials."},
		})
		return
	}

	// 2FA Enforcement if user has MFA active
	if isMfa {
		code := req.Code
		if code == "" {
			code = req.OTP
		}
		code = strings.TrimSpace(code)

		if code == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"mfa_required":     true,
				"non_field_errors": []string{"Two-factor authentication code is required."},
			})
			return
		}

		valid := auth.VerifyCode(totpSecret, code)
		if !valid {
			consumed, newJSON := auth.VerifyAndConsumeRecoveryCode(recoveryJSON, code)
			if consumed {
				valid = true
				_, _ = h.db.Exec("UPDATE users SET recovery_codes_json = ? WHERE id = ?", newJSON, id)
			}
		}

		if !valid {
			h.rateLimiter.RecordFailure(clientIP)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"mfa_required":     true,
				"non_field_errors": []string{"Invalid two-factor authentication code."},
			})
			return
		}
	}

	h.rateLimiter.Reset(clientIP)

	if token == "" {
		token = auth.GenerateToken(40)
		_, _ = h.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", token, id)
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	// Also set sessionid cookie for browser compatibility
	http.SetCookie(w, &http.Cookie{
		Name:     "sessionid",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TokenResponse{Token: token})
}

func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Sanitize next parameter against Open Redirect vulnerabilities
	cleanNext := func(next string) string {
		if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
			return "/"
		}
		return next
	}

	next := cleanNext(r.URL.Query().Get("next"))
	if next == "/" && r.FormValue("next") != "" {
		next = cleanNext(r.FormValue("next"))
	}

	// If already authenticated, redirect to next or home
	if user := GetUserFromContext(r.Context()); user != nil {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}

	clientIP := h.getClientIP(r)

	if r.Method == http.MethodPost {
		if allowed, remaining := h.rateLimiter.Allow(clientIP); !allowed {
			h.renderLoginForm(w, fmt.Sprintf("Too many failed login attempts. Please wait %d seconds.", int(remaining.Seconds())), next, false, "", "", false)
			return
		}

		_ = r.ParseForm()
		username := strings.TrimSpace(r.FormValue("login"))
		if username == "" {
			username = strings.TrimSpace(r.FormValue("username"))
		}
		password := r.FormValue("password")
		code := strings.TrimSpace(r.FormValue("code"))
		remember := r.FormValue("remember") != ""

		var id int
		var hash, token, totpSecret, recoveryJSON string
		var isMfa bool
		err := h.db.QueryRow(`
			SELECT id, password_hash, auth_token, is_mfa_enabled, totp_secret, recovery_codes_json
			FROM users WHERE username = ? AND is_active = 1
		`, username).Scan(&id, &hash, &token, &isMfa, &totpSecret, &recoveryJSON)

		if err != nil || !h.authSvc.CheckPassword(hash, password) {
			if err != nil {
				auth.DummyBcryptCompare(password)
			}
			h.rateLimiter.RecordFailure(clientIP)
			h.renderLoginForm(w, "The username and/or password you specified are not correct.", next, false, "", "", false)
			return
		}

		// 2FA Verification
		if isMfa {
			if code == "" {
				// Password verified, render 2FA challenge form!
				h.renderLoginForm(w, "", next, true, username, password, remember)
				return
			}

			valid := auth.VerifyCode(totpSecret, code)
			if !valid {
				consumed, newJSON := auth.VerifyAndConsumeRecoveryCode(recoveryJSON, code)
				if consumed {
					valid = true
					_, _ = h.db.Exec("UPDATE users SET recovery_codes_json = ? WHERE id = ?", newJSON, id)
				}
			}

			if !valid {
				h.rateLimiter.RecordFailure(clientIP)
				h.renderLoginForm(w, "Invalid two-factor authentication code. Please try again.", next, true, username, password, remember)
				return
			}
		}

		// Success!
		h.rateLimiter.Reset(clientIP)

		if token == "" {
			token = auth.GenerateToken(40)
			_, _ = h.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", token, id)
		}

		maxAge := 0
		if remember {
			maxAge = 30 * 24 * 3600 // 30 days
		}

		isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

		http.SetCookie(w, &http.Cookie{
			Name:     "sessionid",
			Value:    token,
			Path:     "/",
			MaxAge:   maxAge,
			HttpOnly: true,
			Secure:   isSecure,
			SameSite: http.SameSiteLaxMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "csrftoken",
			Value:    auth.GenerateToken(32),
			Path:     "/",
			MaxAge:   maxAge,
			HttpOnly: false,
			Secure:   isSecure,
			SameSite: http.SameSiteLaxMode,
		})

		http.Redirect(w, r, next, http.StatusFound)
		return
	}

	h.renderLoginForm(w, "", next, false, "", "", true)
}

func (h *AuthHandler) renderLoginForm(w http.ResponseWriter, errorMsg, next string, is2FA bool, username, password string, remember bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	errorHtml := ""
	if errorMsg != "" {
		errorHtml = fmt.Sprintf(`
		<div class="alert alert-danger d-flex align-items-center mb-3" role="alert">
			<svg class="bi flex-shrink-0 me-2" width="18" height="18" fill="currentColor" viewBox="0 0 16 16"><path d="M8 15A7 7 0 1 1 8 1a7 7 0 0 1 0 14zm0 1A8 8 0 1 0 8 0a8 8 0 0 0 0 16z"/><path d="M7.002 11a1 1 0 1 1 2 0 1 1 0 0 1-2 0zM7.1 4.995a.905.905 0 1 1 1.8 0l-.35 3.507a.552.552 0 0 1-1.1 0z"/></svg>
			<div>%s</div>
		</div>`, html.EscapeString(errorMsg))
	}

	formContent := ""
	if is2FA {
		formContent = fmt.Sprintf(`
		<form method="POST" action="/accounts/login/?next=%s" id="loginForm">
			<input type="hidden" name="login" value="%s">
			<input type="hidden" name="password" value="%s">
			<input type="hidden" name="remember" value="%t">

			<div class="alert alert-info d-flex align-items-center mb-3" role="alert">
				<div>Two-factor authentication is required for <strong>%s</strong>. Enter your 6-digit authenticator code or recovery code.</div>
			</div>

			%s

			<div class="form-floating mb-3">
				<input type="text" class="form-control" id="id_code" name="code" placeholder="6-digit code or recovery code" required autofocus autocomplete="one-time-code" maxlength="24">
				<label for="id_code">Authentication / Recovery Code</label>
			</div>

			<button class="w-100 btn btn-primary mb-3" type="submit" id="submitBtn">
				<span id="btnText">Verify & Sign in</span>
			</button>

			<div class="text-center">
				<a href="/accounts/login/?next=%s" class="small text-muted text-decoration-none">&larr; Back to sign in</a>
			</div>
		</form>
		`, html.EscapeString(next), html.EscapeString(username), html.EscapeString(password), remember, html.EscapeString(username), errorHtml, html.EscapeString(next))
	} else {
		rememberChecked := "checked"
		if !remember {
			rememberChecked = ""
		}

		ssoButtonHtml := ""
		if h.oidcClient != nil && h.oidcClient.IsEnabled() {
			ssoButtonHtml = fmt.Sprintf(`
			<div class="d-grid mb-3">
				<a href="/api/auth/oidc/login?next=%s" class="btn btn-outline-secondary d-flex align-items-center justify-content-center py-2 text-decoration-none">
					<svg class="me-2" width="18" height="18" fill="currentColor" viewBox="0 0 16 16">
						<path d="M8 0a8 8 0 1 0 0 16A8 8 0 0 0 8 0zm3.5 7.5a.5.5 0 0 1 0 1H5.707l2.147 2.146a.5.5 0 0 1-.708.708l-3-3a.5.5 0 0 1 0-.708l3-3a.5.5 0 1 1 .708.708L5.707 7.5H11.5z"/>
					</svg>
					<span>Sign in with %s (SSO)</span>
				</a>
			</div>
			<div class="d-flex align-items-center my-3">
				<hr class="flex-grow-1 my-0" style="border-color: var(--border-color);">
				<span class="px-2 small text-muted">or continue with password</span>
				<hr class="flex-grow-1 my-0" style="border-color: var(--border-color);">
			</div>
			`, html.EscapeString(next), html.EscapeString(h.oidcClient.ProviderName()))
		}

		formContent = fmt.Sprintf(`
		%s
		%s
		<form method="POST" action="/accounts/login/?next=%s" id="loginForm">
			<div class="form-floating mb-3">
				<input type="text" class="form-control" id="id_login" name="login" placeholder="Username" required autofocus autocomplete="username">
				<label for="id_login">Username</label>
			</div>

			<div class="form-floating mb-2 position-relative">
				<input type="password" class="form-control" id="id_password" name="password" placeholder="Password" required autocomplete="current-password">
				<label for="id_password">Password</label>
				<button type="button" class="toggle-pwd" id="togglePasswordBtn" title="Toggle password visibility" aria-label="Toggle password visibility">
					<svg id="eyeIcon" width="18" height="18" fill="currentColor" viewBox="0 0 16 16">
						<path d="M16 8s-3-5.5-8-5.5S0 8 0 8s3 5.5 8 5.5S16 8 16 8zM1.173 8a13.133 13.133 0 0 1 1.66-2.043C4.12 4.668 5.88 3.5 8 3.5c2.12 0 3.879 1.168 5.168 2.457A13.133 13.133 0 0 1 14.828 8c-.058.087-.122.183-.195.288-.335.48-.83 1.12-1.465 1.755C11.879 11.332 10.119 12.5 8 12.5c-2.12 0-3.879-1.168-5.168-2.457A13.134 13.134 0 0 1 1.172 8z"/>
						<path d="M8 5.5a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5zM4.5 8a3.5 3.5 0 1 1 7 0 3.5 3.5 0 0 1-7 0z"/>
					</svg>
					<svg id="eyeSlashIcon" width="18" height="18" fill="currentColor" viewBox="0 0 16 16" style="display:none;">
						<path d="M13.359 11.238C15.06 9.72 16 8 16 8s-3-5.5-8-5.5a7.028 7.028 0 0 0-2.79.588l.77.771A5.944 5.944 0 0 1 8 3.5c2.12 0 3.879 1.168 5.168 2.457A13.134 13.134 0 0 1 14.828 8c-.058.087-.122.183-.195.288-.335.48-.83 1.12-1.465 1.755-.165.165-.337.328-.517.486l.708.709z"/>
						<path d="M11.297 9.176a3.5 3.5 0 0 0-4.474-4.474l.823.823a2.5 2.5 0 0 1 2.829 2.829l.822.822zm-2.943 1.299.822.822a3.5 3.5 0 0 1-4.474-4.474l.823.823a2.5 2.5 0 0 0 2.829 2.829z"/>
						<path d="M3.35 5.47c-.18.16-.353.322-.518.487A13.134 13.134 0 0 0 1.172 8l.195.288c.335.48.83 1.12 1.465 1.755C4.121 11.332 5.881 12.5 8 12.5c.716 0 1.39-.133 2.02-.36l.77.772A7.029 7.029 0 0 1 8 13.5C3 13.5 0 8 0 8s.939-1.721 2.641-3.238l.708.709zm10.296 8.884-12-12 .708-.708 12 12-.708.708z"/>
					</svg>
				</button>
			</div>

			<div id="capsLockWarning" class="caps-warning mb-2">
				&#9888; Warning: Caps Lock is ON
			</div>

			<div class="form-check mb-3 text-start">
				<input class="form-check-input" type="checkbox" name="remember" id="id_remember" value="remember-me" %s>
				<label class="form-check-label small" for="id_remember" style="color: var(--text-muted); cursor: pointer;">
					Remember me on this browser
				</label>
			</div>

			<button class="w-100 btn btn-primary" type="submit" id="submitBtn">
				<span id="btnText">Sign in</span>
			</button>
		</form>
		`, errorHtml, ssoButtonHtml, html.EscapeString(next), rememberChecked)
	}

	htmlContent := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Sign in - Paperless-ngx</title>
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
			--primary-focus-shadow: rgba(23, 84, 31, 0.25);
			--input-bg: #ffffff;
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
				--primary-focus-shadow: rgba(31, 122, 45, 0.35);
				--input-bg: #1c1d21;
			}
		}
		html, body {
			height: 100%%;
			margin: 0;
			background-color: var(--bg-app);
			color: var(--text-main);
			font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
			transition: background-color 0.2s ease, color 0.2s ease;
		}
		body {
			display: flex;
			align-items: center;
			justify-content: center;
			padding: 20px;
		}
		.card-login {
			width: 100%%;
			max-width: 400px;
			background: var(--bg-card);
			border: 1px solid var(--border-color);
			border-radius: 12px;
			padding: 2.25rem;
			box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.08);
			animation: fadeIn 0.25s ease-out;
		}
		@keyframes fadeIn {
			from { opacity: 0; transform: translateY(-8px); }
			to { opacity: 1; transform: translateY(0); }
		}
		.logo-wrap {
			display: flex;
			align-items: center;
			justify-content: center;
			gap: 12px;
			margin-bottom: 1.5rem;
		}
		.logo-title {
			color: var(--primary-color);
			font-weight: 700;
			font-size: 1.6rem;
			letter-spacing: -0.5px;
			margin: 0;
		}
		.form-control {
			background-color: var(--input-bg);
			border-color: var(--border-color);
			color: var(--text-main);
		}
		.form-control:focus {
			background-color: var(--input-bg);
			color: var(--text-main);
			border-color: var(--primary-color);
			box-shadow: 0 0 0 0.25rem var(--primary-focus-shadow);
		}
		.form-floating label {
			color: var(--text-muted);
		}
		.form-floating > .form-control:focus ~ label,
		.form-floating > .form-control:not(:placeholder-shown) ~ label {
			color: var(--text-muted);
		}
		.btn-primary {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
			font-weight: 600;
			padding: 0.75rem 1rem;
			border-radius: 8px;
			transition: background-color 0.15s ease-in-out, border-color 0.15s ease-in-out;
		}
		.btn-primary:hover, .btn-primary:active, .btn-primary:focus {
			background-color: var(--primary-hover) !important;
			border-color: var(--primary-hover) !important;
		}
		.toggle-pwd {
			position: absolute;
			right: 12px;
			top: 50%%;
			transform: translateY(-50%%);
			background: none;
			border: none;
			color: var(--text-muted);
			cursor: pointer;
			padding: 4px;
			z-index: 10;
		}
		.toggle-pwd:hover {
			color: var(--text-main);
		}
		.caps-warning {
			display: none;
			font-size: 0.82rem;
			color: #eab308;
			margin-top: 4px;
		}
		.form-check-input:checked {
			background-color: var(--primary-color);
			border-color: var(--primary-color);
		}
	</style>
</head>
<body>
	<main class="card-login">
		<div class="logo-wrap">
			<svg width="40" height="40" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
				<rect width="48" height="48" rx="10" fill="var(--primary-color)"/>
				<path d="M14 12C14 10.8954 14.8954 10 16 10H26L34 18V36C34 37.1046 33.1046 38 32 38H16C14.8954 38 14 37.1046 14 36V12Z" fill="white" fill-opacity="0.95"/>
				<path d="M26 10V18H34L26 10Z" fill="white" fill-opacity="0.6"/>
				<path d="M19 24H29" stroke="var(--primary-color)" stroke-width="2.2" stroke-linecap="round"/>
				<path d="M19 29H29" stroke="var(--primary-color)" stroke-width="2.2" stroke-linecap="round"/>
				<path d="M19 34H25" stroke="var(--primary-color)" stroke-width="2.2" stroke-linecap="round"/>
			</svg>
			<div>
				<h1 class="logo-title">Paperless-ngx</h1>
				<small class="text-muted d-block" style="font-size: 0.8rem; margin-top: -2px;">High-Performance Go Backend</small>
			</div>
		</div>

		%s
	</main>

	<script>
		const pwdInput = document.getElementById('id_password');
		const toggleBtn = document.getElementById('togglePasswordBtn');
		const eyeIcon = document.getElementById('eyeIcon');
		const eyeSlashIcon = document.getElementById('eyeSlashIcon');
		const capsWarning = document.getElementById('capsLockWarning');
		const form = document.getElementById('loginForm');
		const submitBtn = document.getElementById('submitBtn');
		const btnText = document.getElementById('btnText');

		if (toggleBtn && pwdInput) {
			toggleBtn.addEventListener('click', () => {
				if (pwdInput.type === 'password') {
					pwdInput.type = 'text';
					eyeIcon.style.display = 'none';
					eyeSlashIcon.style.display = 'block';
				} else {
					pwdInput.type = 'password';
					eyeIcon.style.display = 'block';
					eyeSlashIcon.style.display = 'none';
				}
			});

			pwdInput.addEventListener('keyup', (e) => {
				if (e.getModifierState && e.getModifierState('CapsLock')) {
					capsWarning.style.display = 'block';
				} else {
					capsWarning.style.display = 'none';
				}
			});
		}

		if (form && submitBtn) {
			form.addEventListener('submit', () => {
				submitBtn.disabled = true;
				if (btnText) {
					btnText.textContent = 'Verifying...';
				}
			});
		}
	</script>
</body>
</html>`, formContent)

	_, _ = w.Write([]byte(htmlContent))
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "sessionid",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/accounts/login/", http.StatusFound)
}

func (h *AuthHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, `{"detail": "Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var token string
	var isMfa bool
	_ = h.db.QueryRow("SELECT auth_token, is_mfa_enabled FROM users WHERE id = ?", user.ID).Scan(&token, &isMfa)

	resp := map[string]interface{}{
		"id":                  user.ID,
		"username":            user.Username,
		"email":               user.Email,
		"first_name":          user.FirstName,
		"last_name":           user.LastName,
		"has_usable_password": true,
		"is_mfa_enabled":      isMfa,
		"auth_token":          token,
		"social_accounts":     []interface{}{},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, `{"detail": "Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var payload struct {
		Email     *string `json:"email"`
		FirstName *string `json:"first_name"`
		LastName  *string `json:"last_name"`
		Password  *string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if payload.Email != nil {
		_, _ = h.db.Exec("UPDATE users SET email = ? WHERE id = ?", *payload.Email, user.ID)
	}
	if payload.FirstName != nil {
		_, _ = h.db.Exec("UPDATE users SET first_name = ? WHERE id = ?", *payload.FirstName, user.ID)
	}
	if payload.LastName != nil {
		_, _ = h.db.Exec("UPDATE users SET last_name = ? WHERE id = ?", *payload.LastName, user.ID)
	}
	if payload.Password != nil && *payload.Password != "" {
		hash, err := h.authSvc.HashPassword(*payload.Password)
		if err == nil {
			_, _ = h.db.Exec("UPDATE users SET password_hash = ? WHERE id = ?", hash, user.ID)
		}
	}

	h.GetProfile(w, r)
}

// GetTotpSettings returns secret and QR SVG for enabling TOTP (Feature #15)
func (h *AuthHandler) GetTotpSettings(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	username := "admin"
	if user != nil {
		username = user.Username
	}

	secret, err := auth.GenerateSecret()
	if err != nil {
		http.Error(w, "Failed to generate TOTP secret", http.StatusInternalServerError)
		return
	}

	qrSVG, err := auth.GenerateQRCodeSVG(secret, username, "Paperless-ngx")
	if err != nil {
		http.Error(w, "Failed to generate QR code SVG", http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"secret": secret,
		"qr_svg": qrSVG,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ActivateTotp verifies code and enables TOTP (Feature #15)
func (h *AuthHandler) ActivateTotp(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	userID := 1
	if user != nil {
		userID = user.ID
	}

	var payload struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !auth.VerifyCode(payload.Secret, payload.Code) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"detail":  "Invalid TOTP verification code. Please check your authenticator app time.",
		})
		return
	}

	recoveryCodes := auth.GenerateRecoveryCodes()
	recJSON, _ := json.Marshal(recoveryCodes)

	_, err := h.db.Exec(`
		UPDATE users
		SET totp_secret = ?, is_mfa_enabled = 1, recovery_codes_json = ?
		WHERE id = ?
	`, payload.Secret, string(recJSON), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]interface{}{
		"success":        true,
		"recovery_codes": recoveryCodes,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// DeactivateTotp disables TOTP for the current user
func (h *AuthHandler) DeactivateTotp(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	userID := 1
	if user != nil {
		userID = user.ID
	}

	_, _ = h.db.Exec("UPDATE users SET is_mfa_enabled = 0, totp_secret = '', recovery_codes_json = '[]' WHERE id = ?", userID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(true)
}

func (h *AuthHandler) GenerateAuthToken(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		http.Error(w, `{"detail": "Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	token := auth.GenerateToken(40)
	_, _ = h.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", token, user.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"auth_token": token})
}

func (h *AuthHandler) DisconnectSocialAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	w.Header().Set("Content-Type", "application/json")
	if input.ID > 0 {
		_ = json.NewEncoder(w).Encode(input.ID)
	} else {
		_ = json.NewEncoder(w).Encode(true)
	}
}

func (h *AuthHandler) GetSocialAccountProviders(w http.ResponseWriter, r *http.Request) {
	providers := []map[string]interface{}{}
	if h.oidcClient != nil && h.oidcClient.IsEnabled() {
		providers = append(providers, map[string]interface{}{
			"id":        "oidc",
			"name":      h.oidcClient.ProviderName(),
			"login_url": "/accounts/oidc/login/?process=connect",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(providers)
}

func (h *AuthHandler) Headless(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	var userData interface{}
	status := 200
	if user != nil {
		userData = map[string]interface{}{
			"id":         user.ID,
			"username":   user.Username,
			"email":      user.Email,
			"first_name": user.FirstName,
			"last_name":  user.LastName,
		}
	}
	resp := map[string]interface{}{
		"status": status,
		"data": map[string]interface{}{
			"user":    userData,
			"methods": []string{"password", "token"},
			"flows":   []map[string]interface{}{{"id": "login", "is_pending": false}},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	if h.oidcClient == nil || !h.oidcClient.IsEnabled() {
		http.Error(w, "OIDC / SSO is not configured on this server.", http.StatusServiceUnavailable)
		return
	}

	state := oidc.GenerateRandomState()
	next := r.URL.Query().Get("next")
	if next == "" {
		next = "/"
	}

	stateValue := fmt.Sprintf("%s:%s", state, next)
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    stateValue,
		Path:     "/",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})

	authURL, err := h.oidcClient.GetAuthURL(r.Context(), state, "")
	if err != nil {
		http.Error(w, "Failed to initiate OIDC login: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *AuthHandler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.oidcClient == nil || !h.oidcClient.IsEnabled() {
		http.Error(w, "OIDC / SSO is not configured", http.StatusServiceUnavailable)
		return
	}

	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value == "" {
		http.Error(w, "Missing or expired OIDC state parameter", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(stateCookie.Value, ":", 2)
	expectedState := parts[0]
	next := "/"
	if len(parts) > 1 && parts[1] != "" {
		next = parts[1]
	}

	incomingState := r.URL.Query().Get("state")
	if incomingState != expectedState {
		http.Error(w, "Invalid OIDC state parameter (CSRF protection)", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing authorization code from OIDC provider", http.StatusBadRequest)
		return
	}

	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})

	tokenResp, err := h.oidcClient.ExchangeCode(r.Context(), code, "")
	if err != nil {
		http.Error(w, "Failed to exchange authorization code: "+err.Error(), http.StatusInternalServerError)
		return
	}

	userInfo, err := h.oidcClient.GetUserInfo(r.Context(), tokenResp.AccessToken)
	if err != nil {
		http.Error(w, "Failed to fetch userinfo from OIDC provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var userID int
	var authToken string
	var isActive bool
	err = h.db.QueryRow(`
		SELECT id, auth_token, is_active FROM users
		WHERE oidc_sub = ? OR (email != '' AND email = ?) OR username = ?
	`, userInfo.Sub, userInfo.Email, userInfo.PreferredUsername).Scan(&userID, &authToken, &isActive)

	if err != nil {
		username := userInfo.PreferredUsername
		if username == "" && userInfo.Email != "" {
			username = strings.Split(userInfo.Email, "@")[0]
		}
		if username == "" {
			username = "oidc_" + userInfo.Sub
		}

		newToken := auth.GenerateToken(40)
		res, err := h.db.Exec(`
			INSERT INTO users (username, email, first_name, last_name, oidc_sub, is_active, password_hash, auth_token)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		`, username, userInfo.Email, userInfo.GivenName, userInfo.FamilyName, userInfo.Sub, "oidc_auth_no_password", newToken)
		if err != nil {
			http.Error(w, "Failed to auto-provision user: "+err.Error(), http.StatusInternalServerError)
			return
		}
		id64, _ := res.LastInsertId()
		userID = int(id64)
		authToken = newToken
	} else if !isActive {
		http.Error(w, "Your user account is disabled", http.StatusForbidden)
		return
	} else {
		_, _ = h.db.Exec("UPDATE users SET oidc_sub = ? WHERE id = ?", userInfo.Sub, userID)
		if authToken == "" {
			authToken = auth.GenerateToken(40)
			_, _ = h.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", authToken, userID)
		}
	}

	maxAge := 30 * 24 * 3600 // 30 days
	http.SetCookie(w, &http.Cookie{
		Name:     "sessionid",
		Value:    authToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "csrftoken",
		Value:    auth.GenerateToken(32),
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   isSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, next, http.StatusFound)
}
