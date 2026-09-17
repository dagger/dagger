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

func TestLLMTranscript(t *testing.T) {
	text := func(s string) *LLMContentBlock {
		return &LLMContentBlock{Kind: LLMContentText, Text: s}
	}
	llm := &LLM{Messages: []*LLMMessage{
		{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{text("instructions")}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{text("question"), text("details")}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentThinking, Text: "consider"}, text("first"), text("second"),
			{Kind: LLMContentThinking, Text: "again"},
			{Kind: LLMContentToolCall, ToolName: "lookup", Arguments: JSON(`{"key":1}`)},
			{Kind: LLMContentToolCall, ToolName: "list", Arguments: JSON(`{}`)},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{
			{Kind: LLMContentToolResult, Text: "found"},
			{Kind: LLMContentToolResult, Text: "failed", Errored: true},
		}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{text("answer")}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{
			{Kind: LLMContentToolCall, ToolName: "finish", Arguments: JSON(`{}`)},
		}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{text("")}},
		{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentThinking}}},
		{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult}}},
		{Role: LLMMessageRoleUser},
		{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{text("more instructions")}},
	}}
	user := "[User]: question\n\n[User]: details"
	assistant := "[Assistant thinking]: consider\nagain\n\n[Assistant]: first\nsecond\n\n[Assistant tool calls]: lookup({\"key\":1}); list({})"
	results := "[Tool result]: found\n\n[Tool result ERROR]: failed"
	answer := "[Assistant]: answer"
	tool := "[Assistant tool calls]: finish({})"
	join := func(parts ...string) string { return strings.Join(parts, "\n\n") }
	count := func(n int) dagql.Optional[dagql.Int] { return dagql.Opt(dagql.Int(n)) }
	roles := func(rs ...LLMMessageRole) dagql.Optional[dagql.ArrayInput[LLMMessageRole]] {
		return dagql.Opt(dagql.ArrayInput[LLMMessageRole](rs))
	}
	kinds := func(ks ...LLMContentBlockKind) dagql.Optional[dagql.ArrayInput[LLMContentBlockKind]] {
		return dagql.Opt(dagql.ArrayInput[LLMContentBlockKind](ks))
	}
	for _, tc := range []struct {
		name string
		args LLMTranscriptArgs
		want string
	}{
		{"default compatibility", LLMTranscriptArgs{}, join(user, assistant, results, answer, tool)},
		{"head message grouping", LLMTranscriptArgs{Limit: count(2)}, join(user, assistant)},
		{"head offset", LLMTranscriptArgs{Limit: count(2), Offset: 1}, join(assistant, results)},
		{"offset without count", LLMTranscriptArgs{Offset: 3}, join(answer, tool)},
		{"tail chronological", LLMTranscriptArgs{Last: count(2)}, join(answer, tool)},
		{"tail older page", LLMTranscriptArgs{Last: count(2), Offset: 2}, join(assistant, results)},
		{"tail oldest partial page", LLMTranscriptArgs{Last: count(2), Offset: 4}, user},
		{"text head", LLMTranscriptArgs{Limit: count(2), ContentKinds: kinds(LLMContentText)}, join(user, "[Assistant]: first\nsecond")},
		{"text tail skips tools and empties", LLMTranscriptArgs{Last: count(2), ContentKinds: kinds(LLMContentText)}, join("[Assistant]: first\nsecond", answer)},
		{"text tail offset counts messages", LLMTranscriptArgs{Last: count(1), Offset: 1, ContentKinds: kinds(LLMContentText)}, "[Assistant]: first\nsecond"},
		{"text head offset counts messages", LLMTranscriptArgs{Limit: count(1), Offset: 1, ContentKinds: kinds(LLMContentText)}, "[Assistant]: first\nsecond"},
		{"roles before pagination", LLMTranscriptArgs{Limit: count(1), Roles: roles(LLMMessageRoleAssistant)}, assistant},
		{"roles and kinds", LLMTranscriptArgs{Last: count(1), Offset: 1, Roles: roles(LLMMessageRoleAssistant), ContentKinds: kinds(LLMContentText)}, "[Assistant]: first\nsecond"},
		{"explicit system", LLMTranscriptArgs{Roles: roles(LLMMessageRoleSystem)}, join("[System]: instructions", "[System]: more instructions")},
		{"system with user", LLMTranscriptArgs{Limit: count(2), Roles: roles(LLMMessageRoleSystem, LLMMessageRoleUser)}, join("[System]: instructions", user)},
		{"tool results only", LLMTranscriptArgs{Limit: count(1), ContentKinds: kinds(LLMContentToolResult)}, results},
		{"thinking only", LLMTranscriptArgs{ContentKinds: kinds(LLMContentThinking)}, "[Assistant thinking]: consider\nagain"},
		{"multiple kinds", LLMTranscriptArgs{Limit: count(1), Roles: roles(LLMMessageRoleAssistant), ContentKinds: kinds(LLMContentText, LLMContentThinking)}, "[Assistant thinking]: consider\nagain\n\n[Assistant]: first\nsecond"},
		{"empty roles", LLMTranscriptArgs{Roles: roles()}, join(user, assistant, results, answer, tool)},
		{"empty kinds", LLMTranscriptArgs{ContentKinds: kinds()}, join(user, assistant, results, answer, tool)},
		{"empty filters with tail offset", LLMTranscriptArgs{Roles: roles(), ContentKinds: kinds(), Last: count(2), Offset: 1}, join(results, answer)},
		{"empty roles with text filter", LLMTranscriptArgs{Roles: roles(), ContentKinds: kinds(LLMContentText), Limit: count(1)}, user},
		{"empty kinds with system role", LLMTranscriptArgs{Roles: roles(LLMMessageRoleSystem), ContentKinds: kinds()}, join("[System]: instructions", "[System]: more instructions")},
		{"no matching content", LLMTranscriptArgs{Roles: roles(LLMMessageRoleSystem), ContentKinds: kinds(LLMContentToolCall)}, ""},
		{"zero limit", LLMTranscriptArgs{Limit: count(0)}, join(user, assistant, results, answer, tool)},
		{"zero last", LLMTranscriptArgs{Last: count(0)}, join(user, assistant, results, answer, tool)},
		{"both zero limits", LLMTranscriptArgs{Limit: count(0), Last: count(0)}, join(user, assistant, results, answer, tool)},
		{"zero limit with tail", LLMTranscriptArgs{Limit: count(0), Last: count(2), Offset: 1}, join(results, answer)},
		{"zero last with head", LLMTranscriptArgs{Limit: count(2), Last: count(0), Offset: 1}, join(assistant, results)},
		{"zero limit with offset", LLMTranscriptArgs{Limit: count(0), Offset: 3}, join(answer, tool)},
		{"zero last with offset skips from start", LLMTranscriptArgs{Last: count(0), Offset: 3}, join(answer, tool)},
		{"both zero with filtered offset", LLMTranscriptArgs{Limit: count(0), Last: count(0), Offset: 1, ContentKinds: kinds(LLMContentText)}, join("[Assistant]: first\nsecond", answer)},
		{"head beyond end", LLMTranscriptArgs{Offset: 5}, ""},
		{"tail beyond end", LLMTranscriptArgs{Last: count(2), Offset: 5}, ""},
		{"large head bounds", LLMTranscriptArgs{Limit: count(int(^uint(0) >> 1)), Offset: 4}, tool},
		{"large tail bounds", LLMTranscriptArgs{Last: count(int(^uint(0) >> 1)), Offset: 4}, user},
		{"large offset", LLMTranscriptArgs{Last: count(2), Offset: int(^uint(0) >> 1)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := llm.Transcript(tc.args)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	for _, tc := range []struct {
		name string
		args LLMTranscriptArgs
		err  string
	}{
		{"both limits", LLMTranscriptArgs{Limit: count(1), Last: count(1)}, "positive limit and last values are mutually exclusive"},
		{"negative limit with zero last", LLMTranscriptArgs{Limit: count(-1), Last: count(0)}, "limit must be non-negative"},
		{"negative last with zero limit", LLMTranscriptArgs{Limit: count(0), Last: count(-1)}, "last must be non-negative"},
		{"negative limit", LLMTranscriptArgs{Limit: count(-1)}, "limit must be non-negative"},
		{"negative last", LLMTranscriptArgs{Last: count(-1)}, "last must be non-negative"},
		{"negative offset", LLMTranscriptArgs{Offset: -1}, "offset must be non-negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid arguments must fail even for an empty conversation.
			got, err := (&LLM{}).Transcript(tc.args)
			require.EqualError(t, err, tc.err)
			require.Empty(t, got)
		})
	}

	t.Run("empty conversation", func(t *testing.T) {
		got, err := (&LLM{}).Transcript(LLMTranscriptArgs{Last: count(10), Offset: 2})
		require.NoError(t, err)
		require.Empty(t, got)
	})
	t.Run("unrenderable and nil content", func(t *testing.T) {
		got, err := (&LLM{Messages: []*LLMMessage{nil, {
			Role: LLMMessageRoleUser, Content: []*LLMContentBlock{nil, {Kind: LLMContentThinking, Text: "not rendered"}},
		}}}).Transcript(LLMTranscriptArgs{})
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestLlmConfig(t *testing.T) {
	q := LLMTestQuery{}

	baseCache, err := dagql.NewCache(context.Background(), "", nil, nil)
	assert.NoError(t, err)
	srv := newCoreDagqlServerForTest(t, q)

	vars := map[string]string{
		"file://.env":                         "",
		"env://ANTHROPIC_API_KEY":             "anthropic-api-key",
		"env://ANTHROPIC_BASE_URL":            "anthropic-base-url",
		"env://ANTHROPIC_MODEL":               "anthropic-model",
		"env://ANTHROPIC_SMALL_MODEL":         "anthropic-small-model",
		"env://ANTHROPIC_AUTH_TOKEN":          "anthropic-auth-token",
		"env://ANTHROPIC_REASONING_EFFORT":    "anthropic-reasoning-effort",
		"env://ANTHROPIC_CLAUDE_CODE_VERSION": "2.1.999",
		"env://OPENAI_API_KEY":                "openai-api-key",
		"env://OPENAI_AZURE_VERSION":          "openai-azure-version",
		"env://OPENAI_BASE_URL":               "openai-base-url",
		"env://OPENAI_MODEL":                  "openai-model",
		"env://OPENAI_SMALL_MODEL":            "openai-small-model",
		"env://OPENAI_DISABLE_STREAMING":      "t",
		"env://OPENAI_CODEX_AUTH_TOKEN":       "openai-codex-auth-token",
		"env://OPENAI_CODEX_MODEL":            "openai-codex-model",
		"env://OPENAI_CODEX_SMALL_MODEL":      "openai-codex-small-model",
		"env://OPENAI_CODEX_REASONING_EFFORT": "openai-codex-reasoning-effort",
		"env://GEMINI_API_KEY":                "gemini-api-key",
		"env://GEMINI_BASE_URL":               "gemini-base-url",
		"env://GEMINI_MODEL":                  "gemini-model",
		"env://GEMINI_SMALL_MODEL":            "gemini-small-model",
		"env://GEMINI_REASONING_EFFORT":       "gemini-reasoning-effort",
		"env://LOCAL_BASE_URL":                "local-base-url",
		"env://LOCAL_MODEL":                   "local-model",
		"env://LOCAL_SMALL_MODEL":             "local-small-model",
		"env://LOCAL_API_COMPAT":              "openai",
		"env://LOCAL_API_KEY":                 "local-api-key",
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
	assert.Equal(t, "anthropic-small-model", r.AnthropicSmallModel)
	assert.Equal(t, "openai-api-key", r.OpenAIAPIKey)
	assert.Equal(t, "openai-azure-version", r.OpenAIAzureVersion)
	assert.Equal(t, "openai-base-url", r.OpenAIBaseURL)
	assert.Equal(t, "openai-model", r.OpenAIModel)
	assert.Equal(t, "openai-small-model", r.OpenAISmallModel)
	assert.True(t, r.OpenAIDisableStreaming)
	assert.Equal(t, "openai-codex-auth-token", r.OpenAICodexAuthToken)
	assert.Equal(t, "openai-codex-model", r.OpenAICodexModel)
	assert.Equal(t, "openai-codex-small-model", r.OpenAICodexSmallModel)
	assert.Equal(t, "openai-codex-reasoning-effort", r.OpenAICodexReasoningEffort)
	assert.Equal(t, "anthropic-auth-token", r.AnthropicAuthToken)
	assert.Equal(t, "anthropic-reasoning-effort", r.AnthropicReasoningEffort)
	assert.Equal(t, "2.1.999", r.AnthropicClaudeCodeVersion)
	assert.Equal(t, "gemini-api-key", r.GeminiAPIKey)
	assert.Equal(t, "gemini-base-url", r.GeminiBaseURL)
	assert.Equal(t, "gemini-model", r.GeminiModel)
	assert.Equal(t, "gemini-small-model", r.GeminiSmallModel)
	assert.Equal(t, "gemini-reasoning-effort", r.GeminiReasoningEffort)
	assert.Equal(t, "local-base-url", r.LocalBaseURL)
	assert.Equal(t, "local-model", r.LocalModel)
	assert.Equal(t, "local-small-model", r.LocalSmallModel)
	assert.Equal(t, "openai", r.LocalAPICompat)
	assert.Equal(t, "local-api-key", r.LocalAPIKey)
}

// getenvFrom builds a LoadConfig getenv func serving values from a plain map,
// so layering tests need no dagql plumbing.
func getenvFrom(m map[string]string) func(context.Context, string) (string, error) {
	return func(_ context.Context, k string) (string, error) {
		return m[k], nil
	}
}

// TestLlmConfigLayeredAnthropicAuth covers layered config loads (host client
// first, then the calling/nested client): the Anthropic API key and the
// subscription OAuth token are alternative credentials for the same slot, so
// whichever one a later load supplies must win outright rather than being
// shadowed by a credential accumulated from an earlier load.
func TestLlmConfigLayeredAnthropicAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("container API key overrides host OAuth login", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_AUTH_TOKEN": "host-oauth",
		}))
		require.NoError(t, err)
		_, err = r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_API_KEY": "container-key",
		}))
		require.NoError(t, err)
		assert.Equal(t, "container-key", r.AnthropicAPIKey)
		assert.Empty(t, r.AnthropicAuthToken)
		assert.False(t, r.AnthropicIsOAuth)
	})

	t.Run("container OAuth overrides host API key", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_API_KEY": "host-key",
		}))
		require.NoError(t, err)
		_, err = r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_AUTH_TOKEN": "container-oauth",
		}))
		require.NoError(t, err)
		assert.Equal(t, "container-oauth", r.AnthropicAuthToken)
		assert.Empty(t, r.AnthropicAPIKey)
		assert.True(t, r.AnthropicIsOAuth)
	})

	t.Run("container with no auth inherits host OAuth", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_AUTH_TOKEN": "host-oauth",
		}))
		require.NoError(t, err)
		_, err = r.LoadConfig(ctx, getenvFrom(map[string]string{}))
		require.NoError(t, err)
		assert.Equal(t, "host-oauth", r.AnthropicAuthToken)
		assert.True(t, r.AnthropicIsOAuth)
	})

	t.Run("single load with both prefers OAuth", func(t *testing.T) {
		r := new(LLMRouter)
		_, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
			"ANTHROPIC_API_KEY":    "key",
			"ANTHROPIC_AUTH_TOKEN": "oauth",
		}))
		require.NoError(t, err)
		assert.Equal(t, "key", r.AnthropicAPIKey)
		assert.Equal(t, "oauth", r.AnthropicAuthToken)
		assert.True(t, r.AnthropicIsOAuth)
	})
}

