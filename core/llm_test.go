package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func llmTestContext() context.Context {
	return engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "llm-test-client",
		SessionID: "llm-test-session",
	})
}

// routerWith builds a router from a single client's provider configs, the way
// a CLI with exactly that configuration would.
func routerWith(t *testing.T, providers map[string]*engine.LLMProviderConfig) *LLMRouter {
	t.Helper()
	r, err := NewLLMRouter(&engine.LLMConfig{Providers: providers}, &engine.ClientMetadata{ClientID: "test"})
	require.NoError(t, err)
	return r
}

func TestLLMRouterApply(t *testing.T) {
	client := &engine.ClientMetadata{ClientID: "host"}
	r, err := NewLLMRouter(&engine.LLMConfig{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-x",
		Providers: map[string]*engine.LLMProviderConfig{
			"anthropic": {
				APIKey:            "env://ANTHROPIC_API_KEY",
				BaseURL:           "anthropic-base-url",
				Model:             "anthropic-model",
				SmallModel:        "anthropic-small-model",
				ReasoningEffort:   "high",
				ClaudeCodeVersion: "2.1.999",
			},
			"openai": {
				APIKey:           "llmconfig://openai/api_key",
				BaseURL:          "openai-base-url",
				Model:            "openai-model",
				SmallModel:       "openai-small-model",
				AzureVersion:     "openai-azure-version",
				DisableStreaming: true,
			},
			"openai-codex": {
				AuthToken:          "llmconfig://openai-codex/auth_token",
				AuthTokenExpiresAt: "llmconfig://openai-codex/auth_token_expires_at",
				Model:              "codex-model",
				SmallModel:         "codex-small-model",
				ReasoningEffort:    "medium",
			},
			"google": {
				APIKey:          "env://GEMINI_API_KEY",
				BaseURL:         "gemini-base-url",
				Model:           "gemini-model",
				SmallModel:      "gemini-small-model",
				ReasoningEffort: "low",
			},
			"local": {
				APIKey:     "env://LOCAL_API_KEY",
				BaseURL:    "http://localhost:11434",
				Model:      "llama3",
				SmallModel: "llama3-small",
				APICompat:  "openai",
			},
			"empty": {},
		},
	}, client)
	require.NoError(t, err)

	model, provider := r.DefaultRoute()
	assert.Equal(t, "claude-x", model)
	assert.Equal(t, Anthropic, provider)

	anthropic := r.Providers[Anthropic]
	assert.Equal(t, "env://ANTHROPIC_API_KEY", anthropic.APIKey)
	assert.Equal(t, "anthropic-base-url", anthropic.BaseURL)
	assert.Equal(t, "anthropic-model", anthropic.Model)
	assert.Equal(t, "anthropic-small-model", anthropic.SmallModel)
	assert.Equal(t, "high", anthropic.ReasoningEffort)
	assert.Equal(t, "2.1.999", anthropic.ClaudeCodeVersion)
	assert.Same(t, client, anthropic.client)

	openai := r.Providers[OpenAI]
	assert.Equal(t, "llmconfig://openai/api_key", openai.APIKey)
	assert.Equal(t, "openai-azure-version", openai.AzureVersion)
	assert.True(t, openai.DisableStreaming)

	codex := r.Providers[OpenAICodex]
	assert.Equal(t, "llmconfig://openai-codex/auth_token", codex.AuthToken)
	assert.Equal(t, "llmconfig://openai-codex/auth_token_expires_at", codex.AuthTokenExpiresAt)
	assert.Equal(t, "medium", codex.ReasoningEffort)

	assert.Equal(t, "gemini-model", r.Providers[Google].Model)
	assert.Equal(t, "openai", r.Providers[Local].APICompat)
	assert.Same(t, client, r.localClient)

	// An empty provider entry configures nothing.
	assert.NotContains(t, r.Providers, LLMProvider("empty"))

	// The routing fields land on the endpoints.
	ep, err := r.Route("openai-model", "")
	require.NoError(t, err)
	assert.Equal(t, "openai-azure-version", ep.azureVersion)
	assert.True(t, ep.disableStreaming)
	ep, err = r.Route("claude-x", "")
	require.NoError(t, err)
	assert.Equal(t, "2.1.999", ep.ClaudeCodeVersion)
	assert.False(t, ep.IsOAuth)
}

