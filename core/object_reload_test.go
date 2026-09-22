package core

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestModuleObjectStateImplementationIdentity(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	dag := newCoreDagqlServerForTest(t, root)
	server.dag = dag
	ctx = dagql.ContextWithCache(ContextWithQuery(ctx, root), cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "identity-client", SessionID: "identity-session"})
	installModuleObjectTestModuleClass(dag)
	// Model the production implementation scope: different recipe handles can
	// carry the same implementation content identity.
	dagql.Fields[*Module]{
		dagql.NodeFunc("_implementationScoped", func(ctx context.Context, self dagql.ObjectResult[*Module], _ struct{}) (dagql.ObjectResult[*Module], error) {
			res, err := dagql.NewObjectResultForCurrentCall(ctx, dag, self.Self().Clone())
			if err != nil {
				return res, err
			}
			return res.WithContentDigest(ctx, digest.FromString(self.Self().Description))
		}),
	}.Install(dag)
	makeModule := func(recipe, implementation string) dagql.ObjectResult[*Module] {
		return newTypeDefAttachedResult(t, ctx, cache, dag, recipe, &Module{
			NameField: "Test", OriginalName: "Test", Description: implementation,
		})
	}
	original := makeModule("original-scope", "revision-one")
	alias := makeModule("another-scope", "revision-one")
	changed := makeModule("changed-scope", "revision-two")
	originalID, err := original.ID()
	require.NoError(t, err)
	aliasID, err := alias.ID()
	require.NoError(t, err)
	require.NotEqual(t, originalID.EngineResultID(), aliasID.EngineResultID())
	same, err := sameModuleImplementation(ctx, original, alias)
	require.NoError(t, err)
	require.True(t, same)
	same, err = sameModuleImplementation(ctx, original, changed)
	require.NoError(t, err)
	require.False(t, same)
}

func stateTestObjectDef(t *testing.T, dag *dagql.Server, name string, fields map[string]*TypeDef) *ObjectTypeDef {
	t.Helper()
	def := NewObjectTypeDef(name, "", nil)
	for fieldName, typeDef := range fields {
		typeRes := newTypeDefDetachedResult(t, dag, name+"."+fieldName+".type", typeDef)
		def.Fields = append(def.Fields, newTypeDefDetachedResult(t, dag, name+"."+fieldName, NewFieldTypeDef(fieldName, typeRes, "", nil)))
	}
	return def
}

func TestModuleObjectStateOverlayKeepsExistingValues(t *testing.T) {
	dag := newTypeDefTestDag(t)
	source := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{
		"count": {Kind: TypeDefKindInteger},
		"label": {Kind: TypeDefKindString, Optional: true},
	})
	target := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{
		"count": {Kind: TypeDefKindInteger},
		"label": {Kind: TypeDefKindString, Optional: true},
		"added": {Kind: TypeDefKindBoolean},
	})
	ref := &Container{}
	previous := map[string]any{
		"count":  json.Number("3"),
		"label":  nil,
		"tags":   []any{},
		"secret": "saved",
		"ref":    ref,
	}
	initial := map[string]any{
		"count":  json.Number("0"),
		"label":  "default",
		"tags":   []any{"default"},
		"secret": "default",
		"ref":    &Container{},
		"added":  true,
	}
	merged, warnings, err := overlayModuleObjectState(initial, previous, target, source)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, json.Number("3"), merged["count"])
	require.Nil(t, merged["label"], "an explicit null wins over the new default")
	require.Empty(t, merged["tags"], "an empty collection wins over the new default")
	require.Equal(t, "saved", merged["secret"], "private fields carry over")
	require.Same(t, ref, merged["ref"])
	require.Equal(t, true, merged["added"], "new fields take the new default")
	merged["secret"] = "changed"
	require.Equal(t, "saved", previous["secret"])
	require.Equal(t, "default", initial["secret"])
}

