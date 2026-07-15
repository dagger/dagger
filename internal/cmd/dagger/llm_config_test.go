package daggercmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

// TestApplyLLMConfigEnvOpenAISlot verifies that when both openai and
// openrouter are enabled, the shared OPENAI_* variables are owned by exactly
// one provider, chosen deterministically rather than by map iteration order.
func TestApplyLLMConfigEnvOpenAISlot(t *testing.T) {
	for _, tc := range []struct {
		name            string
		defaultProvider string
		wantKey         string
		wantBaseURL     string
	}{
		{
			name:            "default openrouter wins the slot",
			defaultProvider: "openrouter",
			wantKey:         "sk-openrouter",
			wantBaseURL:     "https://openrouter.ai/api/v1",
		},
		{
			name:            "openai wins when default is neither",
			defaultProvider: "anthropic",
			wantKey:         "sk-openai",
			wantBaseURL:     "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			origConfigRoot := llmconfig.ConfigRoot
			origConfigFile := llmconfig.ConfigFile
			t.Cleanup(func() {
				llmconfig.ConfigRoot = origConfigRoot
				llmconfig.ConfigFile = origConfigFile
			})
			llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
			llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

			for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_MODEL", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL"} {
				if val, ok := os.LookupEnv(key); ok {
					t.Cleanup(func() { os.Setenv(key, val) })
					os.Unsetenv(key)
				} else {
					t.Cleanup(func() { os.Unsetenv(key) })
				}
			}

			cfg := &llmconfig.Config{
				LLM: llmconfig.LLMConfig{
					DefaultProvider: tc.defaultProvider,
					Providers: map[string]llmconfig.Provider{
						"openai":     {APIKey: "sk-openai", Enabled: true},
						"openrouter": {APIKey: "sk-openrouter", Enabled: true},
						"anthropic":  {APIKey: "sk-anthropic", Enabled: true},
					},
				},
			}
			if err := cfg.Save(); err != nil {
				t.Fatalf("Save() failed: %v", err)
			}

			applyLLMConfigEnv()

			if got := os.Getenv("OPENAI_API_KEY"); got != tc.wantKey {
				t.Errorf("OPENAI_API_KEY = %q, want %q", got, tc.wantKey)
			}
			if got := os.Getenv("OPENAI_BASE_URL"); got != tc.wantBaseURL {
				t.Errorf("OPENAI_BASE_URL = %q, want %q", got, tc.wantBaseURL)
			}
		})
	}
}
