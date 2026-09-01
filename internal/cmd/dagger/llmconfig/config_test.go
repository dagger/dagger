package llmconfig

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	toml "github.com/pelletier/go-toml"
)

func TestConfigSaveAndLoad(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			DefaultModel:    "anthropic/claude-sonnet-4.5",
			Providers: map[string]Provider{
				"openrouter": {
					APIKey:  "sk-or-v1-test-key",
					BaseURL: "https://openrouter.ai/api/v1",
					Enabled: true,
				},
				"anthropic": {
					APIKey:  "sk-ant-test-key",
					Enabled: false,
				},
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if !ConfigExists() {
		t.Fatal("ConfigExists() returned false after Save()")
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if diff := cmp.Diff(cfg, loaded); diff != "" {
		t.Errorf("Loaded config differs from original (-want +got):\n%s", diff)
	}
}

func TestConfigIsTOML(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]Provider{
				"anthropic": {
					APIKey:  "sk-ant-test",
					Enabled: true,
				},
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Read raw file and verify it's valid TOML
	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	tree, err := toml.LoadBytes(data)
	if err != nil {
		t.Fatalf("File is not valid TOML: %v", err)
	}

	// Verify structure
	if tree.Get("llm.default_provider").(string) != "anthropic" {
		t.Errorf("expected default_provider = anthropic, got %v", tree.Get("llm.default_provider"))
	}
	if tree.Get("llm.default_model").(string) != "claude-sonnet-4.5" {
		t.Errorf("expected default_model = claude-sonnet-4.5, got %v", tree.Get("llm.default_model"))
	}
}

func TestConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping file permission test on Windows")
	}

	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	info, err := os.Stat(ConfigFile)
	if err != nil {
		t.Fatalf("Stat() failed: %v", err)
	}

	perm := info.Mode().Perm()
	expectedPerm := os.FileMode(0600)
	if perm != expectedPerm {
		t.Errorf("Config file has incorrect permissions: got %o, want %o", perm, expectedPerm)
	}
}

func TestLoadNonExistentConfig(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should not error on non-existent config: %v", err)
	}
	if cfg != nil {
		t.Errorf("Load() should return nil for non-existent config, got %+v", cfg)
	}
}

func TestLoadMalformedConfig(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	if err := os.MkdirAll(ConfigRoot, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	if err := os.WriteFile(ConfigFile, []byte("not valid toml [[["), 0600); err != nil {
		t.Fatalf("Failed to write malformed config: %v", err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() should error on malformed config, got config: %+v", cfg)
	}
}

func TestConfigRemove(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	if !ConfigExists() {
		t.Fatal("ConfigExists() returned false after Save()")
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove() failed: %v", err)
	}

	if ConfigExists() {
		t.Fatal("ConfigExists() returned true after Remove()")
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove() should not error when file doesn't exist: %v", err)
	}
}

func TestLLMConfigured(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	// No file => not configured
	if LLMConfigured() {
		t.Fatal("LLMConfigured() should be false with no file")
	}

	// Empty providers => not configured
	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if LLMConfigured() {
		t.Fatal("LLMConfigured() should be false with empty providers")
	}

	// With provider => configured
	cfg.LLM.Providers["anthropic"] = Provider{APIKey: "test", Enabled: true}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if !LLMConfigured() {
		t.Fatal("LLMConfigured() should be true with a provider")
	}
}

// TestConfigSaveOutsideConfigRoot verifies that Save creates the directory the
// config file actually lives in. DAGGER_CONFIG can point anywhere, and a Save
// that only mkdirs ConfigRoot fails for such a user — which for an OAuth
// refresh means the one-time refresh token is spent and the result is lost.
func TestConfigSaveOutsideConfigRoot(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	// The XDG root and the config file deliberately live in different trees,
	// as they do when DAGGER_CONFIG is set.
	ConfigRoot = filepath.Join(tempDir, "xdg", "dagger")
	ConfigFile = filepath.Join(tempDir, "elsewhere", "nested", "dagger.toml")

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]Provider{
				"anthropic": {APIKey: "sk-ant-test", Enabled: true},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if !ConfigExists() {
		t.Fatalf("Save() did not create %s", ConfigFile)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if diff := cmp.Diff(cfg, loaded); diff != "" {
		t.Errorf("Loaded config differs from original (-want +got):\n%s", diff)
	}
}

func TestConfigConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
			Providers:       make(map[string]Provider),
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Initial Save() failed: %v", err)
	}

	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cfg := &Config{
				LLM: LLMConfig{
					DefaultProvider: "openrouter",
					Providers: map[string]Provider{
						"provider": {
							APIKey:  "test-key",
							Enabled: true,
						},
					},
				},
			}
			done <- cfg.Save()
		}()
	}

	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent write %d failed: %v", i, err)
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after concurrent writes failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("Load() returned nil after concurrent writes")
	}
}

func TestConfigEmptyProviders(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openrouter",
		},
	}

	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if loaded.LLM.Providers == nil {
		t.Error("Providers map should be initialized after load")
	}

	if len(loaded.LLM.Providers) != 0 {
		t.Errorf("Providers map should be empty, got %d providers", len(loaded.LLM.Providers))
	}
}

