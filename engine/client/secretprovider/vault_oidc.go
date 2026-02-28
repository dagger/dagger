package secretprovider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/pkg/browser"
)

const (
	defaultVaultOIDCMountPath    = "oidc"
	defaultVaultOIDCCallbackPort = "8250"
	vaultOIDCCallbackPath        = "/oidc/callback"
	defaultVaultOIDCWaitTimeout  = 2 * time.Minute
)

type cachedVaultToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type vaultOIDCCallback struct {
	State string
	Code  string
	Err   error
}

var (
	vaultNowFn            = time.Now
	vaultOpenURL          = browser.OpenURL
	vaultOIDCWaitTimeout  = defaultVaultOIDCWaitTimeout
	vaultTokenCachePathFn = defaultVaultTokenCachePath
)

func vaultOIDCLogin(ctx context.Context, client *vault.Client) (time.Duration, error) {
	mount := strings.Trim(os.Getenv("VAULT_OIDC_MOUNT_PATH"), "/")
	if mount == "" {
		mount = defaultVaultOIDCMountPath
	}

	port, err := vaultOIDCCallbackPort()
	if err != nil {
		return 0, err
	}

	redirectURI := fmt.Sprintf("http://localhost:%s%s", port, vaultOIDCCallbackPath)
	nonce, err := vaultOIDCNonce()
	if err != nil {
		return 0, fmt.Errorf("failed generating OIDC nonce: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return 0, fmt.Errorf("failed to listen on OIDC callback port %s: %w (set VAULT_OIDC_CALLBACK_PORT to a free port)", port, err)
	}

	callbackCh := make(chan vaultOIDCCallback, 1)
	serveErrCh := make(chan error, 1)
	server := vaultOIDCCallbackServer(callbackCh)
	go func() {
		defer close(serveErrCh)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	role := os.Getenv("VAULT_OIDC_ROLE")
	authURL, err := vaultOIDCAuthURL(ctx, client, mount, redirectURI, role, nonce)
	if err != nil {
		return 0, err
	}

	fmt.Fprintln(os.Stderr, "Opening browser for Vault OIDC authentication...")
	if os.Getenv("VAULT_OIDC_SKIP_BROWSER") != "" {
		fmt.Fprintf(os.Stderr, "Open this URL to authenticate: %s\n", authURL)
	} else if err := vaultOpenURL(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open browser: %v\n", err)
		fmt.Fprintf(os.Stderr, "Open this URL to authenticate: %s\n", authURL)
	}
	fmt.Fprintln(os.Stderr, "Waiting for Vault OIDC authentication callback...")

	waitCtx, waitCancel := context.WithTimeout(ctx, vaultOIDCWaitTimeout)
	defer waitCancel()

	var callback vaultOIDCCallback
	select {
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return 0, fmt.Errorf("timed out waiting for Vault OIDC callback")
		}
		return 0, waitCtx.Err()
	case serveErr := <-serveErrCh:
		if serveErr != nil {
			return 0, fmt.Errorf("oidc callback server error: %w", serveErr)
		}
	case callback = <-callbackCh:
		if callback.Err != nil {
			return 0, callback.Err
		}
	}

	secret, err := client.Logical().ReadWithDataWithContext(waitCtx, "auth/"+mount+"/oidc/callback", vaultOIDCCallbackQuery(callback.State, callback.Code, nonce))
	if err != nil {
		return 0, fmt.Errorf("vault OIDC callback exchange failed: %w", err)
	}
	if secret == nil || secret.Auth == nil || secret.Auth.ClientToken == "" {
		return 0, fmt.Errorf("vault OIDC callback returned no token")
	}

	client.SetToken(secret.Auth.ClientToken)
	fmt.Fprintln(os.Stderr, "Successfully authenticated to Vault")

	return time.Duration(secret.Auth.LeaseDuration) * time.Second, nil
}

func vaultOIDCCallbackServer(callbackCh chan<- vaultOIDCCallback) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(vaultOIDCCallbackPath, func(w http.ResponseWriter, r *http.Request) {
		state, code, err := parseVaultOIDCCallback(r)
		if err != nil {
			http.Error(w, "Vault OIDC callback error: "+err.Error(), http.StatusBadRequest)
			select {
			case callbackCh <- vaultOIDCCallback{Err: err}:
			default:
			}
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("Authentication successful. You can close this tab."))
		select {
		case callbackCh <- vaultOIDCCallback{State: state, Code: code}:
		default:
		}
	})

	return &http.Server{Handler: mux}
}

