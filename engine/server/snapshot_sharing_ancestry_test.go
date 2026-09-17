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

// markedDecodeLives runs a test over two lives of one persistent cache with
// the engine's sharing preparation registered: rows saved in the first are
// restored encoded in the second.
type markedDecodeLives struct {
	t       *testing.T
	path    string
	session string
}

func (l *markedDecodeLives) open() (context.Context, *Server, *dagql.Cache, dagql.PartPreparationContext) {
	l.t.Helper()
	cache, err := dagql.NewCache(l.t.Context(), l.path, nil, nil)
	require.NoError(l.t, err)
	// Close runs its body once, so after an explicit checkpoint this only
	// reports that result again. The context is fresh and bounded: the test's
	// own is canceled by the time cleanup runs.
	l.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cache.Close(ctx); err != nil {
			l.t.Errorf("close marked-decode cache: %v", err)
		}
	})
	srv := &Server{engineCache: cache, shutdownCtx: l.t.Context()}
	ctx := dagql.ContextWithCache(l.t.Context(), cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: l.session + "-client", SessionID: l.session})
	opts := &NewServerOpts{RemoteCacheIntegration: &RemoteCacheIntegrationConfig{Run: func(context.Context, *RemoteCacheAdapter) error { return nil }}}
	require.NoError(l.t, srv.initSnapshotSharing(ctx, opts))
	prepare, err := srv.snapshotSharePreparation(ctx)
	require.NoError(l.t, err)
	return ctx, srv, cache, prepare
}

func (l *markedDecodeLives) attach(ctx context.Context, cache *dagql.Cache, dag *dagql.Server, field string, value dagql.Typed) dagql.AnyResult {
	l.t.Helper()
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: field, Type: dagql.NewResultCallType(value.Type())}
	res, err := cache.GetOrInitCall(ctx, l.session, dag, &dagql.CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(value, frame)
	})
	require.NoError(l.t, err)
	return res
}

func (l *markedDecodeLives) checkpoint(ctx context.Context, cache *dagql.Cache) {
	l.t.Helper()
	require.NoError(l.t, cache.ReleaseSession(ctx, l.session))
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	require.NoError(l.t, cache.Close(bounded))
}

func markedDecodeObject[T dagql.Typed](t *testing.T, res dagql.AnyResult) dagql.ObjectResult[T] {
	t.Helper()
	object, ok := res.(dagql.ObjectResult[T])
	require.True(t, ok, "%T", res)
	return object
}

