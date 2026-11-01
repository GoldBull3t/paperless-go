package consumer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuth2_TokenRefreshAndSASL(t *testing.T) {
	// Mock OAuth2 Token Endpoint (e.g. Google or Microsoft)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}
		if r.Form.Get("refresh_token") != "valid-refresh-token" {
			http.Error(w, "invalid refresh_token", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fresh-access-token-12345",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	// 1. Successful Refresh
	token, err := RefreshOAuth2Token(server.URL, "my-client-id", "my-client-secret", "valid-refresh-token")
	if err != nil {
		t.Fatalf("RefreshOAuth2Token failed: %v", err)
	}
	if token != "fresh-access-token-12345" {
		t.Errorf("Expected token 'fresh-access-token-12345', got %q", token)
	}

	// 2. Failed Refresh (Bad refresh token)
	_, err = RefreshOAuth2Token(server.URL, "my-client-id", "my-client-secret", "invalid-refresh-token")
	if err == nil {
		t.Errorf("Expected error with invalid refresh token, got nil")
	}

	// 3. Test SASL XOAUTH2 client structure
	saslClient := NewXOAuth2Client("user@example.com", token)
	mech, ir, err := saslClient.Start()
	if err != nil {
		t.Fatalf("saslClient.Start failed: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("Expected mechanism XOAUTH2, got %q", mech)
	}
	expectedIR := "user=user@example.com\x01auth=Bearer fresh-access-token-12345\x01\x01"
	if string(ir) != expectedIR {
		t.Errorf("Expected IR %q, got %q", expectedIR, string(ir))
	}
}

