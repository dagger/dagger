package core

import (
	"context"
	"maps"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func collectionTestObject(t *testing.T, kind TypeDefKind, keys any) (*ModuleObject, *dagql.Server) {
	t.Helper()
	dag := newTypeDefTestDag(t)
	key := newTypeDefDetachedResult(t, dag, "key", (&TypeDef{}).WithKind(kind))
	list := newTypeDefDetachedResult(t, dag, "keyList", &ListTypeDef{ElementTypeDef: key})
	keyList := newTypeDefDetachedResult(t, dag, "keys", (&TypeDef{}).WithListOf(list))
	item := newTypeDefDetachedResult(t, dag, "item", NewObjectTypeDef("Item", "", nil))
	itemType := newTypeDefDetachedResult(t, dag, "itemType", (&TypeDef{}).WithObjectTypeDef(item))
	arg := newTypeDefDetachedResult(t, dag, "arg", &FunctionArg{Name: "name", OriginalName: "name", TypeDef: key})
	field := newTypeDefDetachedResult(t, dag, "field", &FieldTypeDef{Name: "keys", OriginalName: "Keys", TypeDef: keyList})
	get := newTypeDefDetachedResult(t, dag, "get", &Function{Name: "get", OriginalName: "Get", ReturnType: itemType, Args: dagql.ObjectResultArray[*FunctionArg]{arg}})
	obj := NewObjectTypeDef("Items", "", nil)
	obj.Collection = &CollectionConfig{Enabled: true}
	obj.Fields = dagql.ObjectResultArray[*FieldTypeDef]{field}
	obj.Functions = dagql.ObjectResultArray[*Function]{get}
	value := &ModuleObject{TypeDef: obj, Fields: map[string]any{"Keys": keys, "privateState": "retained"}}
	attachCollectionTestObject(t, value)
	return value, dag
}

func attachCollectionTestObject(t *testing.T, obj *ModuleObject) {
	t.Helper()
	self, err := dagql.NewResultForCall(obj, moduleObjectTestSyntheticCall("collection", obj))
	require.NoError(t, err)
	_, err = obj.attachCollectionBase(self, func(value dagql.AnyResult) (dagql.AnyResult, error) { return value, nil })
	require.NoError(t, err)
}

func TestCollectionSubsetDelta(t *testing.T) {
	obj, dag := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	selectKeys := func(obj *ModuleObject, names ...string) *ModuleObject {
		t.Helper()
		keys := make([]collectionKey, 0, len(names))
		for _, name := range names {
			key, err := collectionKeyFromInput(dagql.String(name))
			require.NoError(t, err)
			keys = append(keys, key)
		}
		subset, err := obj.collectionSubset(t.Context(), keys)
		require.NoError(t, err)
		return subset
	}
	delta, err := obj.collectionDelta(t.Context())
	require.NoError(t, err)
	require.Empty(t, delta.AddedKeys)
	require.Empty(t, delta.RemovedKeys)
	first := selectKeys(obj, "c", "a")
	require.Equal(t, []any{"a", "c"}, first.Fields["Keys"])
	require.Equal(t, []any{"a", "b", "c"}, obj.Fields["Keys"])
	require.Equal(t, "retained", first.Fields["privateState"])
	delta, err = first.collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, delta.RemovedKeys)
	second := selectKeys(first, "c")
	delta, err = selectKeys(second, "c").collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, delta.RemovedKeys)
	require.Empty(t, delta.AddedKeys)
	delta, err = selectKeys(second).collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, delta.RemovedKeys)

	// The original is persisted without a recursive reference to itself.
	installModuleObjectTestModuleClass(dag)
	obj.Module = newTypeDefDetachedResult(t, dag, "module", &Module{})
	encoded, err := obj.EncodePersistedObject(context.Background(), nil)
	require.NoError(t, err)
	decoded, err := obj.DecodePersistedObject(context.Background(), dagql.NewPersistDecodeContext(dag, 0, nil), encoded.JSON)
	require.NoError(t, err)
	reloaded := decoded.(*ModuleObject)
	attachCollectionTestObject(t, reloaded)
	delta, err = selectKeys(reloaded, "c").collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, delta.RemovedKeys)
}

