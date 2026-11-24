package dagui

import (
	"context"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/internal/spanfootprint"
)

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
	for _, shape := range []struct {
		name              string
		nested, contained bool
	}{
		{name: "agent"},
		{name: "nested", nested: true},
		// with the waits real sessions mostly carry, on spans beneath
		// their carrier, which fold
		{name: "agent-contained", contained: true},
		{name: "nested-contained", nested: true, contained: true},
	} {
		nested := shape.nested
		t.Run(shape.name, func(t *testing.T) {
			wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
				next = tr.WaitChains(next)
				if shape.contained {
					next = tr.ContainedWaits(next)
				}
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
					// a row whose waiting went away holds none, as
					// one that never had any
					if wantRW != nil && len(wantRW.Segments) == 0 {
						wantRW = nil
					}
					if gotRW != nil && len(gotRW.Segments) == 0 {
						gotRW = nil
					}
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
			t.Logf("%d of %d spans retained (%.1f%%), stubs %.1f B/span; contained waits broken on %d spans (%d targets read back); %d waits on spans still to come", st.Retained, st.Spans,
				100*float64(st.Retained)/float64(st.Spans), float64(st.StubBytes)/float64(st.Spans),
				folded.Fold().Broken, folded.Fold().BrokenTargets, folded.Fold().Pended)
			if shape.contained && folded.Fold().Pended == 0 {
				t.Fatal("no waits on spans still to come")
			}
			if shape.contained && folded.Fold().reads == nil {
				t.Fatal("no contained waits")
			}
		})
	}
}
