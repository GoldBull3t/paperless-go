package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Enabled      bool
	ProviderName string
	ClientID     string
	ClientSecret string
	DiscoveryURL string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	RedirectURI  string
	Scopes       []string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token,omitempty"`
}

type UserInfo struct {
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	GivenName         string `json:"given_name"`
	FamilyName        string `json:"family_name"`
}

type Client struct {
	cfg        Config
	httpClient *http.Client
	mu         sync.RWMutex
	discovered bool
}

func NewClient(cfg Config) *Client {
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = "OpenID Connect"
	}
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) IsEnabled() bool {
	return c.cfg.Enabled && c.cfg.ClientID != ""
}

func (c *Client) ProviderName() string {
	return c.cfg.ProviderName
}

func (c *Client) ensureDiscovery(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.discovered || c.cfg.DiscoveryURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.cfg.DiscoveryURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("OIDC discovery request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC discovery returned HTTP %d", resp.StatusCode)
	}

	var discovery struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		UserinfoEndpoint      string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&discovery); err != nil {
		return fmt.Errorf("failed to decode discovery JSON: %w", err)
	}

	if discovery.AuthorizationEndpoint != "" {
		c.cfg.AuthURL = discovery.AuthorizationEndpoint
	}
	if discovery.TokenEndpoint != "" {
		c.cfg.TokenURL = discovery.TokenEndpoint
	}
	if discovery.UserinfoEndpoint != "" {
		c.cfg.UserInfoURL = discovery.UserinfoEndpoint
	}
	c.discovered = true
	return nil
}

func (c *Client) GetAuthURL(ctx context.Context, state, redirectURI string) (string, error) {
	if err := c.ensureDiscovery(ctx); err != nil {
		return "", err
	}
	if c.cfg.AuthURL == "" {
		return "", fmt.Errorf("OIDC AuthURL is not configured")
	}

	u, err := url.Parse(c.cfg.AuthURL)
	if err != nil {
		return "", err
	}

	rURI := redirectURI
	if rURI == "" {
		rURI = c.cfg.RedirectURI
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", rURI)
	q.Set("scope", strings.Join(c.cfg.Scopes, " "))
	q.Set("state", state)
	u.RawQuery = q.Encode()

	return u.String(), nil
}

func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) (*TokenResponse, error) {
	if err := c.ensureDiscovery(ctx); err != nil {
		return nil, err
	}
	if c.cfg.TokenURL == "" {
		return nil, fmt.Errorf("OIDC TokenURL is not configured")
	}

	rURI := redirectURI
	if rURI == "" {
		rURI = c.cfg.RedirectURI
	}

	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", rURI)
	data.Set("client_id", c.cfg.ClientID)
	data.Set("client_secret", c.cfg.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST", c.cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var token TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, err
	}
	return &token, nil
}

func (c *Client) GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	if err := c.ensureDiscovery(ctx); err != nil {
		return nil, err
	}
	if c.cfg.UserInfoURL == "" {
		return nil, fmt.Errorf("OIDC UserInfoURL is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.cfg.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var user UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func GenerateRandomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