func TestCollectionBaseTracksModuleChanges(t *testing.T) {
	original, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	// This type has no delta field. The base exists before any subset call.
	require.Same(t, original, original.CollectionBase.Unwrap())
	fields, err := original.collectionSDKFields(t.Context())
	require.NoError(t, err)
	require.NotContains(t, original.Fields, collectionBaseField)
	fields["Keys"] = []any{"a", "c", "d"}
	changed := &ModuleObject{TypeDef: original.TypeDef, Fields: fields}
	require.NoError(t, changed.restoreCollectionBase(t.Context()))
	attachCollectionTestObject(t, changed)
	require.Same(t, original, changed.CollectionBase.Unwrap())
	delta, err := changed.collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"d"}, delta.AddedKeys)
	require.Equal(t, []string{"b"}, delta.RemovedKeys)

	// Ordinary state copies preserve the base. Restoring a removed key cancels
	// its removal; neither operation needs to notify the engine.
	restored := *changed
	restored.Fields = maps.Clone(changed.Fields)
	restored.Fields["Keys"] = []any{"d", "c", "b", "a"}
	attachCollectionTestObject(t, &restored)
	delta, err = restored.collectionDelta(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"d"}, delta.AddedKeys)
	require.Empty(t, delta.RemovedKeys)

	fresh := &ModuleObject{TypeDef: original.TypeDef, Fields: map[string]any{"Keys": []any{"d"}}}
	attachCollectionTestObject(t, fresh)
	delta, err = fresh.collectionDelta(t.Context())
	require.NoError(t, err)
	require.Empty(t, delta.AddedKeys)
	require.Empty(t, delta.RemovedKeys)
}

func TestCollectionInvalidKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []any
		want string
	}{
		{"null", []any{"a", nil}, "null key"},
		{"duplicate", []any{"a", "a"}, "duplicate key"},
		{"wrong type", []any{false}, "key:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj, _ := collectionTestObject(t, TypeDefKindString, tc.keys)
			_, err := obj.collectionKeys(t.Context())
			require.ErrorContains(t, err, tc.want)
		})
	}
	obj, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b"})
	a, err := collectionKeyFromInput(dagql.String("a"))
	require.NoError(t, err)
	missing, err := collectionKeyFromInput(dagql.String("missing"))
	require.NoError(t, err)
	_, err = obj.collectionSubset(t.Context(), []collectionKey{a, a})
	require.ErrorContains(t, err, "duplicate key")
	_, err = obj.collectionSubset(t.Context(), []collectionKey{missing})
	require.ErrorContains(t, err, "does not contain")
}

func TestCollectionTypedKeyText(t *testing.T) {
	for _, tc := range []struct {
		kind TypeDefKind
		keys []any
		want []string
	}{
		{TypeDefKindString, []any{"", "a/b&c=+", "1"}, []string{"", "a/b&c=+", "1"}},
		{TypeDefKindInteger, []any{1, -2}, []string{"1", "-2"}},
		{TypeDefKindFloat, []any{1.25, -2.5}, []string{"1.25", "-2.5"}},
		{TypeDefKindBoolean, []any{true, false}, []string{"true", "false"}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			obj, _ := collectionTestObject(t, tc.kind, tc.keys)
			keys, err := obj.collectionKeys(t.Context())
			require.NoError(t, err)
			var texts []string
			for _, key := range keys {
				texts = append(texts, key.text)
			}
			require.Equal(t, tc.want, texts)
		})
	}
}

func TestCollectionDeclarations(t *testing.T) {
	obj, _ := collectionTestObject(t, TypeDefKindString, nil)
	_, err := obj.TypeDef.CollectionMembers()
	require.NoError(t, err)
	override, err := obj.TypeDef.WithCollectionMember("keys", "OtherKeys")
	require.NoError(t, err)
	_, err = override.CollectionMembers()
	require.ErrorContains(t, err, "stored keys field")
	_, err = override.WithCollectionMember("keys", "ThirdKeys")
	require.ErrorContains(t, err, "multiple keys")
	require.Empty(t, obj.TypeDef.Collection.Keys)
	orphan := obj.TypeDef.Clone()
	orphan.Collection = &CollectionConfig{Keys: "keys"}
	_, err = orphan.CollectionMembers()
	require.ErrorContains(t, err, "not marked as a collection")
	noGet := obj.TypeDef.Clone()
	noGet.Functions = nil
	_, err = noGet.CollectionMembers()
	require.ErrorContains(t, err, "get function")
	badDelta := obj.TypeDef.Clone()
	badDelta.Collection = &CollectionConfig{Enabled: true, Delta: "absent"}
	_, err = badDelta.CollectionMembers()
	require.ErrorContains(t, err, "delta field")
}

