package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/core/llmconfig"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type LLMTestQuery struct{}

func (LLMTestQuery) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

type mockSecret struct {
	uri string
}

func (mockSecret) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Secret",
		NonNull:   true,
	}
}

// newTestServer creates a dagql server with secret resolution stubs.
func newTestServer(t *testing.T, secrets map[string]string) *dagql.Server {
	t.Helper()
	q := LLMTestQuery{}
	baseCache, err := dagql.NewCache(context.Background(), "")
	assert.NoError(t, err)
	srv := dagql.NewServer(q, dagql.NewSessionCache(baseCache))

	dagql.Fields[LLMTestQuery]{
		dagql.Func("secret", func(ctx context.Context, self LLMTestQuery, args struct {
			URI string
		}) (mockSecret, error) {
			return mockSecret{uri: args.URI}, nil
		}),
	}.Install(srv)

	dagql.Fields[mockSecret]{
		dagql.Func("plaintext", func(ctx context.Context, self mockSecret, _ struct{}) (string, error) {
			if v, ok := secrets[self.uri]; ok {
				return v, nil
			}
			return "", nil
		}),
	}.Install(srv)

	return srv
}

func TestLlmConfigFromEnvOverlay(t *testing.T) {
	// Simulate: client sends env-var overrides in LLMConfig, no config file.
	srv := newTestServer(t, nil)

	envCfg := &llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "env-key", BaseURL: "env-url", Model: "env-model"},
			"openai":    {APIKey: "oai-key", AzureVersion: "v1", BaseURL: "oai-url", Model: "oai-model", DisableStreaming: true},
			"google":    {APIKey: "gem-key", BaseURL: "gem-url", Model: "gem-model"},
		},
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		LLMConfig: envCfg,
	})

	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)

	assert.Equal(t, "env-key", r.provider("anthropic").APIKey)
	assert.Equal(t, "env-url", r.provider("anthropic").BaseURL)
	assert.Equal(t, "env-model", r.provider("anthropic").Model)

	assert.Equal(t, "oai-key", r.provider("openai").APIKey)
	assert.Equal(t, "v1", r.provider("openai").AzureVersion)
	assert.Equal(t, "oai-url", r.provider("openai").BaseURL)
	assert.Equal(t, "oai-model", r.provider("openai").Model)
	assert.True(t, r.provider("openai").DisableStreaming)

	assert.Equal(t, "gem-key", r.provider("google").APIKey)
	assert.Equal(t, "gem-url", r.provider("google").BaseURL)
	assert.Equal(t, "gem-model", r.provider("google").Model)
}

func TestLlmConfigMerge(t *testing.T) {
	// Test that mergeLLMConfigs correctly overlays env vars onto file config.
	base := &llmconfig.LLMConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-6",
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "file-key", BaseURL: "file-url", Enabled: true, ThinkingMode: "adaptive"},
			"openai":    {APIKey: "file-oai-key", Enabled: true},
		},
	}
	overlay := &llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "env-key"},                 // override APIKey only
			"google":    {APIKey: "env-gem", Model: "gem-2"}, // new provider from env
		},
	}

	merged := mergeLLMConfigs(base, overlay)

	// Anthropic: env key overrides, file fields preserved.
	assert.Equal(t, "env-key", merged.Providers["anthropic"].APIKey)
	assert.Equal(t, "file-url", merged.Providers["anthropic"].BaseURL)
	assert.True(t, merged.Providers["anthropic"].Enabled)
	assert.Equal(t, "adaptive", merged.Providers["anthropic"].ThinkingMode)

	// OpenAI: untouched by overlay.
	assert.Equal(t, "file-oai-key", merged.Providers["openai"].APIKey)

	// Google: new from env.
	assert.Equal(t, "env-gem", merged.Providers["google"].APIKey)
	assert.Equal(t, "gem-2", merged.Providers["google"].Model)

	// File-level defaults preserved.
	assert.Equal(t, "anthropic", merged.DefaultProvider)
	assert.Equal(t, "claude-sonnet-4-6", merged.DefaultModel)
}

func TestLlmConfigMergeDefaultModelOverride(t *testing.T) {
	// Test that overlay DefaultModel (from env vars) overrides file-level DefaultModel.
	base := &llmconfig.LLMConfig{
		DefaultModel: "file-model",
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "key", Enabled: true},
		},
	}
	overlay := &llmconfig.LLMConfig{
		DefaultModel: "env-model",
	}
	merged := mergeLLMConfigs(base, overlay)
	assert.Equal(t, "env-model", merged.DefaultModel)
}

func TestLlmConfigSecretRefResolution(t *testing.T) {
	srv := newTestServer(t, map[string]string{
		"op://vault/item/key": "resolved-secret-key",
	})

	envCfg := &llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "op://vault/item/key"},
		},
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		LLMConfig: envCfg,
	})

	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)

	// Secret should NOT be resolved yet — resolution is lazy.
	assert.Equal(t, "op://vault/item/key", r.provider("anthropic").APIKey)

	// After resolving the provider secret (as Route would do), it should be resolved.
	err = r.resolveProviderSecret(ctx, "anthropic")
	assert.NoError(t, err)
	assert.Equal(t, "resolved-secret-key", r.provider("anthropic").APIKey)
}

func TestLlmConfigSecretRefLazyResolution(t *testing.T) {
	// Verify that only the routed provider's secret is resolved, not all of them.
	resolveCalls := map[string]int{}
	secrets := map[string]string{
		"op://vault/anthropic-key": "anthropic-resolved",
		"op://vault/openai-key":    "openai-resolved",
	}
	srv := newTestServer(t, secrets)

	// Wrap to count resolution calls by tracking provider state.
	envCfg := &llmconfig.LLMConfig{
		Providers: map[string]llmconfig.Provider{
			"anthropic": {APIKey: "op://vault/anthropic-key"},
			"openai":    {APIKey: "op://vault/openai-key"},
		},
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		LLMConfig: envCfg,
	})

	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)

	// Neither secret should be resolved yet.
	assert.Equal(t, "op://vault/anthropic-key", r.provider("anthropic").APIKey)
	assert.Equal(t, "op://vault/openai-key", r.provider("openai").APIKey)

	// Resolve only anthropic.
	err = r.resolveProviderSecret(ctx, "anthropic")
	assert.NoError(t, err)
	assert.Equal(t, "anthropic-resolved", r.provider("anthropic").APIKey)
	// OpenAI should still be unresolved.
	assert.Equal(t, "op://vault/openai-key", r.provider("openai").APIKey)

	_ = resolveCalls // unused in this simplified form
}

func TestLlmConfigMergeNils(t *testing.T) {
	assert.Nil(t, mergeLLMConfigs(nil, nil))

	base := &llmconfig.LLMConfig{DefaultProvider: "x"}
	assert.Equal(t, "x", mergeLLMConfigs(base, nil).DefaultProvider)

	overlay := &llmconfig.LLMConfig{DefaultProvider: "y"}
	assert.Equal(t, "y", mergeLLMConfigs(nil, overlay).DefaultProvider)
}
