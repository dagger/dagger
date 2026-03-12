package llmconfig

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// Claude Code OAuth configuration
	oauthClientID    = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	oauthAuthorize   = "https://claude.ai/oauth/authorize"
	oauthTokenURL    = "https://console.anthropic.com/v1/oauth/token"
	oauthRedirectURI = "https://console.anthropic.com/oauth/code/callback"
	oauthScopes      = "org:create_api_key user:profile user:inference"
)

// OAuthTokenResponse represents the token endpoint response.
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// GenerateOAuthURL generates a PKCE-protected OAuth authorization URL.
// Returns the URL the user should visit and the PKCE verifier for later use.
func GenerateOAuthURL() (authURL, verifier string, err error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE: %w", err)
	}

	params := url.Values{
		"code":                  {"true"},
		"client_id":             {oauthClientID},
		"response_type":         {"code"},
		"redirect_uri":          {oauthRedirectURI},
		"scope":                 {oauthScopes},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {verifier},
	}

	return oauthAuthorize + "?" + params.Encode(), verifier, nil
}

// ExchangeOAuthCode exchanges an authorization code for tokens.
// The authCode should be in the format "code#state" as provided by the callback.
func ExchangeOAuthCode(authCode, verifier string) (*Provider, error) {
	// The auth code may include a state suffix separated by #
	code := authCode
	state := ""
	for i := len(authCode) - 1; i >= 0; i-- {
		if authCode[i] == '#' {
			code = authCode[:i]
			state = authCode[i+1:]
			break
		}
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     oauthClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  oauthRedirectURI,
		"code_verifier": verifier,
	})
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(oauthTokenURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	// Subtract 5 minutes from expiry for safety margin
	expiryMs := time.Now().UnixMilli() + int64(tokenResp.ExpiresIn)*1000 - 5*60*1000

	return &Provider{
		AuthType:     "oauth",
		AuthToken:    tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenExpiry:  expiryMs,
		Enabled:      true,
	}, nil
}

// RefreshOAuthToken refreshes an expired OAuth token.
func RefreshOAuthToken(provider *Provider) (*Provider, error) {
	if provider.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	body, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     oauthClientID,
		"refresh_token": provider.RefreshToken,
	})
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(oauthTokenURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp OAuthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode refresh response: %w", err)
	}

	expiryMs := time.Now().UnixMilli() + int64(tokenResp.ExpiresIn)*1000 - 5*60*1000

	updated := *provider
	updated.AuthToken = tokenResp.AccessToken
	updated.RefreshToken = tokenResp.RefreshToken
	updated.TokenExpiry = expiryMs
	return &updated, nil
}

// IsTokenExpired checks if the OAuth token has expired.
func IsTokenExpired(provider *Provider) bool {
	if provider.TokenExpiry == 0 {
		return true
	}
	return time.Now().UnixMilli() >= provider.TokenExpiry
}

// generatePKCE generates a PKCE verifier and challenge pair.
func generatePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)

	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])

	return verifier, challenge, nil
}
