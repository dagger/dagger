package core

import (
	"maps"
	"slices"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestArtifactViewKeepsDistinctCollectionStates(t *testing.T) {
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
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "subset-view-client", SessionID: "subset-view-session"})
	obj, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	obj.CollectionBase = nil
	obj.TypeDef = newTypeDefAttachedResult(t, ctx, cache, srv, "subset-view-type", obj.TypeDef).Self()
	source := newTypeDefAttachedResult(t, ctx, cache, srv, "subset-view-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
	obj.Module = newTypeDefAttachedResult(t, ctx, cache, srv, "subset-view-module", &Module{NameField: "test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(root, nil)})
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: obj}))
	base := newTypeDefAttachedResult(t, ctx, cache, srv, "subset-view-base", obj)
	makeValue := func(name string, keys []string, state string) dagql.ObjectResult[*ModuleObject] {
		t.Helper()
		copy := *base.Self()
		copy.Fields = maps.Clone(copy.Fields)
		values := make([]any, len(keys))
		for i, key := range keys {
			values[i] = key
		}
		copy.Fields["Keys"] = values
		copy.Fields["privateState"] = state
		return newTypeDefAttachedResult(t, ctx, cache, srv, name, &copy)
	}
	for _, tc := range []struct {
		name                  string
		unit, integration     []string
		unitState, otherState string
		selected              []string
		wantUnit, wantOther   []string
	}{
		{
			name: "overlapping subsets", unit: []string{"a", "b"}, integration: []string{"b", "c"},
			selected: []string{"unit.b", "integration.c"}, wantUnit: []string{"b"}, wantOther: []string{"c"},
		},
		{
			name: "empty selection", unit: []string{"a", "b"}, integration: []string{"b", "c"},
			selected: []string{"integration.c"}, wantOther: []string{"c"},
		},
		{
			name: "aliases of one subset", unit: []string{"a", "b"}, integration: []string{"a", "b"},
			selected: []string{"unit.a", "integration.b"}, wantUnit: []string{"a", "b"}, wantOther: []string{"a", "b"},
		},
		{
			name: "aliases of the base", unit: []string{"a", "b", "c"}, integration: []string{"a", "b", "c"},
			unitState: "retained", otherState: "retained", selected: []string{"unit.b", "integration.c"},
			wantUnit: []string{"b", "c"}, wantOther: []string{"b", "c"},
		},
		{
			name: "base and subset", unit: []string{"a", "b", "c"}, integration: []string{"b", "c"},
			unitState: "retained", otherState: "retained", selected: []string{"unit.b", "integration.c"},
			wantUnit: []string{"b"}, wantOther: []string{"c"},
		},
		{
			name: "different state with identical keys", unit: []string{"a", "b", "c"}, integration: []string{"a", "b", "c"},
			unitState: "unit", otherState: "integration", selected: []string{"unit.b", "integration.c"},
			wantUnit: []string{"b"}, wantOther: []string{"c"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				values := []struct {
					name     string
					value    dagql.ObjectResult[*ModuleObject]
					receiver *Artifact
					want     []string
				}{
					{name: "unit", value: makeValue(tc.name+"-unit", tc.unit, tc.unitState), want: tc.wantUnit},
					{name: "integration", value: makeValue(tc.name+"-integration", tc.integration, tc.otherState), want: tc.wantOther},
				}
				if reverse {
					slices.Reverse(values)
				}
				discovery := &artifactViewDiscovery{
					rules:     []artifactViewRule{{Dimension: "Items", Type: "Items", Keys: tc.selected, UniqueType: true}},
					originals: map[string]artifactCollectionSnapshot{},
				}
				for i := range values {
					value := &values[i]
					value.receiver = &Artifact{ModuleName: "app", Node: &ModTreeNode{Name: value.name, Parent: &ModTreeNode{Name: "app"}}}
					node := &ModTreeNode{CollectionDimension: &ArtifactDimension{Identifier: "Items", CollectionType: "Items"}, CollectionRoute: []string{value.name}}
					keys, err := value.value.Self().collectionKeys(ctx)
					require.NoError(t, err)
					require.NoError(t, discovery.record(ctx, value.receiver, value.value, node, keys))
				}
				view, err := discovery.buildView(ctx, nil)
				require.NoError(t, err)
				// The base must retain enough keys to construct both values.
				baseKeys, bound, err := view.collectionSelection(ctx, base)
				require.NoError(t, err)
				require.True(t, bound)
				wantBase := append(slices.Clone(tc.wantUnit), tc.wantOther...)
				slices.Sort(wantBase)
				require.Equal(t, slices.Compact(wantBase), baseKeys)
				for _, value := range values {
					// A scoped call keeps its producer binding despite a new result ID.
					for _, replay := range []bool{false, true} {
						result := value.value
						if replay {
							copy := *result.Self()
							producer, err := result.ResultCall()
							require.NoError(t, err)
							frame := &dagql.ResultCall{
								Kind: dagql.ResultCallKindField, Field: artifactViewField,
								Type:     dagql.NewResultCallType(copy.Type()),
								Receiver: &dagql.ResultCallRef{Call: producer},
							}
							detached, err := dagql.NewObjectResultForCall(&copy, srv, frame)
							require.NoError(t, err)
							attached, err := cache.GetOrInitCall(ctx, "typedef-test-session", srv, &dagql.CallRequest{ResultCall: frame}, dagql.ValueFunc(detached))
							require.NoError(t, err)
							result = attached.(dagql.ObjectResult[*ModuleObject])
						}
						callCtx := dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("scope-"+value.name, result.Self()))
						filtered, err := applyModuleObjectView(callCtx, result, view)
						require.NoError(t, err)
						collection, ok := dagql.UnwrapAs[*ModuleObject](filtered)
						require.True(t, ok)
						keys, err := collection.collectionKeys(ctx)
						require.NoError(t, err)
						var got []string
						for _, key := range keys {
							got = append(got, key.text)
						}
						require.Equal(t, value.want, got, "route=%s replay=%v reverse=%v", value.name, replay, reverse)
						require.NoError(t, discovery.validate(value.receiver, keys))
					}
				}
			}
		})
	}
}