func TestModuleObjectStateOverlayPublicTypeChange(t *testing.T) {
	dag := newTypeDefTestDag(t)
	for _, tc := range []struct {
		name     string
		old, new *TypeDef
		value    any
	}{
		{"scalar kind", &TypeDef{Kind: TypeDefKindInteger}, &TypeDef{Kind: TypeDefKindString}, json.Number("1")},
		{"nullability with a null value", &TypeDef{Kind: TypeDefKindString, Optional: true}, &TypeDef{Kind: TypeDefKindString}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"f": tc.old})
			target := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"f": tc.new})
			_, _, err := overlayModuleObjectState(map[string]any{"f": "x"}, map[string]any{"f": tc.value}, target, source)
			require.ErrorContains(t, err, `field "f" changed type`)
			require.ErrorContains(t, err, "withTools version")
		})
	}

	t.Run("list element type with an empty value", func(t *testing.T) {
		strList := (&TypeDef{}).WithListOf(newTypeDefDetachedResult(t, dag, "strList", &ListTypeDef{
			ElementTypeDef: newTypeDefDetachedResult(t, dag, "strElem", &TypeDef{Kind: TypeDefKindString}),
		}))
		intList := (&TypeDef{}).WithListOf(newTypeDefDetachedResult(t, dag, "intList", &ListTypeDef{
			ElementTypeDef: newTypeDefDetachedResult(t, dag, "intElem", &TypeDef{Kind: TypeDefKindInteger}),
		}))
		source := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"items": strList})
		target := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"items": intList})
		_, _, err := overlayModuleObjectState(map[string]any{"items": []any{}}, map[string]any{"items": []any{}}, target, source)
		require.ErrorContains(t, err, "[String!]! -> [Int!]!")
	})
}

