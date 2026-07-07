package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/vektah/gqlparser/v2/ast"

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

func llmTestContext() context.Context {
	return engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "llm-test-client",
		SessionID: "llm-test-session",
	})
}

func TestLlmConfig(t *testing.T) {
	q := LLMTestQuery{}

	baseCache, err := dagql.NewCache(context.Background(), "", nil, nil)
	assert.NoError(t, err)
	srv := newCoreDagqlServerForTest(t, q)

	vars := map[string]string{
		"file://.env":                      "",
		"env://ANTHROPIC_API_KEY":          "anthropic-api-key",
		"env://ANTHROPIC_BASE_URL":         "anthropic-base-url",
		"env://ANTHROPIC_MODEL":            "anthropic-model",
		"env://ANTHROPIC_AUTH_TOKEN":       "anthropic-auth-token",
		"env://OPENAI_API_KEY":             "openai-api-key",
		"env://OPENAI_AZURE_VERSION":       "openai-azure-version",
		"env://OPENAI_BASE_URL":            "openai-base-url",
		"env://OPENAI_MODEL":               "openai-model",
		"env://OPENAI_DISABLE_STREAMING":   "t",
		"env://OPENAI_CODEX_AUTH_TOKEN":    "openai-codex-auth-token",
		"env://OPENAI_CODEX_MODEL":         "openai-codex-model",
		"env://OPENAI_CODEX_THINKING_MODE": "openai-codex-thinking-mode",
		"env://GEMINI_API_KEY":             "gemini-api-key",
		"env://GEMINI_BASE_URL":            "gemini-base-url",
		"env://GEMINI_MODEL":               "gemini-model",
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

	ctx := dagql.ContextWithCache(llmTestContext(), baseCache)
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
	assert.Equal(t, "openai-codex-auth-token", r.OpenAICodexAuthToken)
	assert.Equal(t, "openai-codex-model", r.OpenAICodexModel)
	assert.Equal(t, "openai-codex-thinking-mode", r.OpenAICodexThinkingMode)
	assert.Equal(t, "anthropic-auth-token", r.AnthropicAuthToken)
	assert.Equal(t, "gemini-api-key", r.GeminiAPIKey)
	assert.Equal(t, "gemini-base-url", r.GeminiBaseURL)
	assert.Equal(t, "gemini-model", r.GeminiModel)
}

func TestCodexModelRouting(t *testing.T) {
	// A model configured in the Codex slot pins to the Codex backend even when
	// its name looks like a plain OpenAI model (post-GPT-5.4, Codex model IDs
	// no longer contain "codex").
	r := &LLMRouter{
		OpenAICodexAuthToken: "tok",
		OpenAICodexModel:     "gpt-5.5",
	}
	assert.Equal(t, "openai-codex/gpt-5.5", r.DefaultModel())

	ep, err := r.Route("")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, ep.Provider)
	// The routing prefix is stripped for display and the API request.
	assert.Equal(t, "gpt-5.5", ep.Model)

	// With only a Codex token, the default model routes to Codex too.
	r2 := &LLMRouter{OpenAICodexAuthToken: "tok"}
	assert.Equal(t, "openai-codex/"+modelDefaultCodex, r2.DefaultModel())
	epDefault, err := r2.Route("")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epDefault.Provider)
	assert.Equal(t, modelDefaultCodex, epDefault.Model)

	// An explicitly prefixed model routes to Codex regardless of the slot.
	epPrefixed, err := r2.Route("openai-codex/gpt-5.4")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epPrefixed.Provider)
	assert.Equal(t, "gpt-5.4", epPrefixed.Model)

	// A "codex"-named model still routes to Codex (backward compatible).
	epNamed, err := r2.Route("gpt-5.3-codex")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epNamed.Provider)
	assert.Equal(t, "gpt-5.3-codex", epNamed.Model)
}

func TestLLMErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"codex detail", `{"detail":"The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."}`, "The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."},
		{"openai error", `{"error":{"message":"invalid model"}}`, "invalid model"},
		{"bare message", `{"message":"boom"}`, "boom"},
		{"empty", ``, ""},
		{"unrecognized", `{"foo":"bar"}`, ""},
		{"not json", `<html>502 Bad Gateway</html>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, llmErrorMessage([]byte(tc.body)))
		})
	}
}

func TestCodexAPIError(t *testing.T) {
	// The Codex backend's {"detail":...} shape is otherwise dropped by the SDK,
	// which surfaces a bare "400 Bad Request".
	body := `{"detail":"The 'gpt-5-codex' model is not supported when using Codex with a ChatGPT account."}`
	aerr := &openai.Error{
		StatusCode: 400,
		Response:   &http.Response{Body: io.NopCloser(strings.NewReader(body))},
	}
	got := codexAPIError(aerr)
	assert.ErrorContains(t, got, "HTTP 400")
	assert.ErrorContains(t, got, "not supported when using Codex")

	// Non-API errors pass through unchanged.
	plain := errors.New("dial tcp: timeout")
	assert.Equal(t, plain, codexAPIError(plain))
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

			baseCache, err := dagql.NewCache(context.Background(), "", nil, nil)
			assert.NoError(t, err)
			srv := newCoreDagqlServerForTest(t, q)
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

			ctx := dagql.ContextWithCache(llmTestContext(), baseCache)
			r, err := NewLLMRouter(ctx, srv)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, r.OpenAIDisableStreaming)
		})
	}
}

func TestLlmConfigEnvFile(t *testing.T) {
	q := LLMTestQuery{}

	baseCache, err := dagql.NewCache(context.Background(), "", nil, nil)
	assert.NoError(t, err)
	srv := newCoreDagqlServerForTest(t, q)
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

	ctx := dagql.ContextWithCache(llmTestContext(), baseCache)
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
