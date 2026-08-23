package clientdb

import (
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

func TestStoreStreamsAndSpanQueries(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	spans := []Span{
		{TraceID: "trace-a", SpanID: "root", Attributes: []byte("root")},
		{TraceID: "trace-a", SpanID: "child", ParentSpanID: validString("root"), Attributes: []byte("first")},
		{TraceID: "trace-a", SpanID: "child", ParentSpanID: validString("root"), Attributes: []byte("final")},
		{TraceID: "trace-a", SpanID: "grandchild", ParentSpanID: validString("child")},
		{TraceID: "trace-a", SpanID: "other", ParentSpanID: validString("elsewhere")},
		// A repeated span ID in another trace must not change trace-a's
		// first-row SelectSpan result.
		{TraceID: "trace-b", SpanID: "child", Attributes: []byte("other-trace")},
	}
	stats, err := store.AppendSpans(spans)
	require.NoError(t, err)
	require.Equal(t, int64(len(spans)), stats.LastID)

	logs := []Log{
		{TraceID: validString("trace-a"), SpanID: validString("root"), Body: []byte("root's own log included")},
		{TraceID: validString("trace-a"), SpanID: validString("child"), Body: []byte("child one")},
		{TraceID: validString("trace-a"), SpanID: validString("other"), Body: []byte("other excluded")},
		{TraceID: validString("trace-a"), SpanID: validString("grandchild"), Body: []byte("grandchild")},
		{TraceID: validString("trace-a"), SpanID: validString("child"), Body: []byte("child two")},
		{Body: []byte("no span excluded")},
	}
	stats, err = store.AppendLogs(logs)
	require.NoError(t, err)
	require.Equal(t, int64(len(logs)), stats.LastID)

	stats, err = store.AppendMetrics([]Metric{{Data: []byte("one")}, {Data: []byte("two")}})
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.LastID)

	span, err := store.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace-a", SpanID: "child"})
	require.NoError(t, err)
	require.Equal(t, int64(2), span.ID)
	require.Equal(t, []byte("first"), span.Attributes)

	span, err = store.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace-b", SpanID: "child"})
	require.NoError(t, err)
	require.Equal(t, int64(6), span.ID)
	require.Equal(t, []byte("other-trace"), span.Attributes)

	_, err = store.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace-a", SpanID: "missing"})
	require.ErrorIs(t, err, sql.ErrNoRows)

	// The capture includes the root's own rows (1) and its subtree's (2, 4,
	// 5); the unrelated span (3) and the span-less record (6) stay out.
	page, err := store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString("root"),
		Limit:  2,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, logIDs(page))

	page, err = store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString("root"),
		ID:     4,
		Limit:  2,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{5}, logIDs(page))

	metrics, err := store.Read().SelectMetricsSince(t.Context(), SelectMetricsSinceParams{Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: []byte("one")}, {ID: 2, Data: []byte("two")}}, metrics)
}

func TestStoreSelectorsHonorNonPositiveLimits(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	_, err = store.AppendSpans([]Span{{TraceID: "trace", SpanID: "span"}})
	require.NoError(t, err)
	spans, err := store.SelectSpansSince(t.Context(), SelectSpansSinceParams{Limit: 0})
	require.NoError(t, err)
	require.Empty(t, spans)
}

