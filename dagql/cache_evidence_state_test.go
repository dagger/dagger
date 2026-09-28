package dagql

// Cache-state evidence tests: what a call span reports of its result's state
// in the cache (CacheDecision.ResultState, read as core reads it when the span
// ends), and what a lazy-evaluation span reports of the entry it evaluated.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func stateTestReceiverCall(field string, receiver AnyResult) *ResultCall {
	return &ResultCall{
		Kind:     ResultCallKindField,
		Field:    field,
		Type:     NewResultCallType(Int(0).Type()),
		Receiver: &ResultCallRef{ResultID: uint64(receiver.cacheSharedResult().id)},
	}
}

func stateTestEdge(c *Cache, res AnyResult) (persistedEdge, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	edge, found := c.persistedEdgesByResult[res.cacheSharedResult().id]
	return edge, found
}

// An executed call reports its result's dependencies and own expiry, and no
// retention when the call is not persistable.
func TestCacheResultStateExecuted(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	leafFrame := cacheTestIntCall("state-executed-leaf")
	leaf, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, &CallRequest{ResultCall: leafFrame}, ValueFunc(cacheTestIntResult(leafFrame, 1)))
	assert.NilError(t, err)

	rootFrame := stateTestReceiverCall("state-executed-root", leaf)
	req := cacheTestArmedRequest(rootFrame)
	req.TTL = 3600
	root, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, req, ValueFunc(cacheTestIntResult(rootFrame, 2)))
	assert.NilError(t, err)
	assert.Equal(t, CacheOutcomeExecuted, req.CacheEvidence.Outcome)

	state, ok := req.CacheEvidence.ResultState(ctx, root)
	assert.Assert(t, ok)
	assert.DeepEqual(t, []uint64{uint64(leaf.cacheSharedResult().id)}, state.Deps)
	assert.Assert(t, !state.Retained)
	assert.Equal(t, int64(0), state.RetentionExpiresAtUnix)
	assert.Assert(t, state.ExpiresAtUnix != 0)
	assert.Equal(t, root.cacheSharedResult().expiresAtUnix, state.ExpiresAtUnix)
	assert.Equal(t, 0, len(state.Parts))
}

// A persistable call's publication retains its result, with the call's TTL as
// the edge's expiry.
func TestCacheResultStatePersistable(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	frame := cacheTestIntCall("state-persistable")
	req := cacheTestArmedRequest(frame)
	req.IsPersistable = true
	req.TTL = 3600
	res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, req, ValueFunc(cacheTestIntResult(frame, 1)))
	assert.NilError(t, err)

	edge, found := stateTestEdge(c, res)
	assert.Assert(t, found)
	state, ok := req.CacheEvidence.ResultState(ctx, res)
	assert.Assert(t, ok)
	assert.Assert(t, state.Retained)
	assert.Assert(t, state.RetentionExpiresAtUnix != 0)
	assert.Equal(t, edge.expiresAtUnix, state.RetentionExpiresAtUnix)
}

