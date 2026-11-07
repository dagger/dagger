package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/core/modelcatalog"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopilotRoute(t *testing.T) {
	for _, baseURL := range []string{"", "https://copilot.example/api"} {
		t.Run(baseURL, func(t *testing.T) {
			md := &engine.ClientMetadata{ClientID: "copilot-test", SessionID: "copilot-session"}
			ctx, env := newLLMEndpointTestClientCtx(t, md)
			env.set("llmconfig://github-copilot/auth_token", "copilot-token")
			r, err := NewLLMRouter(&engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
				"github-copilot": {BaseURL: baseURL, AuthToken: "llmconfig://github-copilot/auth_token", ReasoningEffort: "high"},
			}}, md)
			require.NoError(t, err)
			wantURL := baseURL
			if wantURL == "" {
				wantURL = copilotBaseURL
			}
			for _, resolve := range []bool{false, true} {
				ep, err := r.Route("", "github-copilot")
				if resolve {
					ep, err = r.Endpoint(ctx, "", "github-copilot")
				}
				require.NoError(t, err)
				assert.Equal(t, GitHubCopilot, ep.Provider)
				assert.Equal(t, wantURL, ep.BaseURL)
				assert.Equal(t, modelDefaultCopilot, ep.Model)
				assert.Equal(t, "high", ep.ReasoningEffort)
				assert.True(t, ep.IsOAuth)
				if resolve {
					assert.Equal(t, "copilot-token", ep.AuthToken)
					require.NotNil(t, ep.AuthTokenSource)
				}
			}
		})
	}
	_, err := new(LLMRouter).Route("gpt-4.1", "unknown")
	require.ErrorContains(t, err, "unknown LLM provider")
	assert.ErrorContains(t, err, "github-copilot")
}

func TestCopilotDefaultRoutePinned(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5", "gpt-4.1", "gemini-3.5-flash"} {
		for _, explicit := range []bool{true, false} {
			name := model + "/configured model"
			if explicit {
				name = model + "/explicit default"
			}
			t.Run(name, func(t *testing.T) {
				cfg := &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
					"github-copilot": {Model: model, AuthToken: "llmconfig://github-copilot/auth_token"},
					// An API key must not displace the user's Copilot choice.
					"openai": {APIKey: "env://OPENAI_API_KEY"},
				}}
				if explicit {
					cfg.DefaultProvider, cfg.DefaultModel = "github-copilot", model
					cfg.Providers["github-copilot"].Model = "another-model"
				}
				r, err := NewLLMRouter(cfg, nil)
				require.NoError(t, err)
				gotModel, provider := r.DefaultRoute()
				assert.Equal(t, model, gotModel)
				assert.Equal(t, GitHubCopilot, provider)
				ep, err := r.Route("", "")
				require.NoError(t, err)
				assert.Equal(t, GitHubCopilot, ep.Provider)
				assert.Equal(t, model, ep.Model)
				ep, err = r.Route("", "github-copilot")
				require.NoError(t, err)
				assert.Equal(t, model, ep.Model)
			})
		}
	}
	t.Run("provider default without a model", func(t *testing.T) {
		r, err := NewLLMRouter(&engine.LLMConfig{DefaultProvider: "github-copilot"}, nil)
		require.NoError(t, err)
		model, provider := r.DefaultRoute()
		assert.Equal(t, modelDefaultCopilot, model)
		assert.Equal(t, GitHubCopilot, provider)
	})
}

func TestCopilotNeverInferred(t *testing.T) {
	r := routerWith(t, map[string]*engine.LLMProviderConfig{
		"github-copilot": {AuthToken: "llmconfig://github-copilot/auth_token"},
	})
	for model, provider := range map[string]LLMProvider{
		"claude-sonnet-5": Anthropic, "gpt-4.1": OpenAI, "gemini-3.5-flash": Google,
		"gpt-5.3-codex": OpenAICodex, "github-copilot/gpt-4.1": Other, "unknown-model": Other,
	} {
		ep, err := r.Route(model, "")
		require.NoError(t, err)
		assert.Equal(t, provider, ep.Provider, model)
	}
}