// TestLlmConfigLocalSupplied covers LoadConfig's report of whether THIS load
// supplied a local base URL. The tunnel to a local model must run through the
// session of the client that configured it, and localhost names a different
// host per client — so the signal must fire even when the URL string is
// unchanged from an earlier load.
func TestLlmConfigLocalSupplied(t *testing.T) {
	ctx := context.Background()

	r := new(LLMRouter)
	supplied, err := r.LoadConfig(ctx, getenvFrom(map[string]string{
		"LOCAL_BASE_URL": "http://localhost:11434",
	}))
	require.NoError(t, err)
	assert.True(t, supplied)
	assert.Equal(t, "http://localhost:11434", r.LocalBaseURL)

	// A load supplying nothing leaves the URL alone and reports false.
	supplied, err = r.LoadConfig(ctx, getenvFrom(map[string]string{}))
	require.NoError(t, err)
	assert.False(t, supplied)
	assert.Equal(t, "http://localhost:11434", r.LocalBaseURL)

	// Supplying a value identical to the already-loaded one must still count:
	// the same "http://localhost:11434" string reaches a different host from
	// each client.
	supplied, err = r.LoadConfig(ctx, getenvFrom(map[string]string{
		"LOCAL_BASE_URL": "http://localhost:11434",
	}))
	require.NoError(t, err)
	assert.True(t, supplied)
	assert.Equal(t, "http://localhost:11434", r.LocalBaseURL)
}

