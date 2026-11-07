package llmconfig

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/dagger/dagger/engine/client/secretprovider"
	"github.com/dagger/dagger/internal/version"
)

// Copilot's public OAuth client, also used by GitHub's Copilot language server.
// No Dagger OAuth application or client secret is needed for the device flow.
const copilotClientID = "Iv1.b507a08c87ecfe98"
const copilotTokenEnv = "COPILOT_GITHUB_TOKEN" //nolint:gosec // environment variable name, not a credential
const copilotGitHubTokenField = "github_token" // internal dotenv source, never an API bearer

// Overrideable by package tests; production uses GitHub's published endpoints.
var (
	copilotDeviceCodeURL  = "https://github.com/login/device/code"
	copilotAccessTokenURL = "https://github.com/login/oauth/access_token"      //nolint:gosec // endpoint, not a credential
	copilotTokenURL       = "https://api.github.com/copilot_internal/v2/token" //nolint:gosec // endpoint, not a credential
	copilotAPIBaseURL     = "https://api.githubcopilot.com"
	copilotPollWait       = waitCopilotPoll
)

func copilotHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("User-Agent", "dagger/"+version.Version())
	req.Header.Set("Editor-Version", "dagger/"+version.Version())
}

// RefreshCopilotOAuthToken exchanges a GitHub token for a Copilot API bearer.
// GitHub's published client follows this two-token protocol; see
// https://github.com/github/copilot.vim/blob/release/copilot-language-server/dist/main.js
// (fetchCopilotTokenFromGitHubToken). GitHub also distinguishes GitHub grants
// from direct API tokens in its authentication documentation:
// https://github.com/github/copilot-sdk/blob/main/docs/auth/authenticate.md .
func RefreshCopilotOAuthToken(ctx context.Context, provider *Provider) (*Provider, error) {
	if provider.RefreshToken == "" {
		return nil, errNoRefreshToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenURL, nil)
	if err != nil {
		return nil, err
	}
	copilotHeaders(req)
	req.Header.Set("Authorization", "token "+provider.RefreshToken)
	var payload struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"` // Unix seconds, not a relative lifetime
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if err := copilotJSON(req, &payload); err != nil {
		return nil, err
	}
	if payload.Token == "" || payload.ExpiresAt <= time.Now().Unix() {
		return nil, fmt.Errorf("copilot token exchange returned an empty token or invalid expiry")
	}
	updated := *provider
	updated.AuthType = "oauth"
	updated.AuthToken = payload.Token
	updated.TokenExpiresAt = time.Unix(payload.ExpiresAt, 0).UnixMilli()
	updated.TokenExpiry = time.Unix(payload.ExpiresAt, 0).Add(-tokenExpiryMargin).UnixMilli()
	updated.BaseURL = payload.Endpoints.API
	if updated.BaseURL == "" {
		updated.BaseURL = copilotAPIBaseURL
	}
	return &updated, nil
}

// copilotJSON deliberately omits untrusted response bodies from errors, as
// GitHub may echo credentials. Every call is bounded by oauthHTTPTimeout.
func copilotJSON(req *http.Request, out any) error {
	ctx, cancel := context.WithTimeout(req.Context(), oauthHTTPTimeout)
	defer cancel()
	resp, err := oauthHTTPClient.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("copilot authentication request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if req.URL.String() == copilotTokenURL && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return fmt.Errorf("%w: GitHub rejected the Copilot GitHub token (HTTP %d); run /setup in dagger agent to reauthenticate", ErrOAuthReauthenticationRequired, resp.StatusCode)
		}
		return fmt.Errorf("copilot authentication endpoint returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode Copilot authentication response: %w", err)
	}
	return nil
}

func copilotPost(ctx context.Context, endpoint string, values url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	copilotHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return copilotJSON(req, out)
}

type copilotDeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Error           string `json:"error"`
}

func waitCopilotPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pollCopilotDeviceToken(ctx context.Context, code copilotDeviceCode) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(code.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := copilotPollWait(ctx, interval); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return "", fmt.Errorf("GitHub Copilot device code expired; run /setup in dagger agent again")
			}
			return "", err
		}
		var payload struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		if err := copilotPost(ctx, copilotAccessTokenURL, url.Values{
			"client_id":   {copilotClientID},
			"device_code": {code.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &payload); err != nil {
			return "", err
		}
		switch payload.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			// GitHub's device-flow documentation requires adding five seconds to
			// the previous interval for all subsequent polls.
			interval += 5 * time.Second
		case "access_denied":
			return "", fmt.Errorf("GitHub Copilot authorization denied")
		case "expired_token":
			return "", fmt.Errorf("GitHub Copilot device code expired; run /setup in dagger agent again")
		case "":
			if payload.AccessToken == "" {
				return "", fmt.Errorf("GitHub Copilot authorization returned no token")
			}
			return payload.AccessToken, nil
		default:
			return "", fmt.Errorf("GitHub Copilot device authorization failed")
		}
	}
}

