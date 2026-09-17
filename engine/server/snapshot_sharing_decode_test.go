package server

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
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
	lives := &markedDecodeLives{t: t, path: filepath.Join(t.TempDir(), "cache.db"), session: "marked-decode-session"}

	// First life: save the two rows.
	ctx, _, cache, prepare := lives.open()
	_, dag, err := prepare(engine.WithSnapshotSharePreparation(ctx))
	require.NoError(t, err)
	moduleRes := lives.attach(ctx, cache, dag, "markedDecodeModule", &core.Module{NameField: "marked", OriginalName: "marked"})
	module := markedDecodeObject[*core.Module](t, moduleRes)
	serviceRes := lives.attach(ctx, cache, dag, "markedDecodeService", &core.Service{CustomHostname: "marked-service", ModuleContext: module})
	serviceID, err := cache.PersistedResultID(serviceRes)
	require.NoError(t, err)
	moduleID, err := cache.PersistedResultID(moduleRes)
	require.NoError(t, err)
	lives.checkpoint(ctx, cache)

	// Second life: both rows are restored encoded.
	ctx, srv, cache, prepare := lives.open()
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
