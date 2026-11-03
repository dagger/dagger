package schema

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type liveDefaultLLMServer struct {
	*llmAuthSchemaServer
	revision string
}

func (s *liveDefaultLLMServer) LLMConfigMetadata(context.Context) (*engine.ClientMetadata, *engine.ClientMetadata, string, error) {
	return s.mainClient, s.parent, s.revision, nil
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