// All waiters of one persistable call are in one session, and only the last
// to leave creates the retention edge. A waiter that leaves first, whose span
// may be the session's only span for the call, still reports the retention
// its publication adds, with the expiry the edge gets.
func TestCacheResultStateJoinedNotLast(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	frame := cacheTestIntCall("state-joined")
	release := make(chan struct{})
	started := make(chan struct{})
	hold := make(chan struct{})
	var leaving atomic.Int32
	c.testBeforeWaiterLeave = func() {
		if leaving.Add(1) == 1 {
			<-hold
		}
	}

	type done struct {
		req *CallRequest
		res AnyResult
		err error
	}
	finished := make(chan done, 2)
	call := func(fn func(context.Context) (AnyResult, error)) {
		req := cacheTestArmedRequest(frame)
		req.ConcurrencyKey = "state-joined-key"
		req.IsPersistable = true
		req.TTL = 3600
		go func() {
			res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, req, fn)
			finished <- done{req: req, res: res, err: err}
		}()
	}
	call(func(context.Context) (AnyResult, error) {
		close(started)
		<-release
		return cacheTestIntResult(frame, 1), nil
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for executor start")
	}
	call(func(context.Context) (AnyResult, error) {
		t.Error("joiner resolver must not run")
		return cacheTestIntResult(frame, 2), nil
	})
	waitKeys := callConcurrencyKeys{callKey: cacheTestCallDigest(frame).String(), concurrencyKey: "state-joined-key"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.callsMu.Lock()
		waiters := 0
		if oc := c.ongoingCalls[waitKeys]; oc != nil {
			waiters = oc.waiters
		}
		c.callsMu.Unlock()
		if waiters >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for joiner admission")
		}
		time.Sleep(time.Millisecond)
	}
	close(release)

	// One waiter is held before it leaves; the other leaves first.
	first := <-finished
	assert.NilError(t, first.err)
	_, found := stateTestEdge(c, first.res)
	assert.Assert(t, !found, "the edge waits for the last waiter")
	state, ok := first.req.CacheEvidence.ResultState(ctx, first.res)
	assert.Assert(t, ok)
	assert.Assert(t, state.Retained, "the first waiter's span reports the edge its publication adds")
	pendingExpiry := state.RetentionExpiresAtUnix
	assert.Assert(t, pendingExpiry != 0)

	close(hold)
	last := <-finished
	assert.NilError(t, last.err)
	edge, found := stateTestEdge(c, last.res)
	assert.Assert(t, found, "the last waiter created the edge")
	assert.Equal(t, pendingExpiry, edge.expiresAtUnix, "with the expiry the first span reported")
	state, ok = last.req.CacheEvidence.ResultState(ctx, last.res)
	assert.Assert(t, ok)
	assert.Assert(t, state.Retained)
	assert.Equal(t, edge.expiresAtUnix, state.RetentionExpiresAtUnix)

	// A prune drops the created edge. Either waiter's span, read now, sees
	// that: the publication's edge counts only until it is created.
	_, removed, err := c.removePersistedEdge(ctx, last.res.cacheSharedResult().id)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	for _, waiter := range []done{first, last} {
		state, ok := waiter.req.CacheEvidence.ResultState(ctx, waiter.res)
		assert.Assert(t, ok, "the session still owns the entry")
		assert.Assert(t, !state.Retained)
		assert.Equal(t, int64(0), state.RetentionExpiresAtUnix)
	}
}

// A persistable call's publication creates its edge before the call returns
// when the call is its only waiter. A prune that drops the edge before the
// span reads it leaves the span reporting no retention.
func TestCacheResultStatePublishedEdgePruned(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	frame := cacheTestIntCall("state-published-pruned")
	req := cacheTestArmedRequest(frame)
	req.IsPersistable = true
	res, err := c.GetOrInitCall(ctx, "s", noopTypeResolver{}, req, ValueFunc(cacheTestIntResult(frame, 1)))
	assert.NilError(t, err)
	_, found := stateTestEdge(c, res)
	assert.Assert(t, found)
	_, removed, err := c.removePersistedEdge(ctx, res.cacheSharedResult().id)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	state, ok := req.CacheEvidence.ResultState(ctx, res)
	assert.Assert(t, ok)
	assert.Assert(t, !state.Retained)
}

// A prune is planned while an entry has only its retention edge, then a
// persistable call on another recipe returns that entry, which its session
// now holds, and the planned drop applies before the call's span reads the
// state. The span reports no retention: the publication's edge merged into
// the existing one, which the prune then dropped.
func TestCacheResultStatePrunePlannedBeforePublication(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	frame := cacheTestIntCall("state-planned-prune-root")
	root, err := c.GetOrInitCall(ctx, "producer", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, ValueFunc(cacheTestIntResult(frame, 1)))
	assert.NilError(t, err)
	assert.NilError(t, c.ReleaseSession(ctx, "producer"))
	active := c.snapshotSessionResultIDs()
	snapshot, err := c.snapshotPruneStateCancelable(active, pruneSnapshotMetadata, 1, newPruneCancellationChecker(ctx))
	assert.NilError(t, err)
	candidates := c.collectPruneCandidates(ctx, 0, snapshot, pruneActiveClosure(snapshot, active), CachePrunePolicy{All: true}, time.Now())
	plan, _, _ := buildPrunePlan(snapshot, candidates, 1)
	assert.Equal(t, 1, len(plan))

	req := cacheTestArmedRequest(cacheTestIntCall("state-planned-prune-alias"))
	req.IsPersistable = true
	alias, err := c.GetOrInitCall(ctx, "consumer", noopTypeResolver{}, req, ValueFunc(root))
	assert.NilError(t, err)
	assert.Equal(t, CacheOutcomeExecuted, req.CacheEvidence.Outcome)
	assert.Equal(t, root.cacheSharedResult().id, alias.cacheSharedResult().id)
	_, removed, err := c.removePersistedEdge(ctx, plan[0].candidate.resultID)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	state, ok := req.CacheEvidence.ResultState(ctx, alias)
	assert.Assert(t, ok, "the consumer session owns the entry")
	assert.Assert(t, !state.Retained)
}

