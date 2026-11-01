package consumer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
)

type xoauth2Client struct {
	Username string
	Token    string
}

// NewXOAuth2Client creates an RFC-compliant SASL XOAUTH2 client for Google Workspace and Microsoft 365
func NewXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{Username: username, Token: token}
}

func (a *xoauth2Client) Start() (mech string, ir []byte, err error) {
	return "XOAUTH2", []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.Username, a.Token)), nil
}

func (a *xoauth2Client) Next(challenge []byte) (response []byte, err error) {
	return nil, nil
}

// RefreshOAuth2Token requests a new access token from the identity provider using a refresh token
func RefreshOAuth2Token(tokenURL, clientID, clientSecret, refreshToken string) (string, error) {
	if tokenURL == "" {
		return "", fmt.Errorf("oauth_token_url is not configured")
	}

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("refresh_token", refreshToken)

	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("oauth token refresh returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var res struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("failed decoding oauth token response: %w", err)
	}
	if res.AccessToken == "" {
		return "", fmt.Errorf("no access_token returned in oauth response")
	}
	return res.AccessToken, nil
}

