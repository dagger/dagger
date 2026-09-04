package dagql

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/vektah/gqlparser/v2/ast"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// Part and group shape mirroring the refined container: a cheap metadata
// group and a joint output group filling two parts.
const (
	partsTestPartMeta  PartKey = "meta"
	partsTestPartFS    PartKey = "fs"
	partsTestPartXMeta PartKey = "xmeta"

	partsTestGroupMeta LazyGroupKey = "meta"
	partsTestGroupOut  LazyGroupKey = "out"
)

func partsTestGroupOf(p PartKey) LazyGroupKey {
	if p == partsTestPartMeta {
		return partsTestGroupMeta
	}
	return partsTestGroupOut
}

// cacheTestPartsObject is a fake parts value: per-group bodies with the
// same consumption contract core types follow (a successful body run
// consumes the group's work), plus a pluggable resolution function.
type cacheTestPartsObject struct {
	Value int

	mu        sync.Mutex
	groupEval map[LazyGroupKey]LazyEvalFunc

	// resolveFn maps parts to groups. Tests either use the direct mapping
	// (partsTestDirectResolve) or the container-like shape that settles
	// the metadata part via the cache before mapping positional parts.
	resolveFn func(context.Context, AnyResult, []PartKey) ([]LazyGroupKey, error)
	// resolveCalls counts resolution invocations, to observe the
	// result-level fast path skipping resolution entirely.
	resolveCalls atomic.Int32

	snapshotLinks []PersistedSnapshotRefLink
}

func (*cacheTestPartsObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheTestPartsObject",
		NonNull:   true,
	}
}

func (obj *cacheTestPartsObject) PersistedSnapshotRefLinks() []PersistedSnapshotRefLink {
	if obj == nil {
		return nil
	}
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return append([]PersistedSnapshotRefLink(nil), obj.snapshotLinks...)
}

func (obj *cacheTestPartsObject) LazyEvalFunc() LazyEvalFunc {
	if obj == nil {
		return nil
	}
	obj.mu.Lock()
	defer obj.mu.Unlock()
	for _, fn := range obj.groupEval {
		if fn != nil {
			// Non-nil while any group has work, as the contract requires.
			// The cache must never run this whole-result body for a parts
			// value; failing loudly here gives that assumption teeth.
			return func(context.Context) error {
				return errors.New("whole-result body ran for a parts value")
			}
		}
	}
	return nil
}

func (obj *cacheTestPartsObject) ResolveLazyEvalGroups(ctx context.Context, self AnyResult, parts []PartKey) ([]LazyGroupKey, error) {
	obj.resolveCalls.Add(1)
	return obj.resolveFn(ctx, self, parts)
}

func (obj *cacheTestPartsObject) LazyEvalFuncForGroup(group LazyGroupKey) LazyEvalFunc {
	obj.mu.Lock()
	fn := obj.groupEval[group]
	obj.mu.Unlock()
	if fn == nil {
		return nil
	}
	return func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		obj.mu.Lock()
		obj.groupEval[group] = nil
		obj.mu.Unlock()
		return nil
	}
}

