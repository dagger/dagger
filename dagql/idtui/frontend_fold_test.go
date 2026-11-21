package idtui

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/spanfootprint"
)

// foldTestStore stands in for the live session's store: the latest snapshot
// of every span the session streamed.
type foldTestStore struct {
	mu     sync.Mutex
	latest map[dagui.SpanID]sdktrace.ReadOnlySpan
	// queries counts the spans asked of it.
	queries int
}

func (s *foldTestStore) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, span := range spans {
		s.latest[dagui.SpanID{SpanID: span.SpanContext().SpanID()}] = span
	}
	return nil
}

func (s *foldTestStore) Shutdown(context.Context) error { return nil }

func (s *foldTestStore) query(_ context.Context, ids []dagui.SpanID) ([]sdktrace.ReadOnlySpan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries += len(ids)
	var spans []sdktrace.ReadOnlySpan
	for _, id := range ids {
		if span, ok := s.latest[id]; ok {
			spans = append(spans, span)
		}
	}
	return spans, nil
}

type foldTestTee []sdktrace.SpanExporter

func (t foldTestTee) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, exp := range t {
		if err := exp.ExportSpans(ctx, spans); err != nil {
			return err
		}
	}
	return nil
}

func (t foldTestTee) Shutdown(context.Context) error { return nil }

// foldTestPair is a live CLI frontend that folds and one over a DB holding
// every span, fed the same session.
type foldTestPair struct {
	folded, full *frontendPretty
	lf           *liveFold
	store        *foldTestStore
	trace        *spanfootprint.Trace
}

func newFoldTestPair(t *testing.T, n int, nested bool) *foldTestPair {
	t.Helper()
	db, lf := newFoldingDB()
	folded := NewWithDB(io.Discard, db)
	folded.reportOnly = true
	lf.attach(folded)
	full := NewWithDB(io.Discard, newLocalDB())
	full.reportOnly = true
	store := &foldTestStore{latest: map[dagui.SpanID]sdktrace.ReadOnlySpan{}}
	tr := spanfootprint.New(n)
	p := &foldTestPair{folded: folded, full: full, lf: lf, store: store, trace: tr}
	folded.SetSpanQuery(dagui.TraceID{TraceID: tr.Stub(0, false).SpanContext().TraceID()}, store.query)
	// with the engine's wait chains, in the agent session's shape or the
	// nested engines'
	exp := tr.WaitChains(foldTestTee{store, folded.SpanExporter(), full.SpanExporter()})
	if nested {
		exp = spanfootprint.Nested(exp)
	}
	require.NoError(t, tr.Export(t.Context(), exp, 512, nil))
	p.settle(t)
	return p
}

// settle waits for the folded frontend's reads to land.
func (p *foldTestPair) settle(t *testing.T) {
	t.Helper()
	p.lf.wait(t.Context())
}

// recalc lays both views out again, reading back what the folded one
// shows expanded, until it reads nothing more.
func (p *foldTestPair) recalc(t *testing.T) {
	t.Helper()
	p.full.recalculateViewLocked()
	for range 16 {
		var before, after int
		p.folded.dispatch(func() {
			before = p.folded.db.Fold().Loads
			p.folded.recalculateViewLocked()
			after = p.folded.db.Fold().Loads
		})
		p.settle(t)
		if after == before {
			return
		}
	}
	t.Fatal("folded view kept reading back")
}

// do runs fn on both frontends, on their event loops: the folded one's
// reads land on its own.
func (p *foldTestPair) do(fn func(fe *frontendPretty)) {
	fn(p.full)
	p.folded.dispatch(func() { fn(p.folded) })
}

func rowIDs(rows *dagui.Rows) []dagui.SpanID {
	ids := make([]dagui.SpanID, len(rows.Order))
	for i, row := range rows.Order {
		ids[i] = row.Span.ID
	}
	return ids
}

func TestLiveFoldMatchesFullLoad(t *testing.T) {
	t.Run("agent", func(t *testing.T) { testLiveFold(t, false) })
	t.Run("nested", func(t *testing.T) { testLiveFold(t, true) })
}

