package dagql

import (
	"context"
	"slices"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// A lazy result's evaluations in a session carry its cause links once. The
// first links the call that returned the result and each of its install
// spans; each later one links only that first one, the anchor. When the
// result gains an install span the anchor doesn't link, the next evaluation
// links them all and becomes the anchor. Part demand runs many evaluations
// of one result, so links per evaluation must not grow with its installs.
// Each anchor is marked as one, so a reader can tell that later evaluations
// link it rather than the call.
func TestLazyEvaluationsLinkTheirAnchor(t *testing.T) {
	t.Parallel()
	const resultID = sharedResultID(7)
	sr, rootCtx, root := newLazyRecordingRoot("POST /query")
	defer root.End()
	c := &Cache{}
	span := func(name string) trace.SpanContext {
		_, s := Tracer(rootCtx).Start(rootCtx, name)
		s.End()
		return s.SpanContext()
	}
	// startSession records the call that returned the result in a session,
	// and the spans that installed it there.
	startSession := func(sessionID string, installs int) (trace.SpanContext, []trace.SpanContext) {
		producer := span("Container.withExec")
		c.sessionMu.Lock()
		if c.sessionLazySpansBySession == nil {
			c.sessionLazySpansBySession = map[string]map[sharedResultID]trace.SpanContext{}
		}
		c.sessionLazySpansBySession[sessionID] = map[sharedResultID]trace.SpanContext{resultID: producer}
		c.sessionMu.Unlock()
		var installCtxs []trace.SpanContext
		for range installs {
			installCtx := span("Container.directory")
			c.recordSessionResultInstallSpanLocked(t.Context(), sessionID, resultID, installCtx, false)
			installCtxs = append(installCtxs, installCtx)
		}
		return producer, installCtxs
	}
	evaluate := func(sessionID string, group LazyGroupKey) sdktrace.ReadOnlySpan {
		t.Helper()
		evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "client-1"})
		_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, group, &ResultCall{Field: "withExec"}, "")
		assert.Assert(t, isResume)
		var err error
		endOTelLazyOp(lazySpan, nil, isResume, resultID, group != LazyGroupWhole, false, "", lazySpanCacheState{}, &err)
		return spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
	}
	linked := func(s sdktrace.ReadOnlySpan) []string {
		var ids []string
		for _, link := range s.Links() {
			ids = append(ids, link.SpanContext.SpanID().String())
		}
		slices.Sort(ids)
		return ids
	}
	isAnchor := func(s sdktrace.ReadOnlySpan) bool {
		for _, attr := range s.Attributes() {
			if string(attr.Key) == telemetryattrs.LazyAnchorAttr {
				return attr.Value.AsBool()
			}
		}
		return false
	}
	ids := func(ctxs ...trace.SpanContext) []string {
		var ids []string
		for _, ctx := range ctxs {
			ids = append(ids, ctx.SpanID().String())
		}
		slices.Sort(ids)
		return ids
	}

	producer, installs := startSession("session-a", 50)
	anchor := evaluate("session-a", "metadata")
	assert.DeepEqual(t, linked(anchor), ids(append([]trace.SpanContext{producer}, installs...)...))
	assert.Assert(t, isAnchor(anchor), "the first evaluation is marked as the anchor")
	for _, group := range []LazyGroupKey{"execOutputs", "metadata", LazyGroupWhole, "execOutputs"} {
		later := evaluate("session-a", group)
		assert.DeepEqual(t, linked(later), ids(anchor.SpanContext()))
		assert.Assert(t, !isAnchor(later), "a later evaluation is not marked")
	}

	installs = append(installs, span("Container.stdout"))
	c.recordSessionResultInstallSpanLocked(t.Context(), "session-a", resultID, installs[len(installs)-1], false)
	regrown := evaluate("session-a", "execOutputs")
	assert.DeepEqual(t, linked(regrown), ids(append([]trace.SpanContext{producer}, installs...)...))
	assert.Assert(t, isAnchor(regrown), "an evaluation that takes the anchor's place is marked")
	assert.DeepEqual(t, linked(evaluate("session-a", "metadata")), ids(regrown.SpanContext()))

	// Each session has its own anchor.
	producerB, installsB := startSession("session-b", 3)
	anchorB := evaluate("session-b", "metadata")
	assert.DeepEqual(t, linked(anchorB), ids(append([]trace.SpanContext{producerB}, installsB...)...))
	assert.Assert(t, isAnchor(anchorB))
	assert.DeepEqual(t, linked(evaluate("session-b", "execOutputs")), ids(anchorB.SpanContext()))
	assert.DeepEqual(t, linked(evaluate("session-a", "execOutputs")), ids(regrown.SpanContext()))
}

// Releasing a session drops its lazy evaluation anchors with its other
// telemetry state.
func TestLazyEvaluationAnchorsReleasedWithSession(t *testing.T) {
	t.Parallel()
	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithCache(baseCtx, c)
	sessionID := cacheTestSessionID(t, ctx)

	sr, rootCtx, root := newLazyRecordingRoot("POST /query")
	defer root.End()
	_, producer := Tracer(rootCtx).Start(rootCtx, "Container.withExec")
	producer.End()
	reqCall := cacheTestIntCall("lazy-anchor-release")
	res, err := c.GetOrInitCall(trace.ContextWithSpanContext(ctx, producer.SpanContext()), sessionID, noopTypeResolver{}, &CallRequest{ResultCall: reqCall}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(reqCall, 1), nil
	})
	assert.NilError(t, err)
	resultID := res.cacheSharedResult().id

	evalCtx := engine.ContextWithClientMetadata(rootCtx, &engine.ClientMetadata{SessionID: sessionID, ClientID: "client-1"})
	_, lazySpan, isResume := c.beginOTelLazyOp(evalCtx, resultID, LazyGroupWhole, reqCall, "")
	assert.Assert(t, isResume)
	var evalErr error
	endOTelLazyOp(lazySpan, nil, isResume, resultID, false, false, "", lazySpanCacheState{}, &evalErr)
	spanBySpanID(t, sr.Ended(), lazySpan.SpanContext().SpanID())
	_, anchored := c.sessionLazyAnchorFor(t.Context(), sessionID, resultID, nil)
	assert.Assert(t, anchored)

	cacheTestReleaseSession(t, c, ctx)
	_, anchored = c.sessionLazyAnchorFor(t.Context(), sessionID, resultID, nil)
	assert.Assert(t, !anchored)
}