func TestCopilotDefaultPriority(t *testing.T) {
	for _, provider := range []LLMProvider{OpenAI, Anthropic, Google, OpenRouter} {
		t.Run(string(provider), func(t *testing.T) {
			r := routerWith(t, map[string]*engine.LLMProviderConfig{
				"github-copilot": {AuthToken: "llmconfig://github-copilot/auth_token"},
				string(provider): {APIKey: "env://API_KEY"},
			})
			ep, err := r.Route("", "")
			require.NoError(t, err)
			assert.Equal(t, provider, ep.Provider)
			// The configured-model priority also puts Copilot after API vendors.
			r.Providers[provider].Model = r.providerDefaultModel(provider)
			r.Providers[GitHubCopilot].Model = "gpt-4.1"
			ep, err = r.Route("", "")
			require.NoError(t, err)
			assert.Equal(t, provider, ep.Provider)
		})
	}
	r := routerWith(t, map[string]*engine.LLMProviderConfig{
		"github-copilot": {AuthToken: "llmconfig://github-copilot/auth_token"},
	})
	model, provider := r.DefaultRoute()
	assert.Equal(t, modelDefaultCopilot, model)
	assert.Equal(t, GitHubCopilot, provider)
	ep, err := r.Route("", "")
	require.NoError(t, err)
	assert.Equal(t, GitHubCopilot, ep.Provider)

	small, ok := r.SmallModel(GitHubCopilot)
	require.True(t, ok)
	wantSmall, ok := modelcatalog.DefaultSmallModel("github-copilot")
	require.True(t, ok)
	assert.Equal(t, wantSmall, small)
	r.Providers[GitHubCopilot].SmallModel = "gpt-5-mini"
	small, ok = r.SmallModel(GitHubCopilot)
	require.True(t, ok)
	assert.Equal(t, "gpt-5-mini", small)
	ep, err = r.Route(small, "github-copilot")
	require.NoError(t, err)
	assert.Equal(t, GitHubCopilot, ep.Provider)
}

func TestCopilotResponsesModels(t *testing.T) {
	for model, want := range map[string]bool{
		"gpt-5.3-codex": true, "gpt-5.4": true, "gpt-5.5": true, "gpt-6-sol": true,
		"gpt-5.4-mini": true, "gpt-5-mini": false, "gpt-4.1": false,
		"claude-sonnet-5": false, "gemini-3.5-flash": false,
		"grok-4.5": true, "mai-code-1": true,
	} {
		t.Run(model, func(t *testing.T) {
			ep := &LLMEndpoint{Provider: GitHubCopilot, Model: model, BaseURL: "https://copilot.example"}
			assert.Equal(t, want, ep.usesCopilotResponses())
			client, err := ep.newClient()
			require.NoError(t, err)
			if want {
				assert.IsType(t, &OpenAIResponsesClient{}, client)
			} else {
				assert.IsType(t, &OpenAIClient{}, client)
			}
		})
	}
}

func TestCopilotContextWindow(t *testing.T) {
	r := new(LLMRouter)
	known, err := r.Route("claude-haiku-4.5", "github-copilot")
	require.NoError(t, err)
	m, ok := modelcatalog.Lookup("github-copilot", "claude-haiku-4.5")
	require.True(t, ok)
	assert.Equal(t, m.ContextWindow, known.ContextWindow)
	assert.NotZero(t, known.ContextWindow)
	unknown, err := r.Route("claude-sonnet-4-5", "github-copilot")
	require.NoError(t, err)
	assert.Zero(t, unknown.ContextWindow)
}