// TestLLMRouterClaudeCodeVersion covers the claude_code_version override:
// accepted as a bare X.Y.Z, and rejected loudly otherwise rather than falling
// back to the default the user was trying to replace.
func TestLLMRouterClaudeCodeVersion(t *testing.T) {
	for _, bad := range []string{"v2.1.260", "2.1", "latest", "2.1.260-beta"} {
		_, err := NewLLMRouter(&engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
			"anthropic": {ClaudeCodeVersion: bad},
		}}, &engine.ClientMetadata{})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "claude_code_version")
	}
}

// TestLLMRouterLayeredAnthropicAuth covers layered config loads (host client
// first, then the calling/nested client): the Anthropic API key and the
// subscription OAuth token are alternative credentials for the same slot, so
// whichever one a later load supplies must win outright rather than being
// shadowed by a credential accumulated from an earlier load — and the
// credential must resolve against the client that supplied it.
func TestLLMRouterLayeredAnthropicAuth(t *testing.T) {
	host := &engine.ClientMetadata{ClientID: "host"}
	container := &engine.ClientMetadata{ClientID: "container"}
	anthropic := func(cfg *engine.LLMProviderConfig) *engine.LLMConfig {
		return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"anthropic": cfg}}
	}

	t.Run("container API key overrides host OAuth login", func(t *testing.T) {
		r := new(LLMRouter)
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{AuthToken: "llmconfig://anthropic/auth_token"}), host))
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{APIKey: "env://ANTHROPIC_API_KEY"}), container))
		route := r.Providers[Anthropic]
		assert.Equal(t, "env://ANTHROPIC_API_KEY", route.APIKey)
		assert.Empty(t, route.AuthToken)
		assert.Same(t, container, route.client)
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.False(t, ep.IsOAuth)
	})

	t.Run("container OAuth overrides host API key", func(t *testing.T) {
		r := new(LLMRouter)
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{APIKey: "env://ANTHROPIC_API_KEY"}), host))
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{AuthToken: "env://ANTHROPIC_AUTH_TOKEN"}), container))
		route := r.Providers[Anthropic]
		assert.Equal(t, "env://ANTHROPIC_AUTH_TOKEN", route.AuthToken)
		assert.Empty(t, route.APIKey)
		assert.Same(t, container, route.client)
		ep, err := r.Route("claude-x", "")
		require.NoError(t, err)
		assert.True(t, ep.IsOAuth)
	})

	t.Run("container with no auth inherits host OAuth", func(t *testing.T) {
		r := new(LLMRouter)
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{AuthToken: "llmconfig://anthropic/auth_token"}), host))
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{Model: "claude-nested"}), container))
		route := r.Providers[Anthropic]
		assert.Equal(t, "llmconfig://anthropic/auth_token", route.AuthToken)
		assert.Equal(t, "claude-nested", route.Model)
		assert.Same(t, host, route.client, "the credential stays with the client that supplied it")
	})

	t.Run("single load with both keeps both", func(t *testing.T) {
		r := new(LLMRouter)
		require.NoError(t, r.Apply(anthropic(&engine.LLMProviderConfig{
			APIKey:    "env://ANTHROPIC_API_KEY",
			AuthToken: "env://ANTHROPIC_AUTH_TOKEN",
		}), host))
		route := r.Providers[Anthropic]
		assert.Equal(t, "env://ANTHROPIC_API_KEY", route.APIKey)
		assert.Equal(t, "env://ANTHROPIC_AUTH_TOKEN", route.AuthToken)
	})
}

