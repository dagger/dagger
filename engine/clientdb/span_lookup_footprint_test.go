package clientdb

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"

	telemetry "github.com/dagger/otel-go"
)

// footprintRows is an agent session's span snapshots as the index sees
// them: n spans in fans under a running root, each snapshotted twice
// (started, then ended), every fifth cause-linking the span before it (a
// lazy evaluation linking its call), and every hundredth an agent loop.
func footprintRows(tb testing.TB, n int) []Span {
	tb.Helper()
	const traceID = "0f0e0d0c0b0a09080706050403020100"
	spanHex := func(i int) string { return fmt.Sprintf("%016x", uint64(i+1)*0x9e3779b97f4a7c15) }
	agentAttrs := []byte(agentIDAttrMarker)
	rows := make([]Span, 0, 2*n)
	for i := range n {
		row := Span{TraceID: traceID, SpanID: spanHex(i), Name: "span"}
		if i > 0 {
			row.ParentSpanID = validString(spanHex(i / 16))
		}
		if i%5 == 4 {
			row.Links = linksJSON(tb, spanLink(tb, traceID, spanHex(i-1), telemetry.LinkPurposeCause))
		}
		if i%100 == 1 {
			row.Attributes = agentAttrs
		}
		rows = append(rows, row, row)
	}
	for i := range rows {
		rows[i].ID = int64(i + 1)
	}
	return rows
}

// freshRows copies rows' strings and bytes, as decoding them would.
func freshRows(rows []Span) []Span {
	out := make([]Span, len(rows))
	for i, row := range rows {
		row.TraceID = strings.Clone(row.TraceID)
		row.SpanID = strings.Clone(row.SpanID)
		row.ParentSpanID.String = strings.Clone(row.ParentSpanID.String)
		row.Name = strings.Clone(row.Name)
		row.Attributes = bytes.Clone(row.Attributes)
		row.Links = bytes.Clone(row.Links)
		out[i] = row
	}
	return out
}

var footprintSink *spanLookup

// BenchmarkSpanLookupFootprint measures the heap the span index retains per
// span, for a session of N spans.
func BenchmarkSpanLookupFootprint(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			rows := footprintRows(b, n)
			var retained uint64
			for b.Loop() {
				before := heapInUse()
				lookup := newSpanLookup()
				for start := 0; start < len(rows); start += 1000 {
					// Each batch decoded afresh, as the store's rows are:
					// whatever the index keeps of them is its own.
					lookup.addAll(freshRows(rows[start:min(start+1000, len(rows))]))
				}
				footprintSink = lookup
				if after := heapInUse(); after > before {
					retained = after - before
				}
				footprintSink = nil
				runtime.GC()
			}
			b.ReportMetric(float64(retained)/float64(n), "retained-B/span")
		})
	}
}