// The Module decoder's closure, executed under the marker over an ancestry
// attached by hand: a Service whose module context has a source, a context
// source, a runtime Container, a dependency Module, and object, interface and
// enum type definitions. Every one of those rows is restored encoded and is
// decoded by the worker's context. None may look up a client, evaluate a
// schema or start anything; the only default dependencies come from the pure
// factory, once for each Module. What a real SDK load would add to these rows
// (generated function bodies, a built runtime filesystem, source contents) is
// not here: that is the named limit of decision 5, F2.
func TestSnapshotSharingMarkedDecodeOfModuleAncestry(t *testing.T) {
	lives := &markedDecodeLives{t: t, path: filepath.Join(t.TempDir(), "cache.db"), session: "ancestry-session"}

	// First life: save the ancestry.
	ctx, _, cache, prepare := lives.open()
	_, dag, err := prepare(engine.WithSnapshotSharePreparation(ctx))
	require.NoError(t, err)
	platform := core.Platform{OS: "linux", Architecture: "amd64"}
	source := markedDecodeObject[*core.ModuleSource](t, lives.attach(ctx, cache, dag, "ancestrySource",
		&core.ModuleSource{ModuleName: "ancestry", ModuleOriginalName: "ancestry", Kind: core.ModuleSourceKindDir, SourceRootSubpath: "."}))
	runtime := markedDecodeObject[*core.Container](t, lives.attach(ctx, cache, dag, "ancestryRuntime", core.NewContainer(platform)))
	dependency := markedDecodeObject[*core.Module](t, lives.attach(ctx, cache, dag, "ancestryDependency",
		&core.Module{NameField: "dependency", OriginalName: "dependency"}))
	// A type definition is a row that names another: the kind's own row.
	object := markedDecodeObject[*core.ObjectTypeDef](t, lives.attach(ctx, cache, dag, "ancestryObject", core.NewObjectTypeDef("Ancestry", "an object", nil)))
	objectDef := markedDecodeObject[*core.TypeDef](t, lives.attach(ctx, cache, dag, "ancestryObjectDef", (&core.TypeDef{}).WithObject(object)))
	iface := markedDecodeObject[*core.InterfaceTypeDef](t, lives.attach(ctx, cache, dag, "ancestryInterface", core.NewInterfaceTypeDef("Ancestor", "an interface")))
	interfaceDef := markedDecodeObject[*core.TypeDef](t, lives.attach(ctx, cache, dag, "ancestryInterfaceDef", (&core.TypeDef{}).WithInterface(iface)))
	enum := markedDecodeObject[*core.EnumTypeDef](t, lives.attach(ctx, cache, dag, "ancestryEnum", core.NewEnumTypeDef("Generation", "an enum", dagql.ObjectResult[*core.SourceMap]{})))
	enumDef := markedDecodeObject[*core.TypeDef](t, lives.attach(ctx, cache, dag, "ancestryEnumDef", (&core.TypeDef{}).WithEnum(enum)))
	module := markedDecodeObject[*core.Module](t, lives.attach(ctx, cache, dag, "ancestryModule", &core.Module{
		NameField: "ancestry", OriginalName: "ancestry",
		Source: dagql.NonNull(source), ContextSource: dagql.NonNull(source), Runtime: dagql.NonNull(runtime),
		Deps:       core.NewSchemaBuilder(nil, []core.Mod{core.NewUserMod(dependency)}),
		ObjectDefs: dagql.ObjectResultArray[*core.TypeDef]{objectDef}, InterfaceDefs: dagql.ObjectResultArray[*core.TypeDef]{interfaceDef},
		EnumDefs: dagql.ObjectResultArray[*core.TypeDef]{enumDef},
	}))
	service := lives.attach(ctx, cache, dag, "ancestryService", &core.Service{CustomHostname: "ancestry-service", ModuleContext: module})
	ids := map[string]uint64{}
	for name, res := range map[string]dagql.AnyResult{"service": service, "module": module, "source": source, "runtime": runtime,
		"dependency": dependency, "object": objectDef, "interface": interfaceDef, "enum": enumDef} {
		ids[name], err = cache.PersistedResultID(res)
		require.NoError(t, err, name)
	}
	lives.checkpoint(ctx, cache)

	// Second life: every row is encoded. Decode the Service as the worker does.
	ctx, srv, cache, prepare := lives.open()
	prepared, decodeServer, err := prepare(engine.WithSnapshotSharePreparation(ctx))
	require.NoError(t, err)
	root, err := core.CurrentQuery(prepared)
	require.NoError(t, err)
	base, err := srv.getCoreSchemaBase(ctx)
	require.NoError(t, err)
	var factoryCalls atomic.Int32
	prepared = core.ContextWithPersistedDecodeDefaults(prepared, root, func(view call.View) *core.SchemaBuilder {
		factoryCalls.Add(1)
		return core.NewSchemaBuilder(root, []core.Mod{base.CoreMod(view)})
	})

	loaded, err := cache.LoadResultByResultID(prepared, "", decodeServer, ids["service"])
	require.NoError(t, err, "no decoder in the Module's closure trips a guard under the marker")
	decodedService, ok := loaded.Unwrap().(*core.Service)
	require.True(t, ok, "%T", loaded.Unwrap())
	decoded := decodedService.ModuleContext.Self()
	require.NotNil(t, decoded)
	require.Equal(t, int32(2), factoryCalls.Load(), "the Module and its dependency Module each ask the pure factory once")

	row := func(res dagql.AnyResult) uint64 {
		t.Helper()
		id, err := cache.PersistedResultID(res)
		require.NoError(t, err)
		return id
	}
	require.Equal(t, ids["module"], row(decodedService.ModuleContext))
	require.True(t, decoded.Source.Valid)
	require.Equal(t, ids["source"], row(decoded.Source.Value), "the exact persisted source row")
	require.Equal(t, "ancestry", decoded.Source.Value.Self().ModuleName)
	require.True(t, decoded.ContextSource.Valid)
	require.Equal(t, ids["source"], row(decoded.ContextSource.Value))
	require.True(t, decoded.Runtime.Valid)
	require.Equal(t, ids["runtime"], row(decoded.Runtime.Value), "the exact persisted runtime row")
	require.Equal(t, platform, decoded.Runtime.Value.Self().Platform)
	require.Len(t, decoded.ObjectDefs, 1)
	require.Equal(t, ids["object"], row(decoded.ObjectDefs[0]))
	require.Equal(t, "Ancestry", decoded.ObjectDefs[0].Self().AsObject.Value.Self().Name)
	require.Len(t, decoded.InterfaceDefs, 1)
	require.Equal(t, ids["interface"], row(decoded.InterfaceDefs[0]))
	require.Len(t, decoded.EnumDefs, 1)
	require.Equal(t, ids["enum"], row(decoded.EnumDefs[0]))
	var dependencies []uint64
	for _, mod := range decoded.Deps.Mods() {
		if dep := mod.ModuleResult(); dep.Self() != nil {
			dependencies = append(dependencies, row(dep))
		}
	}
	require.Equal(t, []uint64{ids["dependency"]}, dependencies, "the exact persisted dependency Module row")
}
