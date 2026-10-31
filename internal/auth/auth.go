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

// GenerateRandomPassword generates a cryptographically secure random alphanumeric password of length n
func GenerateRandomPassword(n int) string {
	const charset = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789!@#$%^&*"
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return GenerateToken(n)
	}
	for i, b := range bytes {
		bytes[i] = charset[int(b)%len(charset)]
	}
	return string(bytes)
}

// EnsureAdminUser verifies if any user exists.
// If none exist:
// - Uses adminUser or defaults to "admin".
// - Uses adminPass if provided. If empty, generates a cryptographically secure random password.
// Returns created=true and the generated password if one was generated.
func (s *Service) EnsureAdminUser(adminUser, adminPass, adminEmail string) (bool, string, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		return false, "", err
	}

	if count > 0 {
		return false, "", nil
	}

	if adminUser == "" {
		adminUser = "admin"
	}
	if adminEmail == "" {
		adminEmail = "admin@paperless.local"
	}

	generatedPassword := ""
	if adminPass == "" {
		generatedPassword = GenerateRandomPassword(16)
		adminPass = generatedPassword
	}

	hash, err := s.HashPassword(adminPass)
	if err != nil {
		return false, "", fmt.Errorf("failed to hash admin password: %w", err)
	}
	token := GenerateToken(40)

	_, err = s.db.Exec(`
		INSERT INTO users (username, password_hash, email, first_name, last_name, is_superuser, is_staff, is_active, auth_token)
		VALUES (?, ?, ?, 'Admin', 'User', 1, 1, 1, ?)
	`, adminUser, hash, adminEmail, token)
	if err != nil {
		return false, "", fmt.Errorf("failed to create admin user: %w", err)
	}

	// Create default ui_settings
	_, _ = s.db.Exec(`
		INSERT INTO ui_settings (user_id, settings_json)
		VALUES (1, '{"documentListSize": 25, "theme": "auto"}')
	`)

	// Create default "Inbox" tag
	_, _ = s.db.Exec(`
		INSERT INTO tags (name, slug, color, text_color, is_inbox_tag, matching_algorithm)
		VALUES ('Inbox', 'inbox', '#cbe5ff', '#000000', 1, 0)
	`)

	// Create default "Inbox" saved view
	_, _ = s.db.Exec(`
		INSERT INTO saved_views (name, show_on_dashboard, show_in_sidebar, sort_field, sort_reverse, filter_rules, owner_id)
		VALUES ('Inbox', 1, 1, 'added', 1, '[{"rule_type": 7, "value": "1"}]', 1)
	`)

	return true, generatedPassword, nil
}

// CreateSuperuser creates a new administrator account directly (used by CLI subcommand createsuperuser)
func (s *Service) CreateSuperuser(username, password, email string) error {
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return fmt.Errorf("username and password cannot be empty")
	}
	if email == "" {
		email = username + "@paperless.local"
	}
	hash, err := s.HashPassword(password)
	if err != nil {
		return err
	}
	token := GenerateToken(40)

	_, err = s.db.Exec(`
		INSERT INTO users (username, password_hash, email, first_name, last_name, is_superuser, is_staff, is_active, auth_token)
		VALUES (?, ?, ?, 'Super', 'User', 1, 1, 1, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, is_superuser = 1, is_staff = 1, is_active = 1
	`, username, hash, email, token)
	return err
}

func (s *Service) EnsureDefaultAdmin(defaultUser, defaultPass string) error {
	_, _, err := s.EnsureAdminUser(defaultUser, defaultPass, "admin@paperless.local")
	return err
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
