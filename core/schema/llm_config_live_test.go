package schema

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type liveDefaultLLMServer struct {
	*llmAuthSchemaServer
	revision        string
	sessionContexts atomic.Int64
}

func (s *liveDefaultLLMServer) CurrentModule(context.Context) (dagql.ObjectResult[*core.Module], error) {
	return dagql.ObjectResult[*core.Module]{}, core.ErrNoCurrentModule
}

func (s *liveDefaultLLMServer) SessionScopedContext(ctx context.Context) (context.Context, error) {
	s.sessionContexts.Add(1)
	return s.llmAuthSchemaServer.SessionScopedContext(ctx)
}

func (s *liveDefaultLLMServer) LLMConfigMetadata(context.Context) (*engine.ClientMetadata, *engine.ClientMetadata, string, error) {
	return s.mainClient, s.parent, s.revision, nil
}

func newLiveLLMMetadataTest(t *testing.T, nested bool, config *engine.LLMConfig, secrets *llmAuthSecrets) (context.Context, *dagql.Server, *liveDefaultLLMServer) {
	t.Helper()
	main := &engine.ClientMetadata{SessionID: "metadata-session", ClientID: "main", LLMConfig: config}
	parent := main
	if nested {
		parent = &engine.ClientMetadata{SessionID: main.SessionID, ClientID: "nested"}
	}
	srv := &liveDefaultLLMServer{llmAuthSchemaServer: &llmAuthSchemaServer{
		currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: main},
		parent:                    parent,
		attachables:               map[string]*grpc.ClientConn{main.ClientID: newLLMAuthAttachable(t, secrets)},
	}, revision: "0"}
	ctx := engine.ContextWithClientMetadata(t.Context(), parent)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	root := core.NewRoot(srv)
	ctx = core.ContextWithQuery(ctx, root)
	base, err := NewCoreSchemaBase(ctx, srv)
	require.NoError(t, err)
	dag, err := base.Fork(ctx, root, "")
	require.NoError(t, err)
	srv.dag = dag
	srv.deps = core.NewSchemaBuilder(root, nil)
	return ctx, dag, srv
}

func TestLLMStepsRetainCredentialSource(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			var requests atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
			}))
			defer provider.Close()
			env := &llmAuthSecrets{vars: map[string]string{"env://ANTHROPIC_AUTH_TOKEN": "token-v1"}}
			config := &engine.LLMConfig{DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-5", Providers: map[string]*engine.LLMProviderConfig{
				"anthropic": {AuthToken: "env://ANTHROPIC_AUTH_TOKEN", BaseURL: provider.URL},
			}}
			ctx, dag, srv := newLiveLLMMetadataTest(t, nested, config, env)
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, dag.Select(ctx, dag.Root(), &llm, dagql.Selector{Field: "llm"}))
			// Pure metadata must not accidentally warm the endpoint for this test.
			var model string
			require.NoError(t, dag.Select(ctx, llm, &model, dagql.Selector{Field: "model"}))
			require.Zero(t, srv.sessionContexts.Load())
			var firstReads int
			for i := range 3 {
				var stepped dagql.ObjectResult[*core.LLM]
				require.NoError(t, dag.Select(ctx, llm, &stepped,
					dagql.Selector{Field: "withPrompt", Args: []dagql.NamedInput{{Name: "prompt", Value: dagql.NewString(fmt.Sprintf("turn %d", i))}}},
					dagql.Selector{Field: "step"}))
				llm = stepped
				require.EqualValues(t, i+1, requests.Load())
				require.EqualValues(t, 1, srv.sessionContexts.Load(), "materialized responses retain the first step's credential lifetime")
				env.mu.Lock()
				reads := env.reads
				env.mu.Unlock()
				if i == 0 {
					firstReads = reads
					require.Positive(t, firstReads)
				}
				require.Equal(t, firstReads, reads, "subsequent turns must reuse the credential source")
			}
		})
	}
}

func TestLLMMetadataWithoutCredentials(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			config := &engine.LLMConfig{
				DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-5",
				Providers: map[string]*engine.LLMProviderConfig{
					"anthropic": {APIKey: "file:///missing-key", ReasoningEffort: "high"},
				},
			}
			ctx, dag, _ := newLiveLLMMetadataTest(t, nested, config, &llmAuthSecrets{err: fmt.Errorf("open /missing-key: no such file or directory")})
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, dag.Select(ctx, dag.Root(), &llm, dagql.Selector{Field: "llm"}))
			for field, want := range map[string]string{"model": "claude-sonnet-4-5", "provider": "anthropic", "reasoningEffort": "high"} {
				var got string
				require.NoError(t, dag.Select(ctx, llm, &got, dagql.Selector{Field: field}))
				require.Equal(t, want, got)
			}
			var window dagql.Nullable[dagql.Int]
			require.NoError(t, dag.Select(ctx, llm, &window, dagql.Selector{Field: "contextWindow"}))
			require.True(t, window.Valid)
			require.Positive(t, int(window.Value))
			// Metadata can initialize the CLI and expose /setup; only an actual
			// authenticated endpoint needs to read the missing secret.
			route, err := llm.Self().Route(ctx)
			require.NoError(t, err)
			require.Nil(t, route.Client)
			require.Empty(t, route.Key)
			_, err = llm.Self().Endpoint(ctx)
			require.ErrorContains(t, err, "missing-key")
		})
	}
}