// TestSelectLogsBeneathSpanFollowsCausalLinks covers the cause-link edges in
// the log-scope walk, mirroring how dagui folds a cause-linking span into the
// linked span's subtree: a service's long-lived exec span is parented under
// whatever call triggered the start, but cause-links to the API spans that
// installed the Service value (e.g. Container.asService) — and the service's
// stdio log records are attributed to those install spans (core/service.go
// routes them there). Reading beneath EITHER handle — the install span or the
// exec span — must return the same lines: the stdio rows on the install span
// plus the exec span's subtree (e.g. its healthcheck spans).
func TestSelectLogsBeneathSpanFollowsCausalLinks(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	// Link targets round-trip through OTLP span IDs, so they must be valid
	// 16-hex-char IDs; plain names suffice everywhere else.
	const (
		traceID      = "000102030405060708090a0b0c0d0e0f"
		installSpan  = "00000000000000aa" // Container.asService: produced the Service
		trigger      = "00000000000000bb" // the call that triggered the start
		serviceSpan  = "00000000000000cc" // the long-lived exec span
		serviceChild = "00000000000000dd" // e.g. the exec's healthcheck span
		waiter       = "00000000000000ee" // wait-links to installSpan; NOT causal
	)

	spans := []Span{
		{TraceID: traceID, SpanID: installSpan},
		{TraceID: traceID, SpanID: trigger},
		// The service span's first snapshot has no links yet; a later snapshot
		// carries the cause link added while the span was live
		// (RunningService.addOriginSpanContexts). The duplicate-suppressed
		// children index must not swallow the late edge.
		{TraceID: traceID, SpanID: serviceSpan, ParentSpanID: validString(trigger)},
		{TraceID: traceID, SpanID: serviceSpan, ParentSpanID: validString(trigger), Links: linksJSON(t,
			spanLink(t, traceID, installSpan, telemetry.LinkPurposeCause),
			// self-links must not create self-edges
			spanLink(t, traceID, serviceSpan, telemetry.LinkPurposeCause),
		)},
		{TraceID: traceID, SpanID: serviceChild, ParentSpanID: validString(serviceSpan)},
		// Non-causal purposes (wait, error_origin) are not containment edges.
		{TraceID: traceID, SpanID: waiter, Links: linksJSON(t,
			spanLink(t, traceID, installSpan, telemetryattrs.LinkPurposeWait),
		)},
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)

	logs := []Log{
		{TraceID: validString(traceID), SpanID: validString(installSpan), Body: []byte("service stdio, routed to the install span")},
		{TraceID: validString(traceID), SpanID: validString(serviceSpan), Body: []byte("service span's own log")},
		{TraceID: validString(traceID), SpanID: validString(serviceChild), Body: []byte("healthcheck")},
		{TraceID: validString(traceID), SpanID: validString(trigger), Body: []byte("trigger's own log")},
		{TraceID: validString(traceID), SpanID: validString(waiter), Body: []byte("waiter's own log")},
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)

	// Beneath the install span: its own stdio rows, the cause-linked service
	// span, and the service's subtree.
	page, err := store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(installSpan),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, logIDs(page))

	// Beneath the service exec span: the SAME capture. The stdio rows live on
	// the install span the exec span cause-links to, so the walk must seed
	// from the link's other end too — this is what lets ReadLogs on the span
	// ID that ListServices surfaces reach the container's stdout/stderr.
	page, err = store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(serviceSpan),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, logIDs(page))

	// The plain tree walk still works: beneath the trigger sit its own rows
	// and the service span's subtree — but NOT the install span's stdio rows.
	// Reverse cause edges seed only from the capture root, not from spans
	// merely reached during the walk, or every capture containing the exec
	// span would drag in the install span's rows.
	page, err = store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(trigger),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3, 4}, logIDs(page))

	// A wait link is not a containment edge in either direction: the waiter's
	// capture holds only its own rows.
	page, err = store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(waiter),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{5}, logIDs(page))
}

