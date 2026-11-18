package clientdb

import (
	"database/sql"
	"fmt"
	"runtime"
	"testing"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
)

// importBenchRows is an imported trace of n spans, chained in fans of 100
// under one root, each with a call-sized attribute.
func importBenchRows(tb testing.TB, n int) []Span {
	tb.Helper()
	const traceID = "0f0e0d0c0b0a09080706050403020100"
	spanHex := func(i int) string { return fmt.Sprintf("%016x", i+1) }
	rows := make([]Span, n)
	for i := range rows {
		attrs, err := MarshalProtoList([]*otlpcommonv1.KeyValue{{
			Key:   "dagger.io/bench.payload",
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("%0256x", i)}},
		}})
		if err != nil {
			tb.Fatal(err)
		}
		rows[i] = Span{
			TraceID: traceID, SpanID: spanHex(i), Name: fmt.Sprintf("span %d", i),
			StartTime: int64(i + 1), EndTime: sql.NullInt64{Int64: int64(i + 2), Valid: true},
			Attributes: attrs,
		}
		if i > 0 {
			rows[i].ParentSpanID = sql.NullString{String: spanHex(i / 100), Valid: true}
		}
	}
	return rows
}

// heapInUse is the live heap after a full collection.
func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// BenchmarkImportedTraceResidency measures what an imported trace keeps
// resident in the engine once nothing is using it: the heap retained after
// LoadTrace's import of n spans and one read of it, while the live session
// that imported it stays open.
func BenchmarkImportedTraceResidency(b *testing.B) {
	const n = 20000
	rows := importBenchRows(b, n)
	var retained uint64
	for b.Loop() {
		b.StopTimer()
		registry := NewDBs(b.TempDir())
		live, err := registry.Open(b.Context(), "live")
		if err != nil {
			b.Fatal(err)
		}
		before := heapInUse()
		b.StartTimer()
		imported, err := live.ImportTrace(b.Context(), "trace", func(dst *DB) error {
			for start := 0; start < len(rows); start += 5000 {
				if _, err := dst.AppendSpans(rows[start:min(start+5000, len(rows))]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(imported.SpanIDs()) != n {
			b.Fatal("import lost spans")
		}
		imported = nil
		b.StopTimer()
		releaseIdleImports(b, live)
		if after := heapInUse(); after > before {
			retained += after - before
		}
		if err := live.Close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
	b.ReportMetric(float64(retained)/float64(b.N), "retained-B/op")
	b.ReportMetric(float64(retained)/float64(b.N)/n, "retained-B/span")
}

// releaseIdleImports lets live's imported traces go idle, as they do once
// no reader has used them for a while.
func releaseIdleImports(tb testing.TB, live *DB) {
	tb.Helper()
	// Imports have no idle release: they live as long as the live store.
}