// partsTestDirectResolve maps parts to groups with no self-demand:
// sibling groups are fully independent.
func partsTestDirectResolve(_ context.Context, _ AnyResult, parts []PartKey) ([]LazyGroupKey, error) {
	if parts == nil {
		return []LazyGroupKey{partsTestGroupMeta, partsTestGroupOut}, nil
	}
	var groups []LazyGroupKey
	for _, p := range parts {
		g := partsTestGroupOf(p)
		var seen bool
		for _, existing := range groups {
			if existing == g {
				seen = true
				break
			}
		}
		if !seen {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// partsTestContainerLikeResolve mirrors the refined container's
// resolution: any demand beyond the metadata part settles the value's own
// metadata part first, via the cache, before the mapping is returned.
func partsTestContainerLikeResolve(c *Cache) func(context.Context, AnyResult, []PartKey) ([]LazyGroupKey, error) {
	return func(ctx context.Context, self AnyResult, parts []PartKey) ([]LazyGroupKey, error) {
		metaOnly := parts != nil
		for _, p := range parts {
			if p != partsTestPartMeta {
				metaOnly = false
				break
			}
		}
		if !metaOnly {
			if err := c.EvaluateParts(ctx, self, partsTestPartMeta); err != nil {
				return nil, err
			}
		}
		return partsTestDirectResolve(ctx, self, parts)
	}
}

func cacheTestPartsServer(t *testing.T) *Server {
	t.Helper()
	srv := newDagqlServerForTest(t, cacheTestQuery{})
	Fields[*cacheTestPartsObject]{
		Func("value", func(_ context.Context, self *cacheTestPartsObject, _ struct{}) (Int, error) {
			return NewInt(self.Value), nil
		}),
	}.Install(srv)
	return srv
}

func newPartsTestResult(
	t *testing.T,
	c *Cache,
	ctx context.Context,
	obj *cacheTestPartsObject,
) ObjectResult[*cacheTestPartsObject] {
	t.Helper()
	srv := cacheTestPartsServer(t)
	sessionID := cacheTestSessionID(t, ctx)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestPartsObject{}).Type()),
		Field: "parts-test",
	}
	resAny, err := c.GetOrInitCall(ctx, sessionID, srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		res, err := NewObjectResultForCall(obj, srv, frame)
		if err != nil {
			t.Error(err)
		}
		return res, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return resAny.(ObjectResult[*cacheTestPartsObject])
}

func newPartsTestCache(t *testing.T, mgr bkcache.SnapshotManager) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", mgr, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ContextWithCache(ctx, c), c
}

// Evaluating the metadata part settles metadata and leaves the snapshot
// group pending: the headline stage-2 behavior at the dagql layer.
func TestEvaluatePartsMetadataLeavesSiblingGroupPending(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)

	var metaRuns, outRuns atomic.Int32
	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestContainerLikeResolve(c)
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error {
			metaRuns.Add(1)
			return nil
		},
		partsTestGroupOut: func(context.Context) error {
			outRuns.Add(1)
			return nil
		},
	}
	res := newPartsTestResult(t, c, ctx, obj)

	if err := c.EvaluateParts(ctx, res, partsTestPartMeta); err != nil {
		t.Fatal(err)
	}
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times, want 1", got)
	}
	if got := outRuns.Load(); got != 0 {
		t.Fatalf("output body ran %d times after a metadata read, want 0", got)
	}
	if !HasPendingLazyEvaluation(res) {
		t.Fatal("result must stay pending while the output group has work")
	}

	// A second metadata read re-runs nothing.
	if err := c.EvaluateParts(ctx, res, partsTestPartMeta); err != nil {
		t.Fatal(err)
	}
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times after a repeat read, want 1", got)
	}

	// Full evaluation runs only the remaining group and settles everything.
	if err := c.Evaluate(ctx, res); err != nil {
		t.Fatal(err)
	}
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times after full evaluation, want 1", got)
	}
	if got := outRuns.Load(); got != 1 {
		t.Fatalf("output body ran %d times after full evaluation, want 1", got)
	}
	if HasPendingLazyEvaluation(res) {
		t.Fatal("result must not be pending after full evaluation")
	}
}

