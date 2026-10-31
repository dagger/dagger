package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// A lazy evaluation's set-up and wind-down around its body are steps of its
// op: taking the row's ownership, starting the body's goroutine, the body's
// operation lease, and retiring the attempt. A profile then tells the
// evaluation's own bookkeeping apart from its body's work.
func TestLazyEvaluationPhasesProfiled(t *testing.T) {
	chain := newChainFixture(t, "lazy-phases")
	f := newExhaustionFixture(t, chain)
	f.attach(t, f.receiver, exhaustionOffer(chain, "", true))

	ctx, root := profiledRoot(f.ctx, "test.demand")
	f.ctx = ctx
	require.NoError(t, f.run())
	require.True(t, f.installed())
	root.End(wcprof.OutcomeOK)

	events, ops := profiledUnder(t, root.ID())
	installs := profiledOps(events, "remotecache.install")
	require.Len(t, installs, 1)
	lazy, ok := ops[installs[0].ParentID]
	require.True(t, ok, "the install's task is recorded")
	require.Equal(t, "lazy", lazy.OpKind)

	steps := map[string]profiledEvent{}
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == lazy.OpID && ev.OpKind == "internal" {
			steps[ev.class] = ev
		}
	}
	order := []string{"lazy.ownership", "lazy.spawn", "lazy.operationLease", "lazy.endBody", "lazy.retire"}
	last := lazy.StartNS
	for _, class := range order {
		step, ok := steps[class]
		require.True(t, ok, "no %s step under the task's lazy op", class)
		require.Equal(t, "ok", step.Outcome, class)
		require.LessOrEqual(t, last, step.StartNS, class)
		require.LessOrEqual(t, step.EndNS, lazy.EndNS, class)
		last = step.EndNS
	}
	requireStepsNest(t, events, lazy.OpID)
}

// requireStepsNest checks that the internal steps under parent run one after
// another, never beside each other, and that none of parent's own waits lies
// inside one of them: work a step encloses is recorded under the step, so a
// profile charges it there rather than to the step's own time.
func requireStepsNest(t *testing.T, events []profiledEvent, parent uint64) {
	t.Helper()
	var steps []profiledEvent
	for _, ev := range events {
		if ev.Type == "op" && ev.ParentID == parent && ev.OpKind == "internal" {
			steps = append(steps, ev)
		}
	}
	for i, a := range steps {
		for _, b := range steps[i+1:] {
			require.False(t, a.StartNS < b.EndNS && b.StartNS < a.EndNS, "steps %s and %s overlap", a.class, b.class)
		}
		for _, ev := range events {
			if ev.Type == "wait" && ev.ParentID == parent {
				require.False(t, a.StartNS <= ev.StartNS && ev.EndNS <= a.EndNS, "a wait of the lazy op lies inside its step %s", a.class)
			}
		}
	}
}

// Ending a task's body updates its row's part gate under the graph lock. A
// wait for the lock there is recorded under the step that ends the body.
func TestEndPartTaskBodyWaitsForGraphLockWcprof(t *testing.T) {
	wcprof.EnsureRecorder()
	row := &sharedResult{id: 12}
	row.partGate.loadOrCreate()
	token := &PartTaskToken{row: row}
	token.active.Store(true)
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}

	ctx, root := profiledRoot(t.Context(), "test.endBody")
	stepCtx, step := BeginStep(ctx, "lazy.endBody")
	c.egraphMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.endPartTaskBody(stepCtx, token, true)
	}()
	waitBlockedIn(t, "wcprof.WriteLock(")
	c.egraphMu.Unlock()
	<-done
	step.End(nil)
	root.End(wcprof.OutcomeOK)

	header, events := lockWaitTestDump(t)
	requireWaitUnder(t, header, events, map[uint64]bool{step.op.ID(): true}, egraphLockIdent)
}

// The cache state a lazy op's span reports captures the row's record under
// the graph lock. A wait for the lock there is recorded under the
// evaluation's op, as the part machinery's other graph-lock waits are.
func TestLazySpanCacheStateWaitsForGraphLockWcprof(t *testing.T) {
	wcprof.EnsureRecorder()
	_, rootCtx, root := newLazyRecordingRoot("POST /query")
	defer root.End()
	row := &sharedResult{id: 11}
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}

	evalCtx, lazyOp := wcprof.BeginOp(wcprof.ContextWithProfiling(rootCtx), wcprof.OpKindLazy, "Container.withExec", wcprof.OpOpts{})
	c.egraphMu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.lazySpanCacheState(evalCtx, row, nil)
	}()
	waitBlockedIn(t, "wcprof.WriteLock(")
	c.egraphMu.Unlock()
	<-done
	lazyOp.End(wcprof.OutcomeOK)

	header, events := lockWaitTestDump(t)
	requireWaitUnder(t, header, events, map[uint64]bool{lazyOp.ID(): true}, egraphLockIdent)
}
