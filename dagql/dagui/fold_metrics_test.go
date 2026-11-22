package dagui

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	telemetry "github.com/dagger/otel-go"
)

// LLM token totals count every span's tokens, the folded ones' included: a
// folding DB sums what a full load does.
func TestFoldLLMTokenTotals(t *testing.T) {
	// 1 root
	// └── 2 top-level row
	//     └── 3 call
	//         ├── 4 llm (folded)
	//         └── 5 llm (folded)
	spans := []sdktrace.ReadOnlySpan{
		lateTestSpan(1, 0, false),
		lateTestSpan(2, 1, false),
		lateTestSpan(3, 2, false),
		lateTestSpan(4, 3, false),
		lateTestSpan(5, 3, false),
	}
	point := func(span byte, model string, value int64) metricdata.DataPoint[int64] {
		return metricdata.DataPoint[int64]{
			Attributes: attribute.NewSet(
				attribute.String("model", model),
				attribute.String("provider", "test"),
				attribute.String(telemetry.MetricsSpanIDAttr, testID(span).String()),
			),
			Value: value,
		}
	}
	gauge := func(name string, points ...metricdata.DataPoint[int64]) metricdata.Metrics {
		return metricdata.Metrics{Name: name, Data: metricdata.Gauge[int64]{DataPoints: points}}
	}
	// cumulative per-call gauges, exported repeatedly as a provider streams
	exports := []*metricdata.ResourceMetrics{
		{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{
			gauge(telemetry.LLMInputTokens, point(4, "model-a", 100), point(5, "model-b", 7)),
			gauge(telemetry.LLMOutputTokens, point(4, "model-a", 3)),
		}}}},
		{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{
			gauge(telemetry.LLMInputTokens, point(4, "model-a", 100), point(5, "model-b", 9)),
			gauge(telemetry.LLMOutputTokens, point(4, "model-a", 20)),
			gauge(telemetry.LLMInputTokensCacheReads, point(4, "model-a", 50)),
			gauge(telemetry.LLMInputTokensCacheWrites, point(5, "model-b", 11)),
		}}}},
	}

	totals := func(db *DB) map[string]LLMModelMetrics {
		t.Helper()
		if err := db.ExportSpans(context.Background(), spans); err != nil {
			t.Fatal(err)
		}
		for _, rm := range exports {
			if err := db.MetricExporter().Export(context.Background(), rm); err != nil {
				t.Fatal(err)
			}
		}
		out := map[string]LLMModelMetrics{}
		for _, m := range db.LLMTokenMetrics.Snapshot() {
			out[m.Model] = m
		}
		return out
	}

	want := totals(NewDB())
	folded, _ := newFoldedDB(nil)
	got := totals(folded)

	for _, id := range []byte{4, 5} {
		if !folded.Fold().IsFolded(testID(id)) {
			t.Fatalf("span %d is not folded: the test checks nothing", id)
		}
	}
	if len(want) != 2 || want["model-a"].InputTokens != 100 || want["model-a"].OutputTokens != 20 ||
		want["model-b"].InputTokens != 9 || want["model-b"].CachedTokenWrites != 11 {
		t.Fatalf("full load totals %+v", want)
	}
	if len(got) != len(want) {
		t.Fatalf("folded totals %+v, want %+v", got, want)
	}
	for model, w := range want {
		if got[model] != w {
			t.Errorf("model %s: folded totals %+v, want %+v", model, got[model], w)
		}
	}
}
