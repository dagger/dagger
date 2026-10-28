package clientdb

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreRegistryReopenRecoversState(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.tailBudget = 512

	store, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)

	spans := make([]Span, 600)
	spans[0] = Span{TraceID: "trace", SpanID: "root"}
	spans[1] = Span{
		TraceID:      "trace",
		SpanID:       "child",
		ParentSpanID: validString("root"),
		Attributes:   []byte("first-row"),
	}
	spans[2] = Span{
		TraceID:      "trace",
		SpanID:       "child",
		ParentSpanID: validString("root"),
		Attributes:   []byte("later-row"),
	}
	spans[3] = Span{TraceID: "trace", SpanID: "grandchild", ParentSpanID: validString("child")}
	for i := 4; i < len(spans); i++ {
		spans[i] = Span{TraceID: "trace", SpanID: fmt.Sprintf("filler-%d", i)}
	}
	spanStats, err := store.AppendSpans(spans)
	require.NoError(t, err)
	require.Equal(t, int64(600), spanStats.LastID)

	logs := make([]Log, 600)
	logs[0] = Log{SpanID: validString("root"), Body: []byte("root's own log")}
	logs[1] = Log{SpanID: validString("child"), Body: []byte("child")}
	logs[2] = Log{SpanID: validString("grandchild"), Body: []byte("grandchild")}
	for i := 3; i < len(logs); i++ {
		logs[i] = Log{SpanID: validString("unrelated"), Body: []byte(fmt.Sprintf("log-%d", i))}
	}
	logStats, err := store.AppendLogs(logs)
	require.NoError(t, err)
	require.Equal(t, int64(600), logStats.LastID)

	metrics := make([]Metric, 600)
	for i := range metrics {
		metrics[i].Data = []byte(fmt.Sprintf("metric-%d", i+1))
	}
	metricStats, err := store.AppendMetrics(metrics)
	require.NoError(t, err)
	require.Equal(t, int64(600), metricStats.LastID)

	require.NoError(t, store.Close())
	require.GreaterOrEqual(t, len(store.spans.spill.index), 3)
	require.GreaterOrEqual(t, len(store.logs.spill.index), 3)
	require.GreaterOrEqual(t, len(store.metrics.spill.index), 3)

	reopened, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	require.NotSame(t, store, reopened)
	require.Equal(t, int64(601), reopened.spans.nextID)
	require.Equal(t, int64(601), reopened.logs.nextID)
	require.Equal(t, int64(601), reopened.metrics.nextID)
	require.GreaterOrEqual(t, len(reopened.spans.spill.index), 3)
	require.GreaterOrEqual(t, len(reopened.logs.spill.index), 3)
	require.GreaterOrEqual(t, len(reopened.metrics.spill.index), 3)

	spanStats, err = reopened.AppendSpans([]Span{{
		TraceID:      "trace",
		SpanID:       "new-child",
		ParentSpanID: validString("child"),
	}})
	require.NoError(t, err)
	require.Equal(t, int64(601), spanStats.LastID)
	logStats, err = reopened.AppendLogs([]Log{{SpanID: validString("new-child"), Body: []byte("new child")}})
	require.NoError(t, err)
	require.Equal(t, int64(601), logStats.LastID)
	metricStats, err = reopened.AppendMetrics([]Metric{{Data: []byte("metric-601")}})
	require.NoError(t, err)
	require.Equal(t, int64(601), metricStats.LastID)

	span, err := reopened.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace", SpanID: "child"})
	require.NoError(t, err)
	require.Equal(t, int64(2), span.ID)
	require.Equal(t, []byte("first-row"), span.Attributes)

	beneath, err := reopened.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString("root"),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3, 601}, logIDs(beneath))

	// A behind cursor first drains the on-disk prefix and then continues from
	// the post-reopen in-memory tail without repeating or losing an ID.
	page, err := reopened.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 598, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []Metric{
		{ID: 599, Data: []byte("metric-599")},
		{ID: 600, Data: []byte("metric-600")},
	}, page)
	page, err = reopened.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 600, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 601, Data: []byte("metric-601")}}, page)

	stale, err := reopened.SelectMetricsSince(t.Context(), SelectMetricsSinceParams{ID: 0, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), stale[0].ID)
	require.NoError(t, reopened.Close())

	// A new registry, as after an engine restart, knows nothing of where the
	// store ended and finds it by scanning.
	restarted, err := NewDBs(registry.Root).Open(t.Context(), "client")
	require.NoError(t, err)
	require.Equal(t, int64(602), restarted.spans.nextID)
	require.Equal(t, int64(602), restarted.logs.nextID)
	require.Equal(t, int64(602), restarted.metrics.nextID)
	span, err = restarted.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace", SpanID: "child"})
	require.NoError(t, err)
	require.Equal(t, int64(2), span.ID)
	beneath, err = restarted.SelectLogsBeneathSpan(t.Context(), SelectLogsBeneathSpanParams{
		SpanID: validString("root"),
		Limit:  10,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3, 601}, logIDs(beneath))
	require.NoError(t, restarted.Close())
}