// TestHasDescendants covers the cheap in-memory pre-filter: it must agree
// with the descendant walk the log queries use (child edges plus
// cause-purpose links) without materializing the subtree.
func TestHasDescendants(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	const (
		traceID     = "000102030405060708090a0b0c0d0e0f"
		installSpan = "00000000000000aa" // reached only via a cause link
		trigger     = "00000000000000bb"
		serviceSpan = "00000000000000cc"
		leaf        = "00000000000000dd"
		waiter      = "00000000000000ee" // wait-links only: not a containment edge
		selfParent  = "00000000000000ff" // malformed: parented to itself
	)

	_, err = store.AppendSpans([]Span{
		{TraceID: traceID, SpanID: installSpan},
		{TraceID: traceID, SpanID: trigger},
		{TraceID: traceID, SpanID: serviceSpan, ParentSpanID: validString(trigger), Links: linksJSON(t,
			spanLink(t, traceID, installSpan, telemetry.LinkPurposeCause),
		)},
		{TraceID: traceID, SpanID: leaf, ParentSpanID: validString(serviceSpan)},
		{TraceID: traceID, SpanID: waiter, Links: linksJSON(t,
			spanLink(t, traceID, installSpan, telemetryattrs.LinkPurposeWait),
		)},
		{TraceID: traceID, SpanID: selfParent, ParentSpanID: validString(selfParent)},
	})
	require.NoError(t, err)

	// Plain child edge.
	require.True(t, store.HasDescendants(trigger))
	require.True(t, store.HasDescendants(serviceSpan))
	// Cause-link edge, same as the descendant walk.
	require.True(t, store.HasDescendants(installSpan))
	// Leaves, unknown spans and wait-links have nothing beneath them.
	require.False(t, store.HasDescendants(leaf))
	require.False(t, store.HasDescendants(waiter))
	require.False(t, store.HasDescendants("00000000000000a1"))
	// A self-parented row is not its own descendant.
	require.False(t, store.HasDescendants(selfParent))

	// Agreement with the walk it pre-filters for: a span has descendants iff
	// the log scope reaches beyond its seeds (the span itself plus the
	// cause-link targets it sits beside — those are containment, not
	// nesting, and don't count as descendants).
	for _, spanID := range []string{installSpan, trigger, serviceSpan, leaf, waiter, selfParent} {
		scope := store.lookup.logScope(spanID)
		delete(scope, spanID)
		for _, target := range store.lookup.causalParents[spanID] {
			delete(scope, target)
		}
		require.Equal(t, len(scope) > 0, store.HasDescendants(spanID),
			"HasDescendants(%s) disagrees with logScope()", spanID)
	}
}

// TestScopedSpanSelectors covers the index-backed queries a scoped trace
// load is built from: latest-snapshot span reads, ancestor closures, and the
// check/test marker index.
func TestScopedSpanSelectors(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	spans := []Span{
		{TraceID: "trace-a", SpanID: "root"},
		{TraceID: "trace-a", SpanID: "mid", ParentSpanID: validString("root")},
		// Two snapshots: the scoped load must see only the newest.
		{TraceID: "trace-a", SpanID: "leaf", ParentSpanID: validString("mid"), Attributes: []byte("live")},
		{TraceID: "trace-a", SpanID: "leaf", ParentSpanID: validString("mid"), Attributes: []byte("ended")},
		// Check/test markers are byte-scanned from the encoded attributes.
		{
			TraceID: "trace-a", SpanID: "check", ParentSpanID: validString("root"),
			Attributes: []byte(`[{"key":"` + telemetry.CheckNameAttr + `","value":{"stringValue":"lint"}}]`),
		},
		{
			TraceID: "trace-a", SpanID: "test", ParentSpanID: validString("root"),
			Attributes: []byte(`[{"key":"test.case.name","value":{"stringValue":"TestFoo"}}]`),
		},
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)

	require.True(t, store.HasSpan("leaf"))
	require.False(t, store.HasSpan("missing"))

	// Latest rows, in append order, unknown IDs skipped.
	rows, err := store.SelectSpansLatest(t.Context(), setOf("leaf", "root", "missing"))
	require.NoError(t, err)
	require.Equal(t, []int64{1, 4}, spanRowIDs(rows))
	require.Equal(t, []byte("ended"), rows[1].Attributes)

	// The closure walks each member's chain to its root and dedupes.
	closure := store.AncestorClosure(setOf("leaf", "check"))
	require.ElementsMatch(t, []string{"leaf", "mid", "check", "root"}, keysOf(closure))

	checks, tests := store.CheckTestSpanIDs()
	require.ElementsMatch(t, []string{"check"}, keysOf(checks))
	require.ElementsMatch(t, []string{"test"}, keysOf(tests))
}

// TestSelectLogsForSpans covers the scoped log fetch: append order across
// spans, unknown spans skipped, and the per-span tail cap.
func TestSelectLogsForSpans(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", 256)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	logs := []Log{
		{SpanID: validString("a"), Body: []byte("a1")},
		{SpanID: validString("b"), Body: []byte("b1")},
		{SpanID: validString("a"), Body: []byte("a2")},
		{SpanID: validString("c"), Body: []byte("c1")},
		{SpanID: validString("a"), Body: []byte("a3")},
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)

	page, err := store.SelectLogsForSpans(t.Context(), setOf("a", "b", "missing"), 0)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3, 5}, logIDs(page))

	// The tail cap keeps each span's newest rows, in global append order.
	page, err = store.SelectLogsForSpans(t.Context(), setOf("a", "b"), 2)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3, 5}, logIDs(page))
}