// TestLLMRouterLocalClient covers which client owns the local endpoint. The
// tunnel to a local model must run through the session of the client that
// configured it, and localhost names a different host per client — so a later
// load supplying the same URL string must still take ownership.
func TestLLMRouterLocalClient(t *testing.T) {
	host := &engine.ClientMetadata{ClientID: "host"}
	container := &engine.ClientMetadata{ClientID: "container"}
	local := func(cfg *engine.LLMProviderConfig) *engine.LLMConfig {
		return &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{"local": cfg}}
	}

	r := new(LLMRouter)
	require.NoError(t, r.Apply(local(&engine.LLMProviderConfig{BaseURL: "http://localhost:11434"}), host))
	assert.Same(t, host, r.localClient)

	// A load supplying nothing leaves the URL and its owner alone.
	require.NoError(t, r.Apply(local(&engine.LLMProviderConfig{Model: "llama3"}), container))
	assert.Same(t, host, r.localClient)
	assert.Equal(t, "http://localhost:11434", r.Providers[Local].BaseURL)

	require.NoError(t, r.Apply(local(&engine.LLMProviderConfig{BaseURL: "http://localhost:11434"}), container))
	assert.Same(t, container, r.localClient)
}

func TestLLMRouterDefaultRoute(t *testing.T) {
	t.Run("explicit default wins over provider priority", func(t *testing.T) {
		r, err := NewLLMRouter(&engine.LLMConfig{
			DefaultProvider: "anthropic",
			DefaultModel:    "claude-x",
			Providers: map[string]*engine.LLMProviderConfig{
				"openai":    {APIKey: "env://OPENAI_API_KEY", Model: "gpt-y"},
				"anthropic": {APIKey: "env://ANTHROPIC_API_KEY"},
			},
		}, &engine.ClientMetadata{})
		require.NoError(t, err)
		model, provider := r.DefaultRoute()
		assert.Equal(t, "claude-x", model)
		assert.Equal(t, Anthropic, provider)
	})

	t.Run("openrouter is only ever selected explicitly", func(t *testing.T) {
		// An OpenAI-compatible aggregator serving "anthropic/..." model names.
		// Prefix matching alone would send those to Anthropic, so the
		// provider must come from the config's default or the caller.
		r, err := NewLLMRouter(&engine.LLMConfig{
			DefaultProvider: "openrouter",
			DefaultModel:    "anthropic/claude-sonnet-4.5",
			Providers: map[string]*engine.LLMProviderConfig{
				"openrouter": {APIKey: "env://OPENROUTER_API_KEY"},
				"openai":     {APIKey: "env://OPENAI_API_KEY"},
			},
		}, &engine.ClientMetadata{})
		require.NoError(t, err)
		ep, err := r.Route("", "")
		require.NoError(t, err)
		assert.Equal(t, OpenRouter, ep.Provider)
		assert.Equal(t, "anthropic/claude-sonnet-4.5", ep.Model)
		assert.Equal(t, openRouterBaseURL, ep.BaseURL, "the base URL defaults when the config names none")

		// Both OpenAI-compatible providers coexist with their own credentials.
		ep, err = r.Route("gpt-4.1", "")
		require.NoError(t, err)
		assert.Equal(t, OpenAI, ep.Provider)
		ep, err = r.Route("openai/gpt-4.1", string(OpenRouter))
		require.NoError(t, err)
		assert.Equal(t, OpenRouter, ep.Provider)

		// A configured openrouter model pins the provider too.
		r = routerWith(t, map[string]*engine.LLMProviderConfig{
			"openrouter": {APIKey: "env://OPENROUTER_API_KEY", Model: "google/gemini-2.5-pro", BaseURL: "https://proxy.example/v1"},
		})
		model, provider := r.DefaultRoute()
		assert.Equal(t, "google/gemini-2.5-pro", model)
		assert.Equal(t, OpenRouter, provider)
		ep, err = r.Route(model, string(provider))
		require.NoError(t, err)
		assert.Equal(t, "https://proxy.example/v1", ep.BaseURL)

		// A key alone selects OpenRouter's default model.
		r = routerWith(t, map[string]*engine.LLMProviderConfig{
			"openrouter": {APIKey: "env://OPENROUTER_API_KEY"},
		})
		model, provider = r.DefaultRoute()
		assert.Equal(t, modelDefaultOpenRouter, model)
		assert.Equal(t, OpenRouter, provider)
	})

	t.Run("provider priority among configured models", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {Model: "claude-x"},
			"google":    {Model: "gemini-y"},
		})
		assert.Equal(t, "claude-x", r.DefaultModel())
		r = routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {Model: "claude-x"},
			"openai":    {Model: "gpt-z"},
		})
		assert.Equal(t, "gpt-z", r.DefaultModel())
	})

	t.Run("credential alone selects the provider's built-in default", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"anthropic": {APIKey: "env://ANTHROPIC_API_KEY"},
		})
		assert.Equal(t, modelDefaultAnthropic, r.DefaultModel())
		r = routerWith(t, map[string]*engine.LLMProviderConfig{
			"google": {APIKey: "env://GEMINI_API_KEY"},
		})
		assert.Equal(t, modelDefaultGoogle, r.DefaultModel())
		r = routerWith(t, map[string]*engine.LLMProviderConfig{
			"openai": {BaseURL: "http://model-runner/engines/v1"},
		})
		assert.Equal(t, modelDefaultMeta, r.DefaultModel())
	})

	t.Run("nothing configured", func(t *testing.T) {
		assert.Empty(t, new(LLMRouter).DefaultModel())
	})
}

