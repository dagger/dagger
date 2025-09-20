package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/cache"
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

func TestLlmConfig(t *testing.T) {
	q := LLMTestQuery{}

	baseCache, err := cache.NewCache[string, dagql.AnyResult](context.Background(), "")
	assert.NoError(t, err)
	srv := dagql.NewServer(q, dagql.NewSessionCache(baseCache))

	vars := map[string]string{
		"file://.env":                    "",
		"env://ANTHROPIC_API_KEY":        "anthropic-api-key",
		"env://ANTHROPIC_BASE_URL":       "anthropic-base-url",
		"env://ANTHROPIC_MODEL":          "anthropic-model",
		"env://OPENAI_API_KEY":           "openai-api-key",
		"env://OPENAI_AZURE_VERSION":     "openai-azure-version",
		"env://OPENAI_BASE_URL":          "openai-base-url",
		"env://OPENAI_MODEL":             "openai-model",
		"env://OPENAI_DISABLE_STREAMING": "t",
		"env://GEMINI_API_KEY":           "gemini-api-key",
		"env://GEMINI_BASE_URL":          "gemini-base-url",
		"env://GEMINI_MODEL":             "gemini-model",
	}

	dagql.Fields[LLMTestQuery]{
		dagql.Func("secret", func(ctx context.Context, self LLMTestQuery, args struct {
			URI string
		}) (mockSecret, error) {
			if _, ok := vars[args.URI]; !ok {
				t.Fatalf("uri not found: %s", args.URI)
			}
			return mockSecret{uri: args.URI}, nil
		}),
	}.Install(srv)

	dagql.Fields[mockSecret]{
		dagql.Func("plaintext", func(ctx context.Context, self mockSecret, _ struct{}) (string, error) {
			return vars[self.uri], nil
		}),
	}.Install(srv)

	ctx := context.Background()
	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)
	assert.Equal(t, "anthropic-api-key", r.AnthropicAPIKey)
	assert.Equal(t, "anthropic-base-url", r.AnthropicBaseURL)
	assert.Equal(t, "anthropic-model", r.AnthropicModel)
	assert.Equal(t, "openai-api-key", r.OpenAIAPIKey)
	assert.Equal(t, "openai-azure-version", r.OpenAIAzureVersion)
	assert.Equal(t, "openai-base-url", r.OpenAIBaseURL)
	assert.Equal(t, "openai-model", r.OpenAIModel)
	assert.True(t, r.OpenAIDisableStreaming)
	assert.Equal(t, "gemini-api-key", r.GeminiAPIKey)
	assert.Equal(t, "gemini-base-url", r.GeminiBaseURL)
	assert.Equal(t, "gemini-model", r.GeminiModel)
}

func TestLlmConfigDisableStreaming(t *testing.T) {
	for _, tc := range []struct {
		name     string
		envFile  string
		expected bool
	}{
		{
			"not disabled by default",
			"",
			false,
		},
		{
			"explicitly not disabled, FALSE",
			"OPENAI_DISABLE_STREAMING=FALSE",
			false,
		},
		{
			"explicitly not disabled, 0",
			"OPENAI_DISABLE_STREAMING=0",
			false,
		},
		{
			"disabled, true",
			"OPENAI_DISABLE_STREAMING=true",
			true,
		},
		{
			"disabled, 1",
			"OPENAI_DISABLE_STREAMING=1",
			true,
		},
		{
			"empty value",
			"OPENAI_DISABLE_STREAMING=",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := LLMTestQuery{}

			baseCache, err := cache.NewCache[string, dagql.AnyResult](context.Background(), "")
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
					if self.uri == "file://.env" {
						return tc.envFile, nil
					}
					return "", nil
				}),
			}.Install(srv)

			ctx := context.Background()
			r, err := NewLLMRouter(ctx, srv)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, r.OpenAIDisableStreaming)
		})
	}
}

func TestLlmConfigEnvFile(t *testing.T) {
	q := LLMTestQuery{}

	baseCache, err := cache.NewCache[string, dagql.AnyResult](context.Background(), "")
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
			if self.uri == "file://.env" {
				return `ANTHRIOPIC_API_KEY=anthropic-api-key
ANTHROPIC_BASE_URL=anthropic-base-url
ANTHROPIC_MODEL=anthropic-model
ANTHROPIC_API_KEY=anthropic-api-key
OPENAI_API_KEY=openai-api-key
OPENAI_AZURE_VERSION=openai-azure-version
OPENAI_BASE_URL=openai-base-url
OPENAI_MODEL=openai-model
OPENAI_DISABLE_STREAMING=TRUE
GEMINI_API_KEY=gemini-api-key
GEMINI_BASE_URL=gemini-base-url
GEMINI_MODEL=gemini-model`, nil
			}
			return "", nil
		}),
	}.Install(srv)

	ctx := context.Background()
	r, err := NewLLMRouter(ctx, srv)
	assert.NoError(t, err)
	assert.Equal(t, "anthropic-api-key", r.AnthropicAPIKey)
	assert.Equal(t, "anthropic-base-url", r.AnthropicBaseURL)
	assert.Equal(t, "anthropic-model", r.AnthropicModel)
	assert.Equal(t, "openai-api-key", r.OpenAIAPIKey)
	assert.Equal(t, "openai-azure-version", r.OpenAIAzureVersion)
	assert.Equal(t, "openai-base-url", r.OpenAIBaseURL)
	assert.Equal(t, "openai-model", r.OpenAIModel)
	assert.True(t, r.OpenAIDisableStreaming)
	assert.Equal(t, "gemini-api-key", r.GeminiAPIKey)
	assert.Equal(t, "gemini-base-url", r.GeminiBaseURL)
	assert.Equal(t, "gemini-model", r.GeminiModel)
}