func TestEvaluatePartsResumeSpansMarkPartialCompletion(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)
	spanExporter := &cacheTestSpanExporter{}
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	defer tracerProvider.Shutdown(t.Context())

	producerCtx, producerSpan := tracerProvider.Tracer("dagger.io/test").Start(ctx, "parts producer")
	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestDirectResolve
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error { return nil },
		partsTestGroupOut:  func(context.Context) error { return nil },
	}
	res := newPartsTestResult(t, c, producerCtx, obj)

	metaCtx, metaSpan := tracerProvider.Tracer("dagger.io/test").Start(ctx, "metadata consumer")
	if err := c.EvaluateParts(metaCtx, res, partsTestPartMeta); err != nil {
		t.Fatal(err)
	}
	metaSpan.End()
	outCtx, outSpan := tracerProvider.Tracer("dagger.io/test").Start(ctx, "output consumer")
	if err := c.EvaluateParts(outCtx, res, partsTestPartFS); err != nil {
		t.Fatal(err)
	}
	outSpan.End()
	producerSpan.End()
	if err := tracerProvider.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}

	spanExporter.mu.Lock()
	spans := append([]sdktrace.ReadOnlySpan(nil), spanExporter.spans...)
	spanExporter.mu.Unlock()
	partialAttr := func(span sdktrace.ReadOnlySpan) (bool, bool) {
		for _, attr := range span.Attributes() {
			if string(attr.Key) == telemetryattrs.DagPartialAttr {
				return attr.Value.AsBool(), true
			}
		}
		return false, false
	}
	var sawMetadata, sawOutput bool
	for _, span := range spans {
		switch span.Name() {
		case "resume parts-test (meta)":
			sawMetadata = true
			partial, found := partialAttr(span)
			if !found || !partial {
				t.Fatal("metadata resume span must report partial completion")
			}
		case "resume parts-test (out)":
			sawOutput = true
			if _, found := partialAttr(span); found {
				t.Fatal("final resume span must not report partial completion")
			}
		}
	}
	if !sawMetadata || !sawOutput {
		t.Fatalf("resume spans found: metadata=%v output=%v", sawMetadata, sawOutput)
	}
}

// One joint-group body fills every member part: demanding two different
// parts of the same group runs the body once.
func TestEvaluatePartsJointGroupFillsAllMembersOnce(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)

	var metaRuns, outRuns atomic.Int32
	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestContainerLikeResolve(c)
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error {
			metaRuns.Add(1)
			return nil
		},
		partsTestGroupOut: func(context.Context) error {
			outRuns.Add(1)
			return nil
		},
	}
	res := newPartsTestResult(t, c, ctx, obj)

	if err := c.EvaluateParts(ctx, res, partsTestPartFS); err != nil {
		t.Fatal(err)
	}
	if err := c.EvaluateParts(ctx, res, partsTestPartXMeta); err != nil {
		t.Fatal(err)
	}
	if got := outRuns.Load(); got != 1 {
		t.Fatalf("joint body ran %d times for two member parts, want 1", got)
	}
	// The resolution phase settled metadata exactly once on the way.
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times, want 1", got)
	}
	if HasPendingLazyEvaluation(res) {
		t.Fatal("result must not be pending once every group was consumed")
	}
}

