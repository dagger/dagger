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

func TestLlmConfigFromMetadata(t *testing.T) {
	srv := newTestServer(t, nil)

	// Simulate a client passing a fully-merged LLMConfig via metadata.
	llmCfg := &llmconfig.LLMConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-6",
		Providers: map[string]llmconfig.Provider{
			"anthropic": {
				APIKey:  "anthropic-api-key",
				BaseURL: "anthropic-base-url",
				Model:   "anthropic-model",
				Enabled: true,
			},
			"openai": {
				APIKey:          "openai-api-key",
				AzureVersion:    "openai-azure-version",
				BaseURL:         "openai-base-url",
				Model:           "openai-model",
				DisableStreaming: true,
				Enabled:         true,
			},
			"google": {
				APIKey:  "gemini-api-key",
				BaseURL: "gemini-base-url",
				Model:   "gemini-model",
				Enabled: true,
			},
		},
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		LLMConfig: llmCfg,
	})

	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)

	anthropic := r.provider("anthropic")
	assert.Equal(t, "anthropic-api-key", anthropic.APIKey)
	assert.Equal(t, "anthropic-base-url", anthropic.BaseURL)
	assert.Equal(t, "anthropic-model", anthropic.Model)

	openai := r.provider("openai")
	assert.Equal(t, "openai-api-key", openai.APIKey)
	assert.Equal(t, "openai-azure-version", openai.AzureVersion)
	assert.Equal(t, "openai-base-url", openai.BaseURL)
	assert.Equal(t, "openai-model", openai.Model)
	assert.True(t, openai.DisableStreaming)

	google := r.provider("google")
	assert.Equal(t, "gemini-api-key", google.APIKey)
	assert.Equal(t, "gemini-base-url", google.BaseURL)
	assert.Equal(t, "gemini-model", google.Model)
}

func TestLlmConfigSecretRefResolution(t *testing.T) {
	// Secret references in API keys should be resolved by the engine.
	srv := newTestServer(t, map[string]string{
		"op://vault/item/key": "resolved-secret-key",
	})

	llmCfg := &llmconfig.LLMConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-6",
		Providers: map[string]llmconfig.Provider{
			"anthropic": {
				APIKey:  "op://vault/item/key",
				Enabled: true,
			},
		},
	}

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		LLMConfig: llmCfg,
	})

	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)
	assert.Equal(t, "resolved-secret-key", r.provider("anthropic").APIKey)
}

func TestLlmConfigDisableStreaming(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disable  bool
		expected bool
	}{
		{"not disabled by default", false, false},
		{"disabled", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, nil)

			llmCfg := &llmconfig.LLMConfig{
				Providers: map[string]llmconfig.Provider{
					"openai": {
						APIKey:          "key",
						DisableStreaming: tc.disable,
						Enabled:         true,
					},
				},
			}

			ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
				LLMConfig: llmCfg,
			})

			r, err := NewLLMRouter(ctx, srv)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, r.provider("openai").DisableStreaming)
		})
	}
}
