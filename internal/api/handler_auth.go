package api

import (
	"encoding/json"
	"net/http"

	"paperless-go/internal/auth"
	"paperless-go/internal/database"
)

type AuthHandler struct {
	db      *database.DB
	authSvc *auth.Service
}

func NewAuthHandler(db *database.DB, authSvc *auth.Service) *AuthHandler {
	return &AuthHandler{db: db, authSvc: authSvc}
}

type TokenRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type TokenResponse struct {
	Token string `json:"token"`
}

func (h *AuthHandler) GetToken(w http.ResponseWriter, r *http.Request) {
	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"non_field_errors": ["Unable to log in with provided credentials."]}`, http.StatusBadRequest)
		return
	}

	var id int
	var hash, token string
	err := h.db.QueryRow("SELECT id, password_hash, auth_token FROM users WHERE username = ? AND is_active = 1", req.Username).Scan(&id, &hash, &token)
	if err != nil || !h.authSvc.CheckPassword(hash, req.Password) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"non_field_errors": ["Unable to log in with provided credentials."]}`))
		return
	}

	if token == "" {
		token = auth.GenerateToken(40)
		_, _ = h.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", token, id)
	}

	// Also set sessionid cookie for browser compatibility
	http.SetCookie(w, &http.Cookie{
		Name:     "sessionid",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TokenResponse{Token: token})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sessionid",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
