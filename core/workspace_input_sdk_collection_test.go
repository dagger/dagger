package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInputSDKHiddenCollectionFields(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "hidden-sdk", SessionID: "hidden-sdk"})
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	srv.ObserveCalls(ObserveWorkspaceInputs)

	inner, _ := collectionTestObject(t, TypeDefKindString, []any{"keep", "drop"})
	inner.TypeDef.Name = "SDKHiddenCases"
	innerDef := newTypeDefDetachedResult(t, srv, "hidden-inner-def", inner.TypeDef)
	innerType := newTypeDefDetachedResult(t, srv, "hidden-inner-type", (&TypeDef{}).WithObjectTypeDef(innerDef))
	listDef := newTypeDefDetachedResult(t, srv, "hidden-entry-list", &ListTypeDef{ElementTypeDef: innerType})
	listType := newTypeDefDetachedResult(t, srv, "hidden-entry-type", (&TypeDef{}).WithListOf(listDef))
	outer, _ := collectionTestObject(t, TypeDefKindString, []any{"outer"})
	outer.TypeDef.Name = "SDKHiddenCollections"
	outer.TypeDef.Fields = append(outer.TypeDef.Fields, newTypeDefDetachedResult(t, srv, "hidden-entries", NewFieldTypeDef("entries", listType, "", nil)))
	outer.Fields["entries"] = []any{map[string]any{"Keys": []any{"keep", "drop"}}}
	outerDef := newTypeDefDetachedResult(t, srv, "hidden-outer-def", outer.TypeDef)
	outerType := newTypeDefDetachedResult(t, srv, "hidden-outer-type", (&TypeDef{}).WithObjectTypeDef(outerDef))
	source := newTypeDefDetachedResult(t, srv, "hidden-module-source", &ModuleSource{Kind: ModuleSourceKindDir})
	module := &Module{NameField: "hidden", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(root, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{innerType, outerType, outer.TypeDef.Functions[0].Self().ReturnType}}
	mod := newTypeDefAttachedResult(t, ctx, cache, srv, "hidden-module", module)
	for _, obj := range []*ModuleObject{inner, outer} {
		obj.Module = mod
		require.NoError(t, obj.Install(ctx, srv, InstallOpts{SkipConstructor: true}))
	}
	class, ok := srv.ObjectType(outer.TypeDef.Name)
	require.True(t, ok)
	_, exposed := class.FieldSpec("entries", srv.View)
	require.False(t, exposed, "real collection classes must keep stored data fields out of the public schema")

	const boundary = "hidden-sdk-workspace"
	attach := func(name, path string) dagql.AnyResult {
		t.Helper()
		value := &ModuleObject{Module: mod, TypeDef: outer.TypeDef, Fields: outer.Fields}
		frame := moduleObjectTestSyntheticCall(name, value)
		detached, err := dagql.NewObjectResultForCall(value, srv, frame)
		require.NoError(t, err)
		fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: true, CapabilitiesComplete: true,
			Prepared: workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: boundary, KnownBoundary: true,
				Selectors: []provenance.Selector{{Path: path, Kind: provenance.WorkspaceRead, Origin: name}}}}}}
		result, err := cache.GetOrInitCall(ctx, "hidden-sdk", srv, &dagql.CallRequest{ResultCall: frame, ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true},
			func(context.Context) (dagql.AnyResult, error) { return detached, nil })
		require.NoError(t, err)
		result, err = result.WithContentDigestAny(ctx, digest.FromString("equal hidden collection contents"))
		require.NoError(t, err)
		return result
	}
	first, second := attach("hidden-parent-a", "/a"), attach("hidden-parent-b", "/b")
	view := &ArtifactView{state: artifactViewState{Rules: []artifactViewRule{{Dimension: "/nested", Type: inner.TypeDef.Name, Keys: []string{"keep"}, UniqueType: true}}}}
	require.NoError(t, view.setKey())
	type viewObservation struct {
		holderPresent bool
		registered    bool
		receiver      uint64
	}
	var viewMu sync.Mutex
	var itemViews []viewObservation
	srv.ObserveCalls(func(callCtx context.Context, req *dagql.CallRequest) (context.Context, func(), func(dagql.AnyResult, error)) {
		if req.ReceiverTypeName == inner.TypeDef.Name && req.Field == artifactViewField {
			reads := currentWorkspaceInputReads(callCtx)
			observed := viewObservation{holderPresent: reads != nil}
			if req.Receiver != nil {
				observed.receiver = req.Receiver.ResultID
			}
			if reads != nil {
				reads.mu.Lock()
				observed.registered = reads.hasInputLocked(observed.receiver)
				reads.mu.Unlock()
			}
			viewMu.Lock()
			itemViews = append(itemViews, observed)
			viewMu.Unlock()
		}
		return ObserveWorkspaceInputs(callCtx, req)
	})
	var firstJSON []byte
	for _, phase := range []struct {
		name   string
		parent dagql.AnyResult
		path   string
	}{{"cold", first, "/a"}, {"memo", first, "/a"}, {"current parent", second, "/b"}} {
		t.Run(phase.name, func(t *testing.T) {
			reads := &workspaceInputReads{boundary: boundary}
			id, err := phase.parent.ID()
			require.NoError(t, err)
			reads.rememberInputs(map[uint64]*dagql.ResultCallRef{id.EngineResultID(): {ResultID: id.EngineResultID()}})
			callCtx := context.WithValue(dagql.WithResultScope(ctx, view), workspaceInputReadsKey{}, reads)
			viewMu.Lock()
			firstView := len(itemViews)
			viewMu.Unlock()
			got, err := moduleFunctionReceiverJSON(callCtx, &CallOpts{ParentTyped: phase.parent})
			viewMu.Lock()
			observedViews := append([]viewObservation(nil), itemViews[firstView:]...)
			viewMu.Unlock()
			for _, observed := range observedViews {
				require.True(t, observed.holderPresent, "the item view must have an input holder")
				require.True(t, observed.registered, "hidden stored list item %d must be registered before its view", observed.receiver)
			}
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(got, &fields))
			entries, ok := fields["entries"].([]any)
			require.True(t, ok)
			require.Len(t, entries, 1)
			entry, ok := entries[0].(map[string]any)
			require.True(t, ok, "inline collection encoding must remain a map")
			require.Equal(t, []any{"keep"}, entry["Keys"], "nested collection view must retain its selected keys")
			if phase.name == "cold" {
				firstJSON = got
			} else if phase.name == "memo" {
				require.Equal(t, string(firstJSON), string(got))
			}
			selectors, issues := reads.snapshot().forWorkspace(boundary)
			require.Empty(t, issues)
			for _, path := range []string{"/a", "/b"} {
				affected, err := provenance.AffectedByFile(selectors, path+"/input.go", nil)
				require.NoError(t, err)
				require.Equal(t, path == phase.path, affected, "the current parent owns the stored field sources")
			}
			var list dagql.AnyResult
			reads.mu.Lock()
			ids := reads.inputIDsLocked()
			reads.mu.Unlock()
			for _, resultID := range ids {
				frame, err := cache.ResultCallForRef(&dagql.ResultCallRef{ResultID: resultID})
				require.NoError(t, err)
				if frame != nil && frame.Field == "entries" && frame.Nth == 0 && frame.Type != nil && frame.Type.Elem != nil {
					list, err = cache.LoadResultByResultID(callCtx, "hidden-sdk", srv, resultID)
					require.NoError(t, err)
					break
				}
			}
			require.NotNil(t, list, "the completed hidden list projection must be a current input")
			if phase.name == "cold" {
				frame, err := list.ResultCall()
				require.NoError(t, err)
				collector, err := newWorkspaceInputCollector(callCtx)
				require.NoError(t, err)
				for _, change := range []func(*dagql.ResultCall){
					func(f *dagql.ResultCall) { f.SyntheticOp = "anotherField" },
					func(f *dagql.ResultCall) { f.Type = dagql.NewResultCallType(dagql.String("").Type()) },
					func(f *dagql.ResultCall) { f.Args[0].Value.StringValue = "invalid" },
					func(f *dagql.ResultCall) { f.Module.ResultRef = &dagql.ResultCallRef{ResultID: id.EngineResultID()} },
				} {
					invalid := frame.Clone()
					change(invalid)
					require.False(t, collector.verifiedStoredField(&dagql.CallRequest{ResultCall: invalid, ReceiverTypeName: outer.TypeDef.Name}), "a malformed synthetic projection must not authorize prepared capture")
				}
			}
			item, err := list.NthValue(dagql.ContextWithServer(dagql.WithResultScope(callCtx, nil), srv), 1)
			require.NoError(t, err)
			reads.checkValueOrigin(callCtx, item)
			_, issues = reads.snapshot().forWorkspace(boundary)
			require.Empty(t, issues, "list item origin must remain bound after conversion")
		})
	}
	viewMu.Lock()
	viewCount := len(itemViews)
	viewMu.Unlock()
	require.Positive(t, viewCount)
	_, exposed = class.FieldSpec("entries", srv.View)
	require.False(t, exposed, "internal projection must not publish a schema field")
}
