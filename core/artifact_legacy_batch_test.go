package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func legacyBatchFixture(t *testing.T, version string) (context.Context, *Artifacts) {
	t.Helper()
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
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "legacy-batch", SessionID: "legacy-batch"})
	mod := newTypeDefAttachedResult(t, ctx, cache, srv, "batch-module-"+version, &Module{
		NameField: "go", OriginalName: "go", SourceOverrides: &SourceOverrides{EngineVersion: version}, Deps: NewSchemaBuilder(nil, nil),
	})
	checkObj := newTypeDefDetachedResult(t, srv, "batch-check-obj", NewObjectTypeDef("Check", "", nil))
	checkType := newTypeDefDetachedResult(t, srv, "batch-check-type", (&TypeDef{}).WithObjectTypeDef(checkObj))
	check := newTypeDefDetachedResult(t, srv, "batch-check-function", &Function{Name: "test", OriginalName: "test", ReturnType: checkType, IsCheck: true})
	itemObj := NewObjectTypeDef("TestItem", "", nil)
	itemObj.Functions = dagql.ObjectResultArray[*Function]{check}
	itemObjResult := newTypeDefDetachedResult(t, srv, "batch-item-obj", itemObj)
	itemType := newTypeDefDetachedResult(t, srv, "batch-item-type", (&TypeDef{}).WithObjectTypeDef(itemObjResult))
	get := newTypeDefDetachedResult(t, srv, "batch-get", &Function{Name: "get", ReturnType: itemType})
	collectionObj := NewObjectTypeDef("Tests", "", nil)
	collectionObj.Collection = &CollectionConfig{Enabled: true}
	collectionObj.Functions = dagql.ObjectResultArray[*Function]{get, check}
	collectionObjResult := newTypeDefDetachedResult(t, srv, "batch-collection-obj", collectionObj)
	collectionType := newTypeDefDetachedResult(t, srv, "batch-collection-type", (&TypeDef{}).WithObjectTypeDef(collectionObjResult))
	collection := &ModTreeNode{Name: "tests", Type: collectionType, Module: mod, OriginalModule: mod, DagqlServer: srv}
	collection.types = map[string]dagql.ObjectResult[*TypeDef]{"TestItem": itemType}
	dimension := &ArtifactDimension{Identifier: "/go/tests", Name: "go-test"}
	makeEntry := func(key *string) *Artifact {
		parent := collection
		keys := []*ArtifactDimensionKey{{Dimension: "module", Key: "go"}}
		if key != nil {
			parent = &ModTreeNode{Name: "get", Parent: collection, Type: itemType, Module: mod, OriginalModule: mod, DagqlServer: srv, CollectionDimension: dimension, CollectionKey: key}
			keys = append(keys, &ArtifactDimensionKey{Dimension: dimension.Identifier, Key: *key})
		}
		return &Artifact{ModuleName: "go", Path: []string{"tests", "test"}, TypeName: "Check", DimensionKeys: keys,
			Node: &ModTreeNode{Name: "test", Parent: parent, Type: checkType, Module: mod, OriginalModule: mod, DagqlServer: srv, Directives: []string{"check"}}}
	}
	a, b := "TestA", "TestB"
	return ctx, &Artifacts{Entries: []*Artifact{makeEntry(nil), makeEntry(&a), makeEntry(&b)}}
}

func TestLegacyCollectionCheckPlanning(t *testing.T) {
	previous := engine.Version
	engine.Version = engine.CollectionItemChecksVersion
	t.Cleanup(func() { engine.Version = previous })
	for _, tc := range []struct {
		version string
		count   int
	}{
		{"v1.0.0-beta.17", 1}, {"v1.0.0-beta.21", 1},
		{engine.CollectionItemChecksVersion, 3}, {"v1.0.0", 3},
	} {
		t.Run(tc.version, func(t *testing.T) {
			ctx, all := legacyBatchFixture(t, tc.version)
			for range 2 {
				planned, err := all.legacyBatchChecks(ctx)
				require.NoError(t, err)
				require.Len(t, planned, tc.count)
				if tc.count == 1 {
					require.Equal(t, []string{"TestA", "TestB"}, planned[0].Node.LegacyBatchKeys)
					require.Equal(t, "tests", planned[0].Node.Parent.Name)
				}
				require.Nil(t, all.Entries[1].Node.LegacyBatchKeys, "planning must not mutate discovery")
			}
		})
	}
}