// A persistable hit creates or renews the edge before it returns, so its span
// reports retention; a prune that drops the edge before the span reads it
// leaves the span reporting none.
func TestCacheResultStatePersistableHit(t *testing.T) {
	t.Parallel()
	ctx, c := cacheTestEvidenceEnv(t)
	frame := cacheTestIntCall("state-hit")
	_, err := c.GetOrInitCall(ctx, "producer", noopTypeResolver{}, &CallRequest{ResultCall: frame}, ValueFunc(cacheTestIntResult(frame, 1)))
	assert.NilError(t, err)

	req := cacheTestArmedRequest(frame)
	req.IsPersistable = true
	req.TTL = 600
	hit, err := c.GetOrInitCall(ctx, "consumer", noopTypeResolver{}, req, func(context.Context) (AnyResult, error) {
		t.Error("a hit runs no resolver")
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	edge, found := stateTestEdge(c, hit)
	assert.Assert(t, found, "the persistable hit created the edge")
	state, ok := req.CacheEvidence.ResultState(ctx, hit)
	assert.Assert(t, ok)
	assert.Assert(t, state.Retained)
	assert.Equal(t, edge.expiresAtUnix, state.RetentionExpiresAtUnix)
	assert.Assert(t, req.CacheEvidence.pendingEdge == nil, "a hit publishes nothing")

	// A prune drops the edge between the hit and the span's end.
	_, removed, err := c.removePersistedEdge(ctx, hit.cacheSharedResult().id)
	assert.NilError(t, err)
	assert.Assert(t, removed)
	state, ok = req.CacheEvidence.ResultState(ctx, hit)
	assert.Assert(t, ok, "the consumer session still holds the result")
	assert.Assert(t, !state.Retained)
	assert.Equal(t, int64(0), state.RetentionExpiresAtUnix)
}

// A restored entry's first hit decodes its value, which adds explicit
// dependencies before the hitting call's span ends: the span reports them.
func TestCacheResultStateDecodeTimeDeps(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	cacheA, err := NewCache(t.Context(), dbPath, nil, nil)
	assert.NilError(t, err)
	ctxA := persistDecodeOwnerContext(t, cacheA, nil, "producer")
	moduleA := persistDecodeOwnerModule(t, ctxA, cacheA, "producer", "state-decode-module-a")
	srvA := newPersistDecodeOwnerTestServer(moduleA)
	ctxA = srvToContext(ctxA, srvA)
	resA, err := srvA.root.Select(ctxA, srvA, Selector{Field: "owned"})
	assert.NilError(t, err)
	rowID := uint64(resA.cacheSharedResult().id)
	assert.NilError(t, cacheA.ReleaseSession(ctxA, "producer"))
	assert.NilError(t, cacheA.Close(context.Background()))

	cacheB, err := NewCache(t.Context(), dbPath, nil, nil)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, cacheB.Close(context.Background())) })
	ctxB := persistDecodeOwnerContext(t, cacheB, nil, "decoder")
	moduleB := persistDecodeOwnerModule(t, ctxB, cacheB, "decoder", "state-decode-module-b")
	srvB := newPersistDecodeOwnerTestServer(moduleB)
	ctxB = srvToContext(ctxB, srvB)

	var (
		state   CacheResultState
		stateOK bool
		outcome CacheOutcome
	)
	srvB.Around(func(ctx context.Context, req *CallRequest) (context.Context, func(AnyResult, bool, *error)) {
		req.CacheEvidence = NewCacheDecision()
		return ctx, func(res AnyResult, _ bool, _ *error) {
			outcome = req.CacheEvidence.Outcome
			state, stateOK = req.CacheEvidence.ResultState(ctx, res)
		}
	})
	hit, err := srvB.root.Select(ctxB, srvB, Selector{Field: "owned"})
	assert.NilError(t, err)
	assert.Equal(t, rowID, uint64(hit.cacheSharedResult().id), "the restored entry keeps its number")
	assert.Equal(t, CacheOutcomeHit, outcome)
	assert.Assert(t, stateOK)
	assert.Assert(t, slices.Contains(state.Deps, uint64(moduleB.cacheSharedResult().id)), "deps %v name the decoder-supplied module", state.Deps)
	assert.Assert(t, state.Retained, "restore kept the entry's retention edge")
}