// Sibling groups evaluate concurrently: two callers wanting different
// parts do not serialize on each other's bodies.
func TestEvaluatePartsSiblingGroupsRunConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)

		metaEntered := make(chan struct{})
		metaRelease := make(chan struct{})
		outEntered := make(chan struct{})
		outRelease := make(chan struct{})
		obj := &cacheTestPartsObject{Value: 1}
		obj.resolveFn = partsTestDirectResolve
		obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
			partsTestGroupMeta: func(context.Context) error {
				close(metaEntered)
				<-metaRelease
				return nil
			},
			partsTestGroupOut: func(context.Context) error {
				close(outEntered)
				<-outRelease
				return nil
			},
		}
		res := newPartsTestResult(t, c, ctx, obj)

		evalMeta := make(chan error, 1)
		evalOut := make(chan error, 1)
		go func() { evalMeta <- c.EvaluateParts(ctx, res, partsTestPartMeta) }()
		go func() { evalOut <- c.EvaluateParts(ctx, res, partsTestPartFS) }()

		// Both bodies are inside their group's callback at once; neither
		// has been released yet.
		waitLazyRetrySignal(t, metaEntered, "metadata body entry")
		waitLazyRetrySignal(t, outEntered, "output body entry")

		close(metaRelease)
		close(outRelease)
		if err := waitLazyRetryError(t, evalMeta, "metadata evaluation"); err != nil {
			t.Fatal(err)
		}
		if err := waitLazyRetryError(t, evalOut, "output evaluation"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEvaluatePartsOneCallRunsGroupsConcurrently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)

		metaEntered := make(chan struct{})
		metaRelease := make(chan struct{})
		outEntered := make(chan struct{})
		outRelease := make(chan struct{})
		obj := &cacheTestPartsObject{Value: 1}
		obj.resolveFn = partsTestDirectResolve
		obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
			partsTestGroupMeta: func(context.Context) error {
				close(metaEntered)
				<-metaRelease
				return nil
			},
			partsTestGroupOut: func(context.Context) error {
				close(outEntered)
				<-outRelease
				return nil
			},
		}
		res := newPartsTestResult(t, c, ctx, obj)

		eval := make(chan error, 1)
		go func() {
			eval <- c.EvaluateParts(ctx, res, partsTestPartMeta, partsTestPartFS)
		}()

		waitLazyRetrySignal(t, metaEntered, "metadata body entry")
		waitLazyRetrySignal(t, outEntered, "output body entry")
		close(metaRelease)
		close(outRelease)
		if err := waitLazyRetryError(t, eval, "parts evaluation"); err != nil {
			t.Fatal(err)
		}
	})
}

// A group body failure leaves that group retryable and other groups
// untouched: per-group failure isolation.
func TestEvaluatePartsFailureIsolatedPerGroup(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)

	injected := errors.New("output body exploded")
	var metaRuns, outRuns atomic.Int32
	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestContainerLikeResolve(c)
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error {
			metaRuns.Add(1)
			return nil
		},
		partsTestGroupOut: func(context.Context) error {
			if outRuns.Add(1) == 1 {
				return injected
			}
			return nil
		},
	}
	res := newPartsTestResult(t, c, ctx, obj)

	if err := c.EvaluateParts(ctx, res, partsTestPartFS); !errors.Is(err, injected) {
		t.Fatalf("first output evaluation returned %v, want %v", err, injected)
	}
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times, want 1", got)
	}

	// Metadata stays complete across the sibling failure.
	if err := c.EvaluateParts(ctx, res, partsTestPartMeta); err != nil {
		t.Fatal(err)
	}
	if got := metaRuns.Load(); got != 1 {
		t.Fatalf("metadata body ran %d times after sibling failure, want 1", got)
	}

	// The failed group retries and succeeds.
	if err := c.EvaluateParts(ctx, res, partsTestPartFS); err != nil {
		t.Fatal(err)
	}
	if got := outRuns.Load(); got != 2 {
		t.Fatalf("output body ran %d times, want 2 (failed then retried)", got)
	}
	if HasPendingLazyEvaluation(res) {
		t.Fatal("result must not be pending after the retry succeeded")
	}
}

// Whole-result evaluation of a parts value runs the groups sequentially
// and latches the result-level fast path: a second Evaluate performs no
// resolution at all.
func TestEvaluateWholeResultOverGroups(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)

	var order []string
	var orderMu sync.Mutex
	record := func(name string) {
		orderMu.Lock()
		order = append(order, name)
		orderMu.Unlock()
	}
	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestContainerLikeResolve(c)
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error {
			record("meta")
			return nil
		},
		partsTestGroupOut: func(context.Context) error {
			record("out")
			return nil
		},
	}
	res := newPartsTestResult(t, c, ctx, obj)

	if err := c.Evaluate(ctx, res); err != nil {
		t.Fatal(err)
	}
	orderMu.Lock()
	got := strings.Join(order, ",")
	orderMu.Unlock()
	if got != "meta,out" {
		t.Fatalf("group bodies ran in order %q, want %q", got, "meta,out")
	}
	if HasPendingLazyEvaluation(res) {
		t.Fatal("result must not be pending after whole-result evaluation")
	}

	resolvesAfterFirst := obj.resolveCalls.Load()
	if err := c.Evaluate(ctx, res); err != nil {
		t.Fatal(err)
	}
	if got := obj.resolveCalls.Load(); got != resolvesAfterFirst {
		t.Fatalf("second Evaluate resolved groups (%d -> %d calls); the result-level latch must skip resolution", resolvesAfterFirst, got)
	}
}

