package server

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// A marked decode, executed: a persisted Service whose module context is a
// persisted minimal Module is saved, the cache is reopened so both rows are
// genuinely encoded, and the Service is decoded under the sharing preparation
// marker through the engine's registered callback. The Module decoder takes
// its default dependencies from the pure factory exactly once, no client is
// looked up, nothing is evaluated or started, and native values come back.
// The SDK-built ancestry (ModuleSource, runtime Container, type definitions)
// stays with batch 7.
func TestSnapshotSharingMarkedDecodeOfEncodedService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	opts := &NewServerOpts{RemoteCacheIntegration: &RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error { return nil }}}
	const session = "marked-decode-session"

	open := func() (context.Context, *Server, *dagql.Cache, dagql.PartPreparationContext) {
		cache, err := dagql.NewCache(t.Context(), path, nil, nil)
		require.NoError(t, err)
		// Registered before any further assertion, so a failure below cannot
		// leave this cache open. Close runs its body once: after the explicit
		// checkpoint of the first life this only reports that result again.
		// The context is fresh and bounded; the test's own context is
		// canceled by the time cleanup runs and carries no deadline.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := cache.Close(ctx); err != nil {
				t.Errorf("close marked-decode cache: %v", err)
			}
		})
		srv := &Server{engineCache: cache, shutdownCtx: t.Context()}
		ctx := dagql.ContextWithCache(t.Context(), cache)
		ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "marked-decode-client", SessionID: session})
		require.NoError(t, srv.initSnapshotSharing(ctx, opts))
		prepare, err := srv.snapshotSharePreparation(ctx)
		require.NoError(t, err)
		return ctx, srv, cache, prepare
	}
	attach := func(ctx context.Context, cache *dagql.Cache, dag *dagql.Server, field string, value dagql.Typed) dagql.AnyResult {
		frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
		res, err := cache.GetOrInitCall(ctx, session, dag, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
			return dagql.NewResultForCall(value, frame)
		})
		require.NoError(t, err)
		return res
	}

	// First life: save the two rows.
	ctx, _, cache, prepare := open()
	_, dag, err := prepare(engine.WithSnapshotSharePreparation(ctx))
	require.NoError(t, err)
	moduleRes := attach(ctx, cache, dag, "markedDecodeModule", &core.Module{NameField: "marked", OriginalName: "marked"})
	module, ok := moduleRes.(dagql.ObjectResult[*core.Module])
	require.True(t, ok, "%T", moduleRes)
	serviceRes := attach(ctx, cache, dag, "markedDecodeService", &core.Service{CustomHostname: "marked-service", ModuleContext: module})
	serviceID, err := cache.PersistedResultID(serviceRes)
	require.NoError(t, err)
	moduleID, err := cache.PersistedResultID(moduleRes)
	require.NoError(t, err)
	require.NoError(t, cache.ReleaseSession(ctx, session))
	// The first life's checkpoint, with a short deadline of its own.
	checkpoint, cancelCheckpoint := context.WithTimeout(ctx, 10*time.Second)
	defer cancelCheckpoint()
	require.NoError(t, cache.Close(checkpoint))

	// Second life: both rows are restored encoded.
	ctx, srv, cache, prepare := open()
	marked := engine.WithSnapshotSharePreparation(ctx)
	prepared, decodeServer, err := prepare(marked)
	require.NoError(t, err)
	root, err := core.CurrentQuery(prepared)
	require.NoError(t, err)

	// Count the pure factory's calls with the registered root and base.
	base, err := srv.getCoreSchemaBase(ctx)
	require.NoError(t, err)
	var factoryCalls atomic.Int32
	prepared = core.ContextWithPersistedDecodeDefaults(prepared, root, func(view call.View) *core.SchemaBuilder {
		factoryCalls.Add(1)
		return core.NewSchemaBuilder(root, []core.Mod{base.CoreMod(view)})
	})

	loaded, err := cache.LoadResultByResultID(prepared, "", decodeServer, serviceID)
	require.NoError(t, err, "a marked decode of an encoded Service and its encoded Module succeeds")
	service, ok := loaded.Unwrap().(*core.Service)
	require.True(t, ok, "%T", loaded.Unwrap())
	require.Equal(t, "marked-service", service.CustomHostname)
	decodedModule := service.ModuleContext.Self()
	require.NotNil(t, decodedModule, "the Service's module context was decoded to a native Module")
	require.Equal(t, "marked", decodedModule.NameField)
	require.NotNil(t, decodedModule.Deps, "the Module took its default dependencies from the pure factory")
	require.Equal(t, int32(1), factoryCalls.Load(), "one Module decode asks the factory once")
	moduleRow, err := cache.PersistedResultID(service.ModuleContext)
	require.NoError(t, err)
	require.Equal(t, moduleID, moduleRow, "the exact persisted Module row was loaded")

	// The same decode without a registered factory is refused at the decoder,
	// not silently sent to a client lookup.
	_, err = srv.DefaultDeps(marked)
	require.ErrorIs(t, err, engine.ErrSnapshotShareEvaluation)
}
