package core

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestSDKInputPreparation(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "prepared", SessionID: "prepared"})
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	def := newTypeDefAttachedResult(t, ctx, cache, srv, "prepared-type", NewObjectTypeDef("PreparedValue", "", nil)).Self()
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: &ModuleObject{TypeDef: def}}))
	const boundary = "prepared-workspace"
	source := func(path string) workspaceInputSource {
		return workspaceInputSource{Workspace: boundary, KnownBoundary: true, Selectors: []provenance.Selector{{Path: path, Kind: provenance.WorkspaceRead, Origin: "fixture"}}}
	}
	attach := func(name, path string, change func(*workspaceInputFact)) dagql.AnyResult {
		t.Helper()
		value := &ModuleObject{TypeDef: def, Fields: map[string]any{"marker": name}}
		frame := moduleObjectTestSyntheticCall(name, value)
		detached, e := dagql.NewObjectResultForCall(value, srv, frame)
		require.NoError(t, e)
		fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: true, CapabilitiesComplete: true, Prepared: workspaceInputSummary{Sources: []workspaceInputSource{source(path)}}}
		if change != nil {
			change(&fact)
		}
		result, e := cache.GetOrInitCall(ctx, "prepared", srv, &dagql.CallRequest{ResultCall: frame, ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true}, func(context.Context) (dagql.AnyResult, error) { return detached, nil })
		require.NoError(t, e)
		return result
	}
	holder := func() (context.Context, *workspaceInputReads) {
		r := &workspaceInputReads{boundary: boundary}
		return context.WithValue(ctx, workspaceInputReadsKey{}, r), r
	}
	idOf := func(value dagql.AnyResult) uint64 {
		id, e := value.ID()
		require.NoError(t, e)
		return id.EngineResultID()
	}
	compare := func(r *workspaceInputReads, values []dagql.AnyResult) {
		t.Helper()
		normalCtx, normal := holder()
		importSDKInputs(normalCtx, values)
		require.Equal(t, normal.snapshot(), r.snapshot())
		require.Equal(t, normal.snapshot().incomplete, r.snapshot().incomplete)
	}
	a := attach("prepared-a", "/a", func(f *workspaceInputFact) {
		f.FindUps = []workspaceFindUpRule{{Name: ".env", Workspace: boundary, Cwd: "producer"}}
		f.ContextInputs = workspaceInputSummary{Sources: []workspaceInputSource{source("/context-a")}}
	})
	b := attach("prepared-b", "/b", nil)
	t.Run("detached scope result keeps incomplete coverage on reuse", func(t *testing.T) {
		value := &ModuleObject{TypeDef: def}
		result, err := dagql.NewObjectResultForCall(value, srv, moduleObjectTestSyntheticCall("detached-scope", value))
		require.NoError(t, err)
		inputs := &sdkInputResults{}
		capture := context.WithValue(ctx, sdkInputResultsKey{}, inputs)
		rememberSDKInput(capture, result)
		rememberSDKInput(capture, a)
		receiver := inputs.snapshot(nil)
		require.Len(t, receiver.Inputs, 1)
		var first *sdkInputPlan
		for range 2 {
			callCtx, reads := holder()
			receiver.preparation.importInputs(callCtx, receiver.Inputs)
			require.NotNil(t, receiver.preparation.plan)
			if first == nil {
				first = receiver.preparation.plan
			}
			require.Same(t, first, receiver.preparation.plan)
			require.True(t, reads.hasInputLocked(idOf(a)))
			summary := reads.snapshot()
			require.True(t, summary.incomplete)
			require.Contains(t, summary.Issues, "selected SDK input has no attached result ID")
			require.Equal(t, "producer", summary.FindUps[0].Cwd)
		}
	})
	p := &sdkInputPreparation{}
	firstCtx, first := holder()
	p.importInputs(firstCtx, []dagql.AnyResult{a})
	require.NotNil(t, p.plan)
	plan := p.plan
	secondCtx, second := holder()
	p.importInputs(secondCtx, []dagql.AnyResult{a})
	require.Same(t, plan, p.plan)
	require.Same(t, first.prepared[0], second.prepared[0])
	require.Empty(t, second.values, "prepared membership must not be copied into each holder")
	require.True(t, second.hasInputLocked(idOf(a)))
	compare(second, []dagql.AnyResult{a})
	require.Equal(t, "producer", second.snapshot().FindUps[0].Cwd, "fixed input rules keep the producer binding")
	first.rememberInputs(map[uint64]*dagql.ResultCallRef{idOf(b): {ResultID: idOf(b)}})
	require.True(t, first.hasInputLocked(idOf(b)))
	require.False(t, second.hasInputLocked(idOf(b)), "local additions cannot authorize another holder")
	other := &sdkInputPreparation{}
	other.importInputs(secondCtx, []dagql.AnyResult{b})
	require.Len(t, second.prepared, 2)
	require.True(t, second.hasInputLocked(idOf(a)))
	require.True(t, second.hasInputLocked(idOf(b)))
	compare(second, []dagql.AnyResult{a, b})
	// A contextual read remains on the normal path beside the reusable input.
	residual := attach("prepared-residual", "/residual", func(f *workspaceInputFact) {
		f.Reads = []workspaceBoundRead{{Input: workspaceInputPath{"argument:missing"}, Source: source("/bound")}}
	})
	mixed := &sdkInputPreparation{}
	mixedCtx, mixedReads := holder()
	mixed.importInputs(mixedCtx, []dagql.AnyResult{a, residual})
	require.NotNil(t, mixed.plan)
	require.Len(t, mixed.plan.base.values, 1)
	require.Len(t, mixed.plan.residual, 1)
	compare(mixedReads, []dagql.AnyResult{a, residual})
	// Publishing extra diagnostics on the exact same producer invalidates the
	// saved generation, including its previous complete coverage.
	frame, e := a.ResultCall()
	require.NoError(t, e)
	added := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: false, Prepared: workspaceInputSummary{Sources: []workspaceInputSource{source("/new")}}, Issues: []string{"new incomplete generation"}}
	updated, e := cache.GetOrInitCall(ctx, "prepared", srv, &dagql.CallRequest{ResultCall: frame.Clone(), ResultFacts: replaceWorkspaceInputFact(nil, added), ResultFactsIncomplete: true}, func(context.Context) (dagql.AnyResult, error) { return nil, fmt.Errorf("unexpected resolver") })
	require.NoError(t, e)
	require.Equal(t, idOf(a), idOf(updated))
	changedCtx, changed := holder()
	p.importInputs(changedCtx, []dagql.AnyResult{a})
	require.NotSame(t, plan, p.plan)
	require.True(t, changed.snapshot().incomplete)
	require.True(t, changed.incompleteLocked())
	compare(changed, []dagql.AnyResult{a})
	require.False(t, first.prepared[0].summary.incomplete, "later facts must not mutate an earlier holder's base")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			callCtx, reads := holder()
			p.importInputs(callCtx, []dagql.AnyResult{a})
			_ = reads.snapshot()
			reads.rememberInputs(map[uint64]*dagql.ResultCallRef{idOf(b): {ResultID: idOf(b)}})
		})
	}
	wg.Wait()
	// An exhausted budget belongs only to its session. The second session
	// owns its result before the first session releases that result closure.
	budgetA := p.plan.budget
	require.NotNil(t, budgetA)
	budgetA.mu.Lock()
	remaining := maxSDKPreparedStorage - budgetA.used
	budgetA.mu.Unlock()
	require.True(t, budgetA.replace(0, remaining))
	overflowCtx, overflowReads := holder()
	overflow := &sdkInputPreparation{}
	overflow.importInputs(overflowCtx, []dagql.AnyResult{b})
	require.Nil(t, overflow.plan)
	require.Empty(t, overflowReads.prepared)
	compare(overflowReads, []dagql.AnyResult{b})
	// Admission refusal belongs to the memo lifetime, not the current free
	// space. Later imports must still read current source facts normally.
	require.True(t, budgetA.replace(remaining, 0))
	frameB, err := b.ResultCall()
	require.NoError(t, err)
	changedB := workspaceInputFact{Operation: workspaceInputOperation(ctx, frameB), Complete: false,
		Prepared: workspaceInputSummary{Sources: []workspaceInputSource{source("/after-refusal")}},
		Issues:   []string{"facts changed after admission refusal"}}
	bChanged, err := cache.GetOrInitCall(ctx, "prepared", srv,
		&dagql.CallRequest{ResultCall: frameB.Clone(), ResultFacts: replaceWorkspaceInputFact(nil, changedB), ResultFactsIncomplete: true},
		func(context.Context) (dagql.AnyResult, error) { return nil, fmt.Errorf("unexpected resolver") })
	require.NoError(t, err)
	require.Equal(t, idOf(b), idOf(bChanged))
	for range 2 {
		currentCtx, current := holder()
		overflow.importInputs(currentCtx, []dagql.AnyResult{b})
		require.Nil(t, overflow.plan, "a refused memo must not rebuild when budget space is freed")
		require.Empty(t, current.prepared)
		compare(current, []dagql.AnyResult{b})
		require.True(t, current.snapshot().incomplete)
		require.Contains(t, current.snapshot().Issues, "facts changed after admission refusal")
	}
	require.True(t, budgetA.replace(0, remaining))
	ctxB := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: "prepared-other", SessionID: "prepared-other"})
	bOwned, err := cache.GetOrInitCall(ctxB, "prepared-other", srv, &dagql.CallRequest{ResultCall: frameB.Clone()}, func(context.Context) (dagql.AnyResult, error) { return nil, fmt.Errorf("unexpected resolver") })
	require.NoError(t, err)
	require.Equal(t, idOf(b), idOf(bOwned))
	readsB := &workspaceInputReads{boundary: boundary}
	callCtxB := context.WithValue(ctxB, workspaceInputReadsKey{}, readsB)
	preparationB := overflow
	preparationB.importInputs(callCtxB, []dagql.AnyResult{bOwned})
	require.NotNil(t, preparationB.plan, "one full session must not disable another session's prepared plans")
	planB := preparationB.plan
	require.NotSame(t, budgetA, planB.budget)
	require.NotEmpty(t, readsB.prepared)
	// A denied session cannot reuse the saved plan through the opaque tokens.
	require.NoError(t, cache.ReleaseSession(ctx, "prepared"))
	preparationB.importInputs(callCtxB, []dagql.AnyResult{bOwned})
	require.Same(t, planB, preparationB.plan, "releasing the first session must not invalidate the second budget or plan")
	normalB := &workspaceInputReads{boundary: boundary}
	importSDKInputs(context.WithValue(ctxB, workspaceInputReadsKey{}, normalB), []dagql.AnyResult{bOwned})
	require.Equal(t, normalB.snapshot(), readsB.snapshot())
	require.NoError(t, cache.ReleaseSession(ctxB, "prepared-other"))
	closedCtx, closed := holder()
	p.importInputs(closedCtx, []dagql.AnyResult{a})
	require.Empty(t, closed.prepared)
	require.True(t, closed.snapshot().incomplete)
}

