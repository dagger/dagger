package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// overlapExporter records whether two exports ever overlapped, which an
// exporter may assume never happens.
type overlapExporter struct {
	inFlight atomic.Int32
	overlap  atomic.Bool
	exports  atomic.Int32
}

func (e *overlapExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	if e.inFlight.Add(1) > 1 {
		e.overlap.Store(true)
	}
	time.Sleep(time.Millisecond)
	e.inFlight.Add(-1)
	e.exports.Add(1)
	return nil
}

func (e *overlapExporter) Shutdown(context.Context) error { return nil }

// A trace viewer imports the spans of several fetches at once into one
// importer; the importer hands its span sink one export at a time.
func TestTraceImporterExportsOneAtATime(t *testing.T) {
	sink := &overlapExporter{}
	imp := NewTraceImporter(TraceImportSinks{Spans: sink})
	const fetches, batches = 8, 5
	var wg sync.WaitGroup
	for f := range fetches {
		wg.Go(func() {
			for b := range batches {
				id := []byte{byte(f + 1), byte(b + 1), 1, 1, 1, 1, 1, 1}
				req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
					ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
						TraceId: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
						SpanId:  id, Name: "fetched", StartTimeUnixNano: 1,
					}}}},
				}}}
				require.NoError(t, imp.ImportSpans(t.Context(), req))
			}
		})
	}
	wg.Wait()
	require.NoError(t, imp.Seal(t.Context()))
	require.False(t, sink.overlap.Load(), "the importer called its span sink concurrently")
	require.Equal(t, int32(fetches*batches+1), sink.exports.Load(), "every batch and the seal export")
}