func TestLocalModelRouting(t *testing.T) {
	// A local endpoint is keyed by an exact model-name match (it has no naming
	// convention to detect), and wins ahead of the prefix-based heuristics.
	r := routerWith(t, map[string]*engine.LLMProviderConfig{
		"local": {
			BaseURL:   "http://localhost:11434",
			Model:     "llama3",
			APICompat: "openai",
			APIKey:    "env://LOCAL_API_KEY",
		},
	})
	// With only a local endpoint configured, its model is the default.
	assert.Equal(t, "llama3", r.DefaultModel())

	ep, err := r.Route("llama3", "")
	assert.NoError(t, err)
	assert.Equal(t, Local, ep.Provider)
	assert.Equal(t, "llama3", ep.Model)
	assert.Equal(t, "http://localhost:11434", ep.BaseURL)
	assert.Equal(t, "openai", ep.apiCompat)

	// A local model named to look like another provider's still routes local.
	r2 := routerWith(t, map[string]*engine.LLMProviderConfig{
		"local": {BaseURL: "http://localhost:1234", Model: "gpt-oss", APICompat: "anthropic"},
	})
	ep2, err := r2.Route("gpt-oss", "")
	assert.NoError(t, err)
	assert.Equal(t, Local, ep2.Provider)

	// A different model name does not match the local slot.
	assert.False(t, r.isLocalModel("some-other-model"))
	// Nor does the slot match when it is not fully configured.
	assert.False(t, routerWith(t, map[string]*engine.LLMProviderConfig{
		"local": {Model: "llama3"},
	}).isLocalModel("llama3"))

	// An unsupported API compatibility mode is a routing error.
	r3 := routerWith(t, map[string]*engine.LLMProviderConfig{
		"local": {BaseURL: "http://localhost:11434", Model: "llama3", APICompat: "bogus"},
	})
	_, err = r3.Route("llama3", "")
	assert.Error(t, err)
}

func TestCodexModelRouting(t *testing.T) {
	// A model configured in the Codex slot pins to the Codex backend even when
	// its name looks like a plain OpenAI model (post-GPT-5.4, Codex model IDs
	// no longer contain "codex").
	r := routerWith(t, map[string]*engine.LLMProviderConfig{
		"openai-codex": {AuthToken: "llmconfig://openai-codex/auth_token", Model: "gpt-5.5"},
	})
	assert.Equal(t, "openai-codex/gpt-5.5", r.DefaultModel())

	ep, err := r.Route("", "")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, ep.Provider)
	// The routing prefix is stripped for display and the API request.
	assert.Equal(t, "gpt-5.5", ep.Model)

	// With only a Codex token, the default model routes to Codex too.
	r2 := routerWith(t, map[string]*engine.LLMProviderConfig{
		"openai-codex": {AuthToken: "llmconfig://openai-codex/auth_token"},
	})
	assert.Equal(t, "openai-codex/"+modelDefaultCodex, r2.DefaultModel())
	epDefault, err := r2.Route("", "")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epDefault.Provider)
	assert.Equal(t, modelDefaultCodex, epDefault.Model)

	// An explicitly prefixed model routes to Codex regardless of the slot.
	epPrefixed, err := r2.Route("openai-codex/gpt-5.4", "")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epPrefixed.Provider)
	assert.Equal(t, "gpt-5.4", epPrefixed.Model)

	// A "codex"-named model still routes to Codex (backward compatible).
	epNamed, err := r2.Route("gpt-5.3-codex", "")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, epNamed.Provider)
	assert.Equal(t, "gpt-5.3-codex", epNamed.Model)

	// An explicit Codex default is pinned to Codex as well.
	r3, err := NewLLMRouter(&engine.LLMConfig{
		DefaultProvider: "openai-codex",
		DefaultModel:    "gpt-5.5",
	}, &engine.ClientMetadata{})
	require.NoError(t, err)
	model, provider := r3.DefaultRoute()
	assert.Equal(t, "openai-codex/gpt-5.5", model)
	assert.Equal(t, OpenAICodex, provider)
}

