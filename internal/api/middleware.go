package api

import (
	"context"
	"net/http"
	"strings"

	"paperless-go/internal/auth"
	"paperless-go/internal/models"
)

type contextKey string

const UserContextKey contextKey = "user"
const AuthMethodContextKey contextKey = "auth_method"

// SecurityHeadersMiddleware adds modern security headers to all responses
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")

		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware authenticates requests via Token header or sessionid cookie
func AuthMiddleware(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"

			// Ensure CSRF cookie exists (Angular reads csrftoken cookie to set X-CSRFToken header)
			if _, err := r.Cookie("csrftoken"); err != nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "csrftoken",
					Value:    auth.GenerateToken(32),
					Path:     "/",
					HttpOnly: false, // Angular frontend reads this cookie
					Secure:   isSecure,
					SameSite: http.SameSiteLaxMode,
				})
			}

			user, err := authSvc.AuthenticateRequest(r)
			if err == nil && user != nil {
				authMethod := "token"
				if r.Header.Get("Authorization") == "" {
					authMethod = "cookie"
				}

				if _, err := r.Cookie("sessionid"); err != nil && user.AuthToken != nil {
					http.SetCookie(w, &http.Cookie{
						Name:     "sessionid",
						Value:    *user.AuthToken,
						Path:     "/",
						HttpOnly: true,
						Secure:   isSecure,
						SameSite: http.SameSiteLaxMode,
					})
				}

				ctx := context.WithValue(r.Context(), UserContextKey, user)
				ctx = context.WithValue(ctx, AuthMethodContextKey, authMethod)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CSRFMiddleware validates X-CSRFToken for cookie-authenticated state-changing requests
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		// Safe methods do not require CSRF verification
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == http.MethodTrace {
			next.ServeHTTP(w, r)
			return
		}

		// Stateless token authentication is immune to browser CSRF
		if authHeader := r.Header.Get("Authorization"); authHeader != "" {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path
		// Public auth & webhook endpoints are exempt
		if path == "/accounts/login/" || path == "/accounts/logout/" || path == "/api/token/" || strings.HasPrefix(path, "/share/") || strings.HasPrefix(path, "/ws/") {
			next.ServeHTTP(w, r)
			return
		}

		// Only enforce CSRF for requests authenticated via browser sessionid cookie
		if _, err := r.Cookie("sessionid"); err == nil {
			csrfCookie, err := r.Cookie("csrftoken")
			csrfHeader := r.Header.Get("X-CSRFToken")
			if err != nil || csrfCookie.Value == "" || csrfHeader == "" || csrfCookie.Value != csrfHeader {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"detail": "CSRF Failed: CSRF token missing or incorrect."}`))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUserFromContext(r.Context())
		if user == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail": "Authentication credentials were not provided."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func GetUserFromContext(ctx context.Context) *models.User {
	if val := ctx.Value(UserContextKey); val != nil {
		if user, ok := val.(*models.User); ok {
			return user
		}
	}
	return nil
}
