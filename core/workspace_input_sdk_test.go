package core

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInputSDKStoredFields(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{
		ClientID: "sdk-source-client", SessionID: "sdk-source-session",
	})
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Directory]{}))

	var selectedIDs []uint64
	selectedFields := map[string]dagql.AnyResult{}
	resolverRuns := map[string]int{}
	phase := "prepare"
	var childProbeHit, childProbeEntered, childOriginalUnchanged, childSameProducer bool
	var childOriginalID, childSelectedID uint64
	observe := func(callCtx context.Context, req *dagql.CallRequest) (context.Context, func(), func(dagql.AnyResult, error)) {
		field := req.ReceiverTypeName == "SDKSourceParent" && req.Field != artifactViewField
		var prior dagql.AnyResult
		var priorFacts []byte
		var priorCoverage bool
		if field && req.Field == "child" {
			childOriginalID, childSelectedID = 0, 0
			childOriginalUnchanged, childSameProducer = false, false
			childProbeEntered = false
			probe, err := cache.GetOrInitCall(callCtx, "sdk-source-session", srv,
				&dagql.CallRequest{ResultCall: req.ResultCall.Clone()},
				func(context.Context) (dagql.AnyResult, error) {
					childProbeEntered = true
					return nil, errors.New("diagnostic cache miss")
				})
			childProbeHit = err == nil && probe != nil && probe.HitCache()
			if childProbeHit {
				prior = probe
				id, err := prior.ID()
				require.NoError(t, err)
				childOriginalID = id.EngineResultID()
				facts, coverage, err := cache.ResultFacts(callCtx, "sdk-source-session", &dagql.ResultCallRef{ResultID: childOriginalID}, workspaceInputsNamespace)
				require.NoError(t, err)
				priorFacts, _ = json.Marshal(facts)
				priorCoverage = coverage
			}
		}
		observed, finish, complete := ObserveWorkspaceInputs(callCtx, req)
		return observed, finish, func(result dagql.AnyResult, callErr error) {
			if complete != nil {
				complete(result, callErr)
			}
			if callErr != nil || result == nil || result.Unwrap() == nil || !field {
				return
			}
			id, err := result.ID()
			require.NoError(t, err)
			selectedIDs = append(selectedIDs, id.EngineResultID())
			selectedFields[req.Field] = result
			if req.Field == "child" && prior != nil {
				childSelectedID = id.EngineResultID()
				originalFrame, _ := prior.ResultCall()
				selectedFrame, _ := result.ResultCall()
				originalRecipe, _ := originalFrame.RecipeDigest(callCtx)
				selectedRecipe, _ := selectedFrame.RecipeDigest(callCtx)
				childSameProducer = originalRecipe == selectedRecipe && originalFrame.Receiver.ResultID == selectedFrame.Receiver.ResultID && prior.Unwrap() == result.Unwrap()
				facts, coverage, err := cache.ResultFacts(callCtx, "sdk-source-session", &dagql.ResultCallRef{ResultID: childOriginalID}, workspaceInputsNamespace)
				require.NoError(t, err)
				encoded, _ := json.Marshal(facts)
				childOriginalUnchanged = coverage == priorCoverage && string(encoded) == string(priorFacts)
			}
		}
	}
	// Metadata constructors run under the normal observer, outside a measured
	// function holder. Their complete-empty facts come from actual execution.
	srv.ObserveCalls(ObserveWorkspaceInputs)

	childDef := NewObjectTypeDef("SDKSourceChild", "", nil)
	childDefResult := newTypeDefDetachedResult(t, srv, "sdk-source-child-def", childDef)
	childType := newTypeDefDetachedResult(t, srv, "sdk-source-child-type", (&TypeDef{}).WithObjectTypeDef(childDefResult))
	listDef := newTypeDefDetachedResult(t, srv, "sdk-source-list-def", &ListTypeDef{ElementTypeDef: childType})
	listType := newTypeDefDetachedResult(t, srv, "sdk-source-list-type", (&TypeDef{}).WithListOf(listDef))
	optionalChild := newTypeDefDetachedResult(t, srv, "sdk-source-optional-child", childType.Self().WithOptional(true))
	optionalList := newTypeDefDetachedResult(t, srv, "sdk-source-optional-list", listType.Self().WithOptional(true))
	parentDef := NewObjectTypeDef("SDKSourceParent", "", nil)
	for _, field := range []struct {
		name string
		typ  dagql.ObjectResult[*TypeDef]
	}{{"child", childType}, {"children", listType}, {"reference", childType}, {"emptyChild", optionalChild}, {"emptyChildren", optionalList}} {
		parentDef.Fields = append(parentDef.Fields, newTypeDefDetachedResult(t, srv, "sdk-source-field-"+field.name, NewFieldTypeDef(field.name, field.typ, "", nil)))
	}
	parentDefResult := newTypeDefDetachedResult(t, srv, "sdk-source-parent-def", parentDef)
	parentType := newTypeDefDetachedResult(t, srv, "sdk-source-parent-type", (&TypeDef{}).WithObjectTypeDef(parentDefResult))
	source := newTypeDefDetachedResult(t, srv, "sdk-source-module-source", &ModuleSource{Kind: ModuleSourceKindDir})
	mod := &Module{NameField: "sdk-source-test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(root, nil),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{childType, parentType}}
	modResult, err := dagql.NewObjectResultForCall(mod, srv, moduleObjectTestSyntheticCall("sdk-source-module", mod))
	require.NoError(t, err)
	child := &ModuleObject{Module: modResult, TypeDef: childDef, Fields: map[string]any{"marker": "reference"}}
	childClass := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: child})
	childView, err := child.artifactViewField(srv)
	require.NoError(t, err)
	childView.Spec.Module, childView.Spec.ModuleProvider = nil, nil
	childClass.Install(childView)
	srv.InstallObject(childClass)
	childResult, err := dagql.NewObjectResultForCall(child, srv, moduleObjectTestSyntheticCall("sdk-source-reference", child))
	require.NoError(t, err)
	reference, err := cache.AttachResult(ctx, "sdk-source-session", srv, childResult)
	require.NoError(t, err)
	parent := &ModuleObject{Module: modResult, TypeDef: parentDef, Fields: map[string]any{
		"child":         map[string]any{"marker": "inline"},
		"children":      []any{map[string]any{"marker": "first"}, reference},
		"reference":     reference,
		"emptyChild":    nil,
		"emptyChildren": nil,
	}}
	class := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: parent})
	parentView, err := parent.artifactViewField(srv)
	require.NoError(t, err)
	parentView.Spec.Module, parentView.Spec.ModuleProvider = nil, nil
	class.Install(parentView)
	for _, field := range parentDef.Fields {
		resolver, err := objField(modResult, field.Self())
		require.NoError(t, err)
		// This fixture exercises stored-field resolution without a module runtime.
		resolver.Spec.Module, resolver.Spec.ModuleProvider = nil, nil
		name, original := field.Self().Name, resolver.Func
		resolver.Func = func(callCtx context.Context, self dagql.ObjectResult[*ModuleObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
			resolverRuns[name]++
			return original(callCtx, self, args, view)
		}
		class.Install(resolver)
	}
	srv.InstallObject(class)
	for _, field := range parentDef.Fields {
		typ, ok, err := NewUserMod(modResult).ModTypeFor(ctx, field.Self().TypeDef.Self(), true)
		require.NoError(t, err)
		require.True(t, ok)
		_, err = typ.TypeDef(ctx)
		require.NoError(t, err)
	}

	const boundary = "sdk-source-boundary"
	attachSource := func(name string, value dagql.AnyResult, sourcePath string, incomplete bool) dagql.AnyResult {
		t.Helper()
		frame := moduleObjectTestSyntheticCall(name, value)
		fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: !incomplete, CapabilitiesComplete: true,
			Prepared: workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: boundary, KnownBoundary: true,
				Selectors: []provenance.Selector{{Path: sourcePath, Kind: provenance.WorkspaceRead, Origin: name}}}}}}
		if incomplete {
			fact.Prepared.incomplete = true
			fact.Prepared.Issues = []string{"fixture parent source is incomplete"}
		}
		result, err := cache.GetOrInitCall(ctx, "sdk-source-session", srv, &dagql.CallRequest{
			ResultCall: frame, ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true, ResultFactsIncomplete: incomplete,
		}, func(context.Context) (dagql.AnyResult, error) { return value, nil })
		require.NoError(t, err)
		return result
	}
	parentResult, err := dagql.NewObjectResultForCall(parent, srv, moduleObjectTestSyntheticCall("sdk-source-parent-value", parent))
	require.NoError(t, err)
	attached := attachSource("sdk-source-parent", parentResult, "/sdk", false)
	parentID, err := attached.ID()
	require.NoError(t, err)
	parentRef := &dagql.ResultCallRef{ResultID: parentID.EngineResultID()}
	srv.ObserveCalls(observe)

	var firstJSON []byte
	view := &ArtifactView{}
	require.NoError(t, view.setKey())
	for _, currentPhase := range []string{"cold", "memo"} {
		phase = currentPhase
		t.Run(currentPhase, func(t *testing.T) {
			reads := &workspaceInputReads{boundary: boundary}
			reads.rememberInputs(map[uint64]*dagql.ResultCallRef{parentRef.ResultID: parentRef})
			callCtx := context.WithValue(dagql.WithResultScope(ctx, view), workspaceInputReadsKey{}, reads)
			before := len(selectedIDs)
			got, err := moduleFunctionReceiverJSON(callCtx, &CallOpts{ParentTyped: attached, ParentFields: parent.Fields})
			require.NoError(t, err)
			require.NotZero(t, childOriginalID)
			require.NotZero(t, childSelectedID)
			require.True(t, childSameProducer, "selection preserves the exact receiver recipe and borrowed payload")
			require.True(t, childProbeHit, "the exact child recipe must already have a cached row")
			require.False(t, childProbeEntered, "the cache-only probe must not initialize the child")
			require.Zero(t, resolverRuns["child"], "the real stored-field resolver must remain unexecuted on this hit")
			require.True(t, childOriginalUnchanged, "the borrowed child's original facts must not be overwritten")
			var fields map[string]any
			require.NoError(t, json.Unmarshal(got, &fields))
			require.Equal(t, map[string]any{"marker": "inline"}, fields["child"], "an inline object remains a map")
			for _, name := range []string{"emptyChild", "emptyChildren"} {
				value, present := fields[name]
				require.True(t, present, "nullable field %s remains present", name)
				require.Nil(t, value, "nullable field %s remains null", name)
			}
			items, ok := fields["children"].([]any)
			require.True(t, ok)
			require.Len(t, items, 2)
			require.Equal(t, map[string]any{"marker": "first"}, items[0], "an inline list item remains a map")
			for _, encoded := range []any{fields["reference"], items[1]} {
				referenceHandle, ok := encoded.(string)
				require.True(t, ok, "an existing result remains an ID")
				require.NotEmpty(t, referenceHandle)
				var handle call.ID
				require.NoError(t, handle.Decode(referenceHandle))
				loaded, err := srv.Load(dagql.WithResultScope(callCtx, nil), &handle)
				require.NoError(t, err)
				reads.checkValueOrigin(callCtx, loaded)
				loadedChild, ok := dagql.UnwrapAs[*ModuleObject](loaded)
				require.True(t, ok)
				require.Equal(t, "reference", loadedChild.Fields["marker"])
				require.NotNil(t, loadedChild.ArtifactView, "the encoded handle retains the active view")
				require.Equal(t, view.key, loadedChild.ArtifactView.key)
			}
			if phase == "cold" {
				firstJSON = got
				require.GreaterOrEqual(t, len(selectedIDs), 2, "inline fields must complete ordinary stored-field selections")
			} else {
				require.Equal(t, string(firstJSON), string(got))
				require.Equal(t, before, len(selectedIDs), "memo reuse must not reconvert fields")
			}
			require.NotEmpty(t, selectedIDs, "memo must replay completed SDK field results")
			for _, id := range selectedIDs {
				covered := reads.hasInputLocked(id)
				require.True(t, covered, "selected field result %d must be covered in each invocation", id)
			}
			require.NotNil(t, selectedFields["children"], "the declared list field must complete a selection")
			item, err := selectedFields["children"].NthValue(dagql.ContextWithServer(dagql.WithResultScope(callCtx, nil), srv), 1)
			require.NoError(t, err)
			itemFrame, err := item.ResultCall()
			require.NoError(t, err)
			require.Equal(t, int64(1), itemFrame.Nth)
			itemID, err := item.ID()
			require.NoError(t, err)
			itemSources, err := workspaceInputsForCall(callCtx, &dagql.ResultCall{Receiver: &dagql.ResultCallRef{ResultID: itemID.EngineResultID()}})
			require.NoError(t, err)
			itemSelectors, itemIssues := itemSources.forWorkspace(boundary)
			require.Empty(t, itemIssues, "Nth projection must use the selected list's current source fact")
			itemAffected, err := provenance.AffectedByFile(itemSelectors, "/sdk/input.go", nil)
			require.NoError(t, err)
			require.True(t, itemAffected, "Nth projection must retain a nonempty source binding")
			selectors, issues := reads.snapshot().forWorkspace(boundary)
			require.Empty(t, issues, "SDK field inputs must not add an unbound origin")
			affected, err := provenance.AffectedByFile(selectors, "/sdk/input.go", nil)
			require.NoError(t, err)
			require.True(t, affected, "each invocation must receive the selected SDK field's source facts")
			recordWorkspaceRuntimeInputs(callCtx, nil, &FunctionCall{}, &moduleOwnershipRuntime{}, &workspaceInputCall{reads: reads})
			setup, ready := reads.runtimeSnapshot()
			require.True(t, ready)
			selectors, issues = setup.forWorkspace(boundary)
			require.Empty(t, issues)
			affected, err = provenance.AffectedByFile(selectors, "/sdk/input.go", nil)
			require.NoError(t, err)
			require.True(t, affected, "SDK field sources must survive the runtime setup boundary")
		})
	}
	newParent := func(name, sourcePath string, incomplete bool) dagql.AnyResult {
		t.Helper()
		value := &ModuleObject{Module: modResult, TypeDef: parentDef, Fields: maps.Clone(parent.Fields)}
		detached, err := dagql.NewObjectResultForCall(value, srv, moduleObjectTestSyntheticCall(name+"-value", value))
		require.NoError(t, err)
		published := attachSource(name, detached, sourcePath, incomplete)
		published, err = published.WithContentDigestAny(ctx, digest.FromString("equal SDK parent contents"))
		require.NoError(t, err)
		return published
	}
	selectChild := func(value dagql.AnyResult) dagql.AnyResult {
		t.Helper()
		owner, err := srv.ToSelectable(ctx, value)
		require.NoError(t, err)
		result, err := owner.Select(ctx, srv, dagql.Selector{Field: "child"})
		require.NoError(t, err)
		return result
	}
	inspect := func(value dagql.AnyResult) ([]provenance.Selector, []string) {
		t.Helper()
		id, err := value.ID()
		require.NoError(t, err)
		summary, err := workspaceInputsForCall(ctx, &dagql.ResultCall{Receiver: &dagql.ResultCallRef{ResultID: id.EngineResultID()}})
		require.NoError(t, err)
		return summary.forWorkspace(boundary)
	}
	t.Run("equal contents keep current parent origins", func(t *testing.T) {
		firstParent := newParent("sdk-parent-a", "/a", false)
		secondParent := newParent("sdk-parent-b", "/b", false)
		firstObject, ok := firstParent.(dagql.ObjectResult[*ModuleObject])
		require.True(t, ok)
		secondObject, ok := secondParent.(dagql.ObjectResult[*ModuleObject])
		require.True(t, ok)
		firstDigest, err := firstObject.ContentPreferredDigest(ctx)
		require.NoError(t, err)
		secondDigest, err := secondObject.ContentPreferredDigest(ctx)
		require.NoError(t, err)
		require.Equal(t, firstDigest, secondDigest)
		first := selectChild(firstParent)
		before, beforeIssues := inspect(first)
		require.Empty(t, beforeIssues)
		for _, check := range []struct {
			path string
			want bool
		}{{"/a/input.go", true}, {"/b/input.go", false}} {
			affected, err := provenance.AffectedByFile(before, check.path, nil)
			require.NoError(t, err)
			require.Equal(t, check.want, affected, check.path)
		}
		require.NotZero(t, childOriginalID)
		require.True(t, childOriginalUnchanged)
		second := selectChild(secondParent)
		current, issues := inspect(second)
		require.Empty(t, issues)
		for _, check := range []struct {
			path string
			want bool
		}{{"/a/input.go", false}, {"/b/input.go", true}} {
			affected, err := provenance.AffectedByFile(current, check.path, nil)
			require.NoError(t, err)
			require.Equal(t, check.want, affected, check.path)
		}
		after, afterIssues := inspect(first)
		require.Empty(t, afterIssues)
		require.Equal(t, before, after, "reading another equal parent must not rewrite the first output's origin")
		require.NotZero(t, childOriginalID)
		require.True(t, childOriginalUnchanged)
	})
	t.Run("current incomplete parent stays incomplete", func(t *testing.T) {
		selected := selectChild(newParent("sdk-parent-incomplete", "/incomplete", true))
		_, issues := inspect(selected)
		require.Contains(t, issues, "fixture parent source is incomplete")
		id, err := selected.ID()
		require.NoError(t, err)
		_, complete, err := cache.ResultFacts(ctx, "sdk-source-session", &dagql.ResultCallRef{ResultID: id.EngineResultID()}, workspaceInputsNamespace)
		require.NoError(t, err)
		require.False(t, complete, "verified storage must not invent coverage for an incomplete current parent")
		require.NotZero(t, childOriginalID)
		require.True(t, childOriginalUnchanged)
	})

	t.Run("unrelated source stays unbound", func(t *testing.T) {
		directory, err := dagql.NewObjectResultForCall(&Directory{}, srv, moduleObjectTestSyntheticCall("sdk-source-external-value", &Directory{}))
		require.NoError(t, err)
		external := attachSource("sdk-source-external", directory, "/external", false)
		reads := &workspaceInputReads{boundary: boundary}
		reads.rememberInputs(map[uint64]*dagql.ResultCallRef{parentRef.ResultID: parentRef})
		reads.checkValueOrigin(ctx, external)
		require.NotEmpty(t, reads.snapshot().Issues)
		require.Contains(t, reads.snapshot().Issues[0], "body input has a source origin not bound to prepared inputs or recorded source operations:")
	})
}