func TestCopilotRequests(t *testing.T) {
	for model, path := range map[string]string{"gpt-4.1": "/api/chat/completions", "gpt-5.3-codex": "/api/responses"} {
		t.Run(model, func(t *testing.T) {
			type request struct {
				path   string
				header http.Header
				body   map[string]any
			}
			var mu sync.Mutex
			var seen []request
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				mu.Lock()
				seen = append(seen, request{r.URL.Path, r.Header.Clone(), body})
				mu.Unlock()
				w.Header().Set("Content-Type", "text/event-stream")
				if r.URL.Path == "/api/responses" {
					_, _ = io.WriteString(w, "event: response.completed\n"+
						`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp","object":"response","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`+"\n\n")
				} else {
					_, _ = io.WriteString(w, `data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"done"}}]}`+"\n\n"+
						`data: {"id":"test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				}
			}))
			t.Cleanup(server.Close)

			md := &engine.ClientMetadata{ClientID: "copilot-test", SessionID: "copilot-session"}
			ctx, env := newLLMEndpointTestClientCtx(t, md)
			env.set("llmconfig://github-copilot/auth_token", "copilot-token-1")
			r, err := NewLLMRouter(&engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
				"github-copilot": {BaseURL: server.URL + "/api", AuthToken: "llmconfig://github-copilot/auth_token"},
			}}, md)
			require.NoError(t, err)
			ep, err := r.Endpoint(ctx, model, "github-copilot")
			require.NoError(t, err)
			clock := newTestClock()
			ep.AuthTokenSource.now = clock.Now
			client, err := ep.newClient()
			require.NoError(t, err)

			system := &LLMMessage{Role: LLMMessageRoleSystem, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "be terse"}}}
			prompt := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "review this"}}}
			call := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentToolCall, CallID: "call_1", ToolName: "read", Arguments: JSON(`{}`)}}}
			result := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentToolResult, CallID: "call_1", Text: "package main"}}}
			answer := &LLMMessage{Role: LLMMessageRoleAssistant, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "looks fine"}}}
			followUp := &LLMMessage{Role: LLMMessageRoleUser, Content: []*LLMContentBlock{{Kind: LLMContentText, Text: "review the tests too"}}}
			for _, history := range [][]*LLMMessage{
				{system, prompt},
				{system, prompt, call, result},
				{system, prompt, call, result, answer, followUp},
			} {
				resp, err := client.SendQuery(ctx, history, nil, &LLMCallOpts{})
				require.NoError(t, err)
				require.NotEmpty(t, resp.Content)
				assert.Equal(t, "done", resp.Content[0].Text)
				// Simulate CLI rotation without rebuilding the routed endpoint/client.
				env.set("llmconfig://github-copilot/auth_token", "copilot-token-2")
				clock.Advance(credentialRefreshTTL + time.Second)
			}
			// A caller with a bearer snapshot but no live source still uses the
			// subscription token, rather than the API-key slot or engine env.
			snapshot := *ep
			snapshot.AuthTokenSource = nil
			snapshot.Key = "unrelated-api-key"
			staticClient, err := snapshot.newClient()
			require.NoError(t, err)
			_, err = staticClient.SendQuery(ctx, []*LLMMessage{system, prompt}, nil, &LLMCallOpts{})
			require.NoError(t, err)
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, seen, 4)
			for i, req := range seen {
				assert.Equal(t, path, req.path)
				assert.Equal(t, model, req.body["model"])
				token, initiator := "copilot-token-2", "user"
				if i == 0 || i == 3 {
					token = "copilot-token-1"
				}
				if i == 1 {
					initiator = "agent"
				}
				assert.Equal(t, "Bearer "+token, req.header.Get("Authorization"))
				assert.Equal(t, initiator, req.header.Get("X-Initiator"))
				for name, value := range engine.CopilotHeaders() {
					assert.Equal(t, value, req.header.Get(name))
				}
				assert.Equal(t, "vscode-chat", req.header.Get("Copilot-Integration-Id"))
				assert.Contains(t, req.header.Get("User-Agent"), "dagger/")
			}
		})
	}
}
