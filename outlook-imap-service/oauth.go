package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	OAuthClientID      = "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
	OAuthScope         = "https://graph.microsoft.com/Mail.Read"
	OAuthDeviceScope   = "openid profile email offline_access https://graph.microsoft.com/Mail.Read"
	deviceCodeEndpoint = "https://login.microsoftonline.com/common/oauth2/v2.0/devicecode"
	tokenEndpoint      = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	refreshSkew        = 5 * time.Minute
	refreshRetry       = 30 * time.Second
)

type OAuthManager struct {
	refreshToken     string
	refreshTokenFile string
	accessToken      string
	expiresAt        time.Time
	mu               sync.Mutex
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

type oauthTokenResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
}

func NewOAuthManager(refreshToken, refreshTokenFile string) *OAuthManager {
	return &OAuthManager{
		refreshToken:     refreshToken,
		refreshTokenFile: refreshTokenFile,
	}
}

func (m *OAuthManager) StartAutoRefresh() {
	go func() {
		for {
			_, expiresAt, err := m.RefreshAccessToken()
			if err != nil {
				log.Printf("[MAIL] OAuth refresh timer error: %v", err)
				time.Sleep(refreshRetry)
				continue
			}

			wait := time.Until(expiresAt.Add(-refreshSkew))
			if wait < refreshRetry {
				wait = refreshRetry
			}
			log.Printf("[MAIL] OAuth token refreshed; next refresh in %s", wait.Round(time.Second))
			time.Sleep(wait)
		}
	}()
}

func (m *OAuthManager) GetAccessToken() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Add 1-minute buffer for expiration
	if m.accessToken != "" && time.Now().Before(m.expiresAt.Add(-1*time.Minute)) {
		return m.accessToken, nil
	}

	return m.refreshLocked()
}

func (m *OAuthManager) RefreshAccessToken() (string, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	token, err := m.refreshLocked()
	if err != nil {
		return "", time.Time{}, err
	}
	return token, m.expiresAt, nil
}

func (m *OAuthManager) refreshLocked() (string, error) {
	if m.refreshToken == "" {
		return m.deviceFlowLocked("refresh token file is missing or empty")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {OAuthClientID},
		"refresh_token": {m.refreshToken},
		"scope":         {OAuthScope},
	}

	tokenReq, _ := http.NewRequest("POST", tokenEndpoint, strings.NewReader(form.Encode()))
	tokenReq.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("failed to refresh token: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		if shouldRunDeviceFlow(tokenResp, body) {
			reason := tokenResp.Error
			if tokenResp.ErrorDescription != "" {
				reason += ": " + tokenResp.ErrorDescription
			}
			return m.deviceFlowLocked(reason)
		}
		return "", fmt.Errorf("token refresh failed: %s", string(body))
	}

	return m.applyTokenLocked(tokenResp)
}

func (m *OAuthManager) applyTokenLocked(tokenResp oauthTokenResponse) (string, error) {
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token refresh returned empty access token")
	}

	m.accessToken = tokenResp.AccessToken
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	m.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	if tokenResp.RefreshToken != "" && tokenResp.RefreshToken != m.refreshToken {
		m.refreshToken = tokenResp.RefreshToken
		if err := writeRefreshTokenFile(m.refreshTokenFile, tokenResp.RefreshToken); err != nil {
			return "", fmt.Errorf("failed to persist refresh token: %v", err)
		}
	}

	return m.accessToken, nil
}

func (m *OAuthManager) deviceFlowLocked(reason string) (string, error) {
	if reason == "" {
		reason = "refresh token is unavailable"
	}
	log.Printf("[MAIL] OAuth device flow required: %s", reason)
	tokenResp, err := runDeviceFlow()
	if err != nil {
		return "", err
	}
	if tokenResp.RefreshToken == "" {
		return "", fmt.Errorf("device flow returned empty refresh token")
	}
	return m.applyTokenLocked(tokenResp)
}

func runDeviceFlow() (oauthTokenResponse, error) {
	resp, err := http.PostForm(deviceCodeEndpoint, url.Values{
		"client_id": {OAuthClientID},
		"scope":     {OAuthDeviceScope},
	})
	if err != nil {
		return oauthTokenResponse{}, fmt.Errorf("failed to request device code: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var dcResp deviceCodeResponse
	if err := json.Unmarshal(body, &dcResp); err != nil {
		return oauthTokenResponse{}, fmt.Errorf("failed to parse device code response: %v", err)
	}
	if dcResp.DeviceCode == "" {
		return oauthTokenResponse{}, fmt.Errorf("device code request failed: %s", string(body))
	}

	if dcResp.Message != "" {
		log.Printf("[MAIL] %s", dcResp.Message)
	} else {
		log.Printf("[MAIL] Open %s and enter code %s", dcResp.VerificationURI, dcResp.UserCode)
	}

	interval := time.Duration(dcResp.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresAt := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)
	for time.Now().Before(expiresAt) {
		time.Sleep(interval)

		tokenResp, err := pollDeviceToken(dcResp.DeviceCode)
		if err != nil {
			return oauthTokenResponse{}, err
		}
		switch tokenResp.Error {
		case "":
			if tokenResp.AccessToken == "" {
				return oauthTokenResponse{}, fmt.Errorf("device flow returned empty access token")
			}
			log.Println("[MAIL] OAuth device flow completed")
			return tokenResp, nil
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		default:
			return oauthTokenResponse{}, fmt.Errorf("device flow failed: %s %s", tokenResp.Error, tokenResp.ErrorDescription)
		}
	}

	return oauthTokenResponse{}, fmt.Errorf("device flow expired before authorization completed")
}

func pollDeviceToken(deviceCode string) (oauthTokenResponse, error) {
	tokenReq, _ := http.NewRequest("POST", tokenEndpoint, strings.NewReader(url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {OAuthClientID},
		"device_code": {deviceCode},
	}.Encode()))
	tokenReq.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return oauthTokenResponse{}, fmt.Errorf("device flow token request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return oauthTokenResponse{}, fmt.Errorf("failed to parse device token response: %v", err)
	}
	if resp.StatusCode == http.StatusOK || tokenResp.Error != "" {
		return tokenResp, nil
	}
	return oauthTokenResponse{}, fmt.Errorf("device flow token request failed: %s", string(body))
}

func shouldRunDeviceFlow(tokenResp oauthTokenResponse, body []byte) bool {
	text := strings.ToLower(tokenResp.Error + " " + tokenResp.ErrorDescription + " " + string(body))
	if strings.Contains(text, "invalid_grant") {
		return true
	}
	return strings.Contains(text, "expired") ||
		strings.Contains(text, "revoked") ||
		strings.Contains(text, "interaction_required") ||
		strings.Contains(text, "consent_required") ||
		strings.Contains(text, "aadsts700082")
}

func writeRefreshTokenFile(path string, token string) error {
	if path == "" || token == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}
