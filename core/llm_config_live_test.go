package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

type liveLLMConfigTestServer struct {
	*llmEndpointTestServer
	config   *engine.LLMConfig
	revision string
}

func (s *liveLLMConfigTestServer) LLMConfigMetadata(context.Context) (*engine.ClientMetadata, *engine.ClientMetadata, string, error) {
	md := *s.md
	md.LLMConfig = s.config.Clone()
	return &md, &md, s.revision, nil
}

func TestLLMEndpointLiveConfiguration(t *testing.T) {
	ctx, env := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	original := query.Server.(*llmEndpointTestServer)
	live := &liveLLMConfigTestServer{llmEndpointTestServer: original, config: original.md.LLMConfig.Clone(), revision: "0"}
	query.Server = live
	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "anthropic")
	require.NoError(t, err)
	first, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	require.Equal(t, "token-v1", first.AuthToken)
	cached, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	require.Same(t, first, cached)

	// Removing a provider must discard the memo's credential source, not
	// overlay an empty configuration on the old route.
	live.config = nil
	live.revision = "1"
	removed, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	require.NotSame(t, first, removed)
	require.Empty(t, removed.AuthToken)
	require.Nil(t, removed.AuthTokenSource)

	// Re-adding the same secret URI still rebuilds the endpoint and reads the
	// new key, rather than reviving a cached credential.
	env.set("env://ANTHROPIC_AUTH_TOKEN", "token-v2")
	live.config = original.md.LLMConfig.Clone()
	live.revision = "2"
	added, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	require.Equal(t, "token-v2", added.AuthToken)
	require.NotSame(t, first, added)
	require.NotSame(t, removed, added)
	require.Equal(t, "env://ANTHROPIC_AUTH_TOKEN", original.md.LLMConfig.Providers["anthropic"].AuthToken)
}
