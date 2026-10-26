package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOIDCClient(t *testing.T) {
	// Mock OIDC Identity Provider
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token": "mock_token", "token_type": "Bearer", "expires_in": 3600}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sub": "user_12345", "email": "test@example.com", "preferred_username": "john_doe", "name": "John Doe"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(Config{
		Enabled:      true,
		ClientID:     "paperless-app",
		ClientSecret: "secret123",
		AuthURL:      server.URL + "/auth",
		TokenURL:     server.URL + "/token",
		UserInfoURL:  server.URL + "/userinfo",
		RedirectURI:  "http://localhost:8000/api/auth/oidc/callback",
	})

	if !client.IsEnabled() {
		t.Fatal("Expected client to be enabled")
	}

	state := GenerateRandomState()
	authURL, err := client.GetAuthURL(context.Background(), state, "")
	if err != nil {
		t.Fatalf("Failed to get auth URL: %v", err)
	}
	if !strings.Contains(authURL, "client_id=paperless-app") || !strings.Contains(authURL, "state="+state) {
		t.Fatalf("Malformed auth URL: %s", authURL)
	}

	// Test code exchange
	tok, err := client.ExchangeCode(context.Background(), "mock_code", "")
	if err != nil {
		t.Fatalf("Token exchange failed: %v", err)
	}
	if tok.AccessToken != "mock_token" {
		t.Fatalf("Expected token 'mock_token', got %s", tok.AccessToken)
	}

	// Test userinfo
	info, err := client.GetUserInfo(context.Background(), tok.AccessToken)
	if err != nil {
		t.Fatalf("Userinfo failed: %v", err)
	}
	if info.Email != "test@example.com" || info.Sub != "user_12345" || info.PreferredUsername != "john_doe" {
		t.Fatalf("Unexpected userinfo: %+v", info)
	}
}