// A lazy evaluation's span names the entry it evaluated and reports its
// complete parts, the content digest it learned and the entry's dependencies.
func TestCacheResultStateLazyParts(t *testing.T) {
	t.Parallel()
	ctx, c := newPartsTestCache(t, nil)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { assert.NilError(t, tp.Shutdown(context.Background())) })
	spanCtx, demand := tp.Tracer("test").Start(ctx, "demand")
	defer demand.End()

	content := digest.FromString("state-lazy-content")
	obj := &cacheTestPartsObject{resolveFn: partsTestDirectResolve}
	var receiver ObjectResult[*cacheTestPartsObject]
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{partsTestGroupOut: func(ctx context.Context) error {
		host := c.partHostFor(receiver.cacheSharedResult())
		return host.RunNative(ctx, partsTestGroupOut, []PartKey{partsTestPartFS}, func(ctx context.Context) error {
			return host.SetContentDigestAfterEvaluation(ctx, content, call.ExtraDigestLabelContent)
		})
	}}
	receiver = newPartsTestResult(t, c, ctx, obj)
	assert.NilError(t, c.EvaluateParts(spanCtx, receiver, partsTestPartFS))

	fsKey, err := partAddressKey(PersistedPartAddress{Part: partsTestPartFS})
	assert.NilError(t, err)
	var lazy sdktrace.ReadOnlySpan
	for _, span := range rec.Ended() {
		for _, kv := range span.Attributes() {
			if kv.Key == telemetryattrs.CacheResultIDAttr {
				lazy = span
			}
		}
	}
	assert.Assert(t, lazy != nil, "the lazy span names its entry")
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range lazy.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	assert.Equal(t, strconv.FormatUint(uint64(receiver.cacheSharedResult().id), 10), attrs[telemetryattrs.CacheResultIDAttr].AsString())
	assert.DeepEqual(t, []string{fsKey}, attrs[telemetryattrs.CachePartsAttr].AsStringSlice())
	assert.Equal(t, content.String(), attrs[telemetryattrs.CacheOutputContentDigestAttr].AsString())
	deps, ok := attrs[telemetryattrs.CacheDepsAttr]
	assert.Assert(t, ok, "an empty dependency list is reported")
	assert.Equal(t, 0, len(deps.AsStringSlice()))
	_, hasOutcome := attrs[telemetryattrs.CacheOutcomeAttr]
	assert.Assert(t, !hasOutcome, "a lazy span carries no outcome")
}

// statePartValue has one filesystem part, "snapshot", complete once the value
// is evaluated. An eager value is created evaluated. A lazy one completes its
// part in a native lazy evaluation, through the part gate; block, when set,
// holds that evaluation until it closes. With absentMeta, the value has a
// second part, "meta", which the evaluation finds absent: its record reports
// it absent once the value is evaluated, and no task installs it.
type statePartValue struct {
	done       atomic.Bool
	lazy       bool
	absentMeta bool
	block      chan struct{}
	host       *PartHost
	rev        atomic.Uint64
}

type persistedStatePartValue struct {
	Done       bool `json:"done"`
	AbsentMeta bool `json:"absentMeta,omitempty"`
}

func (*statePartValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StatePartValue", NonNull: true}
}

func (v *statePartValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(persistedStatePartValue{Done: v.done.Load(), AbsentMeta: v.absentMeta})
	return PersistedObjectEncoding{JSON: raw}, err
}

func (*statePartValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	var persisted persistedStatePartValue
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, err
	}
	value := &statePartValue{absentMeta: persisted.AbsentMeta}
	value.done.Store(persisted.Done)
	return value, nil
}

func (v *statePartValue) PersistedOutputRevision() (OutputRevision, error) {
	return OutputRevision(v.rev.Load()), nil
}

