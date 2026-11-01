package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type SessionInfo struct {
	ID           int       `json:"id"`
	UserID       int       `json:"user_id"`
	DeviceName   string    `json:"device_name"`
	IPAddress    string    `json:"ip_address"`
	UserAgent    string    `json:"user_agent"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	IsCurrent    bool      `json:"is_current"`
}

// HashToken generates a secure SHA-256 hash of a session/auth token
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ParseDeviceFromUserAgent extracts a clean human-readable device name from a User-Agent header
func ParseDeviceFromUserAgent(ua string) string {
	if ua == "" {
		return "Unknown Device"
	}
	browser := "Browser"
	if strings.Contains(ua, "Firefox") {
		browser = "Firefox"
	} else if strings.Contains(ua, "Chrome") || strings.Contains(ua, "CriOS") {
		browser = "Chrome"
	} else if strings.Contains(ua, "Safari") && !strings.Contains(ua, "Chrome") {
		browser = "Safari"
	} else if strings.Contains(ua, "Edge") || strings.Contains(ua, "Edg") {
		browser = "Edge"
	}

	osName := "Device"
	if strings.Contains(ua, "Windows") {
		osName = "Windows"
	} else if strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS") {
		osName = "macOS"
	} else if strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") {
		osName = "iOS"
	} else if strings.Contains(ua, "Android") {
		osName = "Android"
	} else if strings.Contains(ua, "Linux") {
		osName = "Linux"
	}

	return browser + " on " + osName
}

// CreateSession registers or updates an active device session
func (s *Service) CreateSession(userID int, token string, r *http.Request) error {
	tokenHash := HashToken(token)
	ip := getIP(r)
	ua := ""
	if r != nil {
		ua = r.UserAgent()
	}
	deviceName := ParseDeviceFromUserAgent(ua)

	_, err := s.db.Exec(`
		INSERT INTO user_sessions (user_id, token_hash, device_name, ip_address, user_agent, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(token_hash) DO UPDATE SET
			last_active_at = CURRENT_TIMESTAMP,
			ip_address = excluded.ip_address,
			user_agent = excluded.user_agent,
			device_name = excluded.device_name
	`, userID, tokenHash, deviceName, ip, ua)
	return err
}

// ListUserSessions returns all active sessions for a user
func (s *Service) ListUserSessions(userID int, currentToken string) ([]SessionInfo, error) {
	currentHash := HashToken(currentToken)
	rows, err := s.db.Query(`
		SELECT id, user_id, token_hash, device_name, ip_address, user_agent, created_at, last_active_at
		FROM user_sessions WHERE user_id = ?
		ORDER BY last_active_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var s SessionInfo
		var tHash string
		if err := rows.Scan(&s.ID, &s.UserID, &tHash, &s.DeviceName, &s.IPAddress, &s.UserAgent, &s.CreatedAt, &s.LastActiveAt); err == nil {
			s.IsCurrent = (tHash == currentHash)
			sessions = append(sessions, s)
		}
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	return sessions, nil
}

// RevokeSession terminates a specific device session
func (s *Service) RevokeSession(userID, sessionID int) error {
	_, err := s.db.Exec("DELETE FROM user_sessions WHERE id = ? AND user_id = ?", sessionID, userID)
	return err
}

// RevokeOtherSessions terminates all sessions for this user except the current one
func (s *Service) RevokeOtherSessions(userID int, currentToken string) error {
	currentHash := HashToken(currentToken)
	_, err := s.db.Exec("DELETE FROM user_sessions WHERE user_id = ? AND token_hash != ?", userID, currentHash)
	return err
}

func getIP(r *http.Request) string {
	if r == nil {
		return "127.0.0.1"
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return r.RemoteAddr
}

