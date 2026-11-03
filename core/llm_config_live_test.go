package core

import (
	"context"
	"errors"
	"sync/atomic"
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

type liveConfigRetryClient struct {
	send func() (*LLMResponse, error)
}

func (c *liveConfigRetryClient) SendQuery(context.Context, []*LLMMessage, []LLMTool, *LLMCallOpts) (*LLMResponse, error) {
	return c.send()
}

func (*liveConfigRetryClient) IsRetryable(error) bool { return true }

func TestLLMLiveConfigurationDuringRetry(t *testing.T) {
	ctx, _ := newLLMEndpointTestCtx(t)
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	original := query.Server.(*llmEndpointTestServer)
	live := &liveLLMConfigTestServer{llmEndpointTestServer: original, config: original.md.LLMConfig.Clone(), revision: "0"}
	query.Server = live
	llm, err := query.NewLLM(ctx, "claude-sonnet-4-5", "anthropic")
	require.NoError(t, err)
	endpoint, err := llm.Endpoint(ctx)
	require.NoError(t, err)
	var replacementRequests atomic.Int64
	ts := anthropic401Server(t, func(string) { replacementRequests.Add(1) })
	attempts := 0
	endpoint.Client = &liveConfigRetryClient{send: func() (*LLMResponse, error) {
		attempts++
		if attempts > 1 {
			return &LLMResponse{}, nil
		}
		// Remove the credential while changing the endpoint during a retryable
		// failure. The next attempt must use the replacement's empty auth.
		live.config = &engine.LLMConfig{Providers: map[string]*engine.LLMProviderConfig{
			"anthropic": {BaseURL: ts.URL},
		}}
		live.revision = "1"
		return nil, errors.New("retryable failure")
	}}
	_, err = llm.sendQueryWithRetry(ctx, llmTestHistory(), nil, "", 0)
	require.Error(t, err)
	require.Equal(t, 1, attempts)
	require.Zero(t, replacementRequests.Load(), "the SDK rejects the replacement's empty credentials before sending")
	require.Empty(t, llm.endpoint.AuthToken)
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
