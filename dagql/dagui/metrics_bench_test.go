package dagui

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	telemetry "github.com/dagger/otel-go"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// metricExportPB builds one periodic export shaped like the engine's resource
// gauges: for each of calls exec calls, names gauges with one point each, the
// point keyed by call digest and span ID, carrying a trace-linked exemplar as
// engines without the exemplar-free view send.
func metricExportPB(round, calls, names int) *metricspb.ResourceMetrics {
	str := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	var metrics []*metricspb.Metric
	for n := range names {
		var points []*metricspb.NumberDataPoint
		for c := range calls {
			points = append(points, &metricspb.NumberDataPoint{
				Attributes: []*commonpb.KeyValue{
					str(telemetry.DagDigestAttr, fmt.Sprintf("xxh3:%016x", c)),
					str(telemetry.MetricsSpanIDAttr, fmt.Sprintf("%016x", c+1)),
					str(telemetry.MetricsTraceIDAttr, "0102030405060708090a0b0c0d0e0f10"),
				},
				TimeUnixNano: uint64(round) * 1e9,
				Value:        &metricspb.NumberDataPoint_AsInt{AsInt: int64(round * c)},
				Exemplars: []*metricspb.Exemplar{{
					FilteredAttributes: []*commonpb.KeyValue{str("phase", "sample"), str("cgroup", "/dagger/exec")},
					TimeUnixNano:       uint64(round) * 1e9,
					SpanId:             []byte{1, 2, 3, 4, 5, 6, 7, byte(c)},
					TraceId:            []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
					Value:              &metricspb.Exemplar_AsInt{AsInt: int64(round * c)},
				}},
			})
		}
		metrics = append(metrics, &metricspb.Metric{
			Name: fmt.Sprintf("dagger.io/metrics.bench.%d", n),
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}},
		})
	}
	return &metricspb.ResourceMetrics{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}
}

// TestDBMetricsKeepLatestPoint pins what the DB keeps per metric: the latest
// point, without exemplars, however many exports arrive.
func TestDBMetricsKeepLatestPoint(t *testing.T) {
	db := NewDB()
	exp := db.MetricExporter()
	for r := range 5 {
		rm, err := telemetry.ResourceMetricsFromPB(metricExportPB(r, 3, 2))
		if err != nil {
			t.Fatal(err)
		}
		if err := exp.Export(context.Background(), rm); err != nil {
			t.Fatal(err)
		}
	}
	points := db.MetricsByCall[fmt.Sprintf("xxh3:%016x", 2)]["dagger.io/metrics.bench.1"]
	if len(points) != 1 {
		t.Fatalf("kept %d points, want 1", len(points))
	}
	if points[0].Value != 4*2 {
		t.Fatalf("kept value %d, want the latest, %d", points[0].Value, 4*2)
	}
	if len(points[0].Exemplars) != 0 {
		t.Fatalf("kept %d exemplars", len(points[0].Exemplars))
	}
}

// BenchmarkDBMetricRetention feeds a long session's periodic metric exports
// through the frontend's ingest path (PB -> sdk -> DBMetricExporter) and
// reports the heap the DB still holds afterward. The renderers only ever read
// the last point of each metric, so this should not grow with the rounds.
func BenchmarkDBMetricRetention(b *testing.B) {
	const calls, names = 200, 8
	for _, rounds := range []int{10, 100} {
		exports := make([]*metricspb.ResourceMetrics, rounds)
		for r := range rounds {
			exports[r] = metricExportPB(r, calls, names)
		}
		b.Run(fmt.Sprintf("rounds=%d", rounds), func(b *testing.B) {
			ctx := context.Background()
			var retained uint64
			for b.Loop() {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)
				db := NewDB()
				exp := db.MetricExporter()
				for _, pb := range exports {
					rm, err := telemetry.ResourceMetricsFromPB(pb)
					if err != nil {
						b.Fatal(err)
					}
					if err := exp.Export(ctx, rm); err != nil {
						b.Fatal(err)
					}
				}
				runtime.GC()
				runtime.ReadMemStats(&after)
				if after.HeapAlloc > before.HeapAlloc {
					retained += after.HeapAlloc - before.HeapAlloc
				}
				runtime.KeepAlive(db)
			}
			b.ReportMetric(float64(retained)/float64(b.N)/1024, "retained-KiB/op")
		})
	}
}
