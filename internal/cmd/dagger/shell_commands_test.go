package daggercmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
)

func TestAvailableModelOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    *llmconfig.Config
		env       map[string]string
		dotenv    string
		providers []string
		models    []modelSelection
		absent    []modelSelection
	}{
		{name: "no configuration"},
		{
			name:      "environment only codex",
			env:       map[string]string{"OPENAI_CODEX_AUTH_TOKEN": "test-token"},
			providers: []string{"openai-codex"},
		},
		{
			name: "same ID different providers",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"openai":       {Enabled: true, APIKey: "test-key", Model: "gpt-5"},
				"openai-codex": {Enabled: true, AuthType: "oauth", AuthToken: "test-token", Model: "gpt-5"},
				"anthropic":    {Enabled: false, APIKey: "disabled-key"},
			}}},
			providers: []string{"openai", "openai-codex"},
			models:    []modelSelection{{"openai", "gpt-5"}, {"openai-codex", "gpt-5"}},
		},
		{
			name: "custom default and small models",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{
				DefaultProvider: "local", DefaultModel: "org/default-model",
				Providers: map[string]llmconfig.Provider{
					"local": {Enabled: true, BaseURL: "http://localhost:8080", Model: "org/pinned-model", SmallModel: "org/small-model"},
				},
			}},
			providers: []string{"local"},
			models:    []modelSelection{{"local", "org/default-model"}, {"local", "org/pinned-model"}, {"local", "org/small-model"}},
		},
		{
			name: "dotenv overrides environment and file",
			config: &llmconfig.Config{LLM: llmconfig.LLMConfig{Providers: map[string]llmconfig.Provider{
				"local": {Enabled: true, BaseURL: "http://localhost:8080", Model: "file-model"},
			}}},
			env:       map[string]string{"LOCAL_MODEL": "env-model"},
			dotenv:    "LOCAL_MODEL=org/dotenv-model\nOPENROUTER_API_KEY=test-key\nOPENROUTER_MODEL=vendor/custom-model\n",
			providers: []string{"local", "openrouter"},
			models:    []modelSelection{{"local", "org/dotenv-model"}, {"openrouter", "vendor/custom-model"}},
			absent:    []modelSelection{{"local", "file-model"}, {"local", "env-model"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLLMEnv(t)
			for _, name := range []string{"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENROUTER_MODEL", "OPENROUTER_SMALL_MODEL", "OPENROUTER_REASONING_EFFORT"} {
				t.Setenv(name, "")
			}
			useTempLLMConfig(t, tc.config)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			if tc.dotenv != "" {
				require.NoError(t, os.WriteFile(".env", []byte(tc.dotenv), 0600))
			}
			options, err := availableModelOptions(t.Context())
			require.NoError(t, err)
			seen := map[modelSelection]bool{}
			providers := map[string]bool{}
			for _, option := range options {
				require.False(t, seen[option.Value], "duplicate provider/model option: %v", option.Value)
				seen[option.Value] = true
				providers[option.Value.Provider] = true
			}
			require.Len(t, providers, len(tc.providers))
			for _, provider := range tc.providers {
				require.True(t, providers[provider], "missing provider %s", provider)
			}
			for _, model := range tc.models {
				require.True(t, seen[model], "missing model %v", model)
			}
			for _, model := range tc.absent {
				require.False(t, seen[model], "unexpected model %v", model)
			}
		})
	}
}

func TestAvailableModelOptionsEffectiveConfig(t *testing.T) {
	// The main client can supply credentials to a nested agent with no local
	// configuration. The engine returns only provider names and model metadata.
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	cfg := &engine.LLMConfig{
		DefaultProvider: "local",
		DefaultModel:    "org/inherited-default",
		Providers: map[string]*engine.LLMProviderConfig{
			"openai-codex": {},
			"local":        {Model: "org/inherited-model", SmallModel: "org/inherited-small"},
		},
	}
	calls := 0
	read := llmModelConfigReader(func(ctx context.Context) (*engine.LLMConfig, error) {
		calls++
		require.Equal(t, t.Context().Done(), ctx.Done())
		return cfg, nil
	})
	ctx := context.WithValue(t.Context(), llmModelConfigKey{}, read)
	options, err := availableModelOptions(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	seen := map[modelSelection]bool{}
	for _, option := range options {
		seen[option.Value] = true
		require.Contains(t, []string{"local", "openai-codex"}, option.Value.Provider)
	}
	for _, model := range []string{"org/inherited-default", "org/inherited-model", "org/inherited-small"} {
		require.True(t, seen[modelSelection{Provider: "local", Model: model}])
	}
	for _, model := range llmconfig.ModelsForProvider("openai-codex") {
		require.True(t, seen[modelSelection{Provider: "openai-codex", Model: model.ID}])
	}

	// A live reader is authoritative. Do not reintroduce local providers after
	// the engine reports none (e.g. logout), or conceal a failed config read.
	t.Setenv("OPENAI_API_KEY", "local-key")
	cfg = nil
	options, err = availableModelOptions(ctx)
	require.NoError(t, err)
	require.Empty(t, options)
	wantErr := errors.New("engine config unavailable")
	ctx = context.WithValue(t.Context(), llmModelConfigKey{}, llmModelConfigReader(func(context.Context) (*engine.LLMConfig, error) {
		return nil, wantErr
	}))
	options, err = availableModelOptions(ctx)
	require.ErrorIs(t, err, wantErr)
	require.Empty(t, options)
}

func TestAvailableModelOptionsMalformedConfig(t *testing.T) {
	clearLLMEnv(t)
	useTempLLMConfig(t, nil)
	t.Setenv("OPENAI_DISABLE_STREAMING", "not-a-bool")
	_, err := availableModelOptions(t.Context())
	require.ErrorIs(t, err, llmconfig.ErrMalformed)
}

func TestReasoningEffortOptions(t *testing.T) {
	values := func(opts []huh.Option[string]) []string {
		var vs []string
		for _, o := range opts {
			vs = append(vs, o.Value)
		}
		return vs
	}
	catalogModel := func(match func(llmconfig.ModelInfo) bool) string {
		t.Helper()
		for _, provider := range llmconfig.ProviderEntries() {
			for _, model := range llmconfig.ModelsForProvider(provider.ConfigKey) {
				if match(model) {
					return model.ID
				}
			}
		}
		t.Fatal("catwalk catalog has no model matching the test condition")
		return ""
	}

	// Catalog model marked non-reasoning: only "none", no invented levels.
	nonReasoning := catalogModel(func(model llmconfig.ModelInfo) bool {
		return !model.CanReason
	})
	require.Equal(t, []string{"none"}, values(reasoningEffortOptions(nonReasoning)))

	// Unknown model: conventional fallback.
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions("my-local-model")))

	// Catalog model that can reason but declares no explicit levels
	// gets the conventional fallback.
	withoutLevels := catalogModel(func(model llmconfig.ModelInfo) bool {
		return model.CanReason && len(model.ReasoningLevels) == 0
	})
	require.Equal(t, []string{"none", "low", "medium", "high"},
		values(reasoningEffortOptions(withoutLevels)))
}