func TestUnfilteredInlineCollectionSDKInputHasBase(t *testing.T) {
	original, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b"})
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	dag := newCoreDagqlServerForTest(t, root)
	server.dag = dag
	installModuleObjectTestModuleClass(dag)
	installTypeDefTestClasses(dag)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "inline-client", SessionID: "inline-session"})
	original.TypeDef = newTypeDefAttachedResult(t, ctx, cache, dag, "inline-collection-type", original.TypeDef).Self()
	mod := newTypeDefAttachedResult(t, ctx, cache, dag, "inline-module", &Module{NameField: "test", Deps: NewSchemaBuilder(nil, nil)})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: original.TypeDef}}))
	typ := &ModuleObjectType{mod: mod, typeDef: original.TypeDef}
	ctx = dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("inline-collection", original))
	decoded, err := typ.ConvertFromSDKResult(ctx, maps.Clone(original.Fields))
	require.NoError(t, err)
	require.Nil(t, dagql.CurrentResultScope(ctx))
	converted, err := typ.ConvertToSDKInput(ctx, decoded)
	require.NoError(t, err)
	require.NotEmpty(t, converted.(map[string]any)[collectionBaseField])
}

// Keep the real attachment/cache path while leaving the selected keys unchanged.
type privateCollectionIdentityScope struct{ results []dagql.AnyObjectResult }

func (scope *privateCollectionIdentityScope) Apply(_ context.Context, _ *dagql.Server, value dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	scope.results = append(scope.results, value)
	return value, nil
}

func TestPrivateCollectionSDKInputKeepsDistinctStoredPaths(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "private-path-client", SessionID: "private-path-session"})
	original, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	original.TypeDef = newTypeDefAttachedResult(t, ctx, cache, srv, "private-path-type", original.TypeDef).Self()
	source := newTypeDefAttachedResult(t, ctx, cache, srv, "private-path-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
	mod := newTypeDefAttachedResult(t, ctx, cache, srv, "private-path-module", &Module{NameField: "test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(nil, nil)})
	original.Module = mod
	class := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: original.TypeDef}})
	stateField, err := original.privateCollectionStateField(srv)
	require.NoError(t, err)
	// This fixture installs the resolver directly, without a module runtime.
	stateField.Spec.Module, stateField.Spec.ModuleProvider = nil, nil
	class.Install(stateField)
	srv.InstallObject(class)
	typ := &ModuleObjectType{mod: mod, typeDef: original.TypeDef}
	ctx = dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("private-path-base", original))
	base, err := typ.ConvertFromSDKResult(ctx, maps.Clone(original.Fields))
	require.NoError(t, err)
	base, err = cache.AttachResult(ctx, "private-path-session", srv, base)
	require.NoError(t, err)
	inline := func(key string) map[string]any {
		return map[string]any{"Keys": []any{key}, collectionBaseField: base}
	}
	input := map[string]any{
		"first":  inline("a"),
		"second": inline("b"),
		"nested": []any{inline("c"), []any{inline("b"), inline("a")}},
	}
	parent := &ModuleObjectType{typeDef: NewObjectTypeDef("Holder", "", nil)}
	frame := moduleObjectTestSyntheticCall("private-path-holder", &ModuleObject{TypeDef: parent.typeDef})
	scope := &privateCollectionIdentityScope{}
	ctx = dagql.WithResultScope(dagql.ContextWithCall(ctx, frame), scope)
	var previous map[string]uint64
	for range 2 {
		scope.results = nil
		got, err := moduleObjectFieldsToSDKInput(ctx, parent, frame, input)
		require.NoError(t, err)
		check := func(value any, key string) {
			fields := value.(map[string]any)
			require.Equal(t, []any{key}, fields["Keys"])
			require.NotEmpty(t, fields[collectionBaseField])
		}
		check(got["first"], "a")
		check(got["second"], "b")
		nested := got["nested"].([]any)
		check(nested[0], "c")
		inner := nested[1].([]any)
		check(inner[0], "b")
		check(inner[1], "a")
		// Scope callbacks also see schema objects and can visit a collection
		// more than once. Inspect the reconstructed states, not callback order.
		unscoped := dagql.WithResultScope(ctx, nil)
		baseRecipe, err := base.RecipeID(unscoped)
		require.NoError(t, err)
		current := map[string]uint64{}
		for _, result := range scope.results {
			obj, ok := dagql.UnwrapAs[*ModuleObject](result)
			if !ok || obj.TypeDef.Name != original.TypeDef.Name {
				continue
			}
			recipe, err := result.RecipeID(unscoped)
			require.NoError(t, err)
			if recipe.Field() != privateCollectionStateFieldName {
				continue
			}
			require.Equal(t, baseRecipe.Digest(), recipe.Receiver().Digest())
			keys, err := obj.collectionKeys(unscoped)
			require.NoError(t, err)
			require.Len(t, keys, 1)
			key := keys[0].text
			require.Contains(t, []string{"a", "b", "c"}, key)
			id, err := result.ID()
			require.NoError(t, err)
			if existing, ok := current[key]; ok {
				require.Equal(t, existing, id.EngineResultID(), "identical stored states must reuse the same result")
			}
			current[key] = id.EngineResultID()
			loaded, err := srv.Load(unscoped, recipe)
			require.NoError(t, err)
			restored, ok := dagql.UnwrapAs[*ModuleObject](loaded)
			require.True(t, ok)
			require.Equal(t, []any{key}, restored.Fields["Keys"])
		}
		require.Len(t, current, 3, "all three stored states must reach the scope")
		require.NotEqual(t, current["a"], current["b"])
		require.NotEqual(t, current["a"], current["c"])
		require.NotEqual(t, current["b"], current["c"])
		if previous != nil {
			require.Equal(t, previous, current, "a second conversion must reuse each stored state")
		}
		previous = current
	}
	require.Equal(t, []any{"a", "b", "c"}, original.Fields["Keys"])
}