// Re-entering the same (result, group) from inside its own body is
// refused as recursion; the resolution-phase re-entry into the same
// result's metadata group is legal and exercised by every container-like
// test above.
func TestEvaluatePartsRecursionRefusedPerGroup(t *testing.T) {
	t.Parallel()

	ctx, c := newPartsTestCache(t, nil)

	obj := &cacheTestPartsObject{Value: 1}
	obj.resolveFn = partsTestContainerLikeResolve(c)
	var res ObjectResult[*cacheTestPartsObject]
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
		partsTestGroupMeta: func(context.Context) error { return nil },
		partsTestGroupOut: func(ctx context.Context) error {
			return c.EvaluateParts(ctx, res, partsTestPartFS)
		},
	}
	res = newPartsTestResult(t, c, ctx, obj)

	err := c.EvaluateParts(ctx, res, partsTestPartFS)
	if err == nil || !strings.Contains(err.Error(), "recursive lazy evaluation detected") {
		t.Fatalf("self-demand of the same (result, group) returned %v, want recursion refusal", err)
	}
}

// A body-success bookkeeping-failure retries only the bookkeeping of that
// group: the pending-bookkeeping precedence rule, per group.
func TestEvaluatePartsSyncFailureRetriesOnlyBookkeepingPerGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := &lazyBookkeepingSnapshotManager{
			attachEntered: make(chan struct{}),
			attachResult:  make(chan error),
		}
		ctx, c := newPartsTestCache(t, mgr)

		var metaRuns, outRuns atomic.Int32
		obj := &cacheTestPartsObject{Value: 1}
		obj.resolveFn = partsTestContainerLikeResolve(c)
		obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
			partsTestGroupMeta: func(context.Context) error {
				metaRuns.Add(1)
				return nil
			},
			partsTestGroupOut: func(context.Context) error {
				outRuns.Add(1)
				obj.mu.Lock()
				obj.snapshotLinks = []PersistedSnapshotRefLink{{
					RefKey: "parts-produced-snapshot",
					Role:   "snapshot",
				}}
				obj.mu.Unlock()
				return nil
			},
		}
		res := newPartsTestResult(t, c, ctx, obj)

		// Settle metadata first so the output attempt's bookkeeping is the
		// only attach in flight. The metadata group produced no links, so
		// its bookkeeping performed no attach.
		if err := c.EvaluateParts(ctx, res, partsTestPartMeta); err != nil {
			t.Fatal(err)
		}

		eval1 := make(chan error, 1)
		go func() { eval1 <- c.EvaluateParts(ctx, res, partsTestPartFS) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "output bookkeeping attach")

		injected := errors.New("attach owner lease failed")
		mgr.attachResult <- injected
		if err := waitLazyRetryError(t, eval1, "first output evaluation"); !errors.Is(err, injected) {
			t.Fatalf("first output evaluation returned %v, want %v", err, injected)
		}
		if got := outRuns.Load(); got != 1 {
			t.Fatalf("output body ran %d times, want 1", got)
		}

		// The retry runs only the bookkeeping: the body already consumed
		// its work, and the value's re-exposed state must not be re-read
		// while the group's bookkeeping is pending.
		eval2 := make(chan error, 1)
		go func() { eval2 <- c.EvaluateParts(ctx, res, partsTestPartFS) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "retried bookkeeping attach")
		mgr.attachResult <- nil
		if err := waitLazyRetryError(t, eval2, "retried output evaluation"); err != nil {
			t.Fatal(err)
		}
		if got := outRuns.Load(); got != 1 {
			t.Fatalf("output body ran %d times after the bookkeeping retry, want 1", got)
		}
		if got := metaRuns.Load(); got != 1 {
			t.Fatalf("metadata body ran %d times, want 1", got)
		}
		if HasPendingLazyEvaluation(res) {
			t.Fatal("result must not be pending after bookkeeping settled")
		}
	})
}

