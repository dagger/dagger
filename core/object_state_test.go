package core

import (
	"encoding/json"
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

	_, err = changedStateFields(prev, map[string]any{"a": "x"})
	require.ErrorContains(t, err, "removed")
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

	// Object references travel as ID edges and come back attached.
	ref := newTypeDefAttachedResult(t, ctx, cache, dag, "referenced", &ModuleSource{
		Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/ref"},
	})
	refID, err := ref.ID()
	require.NoError(t, err)
	withRef := makeObject("effectful-ref-method", map[string]any{
		"count": json.Number("1"),
		"label": "same",
		"tags":  []any{"a"},
		"ref":   ref,
	})
	refResult, err := WithModuleObjectFields(ctx, dag, prev, withRef)
	require.NoError(t, err)
	refObj, ok := dagql.UnwrapAs[*ModuleObject](refResult)
	require.True(t, ok)
	gotRef, ok := refObj.Fields["ref"].(dagql.AnyResult)
	require.True(t, ok, "reference field is %T", refObj.Fields["ref"])
	gotRefID, err := gotRef.ID()
	require.NoError(t, err)
	require.Equal(t, stableIDDigest(refID), stableIDDigest(gotRefID))
	unchangedRef, err := WithModuleObjectFields(ctx, dag, refResult, withRef)
	require.NoError(t, err)
	require.Nil(t, unchangedRef, "references compare by identity")

	// The recorded chain is prev!__withField(count)!__withField(extra)!__withField(tags):
	// rooted at the previous state, one frame per changed field in name order,
	// with no trace of the producing call.
	recipe, err := result.RecipeID(ctx)
	require.NoError(t, err)
	prevRecipe, err := prev.RecipeID(ctx)
	require.NoError(t, err)
	var fieldsSet []string
	cur := recipe
	for cur.Field() == withModuleObjectFieldName {
		require.Len(t, cur.Args(), 2)
		require.Equal(t, "name", cur.Args()[0].Name())
		name, ok := cur.Args()[0].Value().ToInput().(string)
		require.True(t, ok)
		fieldsSet = append([]string{name}, fieldsSet...)
		require.NotNil(t, cur.Module(), "the frame keeps module provenance")
		cur = cur.Receiver()
		require.NotNil(t, cur)
	}
	require.Equal(t, []string{"count", "extra", "tags"}, fieldsSet)
	require.Equal(t, prevRecipe.Digest(), cur.Digest(), "the chain is rooted at the previous state")
	require.NotContains(t, recipe.Display(), "effectful-method")

	// Loading the recipe rebuilds the same state.
	loaded, err := dag.Load(ctx, recipe)
	require.NoError(t, err)
	loadedObj, ok := dagql.UnwrapAs[*ModuleObject](loaded)
	require.True(t, ok)
	require.Equal(t, next.Self().Fields, loadedObj.Fields)

	// An unchanged return records nothing.
	same := makeObject("idempotent-method", map[string]any{
		"count": json.Number("1"),
		"label": "same",
		"tags":  []any{"a"},
	})
	unchanged, err := WithModuleObjectFields(ctx, dag, prev, same)
	require.NoError(t, err)
	require.Nil(t, unchanged)
}
