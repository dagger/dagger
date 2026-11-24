package clientdb

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// footprintLogs is an agent session's log rows as the log index sees them:
// n rows over n/4 spans, mostly exec output, some progress records, and a
// call payload record per eight spans, each with its own dag.digest.
func footprintLogs(tb testing.TB, n int) []Log {
	tb.Helper()
	spanHex := func(i int) string { return fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15) }
	stdout := selectionLog(tb, "", selectionText("a line of ordinary exec output"),
		&commonpb.KeyValue{Key: "stdio.stream", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 1}}})
	progress := selectionLog(tb, "", selectionText("progress"), selectionAttr(telemetryattrs.ProgressItemAttr, "item"))
	rows := make([]Log, n)
	for i := range rows {
		var row Log
		switch {
		case i%32 == 0:
			row = selectionLog(tb, "", &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: []byte{1}}},
				selectionAttr(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
				selectionAttr(telemetry.DagDigestAttr, fmt.Sprintf("xxh3:%016x", uint64(i)*0x9e3779b97f4a7c15)))
		case i%10 == 0:
			row = progress
		default:
			row = stdout
		}
		row.SpanID.String = spanHex(i / 4)
		row.Timestamp = int64(i)
		row.ID = int64(i + 1)
		rows[i] = row
	}
	return rows
}

// freshLogs copies rows' strings and bytes, as decoding them would.
func freshLogs(rows []Log) []Log {
	out := make([]Log, len(rows))
	for i, row := range rows {
		row.TraceID.String = strings.Clone(row.TraceID.String)
		row.SpanID.String = strings.Clone(row.SpanID.String)
		row.Attributes = bytes.Clone(row.Attributes)
		row.Body = bytes.Clone(row.Body)
		out[i] = row
	}
	return out
}

var logFootprintSink *logLookup

// BenchmarkLogLookupFootprint measures the heap the log index retains per
// log row, for a session of N rows.
func BenchmarkLogLookupFootprint(b *testing.B) {
	for _, n := range []int{40000, 400000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rows := footprintLogs(b, n)
			var retained uint64
			for b.Loop() {
				before := heapInUse()
				idx := newLogLookup()
				for start := 0; start < len(rows); start += 1000 {
					idx.addAll(freshLogs(rows[start:min(start+1000, len(rows))]))
				}
				logFootprintSink = idx
				// The attribute memo is bounded, not per row: leave it out.
				idx.memoMu.Lock()
				clear(idx.attrMemo)
				idx.memoMu.Unlock()
				if after := heapInUse(); after > before {
					retained = after - before
				}
				require.Len(b, idx.meta, n)
				logFootprintSink = nil
				runtime.GC()
			}
			b.ReportMetric(float64(retained)/float64(n), "retained-B/row")
		})
	}
}