func setupCopilotOAuth(ctx context.Context, ph PromptHandler) (string, *Provider, string, error) {
	var code copilotDeviceCode
	if err := copilotPost(ctx, copilotDeviceCodeURL, url.Values{"client_id": {copilotClientID}, "scope": {"read:user"}}, &code); err != nil {
		return "", nil, "", err
	}
	if code.Error != "" || code.DeviceCode == "" || code.UserCode == "" || code.VerificationURI == "" || code.ExpiresIn <= 0 {
		return "", nil, "", fmt.Errorf("GitHub Copilot returned an invalid device code")
	}
	// Include time spent showing the fallback form in the device lifetime.
	deviceCtx, cancel := context.WithTimeout(ctx, time.Duration(code.ExpiresIn)*time.Second)
	defer cancel()
	instructions := fmt.Sprintf("Visit %s and enter code: %s", code.VerificationURI, code.UserCode)
	if printer, ok := ph.(AbovePrinter); ok {
		printer.PrintAbove("GitHub Copilot authorization\n\n" + instructions + "\n\nWaiting for approval…\n")
	}
	if opener, ok := ph.(BrowserOpener); ok {
		_ = opener.OpenBrowser(code.VerificationURI)
	}
	if _, ok := ph.(AbovePrinter); !ok {
		form := huh.NewForm(huh.NewGroup(huh.NewNote().Title("GitHub Copilot authorization").Description(instructions).Next(true)))
		if err := checkAbort(ph.HandleForm(deviceCtx, form)); err != nil {
			return "", nil, "", err
		}
	}
	githubToken, err := pollCopilotDeviceToken(deviceCtx, code)
	cancel() // Approval ends the device lifetime; later prompts use the caller context.
	if err != nil {
		return "", nil, "", err
	}
	provider, err := RefreshCopilotOAuthToken(ctx, &Provider{AuthType: "oauth", RefreshToken: githubToken, Enabled: true, SubscriptionType: "copilot"})
	if err != nil {
		return "", nil, "", err
	}
	model, err := promptModelSelection(ctx, ph, "github-copilot", DefaultModelForProvider("github-copilot"))
	if err != nil {
		return "", nil, "", err
	}
	if err := promptReasoningEffort(ctx, ph, "github-copilot", model, provider); err != nil {
		return "", nil, "", err
	}
	provider.Model = model
	return "github-copilot", provider, model, nil
}

// copilotEnvCache belongs only to the explicit environment credential. It
// never persists anything or spends the stored login's GitHub token. Guarded
// by oauthRefreshMu, including the network exchange, to coalesce resolutions.
var copilotEnvCache struct {
	sourceFingerprint string
	provider          Provider
	lastAttempt       time.Time
	lastError         error
	rejected          map[string]bool
}

func copilotGitHubToken() string {
	dotEnvSecretsMu.Lock()
	token := dotEnvSecrets["github-copilot/"+copilotGitHubTokenField]
	dotEnvSecretsMu.Unlock()
	if token == "" {
		token = os.Getenv(copilotTokenEnv)
	}
	return token
}

func copilotFingerprint(token string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(token))) }

// resolveCopilotEnvProvider returns the cached credential as one unit,
// including its true expiry and API base URL. The bool reports whether the
// environment overrides the stored login, even when its exchange fails.
func resolveCopilotEnvProvider(ctx context.Context) (*Provider, bool, error) {
	source := copilotGitHubToken()
	if source == "" {
		return nil, false, nil
	}
	token, err := resolveStored(ctx, source)
	if err != nil {
		return nil, true, err
	}
	oauthRefreshMu.Lock()
	defer oauthRefreshMu.Unlock()
	cache := &copilotEnvCache
	fingerprint := copilotFingerprint(string(token))
	if cache.sourceFingerprint != fingerprint {
		cache.sourceFingerprint = fingerprint
		cache.provider = Provider{AuthType: "oauth", RefreshToken: string(token), Enabled: true}
		cache.lastAttempt, cache.lastError = time.Time{}, nil
		// Rejection is about the bearer value, even if its GitHub source
		// changes. Never accept that value from a later exchange either.
		if cache.rejected == nil {
			cache.rejected = map[string]bool{}
		}
	}
	if rejected := secretprovider.RejectedSecretValue(ctx); rejected != "" {
		cache.rejected[rejected] = true
	}
	rejected := cache.rejected[copilotFingerprint(cache.provider.AuthToken)]
	if cache.provider.AuthToken != "" && !rejected && !IsTokenExpired(&cache.provider) {
		p := cache.provider
		return &p, true, nil
	}
	if time.Since(cache.lastAttempt) < oauthRefreshFloor {
		if cache.lastError != nil {
			return nil, true, cache.lastError
		}
		if cache.provider.AuthToken != "" && !rejected && time.Now().Before(cache.provider.TokenExpiresAtTime()) {
			p := cache.provider
			return &p, true, nil
		}
	}
	cache.lastAttempt = time.Now()
	updated, err := RefreshCopilotOAuthToken(ctx, &cache.provider)
	if err == nil && cache.rejected[copilotFingerprint(updated.AuthToken)] {
		err = fmt.Errorf("copilot token exchange returned a previously rejected token")
	}
	cache.lastError = err
	if err != nil {
		return nil, true, err
	}
	cache.provider = *updated
	p := cache.provider
	return &p, true, nil
}