func testLiveFold(t *testing.T, nested bool) {
	p := newFoldTestPair(t, 20000, nested)
	stats := p.folded.db.Fold().Stats()
	t.Logf("retained %d of %d spans (%.1f%%), read back %d", stats.Retained, stats.Spans,
		100*float64(stats.Retained)/float64(stats.Spans), p.folded.db.Fold().Loads)
	require.Less(t, stats.Retained, stats.Spans*3/4, "spans fold")

	t.Run("rows", func(t *testing.T) {
		p.recalc(t)
		require.Equal(t, rowIDs(p.full.rows), rowIDs(p.folded.rows))
	})

	t.Run("expand", func(t *testing.T) {
		// expand every row with children, as a user opening them would
		for range 3 {
			for _, row := range slices.Clone(p.full.rows.Order) {
				if row.HasChildren && !row.Expanded {
					p.do(func(fe *frontendPretty) { fe.setExpanded(row.Span.ID, true) })
				}
			}
			p.recalc(t)
			require.Equal(t, rowIDs(p.full.rows), rowIDs(p.folded.rows))
		}
	})

	t.Run("zoom", func(t *testing.T) {
		var target dagui.SpanID
		for _, row := range p.full.rows.Order {
			if row.HasChildren && row.Depth >= 2 {
				target = row.Span.ID
				break
			}
		}
		require.True(t, target.IsValid())
		p.do(func(fe *frontendPretty) { fe.ZoomedSpan = target })
		p.recalc(t)
		require.Equal(t, rowIDs(p.full.rows), rowIDs(p.folded.rows))
		p.do(func(fe *frontendPretty) { fe.ZoomedSpan = dagui.SpanID{} })
	})

	t.Run("search", func(t *testing.T) {
		// a name only folded spans carry
		ids, total := p.folded.db.Fold().MatchNames(func(string) bool { return true }, 0)
		require.NotZero(t, total)
		counts := map[string]int{}
		for _, span := range p.full.db.Spans.Order {
			counts[span.Name]++
		}
		// one the full view finds a match for: a span it can place
		var names []string
		for _, id := range ids {
			if n := p.full.db.Spans.Map[id].Name; !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		slices.SortFunc(names, func(a, b string) int { return counts[a] - counts[b] })
		p.recalc(t)
		var name string
		for _, n := range names {
			if counts[n] > foldSearchLimit {
				break
			}
			p.full.confirmSearch(n)
			if len(p.full.searchMatches) > 0 {
				name = n
				break
			}
		}
		require.NotEmpty(t, name)
		p.folded.dispatch(func() { p.folded.confirmSearch(name) })
		p.settle(t)
		p.recalc(t)
		p.do(func(fe *frontendPretty) { fe.refreshSearchMatches() })
		require.NotEmpty(t, p.full.searchMatches)
		require.Equal(t, p.full.searchMatches, p.folded.searchMatches)
		p.do(func(fe *frontendPretty) { fe.clearSearch() })
	})

	t.Run("final render", func(t *testing.T) {
		p.folded.SettleFold(t.Context())
		var want, got bytes.Buffer
		require.NoError(t, p.full.FinalRender(&want))
		require.NoError(t, p.folded.FinalRender(&got))
		require.NotEmpty(t, want.String())
		require.Equal(t, want.String(), got.String())
	})
}

// TestLiveFoldStreamedMatchesFullLoad streams a session into live TUIs, as
// the CLI receives it -- rendering a frame every few batches, reads landing
// asynchronously -- and requires the folding one to show the rows the full
// one does at the end.
func TestLiveFoldStreamedMatchesFullLoad(t *testing.T) {
	for _, shape := range []string{"agent", "nested"} {
		t.Run(shape, func(t *testing.T) {
			tr := spanfootprint.New(20000)
			full := streamFootprint(t, tr, shape, "full")
			folded := streamFootprint(t, tr, shape, "fold")
			require.Equal(t, rowIDs(full.rows), rowIDs(folded.rows))
		})
	}
}

// TestLiveFoldOtherTraces checks that only the live session's trace folds:
// another's spans (e.g. a resumed session's archived trace) can't be read
// back from the session.
func TestLiveFoldOtherTraces(t *testing.T) {
	db, lf := newFoldingDB()
	fe := NewWithDB(io.Discard, db)
	fe.reportOnly = true
	lf.attach(fe)
	store := &foldTestStore{latest: map[dagui.SpanID]sdktrace.ReadOnlySpan{}}
	fe.SetSpanQuery(dagui.TraceID{TraceID: [16]byte{9}}, store.query)
	tr := spanfootprint.New(2000)
	require.NoError(t, tr.Export(t.Context(), fe.SpanExporter(), 512, nil))
	stats := db.Fold().Stats()
	require.Equal(t, stats.Spans, stats.Retained)
}
