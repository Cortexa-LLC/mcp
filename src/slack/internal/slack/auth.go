package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenManager handles OAuth token refresh for Slack user tokens
type TokenManager struct {
	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	clientID     string
	clientSecret string
}

// NewTokenManager creates a new token manager
func NewTokenManager(accessToken, refreshToken, clientID, clientSecret string) *TokenManager {
	return &TokenManager{
		accessToken:  accessToken,
		refreshToken: refreshToken,
		clientID:     clientID,
		clientSecret: clientSecret,
		// Assume token expires soon to trigger refresh on first use
		expiresAt: time.Now(),
	}
}

// TokenRefreshResponse is the Slack OAuth token refresh response
type TokenRefreshResponse struct {
	OK           bool   `json:"ok"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

// GetAccessToken returns a valid access token, refreshing if necessary
func (tm *TokenManager) GetAccessToken() (string, error) {
	tm.mu.RLock()
	// Check if token is still valid (with 5 min buffer)
	if time.Now().Before(tm.expiresAt.Add(-5 * time.Minute)) {
		token := tm.accessToken
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	// Need to refresh
	return tm.refreshAccessToken()
}

func (tm *TokenManager) refreshAccessToken() (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Double-check after acquiring lock
	if time.Now().Before(tm.expiresAt.Add(-5 * time.Minute)) {
		return tm.accessToken, nil
	}

	// If no refresh token, can't refresh
	if tm.refreshToken == "" {
		return tm.accessToken, nil
	}

	// Prepare refresh request
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", tm.refreshToken)

	// Only include client credentials if available
	if tm.clientID != "" && tm.clientSecret != "" {
		data.Set("client_id", tm.clientID)
		data.Set("client_secret", tm.clientSecret)
	}

	req, err := http.NewRequest("POST", "https://slack.com/api/oauth.v2.access", strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read refresh response: %w", err)
	}

	var result TokenRefreshResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse refresh response: %w", err)
	}

	if !result.OK {
		// If refresh failed, keep using existing token
		return tm.accessToken, fmt.Errorf("token refresh failed: %s", result.Error)
	}

	// Update tokens
	tm.accessToken = result.AccessToken
	if result.RefreshToken != "" {
		tm.refreshToken = result.RefreshToken
	}
	if result.ExpiresIn > 0 {
		tm.expiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	}

	return tm.accessToken, nil
}

// UpdateTokens allows manual token updates (e.g., from config file)
func (tm *TokenManager) UpdateTokens(accessToken, refreshToken string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.accessToken = accessToken
	if refreshToken != "" {
		tm.refreshToken = refreshToken
	}
	// Reset expiry to trigger check on next use
	tm.expiresAt = time.Now()
}