func TestModuleObjectStateOverlayKindMismatch(t *testing.T) {
	dag := newTypeDefTestDag(t)
	empty := stateTestObjectDef(t, dag, "Obj", nil)
	for _, tc := range []struct {
		name     string
		previous any
		initial  any
		want     string
	}{
		{"string vs number", "5", json.Number("0"), "previous value is a string but the new revision's default is a number"},
		{"bool vs string", true, "", "previous value is a boolean but the new revision's default is a string"},
		{"list vs object", []any{"a"}, map[string]any{}, "previous value is a list but the new revision's default is a object"},
		{"nested object field", map[string]any{"n": json.Number("1")}, map[string]any{"n": "one"}, `field "f.n"`},
		{"list element", []any{json.Number("1")}, []any{"one"}, `field "f[]"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := overlayModuleObjectState(map[string]any{"f": tc.initial}, map[string]any{"f": tc.previous}, empty, empty)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestModuleObjectStateOverlayKindCompatibility(t *testing.T) {
	dag := newTypeDefTestDag(t)
	empty := stateTestObjectDef(t, dag, "Obj", nil)
	for _, tc := range []struct {
		name     string
		previous any
		initial  any
	}{
		{"null previous carries no shape", nil, json.Number("1")},
		{"null default carries no shape", "x", nil},
		{"engine reference vs SDK id string", &Container{}, "some-id"},
		{"id string vs engine reference", "some-id", &Container{}},
		{"int literal vs float literal", json.Number("1"), json.Number("1.5")},
		{"empty list vs typed list", []any{}, []any{"x"}},
		{"nested object with disjoint keys", map[string]any{"a": 1}, map[string]any{"b": "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged, _, err := overlayModuleObjectState(map[string]any{"f": tc.initial}, map[string]any{"f": tc.previous}, empty, empty)
			require.NoError(t, err)
			require.Equal(t, tc.previous, merged["f"])
		})
	}
}

func TestModuleObjectStateOverlayUnknownKeys(t *testing.T) {
	dag := newTypeDefTestDag(t)
	empty := stateTestObjectDef(t, dag, "Obj", nil)

	t.Run("removed public field is dropped", func(t *testing.T) {
		source := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"gone": {Kind: TypeDefKindString}})
		merged, warnings, err := overlayModuleObjectState(map[string]any{"kept": "d"}, map[string]any{"gone": "x", "kept": "v"}, empty, source)
		require.NoError(t, err)
		require.NotContains(t, merged, "gone")
		require.Equal(t, "v", merged["kept"])
		require.Len(t, warnings, 1)
		require.Equal(t, "gone", warnings[0].Field)
		require.Contains(t, warnings[0].Reason, "dropped")
	})

	t.Run("public field turned private keeps its value", func(t *testing.T) {
		source := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"hidden": {Kind: TypeDefKindString}})
		merged, warnings, err := overlayModuleObjectState(map[string]any{"hidden": "d"}, map[string]any{"hidden": "v"}, empty, source)
		require.NoError(t, err)
		require.Empty(t, warnings)
		require.Equal(t, "v", merged["hidden"])
	})

	t.Run("unknown private key passes through with a warning", func(t *testing.T) {
		merged, warnings, err := overlayModuleObjectState(map[string]any{"total": json.Number("0")}, map[string]any{"count": json.Number("7")}, empty, empty)
		require.NoError(t, err)
		require.Equal(t, json.Number("7"), merged["count"], "the SDK decoder decides what to do with it")
		require.Equal(t, json.Number("0"), merged["total"], "the renamed field takes the new default")
		require.Len(t, warnings, 1)
		require.Equal(t, "count", warnings[0].Field)
	})
}

func TestModuleObjectStateVersionFieldIsOrdinary(t *testing.T) {
	dag := newTypeDefTestDag(t)
	def := stateTestObjectDef(t, dag, "Obj", map[string]*TypeDef{"stateVersion": {Kind: TypeDefKindString}})
	old := &ModuleObject{TypeDef: def, Fields: map[string]any{"stateVersion": "saved", "count": json.Number("5")}}
	target := &ModuleObject{TypeDef: def, Fields: map[string]any{"stateVersion": "new", "count": json.Number("0"), "added": "default"}}
	fields, err := rebindModuleObjectFields(t.Context(), target, old)
	require.NoError(t, err)
	require.Equal(t, "saved", fields["stateVersion"], "a field named stateVersion has no special semantics")
	require.Equal(t, json.Number("5"), fields["count"])
	require.Equal(t, "default", fields["added"])
	target.Fields["count"] = "zero"
	_, err = rebindModuleObjectFields(t.Context(), target, old)
	require.ErrorContains(t, err, `Obj: field "count"`)
}

func TestModuleObjectStateRebindIdentity(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	dag := newCoreDagqlServerForTest(t, root)
	server.dag = dag
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(ctx, root), &engine.ClientMetadata{ClientID: "rebind-client", SessionID: "rebind-session"})
	ctx = dagql.ContextWithCache(ctx, cache)
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	makeObject := func(revision string, fields map[string]any) dagql.ObjectResult[*ModuleObject] {
		source := newTypeDefAttachedResult(t, ctx, cache, dag, "source-"+revision, &ModuleSource{
			Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"},
		})
		typeDef := NewObjectTypeDef("Test", "", nil)
		objDef := newTypeDefAttachedResult(t, ctx, cache, dag, "object-def-"+revision, typeDef)
		mod := newTypeDefAttachedResult(t, ctx, cache, dag, "module-"+revision, &Module{
			NameField: "Test", OriginalName: "Test", Source: dagql.NonNull(source),
			Deps:       NewSchemaBuilder(root, nil),
			ObjectDefs: dagql.ObjectResultArray[*TypeDef]{newTypeDefAttachedResult(t, ctx, cache, dag, "type-def-"+revision, (&TypeDef{}).WithObjectTypeDef(objDef))},
		})
		obj := &ModuleObject{Module: mod, TypeDef: typeDef, Fields: fields}
		require.NoError(t, obj.Install(ctx, dag, InstallOpts{SkipConstructor: true}))
		class, ok := dag.ObjectType(typeDef.Name)
		require.True(t, ok)
		class.Extend(dagql.FieldSpec{Name: "revision", Type: dagql.String("")},
			func(ctx context.Context, _ dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentCall(ctx, dagql.String(revision))
			})
		return newTypeDefAttachedResult(t, ctx, cache, dag, "receiver-"+revision, obj)
	}
	previous := makeObject("old", map[string]any{"hidden": "saved", "null": nil})
	// Each revision has its own class, even when both expose the same names.
	dag = newCoreDagqlServerForTest(t, root)
	server.dag = dag
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)
	initial := makeObject("new", map[string]any{"hidden": "default", "null": "default", "added": "new default"})
	result, err := RebindModuleObjectState(ctx, dag, initial, previous)
	require.NoError(t, err)
	rebound, ok := dagql.UnwrapAs[*ModuleObject](result)
	require.True(t, ok)
	require.Equal(t, "saved", rebound.Fields["hidden"])
	require.Nil(t, rebound.Fields["null"])
	require.Equal(t, "new default", rebound.Fields["added"])
	require.Same(t, initial.Self().TypeDef, rebound.TypeDef)
	var revision dagql.String
	require.NoError(t, dag.Select(ctx, result, &revision, dagql.Selector{Field: "revision"}))
	require.Equal(t, dagql.String("new"), revision)
	oldID, err := previous.ID()
	require.NoError(t, err)
	initialID, err := initial.ID()
	require.NoError(t, err)
	newID, err := result.ID()
	require.NoError(t, err)
	require.NotEqual(t, oldID.EngineResultID(), newID.EngineResultID())
	require.NotEqual(t, initialID.EngineResultID(), newID.EngineResultID())
	frame, err := result.ResultCall()
	require.NoError(t, err)
	require.Equal(t, rebindModuleObjectStateField, frame.Field)
	require.Equal(t, initialID.EngineResultID(), frame.Receiver.ResultID)
	provenance, err := NewUserMod(initial.Self().Module).ResultCallModule(ctx)
	require.NoError(t, err)
	require.Equal(t, provenance.ResultRef.ResultID, frame.Module.ResultRef.ResultID)
	require.Len(t, frame.Args, 1)
	require.Equal(t, "previous", frame.Args[0].Name)

	// Selecting the same recipe yields the same object, not an alias of either
	// input, and loading its recipe resolves the state-bearing target object.
	again, err := RebindModuleObjectState(ctx, dag, initial, previous)
	require.NoError(t, err)
	againID, err := again.ID()
	require.NoError(t, err)
	require.Equal(t, newID.EngineResultID(), againID.EngineResultID())
	recipe, err := result.RecipeID(ctx)
	require.NoError(t, err)
	loaded, err := dag.Load(ctx, recipe)
	require.NoError(t, err)
	loadedObj, ok := dagql.UnwrapAs[*ModuleObject](loaded)
	require.True(t, ok)
	require.Equal(t, "saved", loadedObj.Fields["hidden"])

	// No code change retains identity without a rebind selection.
	unchanged, err := RebindModuleObjectState(ctx, dag, initial, result)
	require.NoError(t, err)
	unchangedID, err := unchanged.ID()
	require.NoError(t, err)
	require.Equal(t, newID.EngineResultID(), unchangedID.EngineResultID())

	_, _, err = moduleObjectsForRebind(nil, previous)
	require.ErrorContains(t, err, "requires both target and previous receivers")

	binding := func(obj dagql.AnyObjectResult, version int) boundTool {
		return boundTool{object: obj, objType: obj.ObjectType(), Version: version}
	}
	for _, versions := range [][2]int{{1, 2}, {2, 1}, {1, 0}} {
		t.Run(fmt.Sprintf("version %d to %d", versions[0], versions[1]), func(t *testing.T) {
			// A version change resets even when the implementation is unchanged.
			reset, err := recomposeToolReceiver(ctx, dag, binding(result, versions[0]), binding(initial, versions[1]))
			require.NoError(t, err)
			resetID, err := reset.ID()
			require.NoError(t, err)
			require.Equal(t, initialID.EngineResultID(), resetID.EngineResultID())
		})
	}
	sameVersion, err := recomposeToolReceiver(ctx, dag, binding(result, 2), binding(initial, 2))
	require.NoError(t, err)
	sameVersionID, err := sameVersion.ID()
	require.NoError(t, err)
	require.Equal(t, newID.EngineResultID(), sameVersionID.EngineResultID())

	// A version change is not permission to switch object/module origins.
	foreign := *initial.Self()
	foreignMod := foreign.Module.Self().Clone()
	foreignSource := foreignMod.Source.Value.Self().Clone()
	foreignSource.Local = &LocalModuleSource{ContextDirectoryPath: "/other"}
	foreignMod.Source = dagql.NonNull(newTypeDefAttachedResult(t, ctx, cache, dag, "foreign-source", foreignSource))
	foreign.Module = newTypeDefAttachedResult(t, ctx, cache, dag, "foreign-module", foreignMod)
	foreignObj := newTypeDefAttachedResult(t, ctx, cache, dag, "foreign-object", &foreign)
	_, err = recomposeToolReceiver(ctx, dag, binding(result, 1), binding(foreignObj, 2))
	require.ErrorContains(t, err, "module source changed")
}