func parseVaultOIDCCallback(r *http.Request) (string, string, error) {
	if r.Method != http.MethodGet {
		return "", "", fmt.Errorf("unexpected method %s", r.Method)
	}

	query := r.URL.Query()
	if oauthErr := query.Get("error"); oauthErr != "" {
		return "", "", fmt.Errorf("oidc provider returned error %q (%s)", oauthErr, query.Get("error_description"))
	}

	state := query.Get("state")
	if state == "" {
		return "", "", fmt.Errorf("missing state in callback")
	}
	code := query.Get("code")
	if code == "" {
		return "", "", fmt.Errorf("missing code in callback")
	}

	return state, code, nil
}

func vaultOIDCCallbackPort() (string, error) {
	port := strings.TrimSpace(os.Getenv("VAULT_OIDC_CALLBACK_PORT"))
	if port == "" {
		port = defaultVaultOIDCCallbackPort
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("invalid VAULT_OIDC_CALLBACK_PORT %q: %w", port, err)
	}
	return port, nil
}

func vaultOIDCNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func vaultOIDCAuthURL(ctx context.Context, client *vault.Client, mount, redirectURI, role, nonce string) (string, error) {
	secret, err := client.Logical().WriteWithContext(ctx, "auth/"+mount+"/oidc/auth_url", vaultOIDCAuthURLPayload(redirectURI, role, nonce))
	if err != nil {
		return "", fmt.Errorf("failed requesting Vault OIDC auth URL: %w", err)
	}
	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("failed requesting Vault OIDC auth URL: empty response")
	}
	authURL, ok := secret.Data["auth_url"].(string)
	if !ok || authURL == "" {
		return "", fmt.Errorf("failed requesting Vault OIDC auth URL: response missing auth_url")
	}
	return authURL, nil
}

func vaultOIDCAuthURLPayload(redirectURI, role, nonce string) map[string]any {
	payload := map[string]any{
		"redirect_uri": redirectURI,
		"client_nonce": nonce,
	}
	if role != "" {
		payload["role"] = role
	}
	return payload
}

func vaultOIDCCallbackQuery(state, code, nonce string) map[string][]string {
	return map[string][]string{
		"state":        {state},
		"code":         {code},
		"client_nonce": {nonce},
	}
}

func loadCachedVaultToken() (string, error) {
	cachePath, err := vaultTokenCachePathFn()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}

	var cached cachedVaultToken
	if err := json.Unmarshal(data, &cached); err != nil {
		return "", fmt.Errorf("invalid vault token cache format: %w", err)
	}
	if cached.Token == "" {
		return "", nil
	}
	if !cached.ExpiresAt.IsZero() && !cached.ExpiresAt.After(vaultNowFn()) {
		return "", nil
	}

	return cached.Token, nil
}

func clearCachedVaultToken() error {
	cachePath, err := vaultTokenCachePathFn()
	if err != nil {
		return err
	}
	if err := os.Remove(cachePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func validateVaultToken(ctx context.Context, client *vault.Client) error {
	if strings.TrimSpace(client.Token()) == "" {
		return fmt.Errorf("vault token is empty")
	}

	_, err := client.Auth().Token().LookupSelfWithContext(ctx)
	return err
}

func isVaultInvalidTokenError(err error) bool {
	if err == nil {
		return false
	}

	var responseErr *vault.ResponseError
	if errors.As(err, &responseErr) {
		for _, message := range responseErr.Errors {
			if strings.Contains(strings.ToLower(message), "invalid token") {
				return true
			}
		}
	}

	return strings.Contains(strings.ToLower(err.Error()), "invalid token")
}

func saveCachedVaultToken(token string, ttl time.Duration) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("vault token is empty")
	}

	cachePath, err := vaultTokenCachePathFn()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return err
	}

	cached := cachedVaultToken{Token: token}
	if ttl > 0 {
		cached.ExpiresAt = vaultNowFn().Add(ttl)
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}

	return os.WriteFile(cachePath, data, 0o600)
}

func defaultVaultTokenCachePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "dagger", "vault-token"), nil
}