func TestSmallModelRouting(t *testing.T) {
	t.Run("configured model wins and provider remains concrete", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"openai": {SmallModel: "my-fast-model"},
		})
		model, ok := r.SmallModel(OpenAI)
		require.True(t, ok)
		assert.Equal(t, "my-fast-model", model)

		ep, err := r.Route(model, string(OpenAI))
		require.NoError(t, err)
		assert.Equal(t, OpenAI, ep.Provider)
		assert.Equal(t, "my-fast-model", ep.Model)
	})

	t.Run("catalog fallback follows provider", func(t *testing.T) {
		r := new(LLMRouter)
		model, ok := r.SmallModel(Anthropic)
		require.True(t, ok)
		assert.Equal(t, "claude-haiku-4-5-20251001", model)
	})

	t.Run("local and unknown providers safely retain their route", func(t *testing.T) {
		r := new(LLMRouter)
		for _, provider := range []LLMProvider{Local, Other, "unknown"} {
			model, ok := r.SmallModel(provider)
			assert.False(t, ok)
			assert.Empty(t, model)
		}
	})

	t.Run("configured local model is supported", func(t *testing.T) {
		r := routerWith(t, map[string]*engine.LLMProviderConfig{
			"local": {SmallModel: "qwen3:small"},
		})
		model, ok := r.SmallModel(Local)
		require.True(t, ok)
		assert.Equal(t, "qwen3:small", model)
	})
}

func TestExplicitProviderRouting(t *testing.T) {
	r := routerWith(t, map[string]*engine.LLMProviderConfig{
		"anthropic": {APIKey: "env://ANTHROPIC_API_KEY"},
		"openai":    {APIKey: "env://OPENAI_API_KEY"},
	})

	// An explicit provider overrides model-name pattern matching: a "codex"-
	// named fine-tune can be pinned to plain OpenAI, and a model with no
	// recognizable name can be pinned to Anthropic.
	ep, err := r.Route("my-codex-ft", string(OpenAI))
	assert.NoError(t, err)
	assert.Equal(t, OpenAI, ep.Provider)
	assert.Equal(t, "my-codex-ft", ep.Model)

	ep, err = r.Route("some-custom-model", string(Anthropic))
	assert.NoError(t, err)
	assert.Equal(t, Anthropic, ep.Provider)

	// An unknown provider is an error, not a silent fallback.
	_, err = r.Route("some-model", "bogus")
	assert.ErrorContains(t, err, `unknown LLM provider "bogus"`)
}

