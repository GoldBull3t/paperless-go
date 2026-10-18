package api

import (
	"context"
	"net/http"

	"paperless-go/internal/auth"
	"paperless-go/internal/models"
)

type contextKey string

const UserContextKey contextKey = "user"

func AuthMiddleware(authSvc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Ensure CSRF cookie exists (Angular looks for csrftoken cookie)
			if _, err := r.Cookie("csrftoken"); err != nil {
				http.SetCookie(w, &http.Cookie{
					Name:     "csrftoken",
					Value:    auth.GenerateToken(32),
					Path:     "/",
					HttpOnly: false, // Angular needs to read it for X-CSRFToken header
					SameSite: http.SameSiteLaxMode,
				})
			}

			user, err := authSvc.AuthenticateRequest(r)
			if err == nil && user != nil {
				if _, err := r.Cookie("sessionid"); err != nil && user.AuthToken != nil {
					http.SetCookie(w, &http.Cookie{
						Name:     "sessionid",
						Value:    *user.AuthToken,
						Path:     "/",
						HttpOnly: true,
						SameSite: http.SameSiteLaxMode,
					})
				}

				ctx := context.WithValue(r.Context(), UserContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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
