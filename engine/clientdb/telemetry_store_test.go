package clientdb

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
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
