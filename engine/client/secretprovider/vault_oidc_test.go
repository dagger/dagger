package secretprovider

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	vault "github.com/hashicorp/vault/api"
)

func TestVaultTokenCacheRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "vault-token")
	vaultTokenCachePathFn = func() (string, error) { return cachePath, nil }
	t.Cleanup(func() { vaultTokenCachePathFn = defaultVaultTokenCachePath })

	now := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	vaultNowFn = func() time.Time { return now }
	t.Cleanup(func() { vaultNowFn = time.Now })

	if err := saveCachedVaultToken("token-123", 30*time.Minute); err != nil {
		t.Fatalf("saveCachedVaultToken() error = %v", err)
	}

	token, err := loadCachedVaultToken()
	if err != nil {
		t.Fatalf("loadCachedVaultToken() error = %v", err)
	}
	if token != "token-123" {
		t.Fatalf("loadCachedVaultToken() token = %q, want %q", token, "token-123")
	}

	info, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache file: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("cache file mode = %o, want %o", got, want)
	}
}

func TestVaultTokenCacheExpiredToken(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "vault-token")
	vaultTokenCachePathFn = func() (string, error) { return cachePath, nil }
	t.Cleanup(func() { vaultTokenCachePathFn = defaultVaultTokenCachePath })

	now := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	vaultNowFn = func() time.Time { return now }
	t.Cleanup(func() { vaultNowFn = time.Now })

	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache dir: %v", err)
	}

	cacheJSON := `{"token":"expired-token","expires_at":"2026-02-28T09:59:59Z"}`
	if err := os.WriteFile(cachePath, []byte(cacheJSON), 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	token, err := loadCachedVaultToken()
	if err != nil {
		t.Fatalf("loadCachedVaultToken() error = %v", err)
	}
	if token != "" {
		t.Fatalf("loadCachedVaultToken() token = %q, want empty for expired token", token)
	}
}

func TestParseVaultOIDCCallback(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		rawQuery  string
		wantState string
		wantCode  string
		wantErr   bool
	}{
		{
			name:      "valid callback",
			method:    http.MethodGet,
			rawQuery:  "state=abc&code=xyz",
			wantState: "abc",
			wantCode:  "xyz",
		},
		{
			name:     "provider error",
			method:   http.MethodGet,
			rawQuery: "error=access_denied&error_description=user+canceled",
			wantErr:  true,
		},
		{
			name:     "missing state",
			method:   http.MethodGet,
			rawQuery: "code=xyz",
			wantErr:  true,
		},
		{
			name:     "missing code",
			method:   http.MethodGet,
			rawQuery: "state=abc",
			wantErr:  true,
		},
		{
			name:     "unexpected method",
			method:   http.MethodPost,
			rawQuery: "state=abc&code=xyz",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{
				Method: tt.method,
				URL: &url.URL{
					Path:     vaultOIDCCallbackPath,
					RawQuery: tt.rawQuery,
				},
			}

			state, code, err := parseVaultOIDCCallback(req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseVaultOIDCCallback() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if state != tt.wantState {
				t.Fatalf("parseVaultOIDCCallback() state = %q, want %q", state, tt.wantState)
			}
			if code != tt.wantCode {
				t.Fatalf("parseVaultOIDCCallback() code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

func TestVaultOIDCAuthURLPayload(t *testing.T) {
	payload := vaultOIDCAuthURLPayload("http://localhost:8250/oidc/callback", "my-role", "nonce-1")

	if payload["redirect_uri"] != "http://localhost:8250/oidc/callback" {
		t.Fatalf("redirect_uri = %v", payload["redirect_uri"])
	}
	if payload["client_nonce"] != "nonce-1" {
		t.Fatalf("client_nonce = %v", payload["client_nonce"])
	}
	if payload["role"] != "my-role" {
		t.Fatalf("role = %v", payload["role"])
	}

	payloadNoRole := vaultOIDCAuthURLPayload("http://localhost:8250/oidc/callback", "", "nonce-2")
	if _, ok := payloadNoRole["role"]; ok {
		t.Fatalf("role must be omitted when VAULT_OIDC_ROLE is empty")
	}
}

func TestVaultOIDCCallbackQuery(t *testing.T) {
	query := vaultOIDCCallbackQuery("state-1", "code-1", "nonce-1")

	if got := query["state"]; len(got) != 1 || got[0] != "state-1" {
		t.Fatalf("state query = %#v", got)
	}
	if got := query["code"]; len(got) != 1 || got[0] != "code-1" {
		t.Fatalf("code query = %#v", got)
	}
	if got := query["client_nonce"]; len(got) != 1 || got[0] != "nonce-1" {
		t.Fatalf("client_nonce query = %#v", got)
	}
}

func TestIsVaultInvalidTokenError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "response error with invalid token",
			err: &vault.ResponseError{
				StatusCode: 403,
				Errors:     []string{"permission denied", "invalid token"},
			},
			want: true,
		},
		{
			name: "wrapped text contains invalid token",
			err:  errors.New("request failed: invalid token"),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("permission denied"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVaultInvalidTokenError(tt.err); got != tt.want {
				t.Fatalf("isVaultInvalidTokenError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClearCachedVaultToken(t *testing.T) {
	tmp := t.TempDir()
	cachePath := filepath.Join(tmp, "vault-token")
	vaultTokenCachePathFn = func() (string, error) { return cachePath, nil }
	t.Cleanup(func() { vaultTokenCachePathFn = defaultVaultTokenCachePath })

	if err := os.WriteFile(cachePath, []byte(`{"token":"abc"}`), 0o600); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	if err := clearCachedVaultToken(); err != nil {
		t.Fatalf("clearCachedVaultToken() error = %v", err)
	}

	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache file still exists after clear: %v", err)
	}
}
