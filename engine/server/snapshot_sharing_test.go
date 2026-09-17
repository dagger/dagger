package server

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// Only an engine that can receive imports enables sharing: a configured
// remote-cache integration, or the gated transfer fixture. Every other engine
// registers nothing and builds no core schema base early.
func TestSnapshotSharingReceivesImports(t *testing.T) {
	require.False(t, snapshotSharingReceivesImports(nil))
	require.False(t, snapshotSharingReceivesImports(&NewServerOpts{}))
	require.True(t, snapshotSharingReceivesImports(&NewServerOpts{
		RemoteCacheIntegration: &RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error { return nil }},
	}))
	// The fixture is enabled by a non-empty root, and so is sharing.
	t.Setenv(core.RemoteCacheFixtureRootEnv, "")
	require.False(t, snapshotSharingReceivesImports(&NewServerOpts{}))
	t.Setenv(core.RemoteCacheFixtureRootEnv, t.TempDir())
	require.True(t, snapshotSharingReceivesImports(&NewServerOpts{}))
}

// An engine that cannot receive imports leaves the cache exactly as it was:
// admission off, no registered preparation callback, no early core base.
func TestSnapshotSharingSkipsOrdinaryEngine(t *testing.T) {
	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	require.NoError(t, srv.initSnapshotSharing(t.Context(), &NewServerOpts{}))
	require.False(t, cache.SnapshotSharingEnabled())
	require.Nil(t, srv.coreSchemaBase, "no core schema base is built at startup")
}

// The registered preparation context supplies a schema-only server carrying
// the engine root, and a default-dependency factory that needs no client.
// The marked context is refused at the client-dependent boundaries.
func TestSnapshotSharingPreparationContext(t *testing.T) {
	cache := newGCTestCache(t)
	srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
	opts := &NewServerOpts{RemoteCacheIntegration: &RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error { return nil }}}
	ctx := dagql.ContextWithCache(t.Context(), cache)
	// Decision 4's measured cost: the static core schema base moves from the
	// first client to startup on an engine that can receive imports.
	started := time.Now()
	require.NoError(t, srv.initSnapshotSharing(ctx, opts))
	t.Logf("static core schema base construction: %s", time.Since(started))
	require.True(t, cache.SnapshotSharingEnabled())
	require.NotNil(t, srv.coreSchemaBase, "the static core base is built before admission")

	prepare, err := srv.snapshotSharePreparation(ctx)
	require.NoError(t, err)
	require.NotNil(t, prepare, "the engine builds its preparation callback")

	marked := engine.WithSnapshotSharePreparation(ctx)
	prepared, decodeServer, err := prepare(marked)
	require.NoError(t, err)
	require.NotNil(t, decodeServer)
	require.True(t, engine.IsSnapshotSharePreparation(prepared), "the marker survives the preparation context")

	// The decoding server carries the engine root, and asking it for default
	// dependencies uses the pure factory: no client is created or looked up.
	root, err := core.CurrentQuery(prepared)
	require.NoError(t, err)
	require.NotNil(t, root)
	deps := core.NewSchemaBuilder(root, nil)
	require.NotNil(t, deps)

	// The same request through the engine's client-dependent path is refused
	// before it can look up a client.
	_, err = srv.DefaultDeps(marked)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)

	// Building a schema or type definitions from those defaults is refused
	// too, so a decode can never evaluate a schema under the marker.
	_, err = deps.Schema(marked)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
	_, err = deps.TypeDefs(marked, decodeServer)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)

	// The schema-only fork resolves the native decoder classes directly.
	for _, name := range []string{"Container", "Service", "Module", "Directory", "File"} {
		_, ok := decodeServer.ObjectType(name)
		require.True(t, ok, "native decoder class %q resolves on the schema-only fork", name)
	}
	var _ call.View = decodeServer.View
}
