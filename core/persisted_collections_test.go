package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestPersistedCollectionReferences(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "collections-persistence")
	ctx, cache, srv := env.open(t)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CollectionTypeDef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CollectionDelta]{}))
	object := env.attach(t, ctx, cache, srv, "collection-object-type", NewObjectTypeDef("Items", "", nil)).(dagql.ObjectResult[*ObjectTypeDef])
	def := env.attach(t, ctx, cache, srv, "collection-type", &CollectionTypeDef{Object: object})
	delta := env.attach(t, ctx, cache, srv, "collection-delta", &CollectionDelta{AddedKeys: []string{"a"}, RemovedKeys: []string{"b"}})
	require.Equal(t, persistedRowID(t, cache, object), assertPersistedRefsMatchOwnership(t, ctx, cache, def)["objectJSON"])
	require.Empty(t, persistedVisitedRefs(t, ctx, cache, delta))

	mod := env.attach(t, ctx, cache, srv, "collection-module", &Module{NameField: "collections", Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	holderDef := NewObjectTypeDef("Items", "", nil)
	holderDef.Collection = &CollectionConfig{Enabled: true}
	install := func(mod dagql.ObjectResult[*Module]) {
		srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: holderDef}}))
	}
	install(mod)
	base := env.attach(t, ctx, cache, srv, "original-items", &ModuleObject{Module: mod, TypeDef: holderDef, Fields: map[string]any{"keys": []any{"a", "b"}}})
	subset := env.attach(t, ctx, cache, srv, "subset-items", &ModuleObject{Module: mod, TypeDef: holderDef, Fields: map[string]any{"keys": []any{"a"}}, CollectionBase: base})
	require.Equal(t, persistedRowID(t, cache, base), assertPersistedRefsMatchOwnership(t, ctx, cache, subset)["objectJSON.collectionBase"])
	records := map[string]dagql.AnyResult{"def": def, "delta": delta, "subset": subset}
	ids := map[string]uint64{}
	for name, res := range records {
		ids[name] = persistedRowID(t, cache, res)
	}
	modID := persistedRowID(t, cache, mod)
	baseID := persistedRowID(t, cache, base)
	ctx, cache, srv = env.restart(t, ctx, cache)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CollectionTypeDef]{}))
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*CollectionDelta]{}))
	restoredMod, err := cache.LoadResultByResultID(ctx, env.session, srv, modID)
	require.NoError(t, err)
	install(restoredMod.(dagql.ObjectResult[*Module]))
	load := func(name string) dagql.Typed {
		res, err := cache.LoadResultByResultID(ctx, env.session, srv, ids[name])
		require.NoError(t, err)
		return res.Unwrap()
	}
	require.Equal(t, "Items", load("def").(*CollectionTypeDef).Object.Self().Name)
	require.Equal(t, []string{"a"}, load("delta").(*CollectionDelta).AddedKeys)
	restored := load("subset").(*ModuleObject)
	require.Equal(t, baseID, persistedRowID(t, cache, restored.CollectionBase))
	require.Equal(t, []any{"a", "b"}, restored.CollectionBase.Unwrap().(*ModuleObject).Fields["keys"])
}
