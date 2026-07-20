package dagql_test

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestPerClientCacheScope(t *testing.T) {
	t.Parallel()

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "client"})
	base, err := dagql.PerClientInput.Resolver(ctx, nil)
	require.NoError(t, err)

	scopedCtx := dagql.WithPerClientCacheScope(ctx)
	first, err := dagql.PerClientInput.Resolver(scopedCtx, nil)
	require.NoError(t, err)
	second, err := dagql.PerClientInput.Resolver(scopedCtx, nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotEqual(t, base, first)

	other, err := dagql.PerClientInput.Resolver(dagql.WithPerClientCacheScope(ctx), nil)
	require.NoError(t, err)
	require.NotEqual(t, first, other)
}