// TestOpenAIRequestUsesNonStrictNullableToolSchema locks the provider boundary:
// strict mode stays off, while nullable GraphQL arguments remain properties
// that explicitly accept null without becoming required.
func TestOpenAIRequestUsesNonStrictNullableToolSchema(t *testing.T) {
	requestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestBody <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":0,
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
		}`)
	}))
	t.Cleanup(server.Close)

	schema := objectToolsTestSchema(t)
	toolSchema, err := objectMethodSchema(schema, fieldByName(schema.Types["Doug"], "read"), conversationToolArgs)
	require.NoError(t, err)

	endpoint := &LLMEndpoint{
		Model:    "test-model",
		Provider: OpenAI,
		BaseURL:  server.URL,
	}
	client := newOpenAIClient(endpoint, "", true)
	_, err = client.SendQuery(t.Context(), []*LLMMessage{{
		Role:    LLMMessageRoleUser,
		Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}},
	}}, []LLMTool{{
		Name:   "read",
		Schema: toolSchema,
	}}, &LLMCallOpts{})
	require.NoError(t, err)

	var request map[string]any
	require.NoError(t, json.Unmarshal(<-requestBody, &request))
	tools := request["tools"].([]any)
	require.Len(t, tools, 1)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	require.NotContains(t, function, "strict")

	parameters := function["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	require.Contains(t, properties, "offset")
	date := properties["date"].(map[string]any)
	require.Equal(t, "string", requireNullableJSONSchema(t, date)["type"])
	require.Contains(t, date, "default")
	require.Nil(t, date["default"])
	require.Equal(t, []any{"filePath"}, parameters["required"])
}

func TestOpenAIConvertToolCalls(t *testing.T) {
	history := []*LLMMessage{{
		Role: LLMMessageRoleAssistant,
		Content: []*LLMContentBlock{
			{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{"path":"/x"}`)},
			{Kind: LLMContentToolCall, CallID: "call_2", ToolName: "noargs"},
		},
	}}
	messages, err := convertHistoryToOpenAI(history)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	data, err := json.Marshal(messages[0].OfAssistant.ToolCalls)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"/x\"}"}},
		{"id":"call_2","type":"function","function":{"name":"noargs","arguments":"{}"}}
	]`, string(data))
}

func TestContentBlockInputRoundTrip(t *testing.T) {
	// Regression: content block InputObjects must be built via the decoder so
	// their fields are populated. A bare struct literal leaves fields nil and
	// panics ("missing decoded fields") when the withResponse selector is
	// serialized to a call literal — which broke every assistant turn.
	blocks := []*LLMContentBlock{
		{Kind: LLMContentText, Text: "hi"},
		{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{"path":"/x"}`)},
	}
	arr := make(dagql.ArrayInput[dagql.InputObject[LLMContentBlockInput]], len(blocks))
	for i, block := range blocks {
		decoded, err := (dagql.InputObject[LLMContentBlockInput]{}).Decoder().DecodeInput(map[string]any{
			"kind":      string(block.Kind),
			"text":      block.Text,
			"callId":    block.CallID,
			"toolName":  block.ToolName,
			"arguments": string(block.Arguments),
			"errored":   block.Errored,
			"signature": block.Signature,
		})
		assert.NoError(t, err)
		input, ok := decoded.(dagql.InputObject[LLMContentBlockInput])
		assert.True(t, ok)
		arr[i] = input
	}

	// The bug manifested here: ToLiteral panicked when fields were nil.
	assert.NotPanics(t, func() { _ = arr.ToLiteral() })

	// Fields decode onto Value, including the JSON arguments.
	assert.Equal(t, LLMContentText, arr[0].Value.Kind)
	assert.Equal(t, "hi", arr[0].Value.Text)
	assert.Equal(t, LLMContentToolCall, arr[1].Value.Kind)
	assert.Equal(t, "call_1", arr[1].Value.CallID)
	assert.Equal(t, "read", arr[1].Value.ToolName)
	assert.Equal(t, JSON(`{"path":"/x"}`), arr[1].Value.Arguments)

	// Regression: empty "arguments" decodes to nil and is dropped from the
	// serialized literal, so reloading a saved ID decodes a map with no
	// "arguments" key at all. That must not fail as a missing required field
	// (it did, breaking session reload for every non-tool-call block).
	decoded, err := (dagql.InputObject[LLMContentBlockInput]{}).Decoder().DecodeInput(map[string]any{
		"kind":      string(LLMContentText),
		"text":      "reloaded",
		"callId":    "",
		"toolName":  "",
		"errored":   false,
		"signature": "",
	})
	assert.NoError(t, err)
	input, ok := decoded.(dagql.InputObject[LLMContentBlockInput])
	assert.True(t, ok)
	assert.Equal(t, LLMContentText, input.Value.Kind)
	assert.Equal(t, "reloaded", input.Value.Text)
	assert.Nil(t, input.Value.Arguments)
	assert.NotPanics(t, func() { _ = input.ToLiteral() })
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

