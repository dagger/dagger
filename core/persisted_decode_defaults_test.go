package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// The persisted Module decoder's default dependencies come from the
// registered pure factory under a sharing preparation marker, and from the
// ordinary client-backed path everywhere else.
func TestPersistedDecodeDefaultDeps(t *testing.T) {
	ctx := t.Context()
	root := NewRoot(nil)
	srv, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	srv.View = call.View("v0.1.2")
	dec := dagql.NewPersistDecodeContext(srv, 1, nil)

	var views []call.View
	factory := func(view call.View) *SchemaBuilder {
		views = append(views, view)
		return NewSchemaBuilder(root, nil)
	}

	t.Run("marked without a registered factory is refused", func(t *testing.T) {
		_, err := persistedDecodeDefaultDeps(engine.WithSnapshotSharePreparation(ctx), dec)
		require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	})

	t.Run("marked with the registered root returns a fresh builder", func(t *testing.T) {
		marked := ContextWithPersistedDecodeDefaults(engine.WithSnapshotSharePreparation(ctx), root, factory)
		deps, err := persistedDecodeDefaultDeps(marked, dec)
		require.NoError(t, err)
		require.NotNil(t, deps)
		require.Equal(t, []call.View{srv.View}, views, "the builder uses the decoding server's own core view")

		// A nested decode makes its own builder rather than sharing one.
		nested, err := persistedDecodeDefaultDeps(marked, dec)
		require.NoError(t, err)
		require.NotSame(t, deps, nested)
	})

	t.Run("a decoding server with another root is refused", func(t *testing.T) {
		other := NewRoot(nil)
		otherSrv, err := dagql.NewServer(ctx, other)
		require.NoError(t, err)
		marked := ContextWithPersistedDecodeDefaults(engine.WithSnapshotSharePreparation(ctx), root, factory)
		_, err = persistedDecodeDefaultDeps(marked, dagql.NewPersistDecodeContext(otherSrv, 1, nil))
		require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	})

	t.Run("an unmarked context keeps the ordinary path", func(t *testing.T) {
		// Query.DefaultDeps delegates to the engine root, which is nil here:
		// reaching it at all is the assertion, and it panics or errors rather
		// than silently using the background factory.
		withDefaults := ContextWithPersistedDecodeDefaults(ctx, root, factory)
		require.Panics(t, func() { _, _ = persistedDecodeDefaultDeps(withDefaults, dec) },
			"an unmarked decode never consults the background factory")
	})
}

// The core evaluation, service and schema boundaries refuse under a sharing
// preparation marker, before any underlying work starts.
func TestSnapshotSharePreparationCoreGuards(t *testing.T) {
	marked := engine.WithSnapshotSharePreparation(t.Context())
	lazy := NewLazyState()

	var ran int
	run := func(context.Context) error { ran++; return nil }
	require.ErrorIs(t, lazy.Evaluate(marked, "TestValue", run), engine.ErrSnapshotShareEvaluation)
	require.ErrorIs(t, lazy.EvaluateGroup(marked, "TestValue", "group", run), engine.ErrSnapshotShareEvaluation)
	require.Zero(t, ran, "the body never runs under the marker")

	services := NewServices()
	_, err := services.Start(marked, "sha256:deadbeef", nil, false)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	_, _, err = services.StartBindings(marked, nil)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)

	svc := &Service{}
	require.ErrorIs(t, svc.Start(marked, nil, "sha256:deadbeef", ServiceStartOpts{}), engine.ErrSnapshotShareEvaluation)

	// An unmarked context is unaffected: the same calls reach their ordinary
	// failures instead of the sentinel.
	require.NotErrorIs(t, lazy.Evaluate(t.Context(), "TestValue", run), engine.ErrSnapshotShareEvaluation)
	require.Equal(t, 1, ran)
}

// Root and factory agreement is checked on the preparation context itself,
// before the cache can start a shared decode attempt with it.
func TestCheckPersistedDecodeDefaults(t *testing.T) {
	ctx := t.Context()
	root := NewRoot(nil)
	srv, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	factory := func(call.View) *SchemaBuilder { return NewSchemaBuilder(root, nil) }

	require.NoError(t, CheckPersistedDecodeDefaults(ContextWithPersistedDecodeDefaults(ctx, root, factory), srv))
	require.ErrorContains(t, CheckPersistedDecodeDefaults(ctx, srv), "none registered")
	require.ErrorContains(t, CheckPersistedDecodeDefaults(ContextWithPersistedDecodeDefaults(ctx, root, factory), nil), "no decoding server root")

	otherSrv, err := dagql.NewServer(ctx, NewRoot(nil))
	require.NoError(t, err)
	require.ErrorContains(t, CheckPersistedDecodeDefaults(ContextWithPersistedDecodeDefaults(ctx, root, factory), otherSrv), "does not carry the registered engine root")

	empty := func(call.View) *SchemaBuilder { return nil }
	require.ErrorContains(t, CheckPersistedDecodeDefaults(ContextWithPersistedDecodeDefaults(ctx, root, empty), srv), "returned nothing")
}
