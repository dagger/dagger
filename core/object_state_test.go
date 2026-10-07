package core

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestModuleObjectStateValueRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"null", nil},
		{"string", "hello"},
		{"number", json.Number("42")},
		{"bool", true},
		{"empty list", []any{}},
		{"list", []any{"a", json.Number("1"), nil}},
		{"empty map", map[string]any{}},
		{"nested", map[string]any{"items": []any{map[string]any{"k": "v"}}, "n": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, err := stateValueInput(tc.val)
			require.NoError(t, err)
			obj, ok := input.(dagql.InputObject[StateValue])
			require.True(t, ok)
			decoded, err := obj.Value.sdkValue()
			require.NoError(t, err)
			require.Equal(t, tc.val, decoded)
			// The literal round-trips through the decoder, as recipe replay does.
			replayed, err := dagql.InputObject[StateValue]{}.Decoder().DecodeInput(obj.ToLiteral().ToInput())
			require.NoError(t, err)
			again, err := replayed.(dagql.InputObject[StateValue]).Value.sdkValue()
			require.NoError(t, err)
			require.Equal(t, tc.val, again)
		})
	}
}

func TestModuleObjectStateChangedFields(t *testing.T) {
	prev := map[string]any{"a": "x", "b": json.Number("1"), "c": map[string]any{"k": []any{"v"}}}
	changed, err := changedStateFields(prev, map[string]any{"a": "x", "b": json.Number("1"), "c": map[string]any{"k": []any{"v"}}})
	require.NoError(t, err)
	require.Empty(t, changed)

	changed, err = changedStateFields(prev, map[string]any{"a": "y", "b": json.Number("1"), "c": map[string]any{"k": []any{"w"}}, "d": nil})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "c", "d"}, changed)

	// A dropped field counts as null: changed unless it already was null.
	changed, err = changedStateFields(prev, map[string]any{"a": "x", "c": map[string]any{"k": []any{"v"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, changed)
	changed, err = changedStateFields(map[string]any{"a": nil}, map[string]any{})
	require.NoError(t, err)
	require.Empty(t, changed)

	_, err = changedStateFields(prev, map[string]any{"a": "x", "b": math.Inf(1), "c": nil})
	require.ErrorContains(t, err, `field "b"`)
}

func TestModuleObjectStateWithFields(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	dag := newCoreDagqlServerForTest(t, root)
	server.dag = dag
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(ctx, root), &engine.ClientMetadata{ClientID: "state-client", SessionID: "state-session"})
	ctx = dagql.ContextWithCache(ctx, cache)
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)

	source := newTypeDefAttachedResult(t, ctx, cache, dag, "source", &ModuleSource{
		Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"},
	})
	typeDef := NewObjectTypeDef("Test", "", nil)
	objDef := newTypeDefAttachedResult(t, ctx, cache, dag, "object-def", typeDef)
	mod := newTypeDefAttachedResult(t, ctx, cache, dag, "module", &Module{
		NameField: "Test", OriginalName: "Test", Source: dagql.NonNull(source),
		Deps:       NewSchemaBuilder(root, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{newTypeDefAttachedResult(t, ctx, cache, dag, "type-def", (&TypeDef{}).WithObjectTypeDef(objDef))},
	})
	require.NoError(t, (&ModuleObject{Module: mod, TypeDef: typeDef}).Install(ctx, dag, InstallOpts{SkipConstructor: true}))
	makeObject := func(op string, fields map[string]any) dagql.ObjectResult[*ModuleObject] {
		return newTypeDefAttachedResult(t, ctx, cache, dag, op, &ModuleObject{Module: mod, TypeDef: typeDef, Fields: fields})
	}

	prev := makeObject("previous", map[string]any{
		"count": json.Number("1"),
		"label": "same",
		"tags":  []any{"a"},
	})
	// Stand-in for the @cache(Never) method's return value.
	next := makeObject("effectful-method", map[string]any{
		"count": json.Number("2"),
		"label": "same",
		"tags":  []any{"a", "b"},
		"extra": map[string]any{"k": nil},
	})

	result, err := WithModuleObjectFields(ctx, dag, prev, next)
	require.NoError(t, err)
	require.NotNil(t, result)
	got, ok := dagql.UnwrapAs[*ModuleObject](result)
	require.True(t, ok)
	require.Equal(t, next.Self().Fields, got.Fields)

	// Loading the recorded recipe rebuilds the same state.
	loadFields := func(res dagql.AnyObjectResult) map[string]any {
		t.Helper()
		recipe, err := res.RecipeID(ctx)
		require.NoError(t, err)
		loaded, err := dag.Load(ctx, recipe)
		require.NoError(t, err)
		loadedObj, ok := dagql.UnwrapAs[*ModuleObject](loaded)
		require.True(t, ok)
		return loadedObj.Fields
	}
	require.Equal(t, next.Self().Fields, loadFields(result))

	// So does a state recorded on top of a recorded state.
	again := makeObject("effectful-method-again", map[string]any{
		"count": json.Number("3"),
		"label": "relabeled",
		"tags":  []any{"a", "b"},
		"extra": map[string]any{"k": nil},
	})
	result2, err := WithModuleObjectFields(ctx, dag, result, again)
	require.NoError(t, err)
	require.Equal(t, again.Self().Fields, loadFields(result2))

	// An object reference comes back as the object it referenced, and
	// returning the same reference again changes nothing.
	ref := newTypeDefAttachedResult(t, ctx, cache, dag, "referenced", &ModuleSource{
		Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/ref"},
	})
	withRef := makeObject("effectful-ref-method", map[string]any{
		"count": json.Number("1"),
		"label": "same",
		"tags":  []any{"a"},
		"ref":   ref,
	})
	refResult, err := WithModuleObjectFields(ctx, dag, prev, withRef)
	require.NoError(t, err)
	gotRef, ok := loadFields(refResult)["ref"].(dagql.AnyResult)
	require.True(t, ok, "reference field is %T", loadFields(refResult)["ref"])
	gotSource, ok := dagql.UnwrapAs[*ModuleSource](gotRef)
	require.True(t, ok)
	require.Equal(t, "/ref", gotSource.Local.ContextDirectoryPath)
	unchangedRef, err := WithModuleObjectFields(ctx, dag, refResult, withRef)
	require.NoError(t, err)
	require.Nil(t, unchangedRef, "an unchanged reference records nothing")

	// An unchanged return records nothing.
	same := makeObject("idempotent-method", map[string]any{
		"count": json.Number("1"),
		"label": "same",
		"tags":  []any{"a"},
	})
	unchanged, err := WithModuleObjectFields(ctx, dag, prev, same)
	require.NoError(t, err)
	require.Nil(t, unchanged)

	// A dropped field is recorded as null, not refused.
	dropped := makeObject("dropping-method", map[string]any{
		"count": json.Number("1"),
		"tags":  []any{"a"},
	})
	nulled, err := WithModuleObjectFields(ctx, dag, prev, dropped)
	require.NoError(t, err)
	nulledFields := loadFields(nulled)
	require.Contains(t, nulledFields, "label")
	require.Nil(t, nulledFields["label"])
	require.Equal(t, json.Number("1"), nulledFields["count"])

	// A value that can't be encoded fails the tool call; the producing call
	// is never recorded in its place. Core objects pass through as returned.
	unencodable := makeObject("unencodable-method", map[string]any{
		"count": math.Inf(1),
		"label": "same",
		"tags":  []any{"a"},
	})
	_, err = WithModuleObjectFields(ctx, dag, prev, unencodable)
	require.ErrorContains(t, err, `field "count"`)
	_, rebind, err := (&MCP{}).fieldwiseState(ctx, dag, prev, unencodable)
	require.ErrorContains(t, err, "record Test state")
	require.False(t, rebind)
	passed, rebind, err := (&MCP{}).fieldwiseState(ctx, dag, ref, ref)
	require.NoError(t, err)
	require.True(t, rebind)
	passedSource, ok := dagql.UnwrapAs[*ModuleSource](passed)
	require.True(t, ok)
	require.Same(t, ref.Self(), passedSource)
}