// A store opened only to append indexes nothing; the first reader indexes
// every row it holds, including rows appended through that handle, in the
// order they were appended, and later appends are indexed as they arrive.
func TestStoreRegistryIndexesAppendOnlyRowsForFirstReader(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.tailBudget = 512

	store, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	_, err = store.AppendSpans([]Span{
		{TraceID: "trace", SpanID: "root"},
		{TraceID: "trace", SpanID: "child", ParentSpanID: validString("root"), Attributes: []byte("first-row")},
	})
	require.NoError(t, err)
	_, err = store.AppendLogs([]Log{{SpanID: validString("child"), Body: []byte("before")}})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	appender, err := registry.OpenForAppend(t.Context(), "client")
	require.NoError(t, err)
	defer appender.Close()
	fillers := make([]Span, 600)
	for i := range fillers {
		fillers[i] = Span{TraceID: "trace", SpanID: fmt.Sprintf("filler-%d", i)}
	}
	_, err = appender.AppendSpans(fillers)
	require.NoError(t, err)
	_, err = appender.AppendSpans([]Span{
		{TraceID: "trace", SpanID: "child", ParentSpanID: validString("root"), Attributes: []byte("later-row")},
	})
	require.NoError(t, err)
	_, err = appender.AppendLogs([]Log{{SpanID: validString("child"), Body: []byte("appended")}})
	require.NoError(t, err)

	reader, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	defer reader.Close()
	require.Same(t, appender, reader)
	first, err := reader.SelectSpan(t.Context(), SelectSpanParams{TraceID: "trace", SpanID: "child"})
	require.NoError(t, err)
	require.Equal(t, []byte("first-row"), first.Attributes)
	latest, err := reader.SelectSpansLatest(t.Context(), map[string]struct{}{"child": {}})
	require.NoError(t, err)
	require.Len(t, latest, 1)
	require.Equal(t, []byte("later-row"), latest[0].Attributes)
	require.Len(t, reader.SpanIDs(), 602)
	require.Equal(t, map[string]struct{}{"child": {}}, reader.ChildSpanIDs("root"))
	require.Equal(t, []int64{1, 2}, reader.LogRowIDsForSpans(map[string]struct{}{"child": {}}))

	_, err = appender.AppendLogs([]Log{{SpanID: validString("child"), Body: []byte("after")}})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, reader.LogRowIDsForSpans(map[string]struct{}{"child": {}}))
}

// Appends from another goroutine run on while the first reader builds the
// indexes, and every row ends up indexed once. Whether they overlap the build
// is up to the scheduler; TestLogStreamIndexesRowsAppendedDuringReplay forces
// the overlap.
func TestStoreRegistryIndexesRowsAppendedDuringFirstRead(t *testing.T) {
	registry := NewDBs(t.TempDir())
	registry.tailBudget = 512

	appender, err := registry.OpenForAppend(t.Context(), "client")
	require.NoError(t, err)
	defer appender.Close()
	appendBatch := func(batch int) error {
		logs := make([]Log, 50)
		for i := range logs {
			logs[i] = Log{SpanID: validString("span"), Body: []byte(fmt.Sprintf("log-%d-%d", batch, i))}
		}
		_, err := appender.AppendLogs(logs)
		return err
	}
	const before, during = 40, 40
	for batch := range before {
		require.NoError(t, appendBatch(batch))
	}

	appended := make(chan error, 1)
	go func() {
		for batch := range during {
			if err := appendBatch(before + batch); err != nil {
				appended <- err
				return
			}
		}
		appended <- nil
	}()
	reader, err := registry.Open(t.Context(), "client")
	require.NoError(t, err)
	defer reader.Close()
	require.NoError(t, <-appended)

	rows := reader.LogRowIDsForSpans(map[string]struct{}{"span": {}})
	want := make([]int64, (before+during)*50)
	for i := range want {
		want[i] = int64(i + 1)
	}
	require.Equal(t, want, rows)
}