func TestLocalModelRouting(t *testing.T) {
	// A local endpoint is keyed by an exact model-name match (it has no naming
	// convention to detect), and wins ahead of the prefix-based heuristics.
	r := &LLMRouter{
		LocalBaseURL:   "http://localhost:11434",
		LocalModel:     "llama3",
		LocalAPICompat: "openai",
		LocalAPIKey:    "sk-local",
	}
	// With only a local endpoint configured, its model is the default.
	assert.Equal(t, "llama3", r.DefaultModel())

	ep, err := r.Route("llama3", "")
	assert.NoError(t, err)
	assert.Equal(t, Local, ep.Provider)
	assert.Equal(t, "llama3", ep.Model)
	assert.Equal(t, "http://localhost:11434", ep.BaseURL)
	assert.Equal(t, "sk-local", ep.Key)

	// A local model named to look like another provider's still routes local.
	r2 := &LLMRouter{
		LocalBaseURL:   "http://localhost:1234",
		LocalModel:     "gpt-oss",
		LocalAPICompat: "anthropic",
	}
	ep2, err := r2.Route("gpt-oss", "")
	assert.NoError(t, err)
	assert.Equal(t, Local, ep2.Provider)

	// A different model name does not match the local slot.
	assert.False(t, r.isLocalModel("some-other-model"))
	// Nor does the slot match when it is not fully configured.
	assert.False(t, (&LLMRouter{LocalModel: "llama3"}).isLocalModel("llama3"))

	// An unsupported API compatibility mode is a routing error.
	r3 := &LLMRouter{
		LocalBaseURL:   "http://localhost:11434",
		LocalModel:     "llama3",
		LocalAPICompat: "bogus",
	}
	_, err = r3.Route("llama3", "")
	assert.Error(t, err)
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

	ep, err := r.Route("", "")
	assert.NoError(t, err)
	assert.Equal(t, OpenAICodex, ep.Provider)
	// The routing prefix is stripped for display and the API request.
	assert.Equal(t, "gpt-5.5", ep.Model)

	// With only a Codex token, the default model routes to Codex too.
	r2 := &LLMRouter{OpenAICodexAuthToken: "tok"}
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
}

