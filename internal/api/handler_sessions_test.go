package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"paperless-go/internal/auth"
	"paperless-go/internal/config"
	"paperless-go/internal/database"
)

func TestSessions_ListAndRevoke(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := database.Open(filepath.Join(tmpDir, "test_sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := &config.Config{MediaDir: tmpDir, DataDir: tmpDir}
	authSvc := auth.NewService(db, cfg)

	// Create user
	_ = authSvc.CreateSuperuser("admin", "AdminPassword123!", "admin@test.local")
	var userID int
	_ = db.QueryRow("SELECT id FROM users WHERE username = 'admin'").Scan(&userID)

	authH := NewAuthHandler(db, authSvc, nil)

	r := chi.NewRouter()
	r.Use(AuthMiddleware(authSvc))
	r.Use(RequireAuth)
	r.Get("/api/profile/sessions/", authH.ListSessions)
	r.Delete("/api/profile/sessions/{id}/", authH.RevokeSession)
	r.Delete("/api/profile/sessions/other/", authH.RevokeOtherSessions)

	tokenCurrent := "token-device-current-1111111111"
	tokenOther := "token-device-other-2222222222"

	// Create two sessions
	reqA := httptest.NewRequest("GET", "/", nil)
	reqA.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0")
	_ = authSvc.CreateSession(userID, tokenCurrent, reqA)

	reqB := httptest.NewRequest("GET", "/", nil)
	reqB.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Safari/605.1.15")
	_ = authSvc.CreateSession(userID, tokenOther, reqB)

	// 1. List Sessions using current token
	reqList := httptest.NewRequest("GET", "/api/profile/sessions/", nil)
	reqList.Header.Set("Authorization", "Token "+tokenCurrent)
	rrList := httptest.NewRecorder()
	r.ServeHTTP(rrList, reqList)

	if rrList.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rrList.Code, rrList.Body.String())
	}

	var sessions []auth.SessionInfo
	_ = json.Unmarshal(rrList.Body.Bytes(), &sessions)
	if len(sessions) != 2 {
		t.Fatalf("Expected 2 sessions, got %d", len(sessions))
	}

	foundCurrent := false
	var otherID int
	for _, s := range sessions {
		if s.IsCurrent {
			foundCurrent = true
		} else {
			otherID = s.ID
		}
	}
	if !foundCurrent {
		t.Errorf("Expected one session to be marked as IsCurrent=true")
	}

	// 2. Revoke other session by ID
	reqRevoke := httptest.NewRequest("DELETE", "/api/profile/sessions/other/", nil)
	reqRevoke.Header.Set("Authorization", "Token "+tokenCurrent)
	rrRevoke := httptest.NewRecorder()
	r.ServeHTTP(rrRevoke, reqRevoke)

	if rrRevoke.Code != http.StatusNoContent {
		t.Errorf("Expected 204 No Content for revoke other, got %d", rrRevoke.Code)
	}

	// Verify only 1 session remains
	sessionsRemaining, _ := authSvc.ListUserSessions(userID, tokenCurrent)
	if len(sessionsRemaining) != 1 {
		t.Errorf("Expected 1 session remaining after revoking others, got %d", len(sessionsRemaining))
	}

	_ = otherID
}
