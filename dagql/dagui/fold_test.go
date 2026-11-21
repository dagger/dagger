package dagui

import (
	"context"
	"encoding/binary"
	"slices"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/dagger/dagger/internal/spanfootprint"
)

// foldStore stands in for the engine's live store: the latest snapshot of
// every span exported, which a folding DB reads back.
type foldStore struct {
	latest map[SpanID]sdktrace.ReadOnlySpan
	next   sdktrace.SpanExporter
}

func (s *foldStore) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		s.latest[SpanID{span.SpanContext().SpanID()}] = span
	}
	return s.next.ExportSpans(ctx, spans)
}

func (s *foldStore) Shutdown(context.Context) error { return nil }

// newFoldedDB returns a folding DB reading back from a store that the
// returned exporter feeds.
func newFoldedDB(retain FoldRetain) (*DB, *foldStore) {
	store := &foldStore{latest: map[SpanID]sdktrace.ReadOnlySpan{}}
	var db *DB
	db = NewDB(WithFold(FoldOptions{
		Retain: retain,
		Load: func(ids []SpanID) {
			var spans []sdktrace.ReadOnlySpan
			for _, id := range ids {
				if span, ok := store.latest[id]; ok {
					spans = append(spans, span)
				}
			}
			if err := db.ExportSpans(FoldFill(context.Background()), spans); err != nil {
				panic(err)
			}
		},
	}))
	store.next = db
	return db, store
}

// nestedFootprint makes every span whose parent is an agent loop a boundary,
// like tool calls running nested engines: what runs beneath stops surfacing.
type nestedFootprint struct{ next sdktrace.SpanExporter }

func (e nestedFootprint) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = s
		p := s.Parent().SpanID()
		if !p.IsValid() {
			continue
		}
		if pi := binary.BigEndian.Uint64(p[:]) - 1; pi == 0 || pi >= spanfootprint.LongLived {
			continue
		}
		stub := tracetest.SpanStubFromReadOnlySpan(s)
		stub.Attributes = append(slices.Clip(stub.Attributes), attribute.Bool(telemetry.UIBoundaryAttr, true))
		out[i] = stub.Snapshot()
	}
	return e.next.ExportSpans(ctx, out)
}

func (e nestedFootprint) Shutdown(context.Context) error { return nil }

// checkFold compares a folding DB against a full DB of the same trace: every
// retained span carries the full DB's roll-up counts and the expand
// affordance, every retained span's parent is retained, and nothing received
// beneath an expanded span is folded.
//
// With causal links, the counts on a cause can go stale differently in the
// two DBs (a cause's category isn't propagated again when an effect changes
// it), so they're checked against the folding DB's own closure: every span
// it holds counts its last propagated category on the spans it reaches, and
// every retained span the folded spans it owns.
func checkFold(t *testing.T, folded, full *DB, links bool) {
	t.Helper()
	f := folded.Fold()
	stats := f.Stats()
	if stats.Retained == 0 || stats.Retained == stats.Spans {
		t.Fatalf("retained %d of %d spans", stats.Retained, stats.Spans)
	}
	var exact *struct{}
	closure := map[*Span]RollUpState{}
	if !links {
		exact = &struct{}{}
	} else {
		add := func(a *Span, c RollUpState) {
			st := closure[a]
			addRollUp(&st, c, 1)
			closure[a] = st
		}
		for _, span := range folded.Spans.Order {
			if cat := span.lastRollUpCategory; cat != stateUnknown {
				var c RollUpState
				c.incrementCategory(cat)
				span.rollUpAncestors(nil, func(a *Span) { add(a, c) })
			}
			if own := f.ownCounts(span); !own.isZero() {
				add(span, own.RollUpState)
				span.rollUpAncestors(nil, func(a *Span) { add(a, own.RollUpState) })
			}
		}
	}
	for i, st := range f.state {
		if st&foldRetained == 0 {
			continue
		}
		if own, sub := f.ownCounts(f.spanOf(uint32(i))), f.owned(uint32(i)); own != sub.foldCounts {
			t.Errorf("span %s owns %+v, counted %+v", f.spans[i], own, sub.foldCounts)
		}
	}
	var checked int
	for i, st := range f.state {
		if st&foldReceived == 0 {
			continue
		}
		id := f.spans[i]
		if p := f.parent[i]; st&foldRetained != 0 && p != 0 && f.state[p-1]&foldRetained == 0 {
			t.Errorf("retained span %s has a folded parent", id)
		}
		if p := f.parent[i]; p != 0 && f.state[p-1]&foldExpanded != 0 && st&foldRetained == 0 {
			t.Errorf("span %s is folded beneath an expanded span", id)
		}
		if st&foldRetained == 0 {
			if folded.Spans.Map[id] != nil {
				t.Errorf("folded span %s is in the DB", id)
			}
			continue
		}
		got, want := folded.Spans.Map[id], full.Spans.Map[id]
		if got == nil || !got.Received {
			t.Errorf("retained span %s not received in full", id)
			continue
		}
		if exact != nil && rollUpCounts(got) != rollUpCounts(want) {
			t.Errorf("span %s (%s): roll-up counts %+v, want %+v", id, want.Name, rollUpCounts(got), rollUpCounts(want))
		}
		if exact == nil && rollUpCounts(got) != closure[got] {
			t.Errorf("span %s (%s): roll-up counts %+v, want %+v", id, want.Name, rollUpCounts(got), closure[got])
		}
		wantKids := countChildren(want.ChildSpans, FrontendOpts{})
		if (got.Snapshot().ChildCount > 0) != (wantKids > 0) {
			t.Errorf("span %s: child count %d, want %d", id, got.ChildCount, wantKids)
		}
		if got.IsFailedOrCausedFailure() != want.IsFailedOrCausedFailure() {
			t.Errorf("span %s: failed %v, want %v", id, got.IsFailedOrCausedFailure(), want.IsFailedOrCausedFailure())
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no retained spans checked")
	}
	t.Logf("%d of %d spans retained (%.1f%%), %d read back, stubs %d B (%.1f B/span)",
		stats.Retained, stats.Spans, 100*float64(stats.Retained)/float64(stats.Spans), f.Loads,
		stats.StubBytes, float64(stats.StubBytes)/float64(stats.Spans))
}

func TestFoldLiveSession(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	for _, nested := range []bool{false, true} {
		for _, links := range []bool{false, true} {
			name := "agent"
			if nested {
				name = "nested"
			}
			if links {
				name += "/links"
			}
			t.Run(name, func(t *testing.T) {
				wrap := func(next sdktrace.SpanExporter) sdktrace.SpanExporter {
					if !links {
						next = noLinksExporter{next}
					}
					if nested {
						next = nestedFootprint{next}
					}
					return next
				}
				full := NewDB()
				if err := tr.Export(t.Context(), wrap(full), 512, nil); err != nil {
					t.Fatal(err)
				}
				folded, store := newFoldedDB(nil)
				if err := tr.Export(t.Context(), wrap(store), 512, nil); err != nil {
					t.Fatal(err)
				}
				checkFold(t, folded, full, links)
			})
		}
	}
}

// noLinksExporter strips span links.
type noLinksExporter struct{ next sdktrace.SpanExporter }

func (e noLinksExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = withoutLinks(s)
	}
	return e.next.ExportSpans(ctx, out)
}