func TestSmallModelRouting(t *testing.T) {
	t.Run("configured model wins and provider remains concrete", func(t *testing.T) {
		r := &LLMRouter{OpenAISmallModel: "my-fast-model"}
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
		r := &LLMRouter{LocalSmallModel: "qwen3:small"}
		model, ok := r.SmallModel(Local)
		require.True(t, ok)
		assert.Equal(t, "qwen3:small", model)
	})
}

func TestExplicitProviderRouting(t *testing.T) {
	r := &LLMRouter{
		AnthropicAPIKey: "ak",
		OpenAIAPIKey:    "ok",
	}

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
	messages := convertHistoryToOpenAI(history)
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
// into a THINKING block's Signature and reconstructed into a replayable
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
	// thinking signature) is ignored rather than mis-replayed.
	_, ok = decodeCodexReasoning("not-json-opaque-signature")
	assert.False(t, ok)

	// Without encrypted content there's nothing replayable under Store:false.
	_, noEnc := encodeCodexReasoning(responses.ResponseReasoningItem{ID: "rs_x"})
	_, ok = decodeCodexReasoning(noEnc)
	assert.False(t, ok)
}

// TestCodexConvertReasoningOrder covers B1: a reasoning item is replayed
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
	_, items := convertToCodexResponsesFormat(history)
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
	_, items := convertToCodexResponsesFormat(history)
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
			_, items := convertToCodexResponsesFormat(history)
			require.Len(t, items, 1)
			data, err := json.Marshal(items[0])
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(data))
		})
	}
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
