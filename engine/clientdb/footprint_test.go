package clientdb

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/spanfootprint"
)

var footprintSizes = flag.String("footprint.sizes", "", spanfootprint.SizesFlag)

// footprintStore is an sdktrace.SpanExporter appending each span snapshot to
// a store as a row, encoded the way the engine's span processor does
// (engine/server spanRowEncoder): one row per export of a span, so a span
// that starts and ends costs two rows.
type footprintStore struct {
	db *DB
}

func (s footprintStore) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	rows := make([]Span, 0, len(spans))
	for _, span := range spans {
		attrs, err := MarshalKeyValueList(span.Attributes(), telemetryattrs.TelemetryOriginClientIDAttr)
		if err != nil {
			return err
		}
		links, err := MarshalProtoList(telemetry.SpanLinksToPB(span.Links()))
		if err != nil {
			return err
		}
		row := Span{
			TraceID:       span.SpanContext().TraceID().String(),
			SpanID:        span.SpanContext().SpanID().String(),
			Name:          span.Name(),
			Kind:          span.SpanKind().String(),
			StartTime:     span.StartTime().UnixNano(),
			Attributes:    attrs,
			Links:         links,
			StatusCode:    int64(span.Status().Code),
			StatusMessage: span.Status().Description,
		}
		if span.Parent().IsValid() {
			row.ParentSpanID = sql.NullString{String: span.Parent().SpanID().String(), Valid: true}
		}
		if !span.EndTime().Before(span.StartTime()) {
			row.EndTime = sql.NullInt64{Int64: span.EndTime().UnixNano(), Valid: true}
		}
		rows = append(rows, row)
	}
	_, err := s.db.AppendSpans(rows)
	return err
}

func (footprintStore) Shutdown(context.Context) error { return nil }

func footprintHeap() (uint64, uint64) {
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc, ms.HeapObjects
}

var footprintKeep *DB

// BenchmarkStoreSpanFootprint reports the engine's retained heap per span for
// a client's telemetry store holding a large synthetic agent-session trace
// (the generator dagui's and idtui's footprint benchmarks share), split into
// what the store holds unindexed (the row tail and spill bookkeeping) and
// what its in-memory indexes add once a reader needs them (span lookup, call
// and log indexes), plus the cost of a span's first log row (which interns
// the span in the log index) and of each further one:
//
//	go test ./engine/clientdb -run '^$' -bench StoreSpanFootprint -benchtime 1x
func BenchmarkStoreSpanFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	require.NoError(b, err)
	for _, n := range sizes {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var stored, indexed, firstLog, perLog float64
			for b.Loop() {
				before, _ := footprintHeap()
				db, err := openStoreStreams(b.Context(), b.TempDir(), "client", telemetryTailBudget, nil)
				require.NoError(b, err)
				require.NoError(b, spanfootprint.New(n).Export(b.Context(), footprintStore{db}, 512, nil))
				afterSpans, _ := footprintHeap()
				require.NoError(b, db.ensureIndexed(b.Context()))
				afterIndex, _ := footprintHeap()

				// Output lines, one per span, as display text: once for each
				// span's first row, which interns the span in the log index,
				// and again for the cost of a further row.
				appendLogs := func() uint64 {
					const perBatch = 1024
					logs := make([]Log, 0, perBatch)
					tr := spanfootprint.New(n)
					for i := range n {
						logs = append(logs, selectionLogFor(b, tr.SpanID(i).String(), fmt.Sprintf("output line %d", i)))
						if len(logs) == perBatch {
							_, err := db.AppendLogs(logs)
							require.NoError(b, err)
							logs = logs[:0]
						}
					}
					_, err = db.AppendLogs(logs)
					require.NoError(b, err)
					after, _ := footprintHeap()
					return after
				}
				afterFirstLogs := appendLogs()
				afterMoreLogs := appendLogs()

				footprintKeep = db
				stored = float64(int64(afterSpans)-int64(before)) / float64(n)
				indexed = float64(int64(afterIndex)-int64(afterSpans)) / float64(n)
				firstLog = float64(int64(afterFirstLogs)-int64(afterIndex)) / float64(n)
				perLog = float64(int64(afterMoreLogs)-int64(afterFirstLogs)) / float64(n)
				require.NoError(b, db.Close())
			}
			b.ReportMetric(stored, "B/span-stored")
			b.ReportMetric(indexed, "B/span-indexed")
			b.ReportMetric(firstLog, "B/first-logrow-of-span")
			b.ReportMetric(perLog, "B/logrow")
		})
	}
}

func selectionLogFor(t testing.TB, span, body string) Log {
	t.Helper()
	l := selectionLog(t, span, &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}})
	l.TraceID = sql.NullString{String: "01000000000000000000000000000000", Valid: true}
	return l
}
