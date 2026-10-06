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

func TestCacheScopeInput(t *testing.T) {
	t.Parallel()

	// Client identity is not part of the scope: two clients without a scope
	// share one value.
	ctxA := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "a"})
	ctxB := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "b"})
	a, err := dagql.CacheScopeInput.Resolver(ctxA, nil)
	require.NoError(t, err)
	b, err := dagql.CacheScopeInput.Resolver(ctxB, nil)
	require.NoError(t, err)
	require.Equal(t, a, b)
	require.Equal(t, dagql.NewString(""), a)

	// A fresh per-client scope busts the shared value, and is stable within
	// that scope.
	scoped := dagql.WithPerClientCacheScope(ctxA)
	first, err := dagql.CacheScopeInput.Resolver(scoped, nil)
	require.NoError(t, err)
	second, err := dagql.CacheScopeInput.Resolver(scoped, nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.NotEqual(t, a, first)

	named, err := dagql.CacheScopeInput.Resolver(dagql.WithNamedPerClientCacheScope(ctxB, "refresh"), nil)
	require.NoError(t, err)
	require.Equal(t, dagql.NewString("refresh"), named)
}

func TestPerCallWhen(t *testing.T) {
	t.Parallel()

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{ClientID: "client"})
	input := dagql.PerCallWhen("noLock", dagql.PerClientInput)
	require.Equal(t, dagql.PerClientInput.Name, input.Name, "the wrapped input keeps its name")

	base, err := dagql.PerClientInput.Resolver(ctx, nil)
	require.NoError(t, err)
	for name, args := range map[string]map[string]dagql.Input{
		"absent":        nil,
		"false":         {"noLock": dagql.Boolean(false)},
		"unset opt":     {"noLock": dagql.Optional[dagql.Boolean]{}},
		"opt false":     {"noLock": dagql.Opt(dagql.Boolean(false))},
		"dynamic false": {"noLock": dagql.DynamicOptional{Valid: true, Value: dagql.Boolean(false)}},
	} {
		got, err := input.Resolver(ctx, args)
		require.NoError(t, err, name)
		require.Equal(t, base, got, "%s keeps the wrapped input's value", name)
	}

	seen := map[string]bool{base.(dagql.String).String(): true}
	for _, arg := range []dagql.Input{dagql.Boolean(true), dagql.Opt(dagql.Boolean(true))} {
		for range 2 {
			got, err := input.Resolver(ctx, map[string]dagql.Input{"noLock": arg})
			require.NoError(t, err)
			key := got.(dagql.String).String()
			require.False(t, seen[key], "every requested call gets a fresh key")
			seen[key] = true
		}
	}

	_, err = input.Resolver(ctx, map[string]dagql.Input{"noLock": dagql.String("true")})
	require.ErrorContains(t, err, `cache input argument "noLock" must be Boolean`)
}