func TestSDKInputPreparationListParent(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.CloseDiscardingPersistence()) })
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "prepared-list", SessionID: "prepared-list"})
	list := dagql.Array[dagql.String]{"one", "two"}
	frame := moduleObjectTestSyntheticCall("prepared-list", list)
	detached, err := dagql.NewResultForCall(list, frame)
	require.NoError(t, err)
	fact := workspaceInputFact{Operation: workspaceInputOperation(ctx, frame), Complete: true, CapabilitiesComplete: true, Prepared: workspaceInputSummary{Sources: []workspaceInputSource{{Workspace: "list-workspace", KnownBoundary: true, Selectors: []provenance.Selector{{Path: "/old", Kind: provenance.WorkspaceRead}}}}}}
	parent, err := cache.GetOrInitCall(ctx, "prepared-list", srv, &dagql.CallRequest{ResultCall: frame, ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsPrepared: true}, func(context.Context) (dagql.AnyResult, error) { return detached, nil })
	require.NoError(t, err)
	item, err := parent.NthValue(dagql.ContextWithServer(ctx, srv), 1)
	require.NoError(t, err)
	values := []dagql.AnyResult{item}
	holder := func() (context.Context, *workspaceInputReads) {
		r := &workspaceInputReads{}
		return context.WithValue(ctx, workspaceInputReadsKey{}, r), r
	}
	p := &sdkInputPreparation{}
	callCtx, reads := holder()
	p.importInputs(callCtx, values)
	require.NotNil(t, p.plan)
	old := p.plan
	require.Len(t, p.plan.base.values, 1)
	require.GreaterOrEqual(t, len(p.plan.tokens), 2, "list parent is a dependency, not a selected prepared input")
	parentID, err := parent.ID()
	require.NoError(t, err)
	require.False(t, reads.hasInputLocked(parentID.EngineResultID()), "parent ancestry is not prepared membership")
	fact.Complete = false
	fact.Issues = []string{"parent changed after item selection"}
	_, err = cache.GetOrInitCall(ctx, "prepared-list", srv, &dagql.CallRequest{ResultCall: frame.Clone(), ResultFacts: replaceWorkspaceInputFact(nil, fact), ResultFactsIncomplete: true}, func(context.Context) (dagql.AnyResult, error) { return nil, fmt.Errorf("unexpected resolver") })
	require.NoError(t, err)
	changedCtx, changed := holder()
	p.importInputs(changedCtx, values)
	require.NotSame(t, old, p.plan)
	normalCtx, normal := holder()
	importSDKInputs(normalCtx, values)
	require.Equal(t, normal.snapshot(), changed.snapshot())
	require.True(t, changed.snapshot().incomplete)
}

func TestSDKInputPreparationBudget(t *testing.T) {
	b := &sdkInputPlanBudget{}
	require.True(t, b.replace(0, maxSDKPreparedStorage))
	require.False(t, b.replace(0, 1))
	require.Equal(t, maxSDKPreparedStorage, b.used)
	require.True(t, b.replace(maxSDKPreparedStorage, 1))
	require.Equal(t, 1, b.used)
	require.True(t, b.replace(1, 0))
	require.Zero(t, b.used)
}
