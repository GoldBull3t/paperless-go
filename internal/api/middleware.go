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
				ctx := context.WithValue(r.Context(), UserContextKey, user)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func GetUserFromContext(ctx context.Context) *models.User {
	if val := ctx.Value(UserContextKey); val != nil {
		if user, ok := val.(*models.User); ok {
			return user
		}
	}
	return nil
}