// A healthy caller that joins a group attempt whose previous waiters
// caused callback cancellation retries that group instead of returning
// the foreign cancellation.
func TestEvaluatePartsForeignCancellationRetriesPerGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, c := newPartsTestCache(t, nil)

		bodyEntered := make(chan struct{})
		bodyRelease := make(chan struct{})
		var outRuns atomic.Int32
		obj := &cacheTestPartsObject{Value: 1}
		obj.resolveFn = partsTestDirectResolve
		obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
			partsTestGroupMeta: func(context.Context) error { return nil },
			partsTestGroupOut: func(ctx context.Context) error {
				if outRuns.Add(1) == 1 {
					close(bodyEntered)
					// Observe the shared callback context's cancellation,
					// then hold the attempt open until the joiner joined,
					// and return the foreign cancellation as bodies do.
					<-ctx.Done()
					<-bodyRelease
					return context.Cause(ctx)
				}
				return nil
			},
		}
		res := newPartsTestResult(t, c, ctx, obj)
		shared := res.cacheSharedResult()

		leaderCtx, cancelLeader := context.WithCancel(ctx)
		leader := make(chan error, 1)
		go func() { leader <- c.EvaluateParts(leaderCtx, res, partsTestPartFS) }()
		waitLazyRetrySignal(t, bodyEntered, "first output body entry")

		// The leader abandons as the last waiter, which cancels the
		// attempt's shared callback context; the attempt keeps running.
		cancelLeader()
		if err := waitLazyRetryError(t, leader, "leader outcome"); err == nil {
			t.Fatal("canceled leader must return its own cancellation")
		}

		// A healthy joiner joins the still-published, canceled attempt.
		joiner := make(chan error, 1)
		go func() { joiner <- c.EvaluateParts(ctx, res, partsTestPartXMeta) }()
		waitForCondition(t, func() bool {
			shared.lazyMu.Lock()
			defer shared.lazyMu.Unlock()
			g := shared.lazyPartGroups[partsTestGroupOut]
			return g != nil && g.attempt != nil && g.attempt.waiters >= 1
		}, "joiner to join the canceled attempt")

		// The canceled body returns the foreign cancellation; the healthy
		// joiner must retry the group and succeed.
		close(bodyRelease)
		if err := waitLazyRetryError(t, joiner, "joiner outcome"); err != nil {
			t.Fatalf("healthy joiner returned %v, want success after retry", err)
		}
		if got := outRuns.Load(); got != 2 {
			t.Fatalf("output body ran %d times, want 2 (canceled then retried)", got)
		}
	})
}

func waitForCondition(t *testing.T, cond func() bool, description string) {
	t.Helper()
	for range 10000 {
		if cond() {
			return
		}
		synctest.Wait()
	}
	t.Fatalf("timed out waiting for %s", description)
}

// partsTestConsumptionAwareResolve mirrors the container's real resolver
// shape after full consumption: once every group's work is consumed (the
// container.Lazy == nil analog) it under-reports and returns zero
// groups. Cache-side state can still be pending at that point (a body
// consumed its work while its attempt's bookkeeping failed or is in
// flight), and the cache must not treat empty resolution as completion.
func partsTestConsumptionAwareResolve(obj *cacheTestPartsObject) func(context.Context, AnyResult, []PartKey) ([]LazyGroupKey, error) {
	return func(ctx context.Context, self AnyResult, parts []PartKey) ([]LazyGroupKey, error) {
		obj.mu.Lock()
		consumed := true
		for _, fn := range obj.groupEval {
			if fn != nil {
				consumed = false
				break
			}
		}
		obj.mu.Unlock()
		if consumed {
			return nil, nil
		}
		return partsTestDirectResolve(ctx, self, parts)
	}
}

