package schema

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestGitPushReceiptReplay(t *testing.T) {
	newServer := func(clientID string) (context.Context, *dagql.Server) {
		ctx := t.Context()
		cache, err := dagql.NewCache(ctx, "", nil, nil)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
		ctx = dagql.ContextWithCache(ctx, cache)
		ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: clientID, SessionID: clientID + "-session"})
		server := &currentTypeDefsTestServer{}
		base, err := NewCoreSchemaBase(ctx, server)
		require.NoError(t, err)
		dag, err := base.Fork(ctx, core.NewRoot(server), "v1.0.0")
		require.NoError(t, err)
		server.dag = dag
		return ctx, dag
	}
	ctx, srv := newServer("original")
	var receipt dagql.ObjectResult[*core.GitPushResult]
	require.NoError(t, srv.Select(ctx, srv.Root(), &receipt, dagql.Selector{Field: "__gitPushResult", Args: []dagql.NamedInput{
		{Name: "ref", Value: dagql.NewString("refs/heads/main")}, {Name: "previousSHA", Value: dagql.NewString(strings.Repeat("a", 40))},
		{Name: "sha", Value: dagql.NewString(strings.Repeat("b", 40))}, {Name: "disposition", Value: core.GitPushFastForward},
	}}))
	id, err := receipt.RecipeID(ctx)
	require.NoError(t, err)
	require.Equal(t, "__gitPushResult", id.Field())
	// Reconstruct in a fresh cache/server with no source repository, network,
	// originating client or credential service available.
	otherCtx, other := newServer("other")
	restored, err := dagql.NewID[*core.GitPushResult](id).Load(otherCtx, other)
	require.NoError(t, err)
	require.Equal(t, receipt.Self(), restored.Self())
}
