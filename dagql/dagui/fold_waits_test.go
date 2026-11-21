package dagui

import (
	"context"
	"encoding/binary"
	"slices"
	"strconv"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/spanfootprint"
)

// footprintWaits gives some of the spanfootprint session's spans wait links
// on an earlier span, recorded when they end, as the engine records them:
// realistic wait chains for the waiting below rows.
type footprintWaits struct {
	tr   *spanfootprint.Trace
	next sdktrace.SpanExporter
}

func (e footprintWaits) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = s
		id := s.SpanContext().SpanID()
		n := int(binary.BigEndian.Uint64(id[:])) - 1
		h := uint64(n)*0x9e3779b97f4a7c15 ^ 0xabcdef
		h ^= h >> 29
		if n <= spanfootprint.LongLived || h%100 >= 6 || s.EndTime().Before(s.StartTime()) {
			continue
		}
		dur := s.EndTime().Sub(s.StartTime())
		if dur < 10*time.Millisecond {
			continue
		}
		target := e.tr.SpanID(int(h>>8) % n)
		start := s.StartTime().Add(dur / 10)
		end := s.StartTime().Add(dur * 6 / 10)
		stub := tracetest.SpanStubFromReadOnlySpan(s)
		stub.Links = append(slices.Clip(stub.Links), sdktrace.Link{
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: s.SpanContext().TraceID(), SpanID: target}),
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeWait),
				attribute.String(telemetryattrs.WcprofWaitReasonAttr, "singleflight"),
				attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, strconv.FormatInt(start.UnixNano(), 10)),
				attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, strconv.FormatInt(end.UnixNano(), 10)),
			},
		})
		out[i] = stub.Snapshot()
	}
	return e.next.ExportSpans(ctx, out)
}

func (e footprintWaits) Shutdown(context.Context) error { return nil }

// teeExporter exports to both.
type teeExporter struct{ a, b sdktrace.SpanExporter }

func (e teeExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.a.ExportSpans(ctx, spans); err != nil {
		return err
	}
	return e.b.ExportSpans(ctx, spans)
}

func (e teeExporter) Shutdown(context.Context) error { return nil }

func sameBreakdown(a, b *TimeBreakdown) bool {
	return a.Self == b.Self && a.Waiting == b.Waiting && a.Material == b.Material &&
		a.DominantTarget == b.DominantTarget && a.DominantLabel == b.DominantLabel &&
		sameSegments(a.Segments, b.Segments)
}

// A retained row over folded spans reads the same waiting below it, and the
// same time breakdown, as the full DB: what's folded is plain work, from its
// stubs' intervals, and what waits is retained.
func TestFoldRowWaitsMatchFullLoad(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	for _, nested := range []bool{false, true} {
		name := "agent"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
				next = footprintWaits{tr, next}
				if nested {
					next = nestedFootprint{next}
				}
				return next
			}
			// compare mid-session, while rows run, and at the end, each
			// time computed afresh: a DB that computed the waiting below
			// rows before keeps some of what it computed then (a stretch
			// cut at the time it was computed, until the row changes
			// again), and which rows a DB revisits depends on what
			// changed in it
			midFull, endFull := NewDB(), NewDB()
			folded, store := newFoldedDB(nil)
			var batches int
			check := func(final bool) {
				full := midFull
				now := time.Unix(1_700_000_000, 0).Add(time.Duration(batches) * 512 * time.Millisecond)
				if final {
					full = endFull
					now = full.End.Add(time.Second)
					// computed afresh too: what's compared is the
					// computation, not which rows each DB's
					// incremental updates happened to revisit
					folded.rowWaits = nil
				}
				full.UpdateRowWaits(now, 0)
				folded.UpdateRowWaits(now, 0)
				var compared, waiting, over int
				for _, span := range folded.Spans.Order {
					if !span.Received {
						continue
					}
					want := full.Spans.Map[span.ID]
					wantRW, gotRW := want.RowWaits, span.RowWaits
					if (wantRW == nil) != (gotRW == nil) || (wantRW != nil && !sameSegments(wantRW.Segments, gotRW.Segments)) {
						t.Errorf("span %s (%s): row waits %+v, want %+v", span.ID, span.Name, gotRW, wantRW)
					}
					if got, want := span.TimeBreakdown(now), want.TimeBreakdown(now); !sameBreakdown(got, want) {
						t.Errorf("span %s (%s): breakdown self %v waiting %v material %v, want self %v waiting %v material %v",
							span.ID, span.Name, got.Self, got.Waiting, got.Material, want.Self, want.Waiting, want.Material)
					}
					compared++
					if wantRW != nil && len(wantRW.Segments) > 0 {
						waiting++
					}
					if folded.Fold().FoldedBeneath(span) {
						over++
					}
				}
				if waiting == 0 || over == 0 {
					t.Fatalf("nothing compared: %d rows waiting, %d over folded spans", waiting, over)
				}
				t.Logf("compared %d retained rows, %d with waiting below, %d over folded spans", compared, waiting, over)
			}
			if err := tr.Export(t.Context(), wrap(teeExporter{midFull, teeExporter{endFull, store}}), 512, func() {
				batches++
				if batches == n/512/2 {
					check(false)
				}
			}); err != nil {
				t.Fatal(err)
			}
			check(true)
			st := folded.Fold().Stats()
			t.Logf("%d of %d spans retained (%.1f%%), stubs %.1f B/span", st.Retained, st.Spans,
				100*float64(st.Retained)/float64(st.Spans), float64(st.StubBytes)/float64(st.Spans))
		})
	}
}
