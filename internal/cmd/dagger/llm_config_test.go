package daggercmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

// TestRemoveKeyClearsDefaultModel verifies that removing the default provider
// also clears the default model. Otherwise the stale model stays bound to
// whatever provider becomes default next, so applyLLMConfigEnv would export
// e.g. OPENAI_MODEL=claude-sonnet-4.5 and break every LLM call.
func TestRemoveKeyClearsDefaultModel(t *testing.T) {
	tempDir := t.TempDir()
	origConfigRoot := llmconfig.ConfigRoot
	origConfigFile := llmconfig.ConfigFile
	t.Cleanup(func() {
		llmconfig.ConfigRoot = origConfigRoot
		llmconfig.ConfigFile = origConfigFile
	})
	llmconfig.ConfigRoot = filepath.Join(tempDir, "dagger")
	llmconfig.ConfigFile = filepath.Join(llmconfig.ConfigRoot, llmconfig.ConfigFileName)

	cfg := &llmconfig.Config{
		LLM: llmconfig.LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4.5",
			Providers: map[string]llmconfig.Provider{
				"anthropic": {APIKey: "sk-ant", Enabled: true},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	llmRemoveKeyCmd.SetOut(io.Discard)
	if err := llmRemoveKeyCmd.RunE(llmRemoveKeyCmd, []string{"anthropic"}); err != nil {
		t.Fatalf("remove-key failed: %v", err)
	}

	loaded, err := llmconfig.Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if loaded.LLM.DefaultProvider != "" {
		t.Errorf("DefaultProvider = %q, want empty after removing the default provider", loaded.LLM.DefaultProvider)
	}
	if loaded.LLM.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want empty after removing the default provider", loaded.LLM.DefaultModel)
	}
}

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
