package core

import (
	"context"
	"maps"
	"testing"

	"github.com/dagger/dagger/dagql"
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
	decoded, err := obj.DecodePersistedObject(context.Background(), dag, 0, nil, encoded.JSON)
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
