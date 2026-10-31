package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"paperless-go/internal/config"
	"paperless-go/internal/database"
	"paperless-go/internal/models"
)

type Service struct {
	db  *database.DB
	cfg *config.Config
}

func NewService(db *database.DB, cfg *config.Config) *Service {
	return &Service{db: db, cfg: cfg}
}

func (s *Service) HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func (s *Service) CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) EnsureDefaultAdmin(defaultUser, defaultPass string) error {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		hash, err := s.HashPassword(defaultPass)
		if err != nil {
			return err
		}
		token := GenerateToken(40)

		_, err = s.db.Exec(`
			INSERT INTO users (username, password_hash, email, first_name, last_name, is_superuser, is_staff, is_active, auth_token)
			VALUES (?, ?, 'admin@paperless.local', 'Admin', 'User', 1, 1, 1, ?)
		`, defaultUser, hash, token)
		if err != nil {
			return fmt.Errorf("failed to create default admin: %w", err)
		}

		// Also create default ui_settings
		_, _ = s.db.Exec(`
			INSERT INTO ui_settings (user_id, settings_json)
			VALUES (1, '{"documentListSize": 25, "theme": "auto"}')
		`)

		// Also create default "Inbox" tag
		_, _ = s.db.Exec(`
			INSERT INTO tags (name, slug, color, text_color, is_inbox_tag, matching_algorithm)
			VALUES ('Inbox', 'inbox', '#cbe5ff', '#000000', 1, 0)
		`)

		// Create default "Inbox" saved view
		_, _ = s.db.Exec(`
			INSERT INTO saved_views (name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id)
			VALUES ('Inbox', 1, 1, 'added', 1, '[{"rule_type": 7, "value": "1"}]', 1)
		`)
	}
	return nil
}

func (s *Service) AuthenticateRequest(r *http.Request) (*models.User, error) {
	// 1. Check Authorization header (Token <key> or Bearer <key>)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && (strings.EqualFold(parts[0], "Token") || strings.EqualFold(parts[0], "Bearer")) {
			token := parts[1]
			var user models.User
			err := s.db.QueryRow(`
				SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined
				FROM users WHERE auth_token = ? AND is_active = 1
			`, token).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName, &user.IsSuperuser, &user.IsStaff, &user.IsActive, &user.DateJoined)
			if err == nil {
				return &user, nil
			}
		}
	}

	// 2. Check session cookie
	cookie, err := r.Cookie("sessionid")
	if err == nil && cookie.Value != "" {
		var user models.User
		err := s.db.QueryRow(`
			SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined
			FROM users WHERE auth_token = ? AND is_active = 1
		`, cookie.Value).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName, &user.IsSuperuser, &user.IsStaff, &user.IsActive, &user.DateJoined)
		if err == nil {
			return &user, nil
		}
	}

	// 3. Fallback auto-auth ONLY IF explicitly enabled via PAPERLESS_AUTO_LOGIN (default true)
	if s.cfg != nil && s.cfg.AutoLogin {
		var user models.User
		var token sql.NullString
		err = s.db.QueryRow(`
			SELECT id, username, email, first_name, last_name, is_superuser, is_staff, is_active, date_joined, auth_token
			FROM users WHERE is_superuser = 1 AND is_active = 1 LIMIT 1
		`).Scan(&user.ID, &user.Username, &user.Email, &user.FirstName, &user.LastName, &user.IsSuperuser, &user.IsStaff, &user.IsActive, &user.DateJoined, &token)
		if err == nil {
			tVal := token.String
			if !token.Valid || tVal == "" {
				tVal = GenerateToken(40)
				_, _ = s.db.Exec("UPDATE users SET auth_token = ? WHERE id = ?", tVal, user.ID)
			}
			user.AuthToken = &tVal
			return &user, nil
		}
	}

	return nil, fmt.Errorf("unauthorized")
}

func GenerateToken(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		return "fallback-token-random-paperless-go"
	}
	return hex.EncodeToString(bytes)
}
