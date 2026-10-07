package schema

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestGitPerClientInput(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	resolve := func(ctx context.Context) dagql.Input {
		t.Helper()
		input, err := gitPerClientInput.Resolver(ctx, nil)
		require.NoError(t, err)
		return input
	}
	plain, err := dagql.PerClientInput.Resolver(ctx, nil)
	require.NoError(t, err)

	require.Equal(t, dagql.PerClientInput.Name, gitPerClientInput.Name)
	require.Equal(t, plain, resolve(ctx), "ordinary lookups keep their call digest")
	agent := resolve(core.WithAgentAddressResolution(ctx))
	require.NotEqual(t, plain, agent, "model-supplied addresses must not share the caller's lookups")
	require.Equal(t, agent, resolve(core.WithAgentAddressResolution(ctx)))
}

func TestAddressRequestedCacheInput(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	requested := dagql.RequestedCacheInput("noCache")
	resolve := func(ctx context.Context, input dagql.ImplicitInput, args map[string]dagql.Input) dagql.Input {
		t.Helper()
		resolved, err := input.Resolver(ctx, args)
		require.NoError(t, err)
		return resolved
	}
	cached := map[string]dagql.Input{"noCache": dagql.NewBoolean(false)}
	uncached := map[string]dagql.Input{"noCache": dagql.NewBoolean(true)}

	require.Equal(t, requested.Name, addressRequestedCacheInput.Name)
	require.Equal(t, resolve(ctx, requested, nil), resolve(ctx, addressRequestedCacheInput, nil), "ordinary lookups keep their call digest")
	require.Equal(t, resolve(ctx, requested, cached), resolve(ctx, addressRequestedCacheInput, cached), "ordinary lookups keep their call digest")

	agentCtx := core.WithAgentAddressResolution(ctx)
	agent := resolve(agentCtx, addressRequestedCacheInput, cached)
	require.NotEqual(t, resolve(ctx, requested, cached), agent, "model-supplied addresses must not share the caller's lookups")
	require.Equal(t, agent, resolve(agentCtx, addressRequestedCacheInput, nil))
	require.NotEqual(t, resolve(agentCtx, addressRequestedCacheInput, uncached), resolve(agentCtx, addressRequestedCacheInput, uncached), "noCache still runs every lookup")
}

// TestAddressAgentLookupCacheNamespace checks that Address.directory and
// Address.file results from a model-supplied address, which may have used the
// agent owner's git credentials, don't answer the same client's ordinary
// lookups of the address, and vice versa.
func TestAddressAgentLookupCacheNamespace(t *testing.T) {
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: "client", SessionID: "session"})
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	server := &currentTypeDefsTestServer{}
	root := core.NewRoot(server)
	srv, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	server.dag = srv
	ctx = core.ContextWithQuery(ctx, root)

	(&addressSchema{}).Install(srv)
	srv.InstallObject(dagql.NewClass[*core.File](srv))
	// Fake out the git reads Address.directory and Address.file desugar to.
	// Query.git runs on every call, so it counts the loader's cache misses.
	var fetches atomic.Int32
	dagql.Fields[*core.Query]{
		dagql.Func("git", func(context.Context, *core.Query, struct{ URL string }) (*core.GitRepository, error) {
			fetches.Add(1)
			return &core.GitRepository{}, nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	dagql.Fields[*core.GitRepository]{
		dagql.Func("ref", func(context.Context, *core.GitRepository, struct{ Name string }) (*core.GitRef, error) {
			return &core.GitRef{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*core.GitRef]{
		dagql.Func("tree", func(context.Context, *core.GitRef, struct{}) (*core.Directory, error) {
			return &core.Directory{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*core.Directory]{
		dagql.Func("file", func(context.Context, *core.Directory, struct{ Path string }) (*core.File, error) {
			return &core.File{}, nil
		}),
	}.Install(srv)

	for _, tc := range []struct {
		field string
		addr  string
	}{
		{"directory", "https://example.com/private.git#main"},
		{"file", "https://example.com/private.git#main:README.md"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			lookup := func(ctx context.Context) int32 {
				t.Helper()
				var res dagql.AnyObjectResult
				require.NoError(t, srv.Select(ctx, srv.Root(), &res,
					dagql.Selector{Field: "address", Args: []dagql.NamedInput{{Name: "value", Value: dagql.NewString(tc.addr)}}},
					dagql.Selector{Field: tc.field},
				))
				return fetches.Load()
			}
			agentCtx := core.WithAgentAddressResolution(ctx)
			before := fetches.Load()
			require.Equal(t, before+1, lookup(agentCtx))
			require.Equal(t, before+2, lookup(ctx), "an ordinary lookup must not reuse the agent's")
			require.Equal(t, before+2, lookup(ctx), "ordinary lookups are still cached")
			require.Equal(t, before+2, lookup(agentCtx), "agent lookups are still cached")
		})
	}
}