func (v *statePartValue) BindPartHost(host *PartHost) { v.host = host }

func (v *statePartValue) LazyEvalFunc() LazyEvalFunc {
	if !v.lazy || v.done.Load() {
		return nil
	}
	return func(ctx context.Context) error {
		return v.host.RunNative(ctx, LazyGroupWhole, []PartKey{"snapshot"}, func(context.Context) error {
			if v.block != nil {
				<-v.block
			}
			v.done.Store(true)
			v.rev.Add(1)
			return nil
		})
	}
}

type statePartCodec struct{}

func (statePartCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (statePartCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (statePartCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var persisted persistedStatePartValue
	if err := json.Unmarshal(v.Payload, &persisted); err != nil {
		return nil, err
	}
	out := CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", ValueKind: "directory"}
	if persisted.Done {
		out.State = "completed"
	}
	outputs := []CapturedCodecOutput{out}
	if persisted.AbsentMeta {
		meta := CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: "meta"}, State: "pending", ValueKind: "snapshot"}
		if persisted.Done {
			meta.State = "absent"
		}
		outputs = append(outputs, meta)
	}
	return outputs, nil
}

func (statePartCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	outputs, err := (statePartCodec{}).MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]PartProbe, 0, len(outputs))
	for _, out := range outputs {
		// As core's probe does, an absent output is locally complete.
		probes = append(probes, PartProbe{Descriptor: PartDescriptor{Address: out.Address, Absent: out.State == "absent"}, LocalComplete: out.State == "completed" || out.State == "absent"})
	}
	return probes, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.StatePart", Typed: (*statePartValue)(nil),
		Visitor: PersistedNoReferences{}, Transfer: statePartCodec{},
	})
}

func statePartCache(t *testing.T, path string) (context.Context, *Cache, *Server) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, path, nil, nil)
	assert.NilError(t, err)
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*statePartValue]{}))
	return srvToContext(ContextWithCache(ctx, c), srv), c, srv
}

func statePartFrame(field string) *ResultCall {
	return &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&statePartValue{}).Type())}
}

// statePartHit hits the entry of field and returns the parts its span reports.
func statePartHit(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) (AnyResult, []string) {
	t.Helper()
	req := cacheTestArmedRequest(statePartFrame(field))
	req.IsPersistable = true
	hit, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		t.Errorf("%s: a hit runs no resolver", field)
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	state, ok := req.CacheEvidence.ResultState(ctx, hit)
	assert.Assert(t, ok)
	return hit, state.Parts
}

// A lazy evaluation that finds an output absent completes it without the gate
// settling it: the attempt installs only its other output. The attempt's lazy
// span still lists it, reading the entry's complete parts from its record, as
// a call span does.
func TestCacheResultStateLazyAbsentPart(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	metaKey, err := partAddressKey(PersistedPartAddress{Part: "meta"})
	assert.NilError(t, err)
	ctx, c, srv := statePartCache(t, filepath.Join(t.TempDir(), "cache.db"))
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { assert.NilError(t, tp.Shutdown(context.Background())) })

	lazy := persistedListTestResult(t, ctx, c, srv, "lazy-absent", &statePartValue{lazy: true, absentMeta: true})
	spanCtx, demand := tp.Tracer("test").Start(ctx, "demand")
	assert.NilError(t, c.Evaluate(spanCtx, lazy))
	demand.End()
	assert.DeepEqual(t, []string{snapshotKey}, lazy.cacheSharedResult().settledPartKeys())

	var lazySpans []sdktrace.ReadOnlySpan
	for _, span := range rec.Ended() {
		for _, kv := range span.Attributes() {
			if kv.Key == telemetryattrs.CacheResultIDAttr {
				lazySpans = append(lazySpans, span)
			}
		}
	}
	assert.Equal(t, 1, len(lazySpans), "one lazy span names the entry")
	var parts []string
	for _, kv := range lazySpans[0].Attributes() {
		if kv.Key == telemetryattrs.CachePartsAttr {
			parts = kv.Value.AsStringSlice()
		}
	}
	want := []string{metaKey, snapshotKey}
	slices.Sort(want)
	assert.DeepEqual(t, want, parts)
}