func TestStoreCheckpointAndBoundedRanges(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	_, err = store.AppendSpans([]Span{{TraceID: "trace", SpanID: "one"}, {TraceID: "trace", SpanID: "two"}})
	require.NoError(t, err)
	_, err = store.AppendLogs([]Log{{Body: []byte("one")}, {Body: []byte("two")}})
	require.NoError(t, err)
	_, err = store.AppendMetrics([]Metric{{Data: []byte("one")}, {Data: []byte("two")}})
	require.NoError(t, err)

	var syncs atomic.Int32
	store.spans.spill.testSyncHook = func() error { syncs.Add(1); return nil }
	store.logs.spill.testSyncHook = func() error { syncs.Add(1); return nil }
	store.metrics.spill.testSyncHook = func() error { syncs.Add(1); return nil }

	highWater, err := store.Checkpoint(t.Context())
	require.NoError(t, err)
	require.Equal(t, HighWater{Spans: 2, Logs: 2, Metrics: 2}, highWater)
	require.Equal(t, int32(3), syncs.Load())
	require.Empty(t, store.spans.tail)
	require.Empty(t, store.logs.tail)
	require.Empty(t, store.metrics.tail)
	require.Equal(t, highWater.Spans, store.spans.spill.committedLastID)
	require.Equal(t, highWater.Logs, store.logs.spill.committedLastID)
	require.Equal(t, highWater.Metrics, store.metrics.spill.committedLastID)

	_, err = store.AppendSpans([]Span{{TraceID: "trace", SpanID: "after"}})
	require.NoError(t, err)
	_, err = store.AppendLogs([]Log{{Body: []byte("after")}})
	require.NoError(t, err)
	_, err = store.AppendMetrics([]Metric{{Data: []byte("after")}})
	require.NoError(t, err)
	require.Equal(t, HighWater{Spans: 3, Logs: 3, Metrics: 3}, store.HighWater())

	spans, err := store.SelectSpansRange(t.Context(), SelectSpansRangeParams{
		AfterID:   0,
		ThroughID: highWater.Spans,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, spanRowIDs(spans))
	logs, err := store.SelectLogsRange(t.Context(), SelectLogsRangeParams{
		AfterID:   1,
		ThroughID: highWater.Logs,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2}, logIDs(logs))
	metrics, err := store.SelectMetricsRange(t.Context(), SelectMetricsRangeParams{
		AfterID:   2,
		ThroughID: highWater.Metrics,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Empty(t, metrics)
}

func TestStoreCheckpointPropagatesSyncFailure(t *testing.T) {
	store, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	_, err = store.AppendLogs([]Log{{Body: []byte("must be durable")}})
	require.NoError(t, err)
	injected := errors.New("injected sync failure")
	store.logs.spill.testSyncHook = func() error { return injected }

	highWater, err := store.Checkpoint(t.Context())
	require.Equal(t, HighWater{Logs: 1}, highWater)
	require.ErrorIs(t, err, injected)
	require.ErrorContains(t, err, "checkpoint logs")
}

func TestArchiveIndexesAreTraceScopedAndRecover(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)

	agentAttrs := testAgentIdentityAttrs(t, "agent")
	_, err = store.AppendSpans([]Span{
		{TraceID: "trace-a", SpanID: "root-a"},
		{TraceID: "trace-a", SpanID: "shared", ParentSpanID: validString("root-a"), Attributes: agentAttrs},
		{TraceID: "trace-b", SpanID: "root-b"},
		{TraceID: "trace-b", SpanID: "shared", ParentSpanID: validString("root-b"), Attributes: agentAttrs},
		{TraceID: "trace-a", SpanID: "shared", ParentSpanID: validString("root-a"), Attributes: []byte("latest-a")},
	})
	require.NoError(t, err)

	payloadA, digest := testCallPayloadLog(t, "trace-a", "shared", "resume")
	payloadB, digestB := testCallPayloadLog(t, "trace-b", "shared", "resume")
	require.Equal(t, digest, digestB)
	_, err = store.AppendLogs([]Log{
		{TraceID: validString("trace-a"), SpanID: validString("shared"), Body: []byte("a")},
		{TraceID: validString("trace-b"), SpanID: validString("shared"), Body: []byte("b")},
		payloadA,
		payloadB,
	})
	require.NoError(t, err)
	require.NoError(t, store.closeStreams())

	store, err = openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.closeStreams()) }()

	require.Equal(t, []string{"shared"}, store.AgentIdentitySpanIDs("trace-a"))
	require.Equal(t, []string{"shared"}, store.AgentIdentitySpanIDs("trace-b"))
	require.Empty(t, store.AgentIdentitySpanIDs("trace-c"))
	require.ElementsMatch(t, []string{"root-a", "shared"}, keysOf(store.AncestorClosureForTrace("trace-a", setOf("shared"))))
	require.ElementsMatch(t, []string{"root-b", "shared"}, keysOf(store.AncestorClosureForTrace("trace-b", setOf("shared"))))

	spans, err := store.SelectSpansLatestForTrace(t.Context(), "trace-a", setOf("shared"))
	require.NoError(t, err)
	require.Equal(t, []int64{5}, spanRowIDs(spans))
	logs, err := store.SelectLogsForTraceSpans(t.Context(), "trace-a", setOf("shared"), 0)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, logIDs(logs))
	logs, err = store.SelectLogsForTraceSpans(t.Context(), "trace-b", setOf("shared"), 0)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4}, logIDs(logs))

	payload, err := store.SelectCallPayload(t.Context(), "trace-a", digest)
	require.NoError(t, err)
	require.Equal(t, int64(3), payload.ID)
	payload, err = store.SelectCallPayload(t.Context(), "trace-b", digest)
	require.NoError(t, err)
	require.Equal(t, int64(4), payload.ID)
	_, err = store.SelectCallPayload(t.Context(), "trace-c", digest)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func testAgentIdentityAttrs(t *testing.T, agentID string) []byte {
	t.Helper()
	attrs, err := MarshalProtoJSONs([]*otlpcommonv1.KeyValue{
		{
			Key: telemetryattrs.AgentAttr,
			Value: &otlpcommonv1.AnyValue{
				Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true},
			},
		},
		{
			Key: telemetryattrs.AgentIDAttr,
			Value: &otlpcommonv1.AnyValue{
				Value: &otlpcommonv1.AnyValue_StringValue{StringValue: agentID},
			},
		},
	})
	require.NoError(t, err)
	return attrs
}