func (e noLinksExporter) Shutdown(context.Context) error { return nil }

// A name search finds folded spans by name, and retaining a hit reads it
// back with its ancestors.
func TestFoldMatchNames(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	full := NewDB()
	folded, store := newFoldedDB(nil)
	if err := tr.Export(t.Context(), noLinksExporter{teeExporter{full, store}}, 512, nil); err != nil {
		t.Fatal(err)
	}
	match := func(name string) bool { return name == "exec go test ./pkg42" }
	var want int
	for _, span := range full.Spans.Order {
		if match(span.Name) {
			want++
		}
	}
	var retained int
	for _, span := range folded.Spans.Order {
		if span.Received && match(span.Name) {
			retained++
		}
	}
	ids, total := folded.Fold().MatchNames(match, 3)
	if total+retained != want || total == 0 || len(ids) != 3 {
		t.Fatalf("found %d folded (%d returned) and %d retained, want %d in all", total, len(ids), retained, want)
	}
	folded.Fold().Retain(ids...)
	for _, id := range ids {
		span := folded.Spans.Map[id]
		if span == nil || !span.Received || span.Name != "exec go test ./pkg42" {
			t.Fatalf("hit %s not read back", id)
		}
		if span.FirstMissingAncestor() != nil {
			t.Fatalf("hit %s read back without its ancestors", id)
		}
	}
}

// Expanding a row partway through a session reads back its folded children,
// and retains those to come.
func TestFoldExpand(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	full := NewDB()
	if err := tr.Export(t.Context(), noLinksExporter{full}, 512, nil); err != nil {
		t.Fatal(err)
	}
	folded, store := newFoldedDB(nil)
	loop := SpanID{tr.SpanID(1)}
	var batches, loadsBefore int
	if err := tr.Export(t.Context(), noLinksExporter{store}, 512, func() {
		batches++
		if batches == n/512/2 {
			loadsBefore = folded.Fold().Loads
			if !folded.Fold().Expand(loop) {
				t.Error("expanding mid-session read nothing back")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if folded.Fold().Loads == loadsBefore {
		t.Fatal("expanding mid-session read nothing back")
	}
	checkFold(t, folded, full, false)
	got := folded.Spans.Map[loop]
	want := full.Spans.Map[loop]
	var kids int
	for _, c := range want.ChildSpans.Spans() {
		if c.ParentSpan != want {
			continue
		}
		kids++
		if s := folded.Spans.Map[c.ID]; s == nil || !s.Received {
			t.Errorf("child %s of the expanded loop is not retained", c.ID)
		}
	}
	if got.ChildSpans.Len() < kids {
		t.Errorf("expanded loop has %d children, want %d", got.ChildSpans.Len(), kids)
	}
}
