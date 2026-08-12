package llmconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const (
	// OpenAI Codex OAuth configuration
	openaiClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiAuthorize   = "https://auth.openai.com/oauth/authorize"
	openaiRedirectURI = "http://localhost:1455/auth/callback"
	openaiScopes      = "openid profile email offline_access"
)

// var (not a const) so tests can point it at a local server, mirroring the
// ConfigRoot/ConfigFile override pattern.
var openaiTokenURL = "https://auth.openai.com/oauth/token" //nolint:gosec // OAuth token endpoint URL, not a credential

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
func ExchangeOpenAIOAuthCode(ctx context.Context, code, verifier string) (*Provider, error) {
	body := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openaiClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {openaiRedirectURI},
	}

	var tokenResp OpenAITokenResponse
	if err := postOAuthToken(ctx, openaiTokenURL, "token exchange", "application/x-www-form-urlencoded", strings.NewReader(body.Encode()), &tokenResp); err != nil {
		return nil, err
	}

	provider := &Provider{
		AuthType:     "oauth",
		AuthToken:    tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		Enabled:      true,
	}
	provider.setTokenExpiry(tokenResp.ExpiresIn)
	return provider, nil
}

// RefreshOpenAIOAuthToken refreshes an expired OpenAI OAuth token.
func RefreshOpenAIOAuthToken(ctx context.Context, provider *Provider) (*Provider, error) {
	if provider.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {provider.RefreshToken},
		"client_id":     {openaiClientID},
	}

	var tokenResp OpenAITokenResponse
	if err := postOAuthToken(ctx, openaiTokenURL, "token refresh", "application/x-www-form-urlencoded", strings.NewReader(body.Encode()), &tokenResp); err != nil {
		return nil, err
	}

	updated := *provider
	updated.AuthToken = tokenResp.AccessToken
	// Keep the stored refresh token when the response omits it: RFC 6749 §5.1
	// makes the field optional, and erasing it locks the user out for good.
	if tokenResp.RefreshToken != "" {
		updated.RefreshToken = tokenResp.RefreshToken
	}
	updated.setTokenExpiry(tokenResp.ExpiresIn)
	return &updated, nil
}
