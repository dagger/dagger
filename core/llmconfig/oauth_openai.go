package llmconfig

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// OpenAI Codex OAuth configuration
	openaiClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiAuthorize   = "https://auth.openai.com/oauth/authorize"
	openaiTokenURL    = "https://auth.openai.com/oauth/token"
	openaiRedirectURI = "http://localhost:1455/auth/callback"
	openaiScopes      = "openid profile email offline_access"
)

// OpenAITokenResponse represents the OpenAI token endpoint response.
type OpenAITokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// GenerateOpenAIOAuthURL generates a PKCE-protected OAuth authorization URL
// for OpenAI Codex (ChatGPT subscription).
// Returns the URL, the PKCE verifier, and the state parameter.
func GenerateOpenAIOAuthURL() (authURL, verifier, state string, err error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", "", "", fmt.Errorf("generate PKCE: %w", err)
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate state: %w", err)
	}
	state = hex.EncodeToString(buf)

	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openaiClientID},
		"redirect_uri":               {openaiRedirectURI},
		"scope":                      {openaiScopes},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"dagger"},
	}

	return openaiAuthorize + "?" + params.Encode(), verifier, state, nil
}

// ExchangeOpenAIOAuthCode exchanges an authorization code for OpenAI tokens.
func ExchangeOpenAIOAuthCode(code, verifier string) (*Provider, error) {
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openaiClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {openaiRedirectURI},
	}

	resp, err := http.Post(openaiTokenURL, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp OpenAITokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	expiryMs := time.Now().UnixMilli() + int64(tokenResp.ExpiresIn)*1000 - 5*60*1000

	return &Provider{
		AuthType:     "oauth",
		AuthToken:    tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenExpiry:  expiryMs,
		Enabled:      true,
	}, nil
}

// RefreshOpenAIOAuthToken refreshes an expired OpenAI OAuth token.
func RefreshOpenAIOAuthToken(provider *Provider) (*Provider, error) {
	if provider.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {provider.RefreshToken},
		"client_id":     {openaiClientID},
	}

	resp, err := http.Post(openaiTokenURL, "application/x-www-form-urlencoded", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp OpenAITokenResponse
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