// An entry's complete parts are those the part probe reports locally complete
// in its record, whether the value completed eagerly, at creation, or in a
// lazy evaluation through the gate. A hit reports the same parts before and
// after a clean restart restores the entry.
func TestCacheResultStatePartsSurviveRestart(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	path := filepath.Join(t.TempDir(), "cache.db")

	ctx, c, srv := statePartCache(t, path)
	eager := &statePartValue{}
	eager.done.Store(true)
	persistedListTestResult(t, ctx, c, srv, "eager", eager)
	lazy := persistedListTestResult(t, ctx, c, srv, "lazy", &statePartValue{lazy: true})
	_, before := statePartHit(t, ctx, c, srv, "lazy")
	assert.Equal(t, 0, len(before), "an unevaluated lazy value has no complete part")
	assert.NilError(t, c.Evaluate(ctx, lazy))

	parts := map[string][]string{}
	for _, field := range []string{"eager", "lazy"} {
		_, parts[field] = statePartHit(t, ctx, c, srv, field)
		assert.DeepEqual(t, []string{snapshotKey}, parts[field])
	}
	assert.DeepEqual(t, []string{snapshotKey}, lazy.cacheSharedResult().settledPartKeys())
	assert.Equal(t, 0, len(eager.host.row.settledPartKeys()), "the gate never saw the eager value's part")
	assert.NilError(t, c.ReleaseSession(ctx, "test-session"))
	assert.NilError(t, c.Close(context.Background()))

	ctx, c, srv = statePartCache(t, path)
	t.Cleanup(func() { assert.NilError(t, c.Close(context.Background())) })
	for _, field := range []string{"eager", "lazy"} {
		hit, restored := statePartHit(t, ctx, c, srv, field)
		assert.DeepEqual(t, parts[field], restored)
		assert.Equal(t, 0, len(hit.cacheSharedResult().settledPartKeys()), "restored parts come from the record")
	}
}

// stateTwoPartValue has two filesystem parts, "meta" and "fs", each complete
// once its own lazy group has been evaluated through the part gate. block,
// when set, holds the fs group's evaluation until it closes.
type stateTwoPartValue struct {
	meta, fs atomic.Bool
	block    chan struct{}
	host     *PartHost
	rev      atomic.Uint64
}

type persistedStateTwoPartValue struct {
	Meta bool `json:"meta"`
	FS   bool `json:"fs"`
}

func (*stateTwoPartValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StateTwoPartValue", NonNull: true}
}

func (v *stateTwoPartValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(persistedStateTwoPartValue{Meta: v.meta.Load(), FS: v.fs.Load()})
	return PersistedObjectEncoding{JSON: raw}, err
}

func (*stateTwoPartValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	var persisted persistedStateTwoPartValue
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, err
	}
	value := &stateTwoPartValue{}
	value.meta.Store(persisted.Meta)
	value.fs.Store(persisted.FS)
	return value, nil
}

func (v *stateTwoPartValue) PersistedOutputRevision() (OutputRevision, error) {
	return OutputRevision(v.rev.Load()), nil
}

func (v *stateTwoPartValue) BindPartHost(host *PartHost) { v.host = host }

func (v *stateTwoPartValue) LazyEvalFunc() LazyEvalFunc {
	if v.meta.Load() && v.fs.Load() {
		return nil
	}
	return func(context.Context) error {
		return errors.New("whole-result body ran for a parts value")
	}
}

func (v *stateTwoPartValue) ResolveLazyEvalGroups(_ context.Context, _ AnyResult, parts []PartKey) ([]LazyGroupKey, error) {
	if parts == nil {
		return []LazyGroupKey{"meta", "fs"}, nil
	}
	groups := make([]LazyGroupKey, len(parts))
	for i, part := range parts {
		groups[i] = LazyGroupKey(part)
	}
	return groups, nil
}

func (v *stateTwoPartValue) LazyEvalFuncForGroup(group LazyGroupKey) LazyEvalFunc {
	done := &v.meta
	if group == "fs" {
		done = &v.fs
	}
	if done.Load() {
		return nil
	}
	return func(ctx context.Context) error {
		return v.host.RunNative(ctx, group, []PartKey{PartKey(group)}, func(context.Context) error {
			if group == "fs" && v.block != nil {
				<-v.block
			}
			done.Store(true)
			v.rev.Add(1)
			return nil
		})
	}
}

type stateTwoPartCodec struct{}