func TestConfigPreservesOtherSections(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	if err := os.MkdirAll(ConfigRoot, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}
	if err := os.WriteFile(ConfigFile, []byte("[other]\nkey = \"value\"\n"), 0600); err != nil {
		t.Fatalf("Failed to write existing config: %v", err)
	}

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]Provider{
				"anthropic": {APIKey: "sk-ant-test", Enabled: true},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}
	tree, err := toml.LoadBytes(data)
	if err != nil {
		t.Fatalf("File is not valid TOML: %v", err)
	}
	if got := tree.Get("other.key"); got != "value" {
		t.Errorf("Save() lost [other] section: other.key = %v", got)
	}
	if got := tree.Get("llm.default_provider"); got != "anthropic" {
		t.Errorf("Save() did not write [llm] section: default_provider = %v", got)
	}

	// Removing the LLM config must only drop [llm], not the file.
	if err := Remove(); err != nil {
		t.Fatalf("Remove() failed: %v", err)
	}
	data, err = os.ReadFile(ConfigFile)
	if err != nil {
		t.Fatalf("ReadFile() after Remove() failed: %v", err)
	}
	tree, err = toml.LoadBytes(data)
	if err != nil {
		t.Fatalf("File is not valid TOML after Remove(): %v", err)
	}
	if got := tree.Get("other.key"); got != "value" {
		t.Errorf("Remove() lost [other] section: other.key = %v", got)
	}
	if tree.Has("llm") {
		t.Error("Remove() left the [llm] section behind")
	}
}

// TestRefreshOAuthProviderConcurrent verifies that concurrent calls to
// RefreshOAuthProviderIfNeeded for the same expired provider result in exactly
// one refresh against the token endpoint. Refresh tokens are one-time-use
// (providers rotate them), so without serialization a second interleaved call
// would double-spend the refresh token and fail with invalid_grant.
func TestRefreshOAuthProviderConcurrent(t *testing.T) {
	tempDir := t.TempDir()

	origConfigRoot := ConfigRoot
	origConfigFile := ConfigFile
	origTokenURL := oauthTokenURL
	origProfileURL := oauthProfileURL
	t.Cleanup(func() {
		ConfigRoot = origConfigRoot
		ConfigFile = origConfigFile
		oauthTokenURL = origTokenURL
		oauthProfileURL = origProfileURL
	})

	ConfigRoot = filepath.Join(tempDir, "dagger")
	ConfigFile = filepath.Join(ConfigRoot, ConfigFileName)

	// Fake OAuth server: the token endpoint rotates the refresh token on each
	// successful grant, so a replayed (already-spent) refresh token gets
	// invalid_grant — mirroring Anthropic's one-time refresh token behavior.
	var (
		mu                  sync.Mutex
		currentRefreshToken = "rt-0"
		refreshCalls        int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			var req struct {
				GrantType    string `json:"grant_type"`
				ClientID     string `json:"client_id"`
				RefreshToken string `json:"refresh_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
				return
			}
			if req.GrantType != "refresh_token" || req.ClientID != oauthClientID {
				http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if req.RefreshToken != currentRefreshToken {
				http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
				return
			}
			refreshCalls++
			currentRefreshToken = fmt.Sprintf("rt-%d", refreshCalls)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token":  fmt.Sprintf("access-%d", refreshCalls),
				"refresh_token": currentRefreshToken,
				"expires_in":    3600,
			})
		case "/profile":
			// Best-effort subscription lookup; serve a minimal valid response
			// so it doesn't hit the real network.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"organization":{"organization_type":"claude_max"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oauthTokenURL = srv.URL + "/token"
	oauthProfileURL = srv.URL + "/profile"

	// Seed an OAuth provider whose access token expired long ago.
	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			Providers: map[string]Provider{
				"anthropic": {
					AuthType:     "oauth",
					AuthToken:    "stale-access",
					RefreshToken: "rt-0",
					TokenExpiry:  1,
					Enabled:      true,
				},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	type result struct {
		token string
		err   error
	}
	const workers = 10
	start := make(chan struct{})
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		go func() {
			<-start
			refreshed, err := RefreshOAuthProviderIfNeeded(t.Context(), "anthropic")
			var token string
			if refreshed != nil {
				token = refreshed.AuthToken
			}
			results <- result{token, err}
		}()
	}
	close(start)

	for i := 0; i < workers; i++ {
		res := <-results
		if res.err != nil {
			t.Errorf("RefreshOAuthProviderIfNeeded() call %d failed: %v", i, res.err)
			continue
		}
		if res.token != "access-1" {
			t.Errorf("RefreshOAuthProviderIfNeeded() call %d returned token %q, want %q", i, res.token, "access-1")
		}
	}

	mu.Lock()
	if refreshCalls != 1 {
		t.Errorf("token endpoint hit %d times, want exactly 1", refreshCalls)
	}
	mu.Unlock()

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after concurrent refresh failed: %v", err)
	}
	provider, ok := loaded.LLM.Providers["anthropic"]
	if !ok {
		t.Fatal("anthropic provider missing after refresh")
	}
	if provider.AuthToken != "access-1" {
		t.Errorf("persisted AuthToken = %q, want %q", provider.AuthToken, "access-1")
	}
	if provider.RefreshToken != "rt-1" {
		t.Errorf("persisted RefreshToken = %q, want %q", provider.RefreshToken, "rt-1")
	}
	if provider.TokenExpiry <= time.Now().UnixMilli() {
		t.Errorf("persisted TokenExpiry %d not in the future", provider.TokenExpiry)
	}
}
