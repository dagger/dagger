package clientdb

import (
	"database/sql"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

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
		{TraceID: validString("trace-a"), SpanID: validString("root"), Body: []byte("root excluded")},
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

	page, err := store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString("root"),
		Limit:  2,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4}, logIDs(page))

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
// the descendant walk, mirroring how dagui renders them as parent→child: a
// service's long-lived exec span is parented under whatever call triggered the
// start, but cause-links to the API spans that installed the Service value
// (e.g. Container.asService) — and its logs must be readable beneath those.
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
		serviceChild = "00000000000000dd" // e.g. the exec's process span
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
		{TraceID: validString(traceID), SpanID: validString(installSpan), Body: []byte("install's own logs excluded")},
		{TraceID: validString(traceID), SpanID: validString(serviceSpan), Body: []byte("service stdout")},
		{TraceID: validString(traceID), SpanID: validString(serviceChild), Body: []byte("service child stdout")},
		{TraceID: validString(traceID), SpanID: validString(trigger), Body: []byte("trigger excluded")},
		{TraceID: validString(traceID), SpanID: validString(waiter), Body: []byte("waiter excluded")},
	}
	_, err = store.AppendLogs(logs)
	require.NoError(t, err)

	// Beneath the install span: the cause-linked service span and its subtree.
	page, err := store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(installSpan),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, logIDs(page))

	// The plain tree walk still works and reaches the same subtree.
	page, err = store.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString(trigger),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3}, logIDs(page))
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

	// Agreement with the full walk it stands in for.
	for _, spanID := range []string{installSpan, trigger, serviceSpan, leaf, waiter, selfParent} {
		require.Equal(t, len(store.lookup.descendants(spanID)) > 0, store.HasDescendants(spanID),
			"HasDescendants(%s) disagrees with descendants()", spanID)
	}
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