// TestCodexReasoningRoundTrip covers B1: a streamed reasoning item is packed
// into a THINKING block's Signature and reconstructed into a reusable
// reasoning input item, while non-reasoning signatures are ignored.
func TestCodexReasoningRoundTrip(t *testing.T) {
	r := responses.ResponseReasoningItem{
		ID:               "rs_123",
		EncryptedContent: "encrypted-blob",
		Summary: []responses.ResponseReasoningItemSummary{
			{Text: "step one"},
			{Text: "step two"},
		},
	}
	summary, sig := encodeCodexReasoning(r)
	assert.Equal(t, "step one\n\nstep two", summary)
	require.NotEmpty(t, sig)

	item, ok := decodeCodexReasoning(sig)
	require.True(t, ok)
	assert.Equal(t, "rs_123", item.ID)
	assert.Equal(t, "encrypted-blob", item.EncryptedContent.Or(""))
	require.Len(t, item.Summary, 2)
	assert.Equal(t, "step one", item.Summary[0].Text)

	// A signature that isn't a codex reasoning payload (e.g. an Anthropic
	// thinking signature) is ignored rather than incorrectly resubmitted.
	_, ok = decodeCodexReasoning("not-json-opaque-signature")
	assert.False(t, ok)

	// Without encrypted content there's nothing to resubmit under Store:false.
	_, noEnc := encodeCodexReasoning(responses.ResponseReasoningItem{ID: "rs_x"})
	_, ok = decodeCodexReasoning(noEnc)
	assert.False(t, ok)
}

// TestCodexConvertReasoningOrder covers B1: a reasoning item is resubmitted
// immediately before the function call it produced.
func TestCodexConvertReasoningOrder(t *testing.T) {
	_, sig := encodeCodexReasoning(responses.ResponseReasoningItem{
		ID:               "rs_1",
		EncryptedContent: "enc",
	})
	history := []*LLMMessage{
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "hi"}}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentThinking, Signature: sig},
			{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "do_thing", Arguments: JSON(`{"x":1}`)},
		}},
	}
	_, items, err := convertToCodexResponsesFormat(history)
	require.NoError(t, err)
	require.Len(t, items, 3) // user message, reasoning, function call
	assert.NotNil(t, items[0].OfMessage)
	require.NotNil(t, items[1].OfReasoning)
	assert.Equal(t, "rs_1", items[1].OfReasoning.ID)
	require.NotNil(t, items[2].OfFunctionCall)
	assert.Equal(t, "call_1", items[2].OfFunctionCall.CallID)
}

// TestCodexConvertEmptyToolArgs covers B2: an empty tool-arguments string is
// normalized to "{}" on the outbound request.
func TestCodexConvertEmptyToolArgs(t *testing.T) {
	history := []*LLMMessage{
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentToolCall, CallID: "c1", ToolName: "noargs"},
		}},
	}
	_, items, err := convertToCodexResponsesFormat(history)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].OfFunctionCall)
	assert.Equal(t, "{}", items[0].OfFunctionCall.Arguments)
}

func TestCodexConvertToolResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		errored bool
		want    string
	}{
		{"success", "contents", false, `{"type":"function_call_output","call_id":"call_1","output":"contents"}`},
		{"empty", "", false, `{"type":"function_call_output","call_id":"call_1","output":""}`},
		{"error", "not found", true, `{"type":"function_call_output","call_id":"call_1","output":"error: not found"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history := []*LLMMessage{{
				Role: LLMMessageRoleUser,
				Content: []*LLMContentBlock{{
					Kind: LLMContentToolResult, CallID: "call_1", Text: tc.text, Errored: tc.errored,
				}},
			}}
			_, items, err := convertToCodexResponsesFormat(history)
			require.NoError(t, err)
			require.Len(t, items, 1)
			data, err := json.Marshal(items[0])
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(data))
		})
	}
}