// A resolver that under-reports groups after consumption must not let a
// demand bypass pending bookkeeping: with a group's body consumed and
// its lease sync pending, whole-result evaluation retries exactly the
// bookkeeping, propagates its failure, and never latches completion
// over it.
func TestEvaluatePartsEmptyResolutionRetriesPendingBookkeeping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := &lazyBookkeepingSnapshotManager{
			attachEntered: make(chan struct{}),
			attachResult:  make(chan error),
		}
		ctx, c := newPartsTestCache(t, mgr)

		var outRuns atomic.Int32
		obj := &cacheTestPartsObject{Value: 1}
		obj.resolveFn = partsTestConsumptionAwareResolve(obj)
		obj.groupEval = map[LazyGroupKey]LazyEvalFunc{
			partsTestGroupMeta: func(context.Context) error { return nil },
			partsTestGroupOut: func(context.Context) error {
				outRuns.Add(1)
				obj.mu.Lock()
				obj.snapshotLinks = []PersistedSnapshotRefLink{{
					RefKey: "under-reported-snapshot",
					Role:   "snapshot",
				}}
				obj.mu.Unlock()
				return nil
			},
		}
		res := newPartsTestResult(t, c, ctx, obj)

		// Settle the metadata group (no links yet, so its bookkeeping
		// performs no attach).
		if err := c.EvaluateParts(ctx, res, partsTestPartMeta); err != nil {
			t.Fatal(err)
		}

		// The output body succeeds and consumes its work; the attempt's
		// lease sync fails. Every group is now consumed, so the resolver
		// under-reports zero groups while out's bookkeeping is pending.
		eval1 := make(chan error, 1)
		go func() { eval1 <- c.EvaluateParts(ctx, res, partsTestPartFS) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "first bookkeeping attach")
		injected := errors.New("attach owner lease failed")
		mgr.attachResult <- injected
		if err := waitLazyRetryError(t, eval1, "first output evaluation"); !errors.Is(err, injected) {
			t.Fatalf("first output evaluation returned %v, want %v", err, injected)
		}

		// Whole-result evaluation must retry the pending bookkeeping (not
		// the body) and propagate a repeated failure instead of latching
		// vacuous completion off the empty resolution.
		injected2 := errors.New("attach owner lease failed again")
		eval2 := make(chan error, 1)
		go func() { eval2 <- c.Evaluate(ctx, res) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "retried bookkeeping attach")
		mgr.attachResult <- injected2
		if err := waitLazyRetryError(t, eval2, "second whole evaluation"); !errors.Is(err, injected2) {
			t.Fatalf("second whole evaluation returned %v, want %v", err, injected2)
		}
		if !HasPendingLazyEvaluation(res) {
			t.Fatal("result must stay pending while bookkeeping is unresolved")
		}
		if got := outRuns.Load(); got != 1 {
			t.Fatalf("output body ran %d times, want 1 (bookkeeping-only retries)", got)
		}

		// A successful bookkeeping retry settles everything.
		eval3 := make(chan error, 1)
		go func() { eval3 <- c.Evaluate(ctx, res) }()
		waitLazyRetrySignal(t, mgr.attachEntered, "final bookkeeping attach")
		mgr.attachResult <- nil
		if err := waitLazyRetryError(t, eval3, "final whole evaluation"); err != nil {
			t.Fatal(err)
		}
		if HasPendingLazyEvaluation(res) {
			t.Fatal("result must not be pending after bookkeeping settled")
		}
		if got := outRuns.Load(); got != 1 {
			t.Fatalf("output body ran %d times after settling, want 1", got)
		}
	})
}