func TestLLMMetadataTracksLiveInference(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			config := &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
				"openai": {APIKey: "env://OPENAI_API_KEY", ReasoningEffort: "low", SmallModel: "gpt-5-mini"},
			}}
			ctx, dag, srv := newLiveLLMMetadataTest(t, nested, config, &llmAuthSecrets{vars: map[string]string{
				"env://OPENAI_API_KEY": "test-key", "env://OPENAI_CODEX_AUTH_TOKEN": "test-token",
			}})
			var llm dagql.ObjectResult[*core.LLM]
			require.NoError(t, dag.Select(ctx, dag.Root(), &llm,
				dagql.Selector{Field: "llm", Args: []dagql.NamedInput{{Name: "model", Value: dagql.Opt(dagql.NewString("claude-sonnet-4-5"))}}},
				dagql.Selector{Field: "withModel", Args: []dagql.NamedInput{{Name: "model", Value: dagql.NewString("gpt-5")}}},
			))
			assertMetadata := func(provider, effort, smallModel string) {
				t.Helper()
				ep, err := llm.Self().Endpoint(ctx)
				require.NoError(t, err)
				require.Equal(t, core.LLMProvider(provider), ep.Provider)
				for field, want := range map[string]string{"model": ep.Model, "provider": string(ep.Provider), "reasoningEffort": effort} {
					var got string
					require.NoError(t, dag.Select(ctx, llm, &got, dagql.Selector{Field: field}))
					require.Equal(t, want, got, field)
				}
				var window dagql.Nullable[dagql.Int]
				require.NoError(t, dag.Select(ctx, llm, &window, dagql.Selector{Field: "contextWindow"}))
				require.Equal(t, ep.ContextWindow, int64(window.Value))
				var small dagql.ObjectResult[*core.LLM]
				require.NoError(t, dag.Select(ctx, llm, &small, dagql.Selector{Field: "withSmallModel"}))
				var gotSmall string
				require.NoError(t, dag.Select(ctx, small, &gotSmall, dagql.Selector{Field: "model"}))
				require.Equal(t, smallModel, gotSmall)
			}
			assertMetadata("openai", "low", "gpt-5-mini")
			srv.mainClient.LLMConfig = &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
				"openai-codex": {AuthToken: "env://OPENAI_CODEX_AUTH_TOKEN", ReasoningEffort: "high", SmallModel: "gpt-5.4-mini"},
			}}
			srv.revision = "1"
			assertMetadata("openai-codex", "high", "gpt-5.4-mini")
		})
	}
}

func TestLLMLiveDefaultConfiguration(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%t", nested), func(t *testing.T) {
			main := &engine.ClientMetadata{SessionID: "session", ClientID: "main", LLMConfig: &engine.LLMConfig{
				DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-5",
			}}
			parent := main
			if nested {
				parent = &engine.ClientMetadata{SessionID: main.SessionID, ClientID: "nested"}
			}
			srv := &liveDefaultLLMServer{llmAuthSchemaServer: &llmAuthSchemaServer{
				currentTypeDefsTestServer: &currentTypeDefsTestServer{mainClient: main}, parent: parent,
			}, revision: "0"}
			ctx := engine.ContextWithClientMetadata(t.Context(), parent)
			cache, err := dagql.NewCache(ctx, "", nil, nil)
			require.NoError(t, err)
			ctx = dagql.ContextWithCache(ctx, cache)
			root := core.NewRoot(srv)
			ctx = core.ContextWithQuery(ctx, root)
			base, err := NewCoreSchemaBase(ctx, srv)
			require.NoError(t, err)
			dag, err := base.Fork(ctx, root, "")
			require.NoError(t, err)
			srv.dag = dag
			selectLLM := func(args ...dagql.NamedInput) dagql.ObjectResult[*core.LLM] {
				var llm dagql.ObjectResult[*core.LLM]
				require.NoError(t, dag.Select(ctx, dag.Root(), &llm, dagql.Selector{Field: "llm", Args: args}))
				return llm
			}
			assertRoute := func(llm dagql.ObjectResult[*core.LLM], model, provider string) {
				ep, err := llm.Self().Endpoint(ctx)
				require.NoError(t, err)
				require.Equal(t, model, ep.Model)
				require.Equal(t, core.LLMProvider(provider), ep.Provider)
			}
			before := selectLLM()
			assertRoute(before, "claude-sonnet-4-5", "anthropic")
			pinnedArgs := []dagql.NamedInput{
				{Name: "model", Value: dagql.Opt(dagql.NewString("claude-sonnet-4-5"))},
				{Name: "provider", Value: dagql.Opt(dagql.NewString("anthropic"))},
			}
			pinnedBefore := selectLLM(pinnedArgs...)
			main.LLMConfig = &engine.LLMConfig{DefaultProvider: "openai", DefaultModel: "gpt-5"}
			srv.revision = "1"
			after := selectLLM()
			assertRoute(after, "gpt-5", "openai")
			assertRoute(before, "claude-sonnet-4-5", "anthropic")
			pinnedAfter := selectLLM(pinnedArgs...)
			beforeID, err := pinnedBefore.ID()
			require.NoError(t, err)
			afterID, err := pinnedAfter.ID()
			require.NoError(t, err)
			beforeEncoded, err := beforeID.Encode()
			require.NoError(t, err)
			afterEncoded, err := afterID.Encode()
			require.NoError(t, err)
			require.Equal(t, beforeEncoded, afterEncoded, "explicit routes retain their identity")
		})
	}
}