func testCallPayloadLog(t *testing.T, traceID, spanID, field string) (Log, string) {
	t.Helper()
	callPB := &callpbv1.Call{Field: field}
	dgst, err := call.CanonicalDigest(callPB)
	require.NoError(t, err)
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(callPB)
	require.NoError(t, err)
	body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
	require.NoError(t, err)
	attrs, err := MarshalProtoJSONs([]*otlpcommonv1.KeyValue{{
		Key:   telemetryattrs.DagCallPayloadAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}},
	}})
	require.NoError(t, err)
	scope, err := protojson.Marshal(&otlpcommonv1.InstrumentationScope{Name: telemetryattrs.CallPayloadInstrumentationScope})
	require.NoError(t, err)
	return Log{
		TraceID:              validString(traceID),
		SpanID:               validString(spanID),
		Body:                 body,
		Attributes:           attrs,
		InstrumentationScope: scope,
	}, dgst.String()
}

func setOf(ids ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func keysOf(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	return keys
}

func spanRowIDs(rows []Span) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func spanLink(t *testing.T, traceID, spanID, purpose string) sdktrace.Link {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	require.NoError(t, err)
	sid, err := trace.SpanIDFromHex(spanID)
	require.NoError(t, err)
	link := sdktrace.Link{
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: tid,
			SpanID:  sid,
		}),
	}
	if purpose != "" {
		link.Attributes = []attribute.KeyValue{
			attribute.String(telemetry.LinkPurposeAttr, purpose),
		}
	}
	return link
}

func linksJSON(t *testing.T, links ...sdktrace.Link) []byte {
	t.Helper()
	data, err := MarshalProtoJSONs(telemetry.SpanLinksToPB(links))
	require.NoError(t, err)
	return data
}

func validString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func logIDs(logs []Log) []int64 {
	ids := make([]int64, len(logs))
	for i, row := range logs {
		ids[i] = row.ID
	}
	return ids
}