func TestLegacyCollectionCheckSelectedKeys(t *testing.T) {
	ctx, all := legacyBatchFixture(t, "v1.0.0-beta.17")
	for _, tc := range []struct {
		name    string
		entries []*Artifact
		keys    []string
	}{
		{"one selected item", all.Entries[1:2], []string{"TestA"}},
		{"excluded item", all.Entries[:2], []string{"TestA"}},
		{"all items excluded", all.Entries[:1], nil},
		{"duplicate item", []*Artifact{all.Entries[1], all.Entries[1]}, []string{"TestA"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planned, err := (&Artifacts{Entries: tc.entries}).legacyBatchChecks(ctx)
			require.NoError(t, err)
			if tc.keys == nil {
				require.Empty(t, planned)
				return
			}
			require.Len(t, planned, 1)
			require.Equal(t, tc.keys, planned[0].Node.LegacyBatchKeys)
		})
	}
}

func TestLegacyCollectionCheckKeepsParentGroups(t *testing.T) {
	ctx, all := legacyBatchFixture(t, "v1.0.0-beta.17")
	other := all.Entries[1].Clone()
	other.DimensionKeys = append([]*ArtifactDimensionKey{}, other.DimensionKeys...)
	other.DimensionKeys = append(other.DimensionKeys, &ArtifactDimensionKey{Dimension: "/go/packages", Key: "other"})
	all.Entries = append(all.Entries, other)
	planned, err := all.legacyBatchChecks(ctx)
	require.NoError(t, err)
	require.Len(t, planned, 2)
	require.Equal(t, []string{"TestA", "TestB"}, planned[0].Node.LegacyBatchKeys)
	require.Equal(t, []string{"TestA"}, planned[1].Node.LegacyBatchKeys)
}

func TestLegacyCollectionCheckMixedVersions(t *testing.T) {
	ctx, old := legacyBatchFixture(t, "v1.0.0-beta.21")
	_, current := legacyBatchFixture(t, engine.CollectionItemChecksVersion)
	all := &Artifacts{Entries: append(old.Entries, current.Entries...)}
	planned, err := all.legacyBatchChecks(ctx)
	require.NoError(t, err)
	require.Len(t, planned, 4)
	require.Equal(t, []string{"TestA", "TestB"}, planned[0].Node.LegacyBatchKeys)
	for _, item := range planned[1:] {
		require.Nil(t, item.Node.LegacyBatchKeys)
	}
	// The module that defines the check controls compatibility, not a caller
	// that imports it or its client version.
	node := *old.Entries[1].Node
	node.Module = current.Entries[1].Node.Module
	require.True(t, legacyCollectionChecks(&node))
	node.OriginalModule = current.Entries[1].Node.Module
	require.False(t, legacyCollectionChecks(&node))
}

func TestLegacyCollectionCheckPersistedSelection(t *testing.T) {
	original := &ModTreeNode{Name: "test", LegacyBatchKeys: []string{"a", "b"}}
	encoder := newPersistedModTreeEncoder(nil)
	id, err := encoder.Add(original)
	require.NoError(t, err)
	data, err := json.Marshal(encoder.tree)
	require.NoError(t, err)
	var tree persistedModTree
	require.NoError(t, json.Unmarshal(data, &tree))
	restored, err := decodePersistedModTree(t.Context(), nil, tree)
	require.NoError(t, err)
	require.Equal(t, original.LegacyBatchKeys, restored[id].LegacyBatchKeys)
	restored[id].LegacyBatchKeys[0] = "different"
	require.Equal(t, []string{"a", "b"}, original.LegacyBatchKeys)
}

func TestLegacyCollectionCheckWithoutBatchHelper(t *testing.T) {
	ctx, all := legacyBatchFixture(t, "v1.0.0-beta.17")
	collection := all.Entries[1].Node.Parent.Parent
	obj := collection.ObjectType().Clone()
	obj.Functions = obj.Functions[:1] // Keep get; the collection has no check.
	objResult := newTypeDefDetachedResult(t, collection.DagqlServer, "no-batch-object", obj)
	collection.Type = newTypeDefDetachedResult(t, collection.DagqlServer, "no-batch-type", (&TypeDef{}).WithObjectTypeDef(objResult))
	all.Entries = all.Entries[1:]
	planned, err := all.legacyBatchChecks(ctx)
	require.NoError(t, err)
	require.Len(t, planned, 2, "Go#84-style item checks keep their shared helper behavior")
	for _, item := range planned {
		require.Nil(t, item.Node.LegacyBatchKeys)
	}
}