func (stateTwoPartCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (stateTwoPartCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (stateTwoPartCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var persisted persistedStateTwoPartValue
	if err := json.Unmarshal(v.Payload, &persisted); err != nil {
		return nil, err
	}
	var out []CapturedCodecOutput
	for part, done := range map[PartKey]bool{"meta": persisted.Meta, "fs": persisted.FS} {
		o := CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: part}, State: "pending", ValueKind: "directory"}
		if done {
			o.State = "completed"
		}
		out = append(out, o)
	}
	return out, nil
}

func (stateTwoPartCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	outputs, err := (stateTwoPartCodec{}).MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]PartProbe, 0, len(outputs))
	for _, out := range outputs {
		probes = append(probes, PartProbe{Descriptor: PartDescriptor{Address: out.Address}, LocalComplete: out.State == "completed"})
	}
	return probes, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.StateTwoPart", Typed: (*stateTwoPartValue)(nil),
		Visitor: PersistedNoReferences{}, Transfer: stateTwoPartCodec{},
	})
}

// While an evaluation of the entry is in flight its record cannot be read;
// the span then reports the parts the gate has already settled, without
// waiting. Once the evaluation is done, the record gives every part.
func TestCacheResultStatePartsNotReady(t *testing.T) {
	t.Parallel()
	metaKey, err := partAddressKey(PersistedPartAddress{Part: "meta"})
	assert.NilError(t, err)
	fsKey, err := partAddressKey(PersistedPartAddress{Part: "fs"})
	assert.NilError(t, err)
	ctx, c, srv := statePartCache(t, "")
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	srv.InstallObject(NewClass(srv, ClassOpts[*stateTwoPartValue]{}))
	block := make(chan struct{})
	res := persistedListTestResult(t, ctx, c, srv, "not-ready", &stateTwoPartValue{block: block})
	hit := func() []string {
		t.Helper()
		frame := &ResultCall{Kind: ResultCallKindField, Field: "not-ready", Type: NewResultCallType((&stateTwoPartValue{}).Type())}
		req := cacheTestArmedRequest(frame)
		req.IsPersistable = true
		hit, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
			t.Error("a hit runs no resolver")
			return nil, nil
		})
		assert.NilError(t, err)
		state, ok := req.CacheEvidence.ResultState(ctx, hit)
		assert.Assert(t, ok)
		return state.Parts
	}

	// The meta group settles; the fs group's evaluation then holds. No span
	// has read the entry yet, so nothing is cached for it.
	assert.NilError(t, c.EvaluateParts(ctx, res, "meta"))
	evaluated := make(chan error, 1)
	go func() { evaluated <- c.EvaluateParts(ctx, res, "fs") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		shared := res.cacheSharedResult()
		shared.lazyMu.Lock()
		running := shared.lazyPartGroups["fs"] != nil && shared.lazyPartGroups["fs"].attempt != nil
		shared.lazyMu.Unlock()
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the fs evaluation to start")
		}
		time.Sleep(time.Millisecond)
	}
	// The settled part, read without waiting on the running one.
	assert.DeepEqual(t, []string{metaKey}, hit())

	close(block)
	assert.NilError(t, <-evaluated)
	// Every part, from the record.
	assert.DeepEqual(t, []string{fsKey, metaKey}, sortedStrings(hit()))
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

// A part installed from an offer reaches the entry's record in the same step
// that bumps its revision, so a span after the install reports it, although
// an earlier span cached the entry's parts.
func TestCacheResultStatePartsAfterOfferInstall(t *testing.T) {
	chain := newChainFixture(t, "state-offer")
	f := newExhaustionFixture(t, chain)
	f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
	ev := &CacheDecision{cache: f.cache}

	state, ok := ev.ResultState(f.ctx, f.receiver)
	assert.Assert(t, ok)
	assert.Equal(t, 0, len(state.Parts), "the offered part is not complete yet")
	assert.Assert(t, f.receiver.cacheSharedResult().completeParts.Load() != nil, "the span cached the entry's parts")

	assert.NilError(t, f.run())
	assert.Assert(t, f.installed(), "the part installed from its offer")
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)
	state, ok = ev.ResultState(f.ctx, f.receiver)
	assert.Assert(t, ok)
	assert.DeepEqual(t, []string{snapshotKey}, state.Parts)
}