type receiverCollectionScope struct{}

func (receiverCollectionScope) Apply(ctx context.Context, srv *dagql.Server, value dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	obj, ok := dagql.UnwrapAs[*ModuleObject](value)
	if !ok || obj.TypeDef.Collection == nil || !obj.TypeDef.Collection.Enabled {
		return value, nil
	}
	key, err := collectionKeyFromInput(dagql.String("b"))
	if err != nil {
		return nil, err
	}
	subset, err := obj.collectionSubset(ctx, []collectionKey{key})
	if err != nil {
		return nil, err
	}
	frame, err := value.ResultCall()
	if err != nil {
		return nil, err
	}
	return dagql.NewObjectResultForCall(subset, srv, frame)
}

func TestModuleFunctionReceiverFiltersStoredCollections(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "receiver-client", SessionID: "receiver-session"})
	original, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	original.TypeDef = newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-collection-type", original.TypeDef).Self()
	source := newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
	mod := newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-module", &Module{NameField: "test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(nil, nil)})
	original.Module = mod
	class := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{Module: mod, TypeDef: original.TypeDef}})
	stateField, err := original.privateCollectionStateField(srv)
	require.NoError(t, err)
	// This fixture installs the resolver directly, without a module runtime.
	stateField.Spec.Module, stateField.Spec.ModuleProvider = nil, nil
	class.Install(stateField)
	srv.InstallObject(class)
	typ := &ModuleObjectType{mod: mod, typeDef: original.TypeDef}
	ctx = dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("receiver-base", original))
	base, err := typ.ConvertFromSDKResult(ctx, maps.Clone(original.Fields))
	require.NoError(t, err)
	base, err = cache.AttachResult(ctx, "receiver-session", srv, base)
	require.NoError(t, err)
	baseID, err := base.ID()
	require.NoError(t, err)
	baseEncoded, err := baseID.Encode()
	require.NoError(t, err)

	holderType := newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-holder-type", NewObjectTypeDef("Holder", "", nil)).Self()
	holder := &ModuleObject{Module: mod, TypeDef: holderType, Fields: map[string]any{
		"private": map[string]any{"Keys": []any{"a", "b", "c"}, collectionBaseField: base},
	}}
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: holder}))
	receiver, err := dagql.NewObjectResultForCall(holder, srv, moduleObjectTestSyntheticCall("receiver-holder", holder))
	require.NoError(t, err)
	attached, err := cache.AttachResult(ctx, "receiver-session", srv, receiver)
	require.NoError(t, err)
	ctx = dagql.WithResultScope(ctx, receiverCollectionScope{})
	fields, err := moduleFunctionReceiverFields(ctx, &CallOpts{ParentTyped: attached, ParentFields: holder.Fields})
	require.NoError(t, err)
	private := fields["private"].(map[string]any)
	require.Equal(t, []any{"b"}, private["Keys"])
	require.Equal(t, baseEncoded, private[collectionBaseField], "the original base must not be filtered")
	require.Equal(t, []any{"a", "b", "c"}, holder.Fields["private"].(map[string]any)["Keys"])
	require.Equal(t, []any{"a", "b", "c"}, original.Fields["Keys"])
}
