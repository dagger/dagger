package llmconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeEnvVars(t *testing.T) {
	// Set env vars for the test.
	for k, v := range map[string]string{
		"ANTHROPIC_API_KEY":        "ak-test",
		"ANTHROPIC_BASE_URL":       "https://anthropic.example.com",
		"ANTHROPIC_MODEL":          "claude-test",
		"OPENAI_API_KEY":           "sk-test",
		"OPENAI_BASE_URL":          "https://openai.example.com",
		"OPENAI_MODEL":             "gpt-test",
		"OPENAI_AZURE_VERSION":     "2024-01-01",
		"OPENAI_DISABLE_STREAMING": "true",
		"GEMINI_API_KEY":           "gem-test",
		"GEMINI_BASE_URL":          "https://gemini.example.com",
		"GEMINI_MODEL":             "gemini-test",
	} {
		t.Setenv(k, v)
	}

	cfg := MergeEnvVars(nil)

	anthropic := cfg.LLM.Providers["anthropic"]
	assert.Equal(t, "ak-test", anthropic.APIKey)
	assert.Equal(t, "https://anthropic.example.com", anthropic.BaseURL)
	assert.Equal(t, "claude-test", anthropic.Model)

	openai := cfg.LLM.Providers["openai"]
	assert.Equal(t, "sk-test", openai.APIKey)
	assert.Equal(t, "https://openai.example.com", openai.BaseURL)
	assert.Equal(t, "gpt-test", openai.Model)
	assert.Equal(t, "2024-01-01", openai.AzureVersion)
	assert.True(t, openai.DisableStreaming)

	google := cfg.LLM.Providers["google"]
	assert.Equal(t, "gem-test", google.APIKey)
	assert.Equal(t, "https://gemini.example.com", google.BaseURL)
	assert.Equal(t, "gemini-test", google.Model)
}

func TestMergeEnvVarsOverridesConfig(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")

	cfg := &Config{
		LLM: LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-sonnet-4-6",
			Providers: map[string]Provider{
				"anthropic": {
					APIKey:  "config-key",
					Enabled: true,
				},
			},
		},
	}

	cfg = MergeEnvVars(cfg)

	// Env var should override config file value.
	assert.Equal(t, "env-key", cfg.LLM.Providers["anthropic"].APIKey)
	// Other fields should be preserved.
	assert.True(t, cfg.LLM.Providers["anthropic"].Enabled)
	assert.Equal(t, "anthropic", cfg.LLM.DefaultProvider)
}

func TestDaggerModelEnvVar(t *testing.T) {
	t.Setenv("DAGGER_MODEL", "my-custom-model")

	cfg := MergeEnvVars(nil)
	assert.Equal(t, "my-custom-model", cfg.LLM.DefaultModel)
}

func TestDaggerModelEnvVarOverridesConfig(t *testing.T) {
	t.Setenv("DAGGER_MODEL", "env-model")

	cfg := &Config{
		LLM: LLMConfig{
			DefaultModel: "config-model",
			Providers:    map[string]Provider{},
		},
	}

	cfg = MergeEnvVars(cfg)
	assert.Equal(t, "env-model", cfg.LLM.DefaultModel)
}
