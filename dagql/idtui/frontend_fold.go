package idtui

import (
	"context"
	"io"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
)

// NoFoldEnv turns fold-on-ingest off for the live CLI: set, its frontend
// keeps every span it receives in full, as before.
const NoFoldEnv = "DAGGER_NO_FOLD"

// SpanQueryFunc reads the newest snapshots of spans from the store of the
// live session that streams them.
type SpanQueryFunc func(ctx context.Context, ids []dagui.SpanID) ([]sdktrace.ReadOnlySpan, error)

// FoldFrontend is a frontend whose DB folds the spans it doesn't render
// (dagui.WithFold), reading them back from the live session on demand.
type FoldFrontend interface {
	// SetSpanQuery starts folding the live session's trace: from then on,
	// the spans of trace arriving that the frontend doesn't render are
	// folded, and read back with query when a row is expanded over them,
	// zoomed into or searched for. Until it's called, and for other traces
	// (e.g. one a resumed agent session loads), every span is retained.
	SetSpanQuery(trace dagui.TraceID, query SpanQueryFunc)
	// SettleFold reads back the folded spans the final render shows, while
	// the session can still answer: call it before the session closes.
	SettleFold(ctx context.Context)
}

// foldQueryTimeout bounds a read of folded spans.
const foldQueryTimeout = 30 * time.Second

// foldReadBackoff is the first wait before a failed read of folded spans is
// asked again; each further failure doubles it, up to 10s.
var foldReadBackoff = 250 * time.Millisecond

// foldSearchLimit bounds the folded spans a name search reads back.
const foldSearchLimit = 1000

// foldSettleRounds bounds SettleFold's rounds: each reads back the
// children of the rows the last showed expanded.
const foldSettleRounds = 16

// liveFold is the live CLI's side of a folding DB.
type liveFold struct {
	fe *frontendPretty
	// query reads folded spans of trace back; nil until the session serves
	// span queries, and until then nothing folds (retain).
	trace dagui.TraceID
	query SpanQueryFunc
	// inflight counts the reads not dispatched yet; idle is closed when
	// there are none.
	mu       sync.Mutex
	inflight int
	idle     chan struct{}
	// reading marks a read under way; queued holds the spans asked for
	// meanwhile, by queuedReads loads, which the next round trip reads
	// together: one at a time, so a burst of promotions is few round
	// trips, not one per batch at once.
	reading     bool
	queued      []dagui.SpanID
	queuedReads int
	// Reads counts the round trips, ReadSpans the spans asked in them.
	Reads, ReadSpans int
}

func (lf *liveFold) begin() {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.inflight == 0 {
		lf.idle = make(chan struct{})
	}
	lf.inflight++
}

func (lf *liveFold) end() {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.inflight--
	if lf.inflight == 0 {
		close(lf.idle)
	}
}

// newFolding builds the pretty frontend of the live CLI: its DB folds what it
// doesn't render once the session it runs serves span queries (see
// SetSpanQuery), unless NoFoldEnv is set.
func newFolding(w io.Writer, reportOnly bool) *frontendPretty {
	if os.Getenv(NoFoldEnv) != "" {
		fe := NewWithDB(w, newLocalDB())
		fe.reportOnly = reportOnly
		return fe
	}
	db, lf := newFoldingDB()
	fe := NewWithDB(w, db)
	fe.reportOnly = reportOnly
	lf.attach(fe)
	return fe
}

// newFoldingDB builds the DB of a live CLI frontend that folds, to attach
// to the frontend built over it.
func newFoldingDB() (*dagui.DB, *liveFold) {
	lf := &liveFold{}
	db := dagui.NewDB(dagui.WithFold(dagui.FoldOptions{
		Retain:   lf.retain,
		Load:     lf.load,
		Foldable: lf.foldable,
	}))
	return db, lf
}

func (lf *liveFold) attach(fe *frontendPretty) {
	fe.fold = lf
	lf.fe = fe
}

// retain is the fold policy: everything until the session can read folded
// spans back, then what the live CLI may render.
func (lf *liveFold) retain(span dagui.FoldSpan) bool {
	return lf.query == nil || dagui.RetainRendered(span)
}

// foldable folds only the live session's trace: what query reads.
func (lf *liveFold) foldable(trace dagui.TraceID) bool {
	return lf.query != nil && trace == lf.trace
}

// load reads back the folded spans the DB retained, asynchronously. It runs
// on the frontend's event loop, as every DB call does. While a read is
// under way, the spans asked for wait to be read with the next.
func (lf *liveFold) load(ids []dagui.SpanID) {
	query := lf.query
	if query == nil {
		return
	}
	lf.begin()
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.queued = append(lf.queued, ids...)
	lf.queuedReads++
	if !lf.reading {
		lf.reading = true
		go lf.read(query)
	}
}

// read reads the queued spans back, until none are. A read that fails is
// asked again, with what was queued since, after a backoff.
func (lf *liveFold) read(query SpanQueryFunc) {
	var failures int
	for {
		lf.mu.Lock()
		ids, reads := lf.queued, lf.queuedReads
		lf.queued, lf.queuedReads = nil, 0
		if reads == 0 {
			lf.reading = false
			lf.mu.Unlock()
			return
		}
		lf.Reads++
		lf.ReadSpans += len(ids)
		lf.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), foldQueryTimeout)
		spans, err := query(ctx, ids)
		cancel()
		if len(spans) > 0 {
			lf.fe.dispatch(func() {
				lf.fe.exportSpansLocked(dagui.FoldFill(context.Background()), spans)
			})
		}
		if err != nil {
			// what it did answer is taken; the DB keeps only the first
			// answer for each span
			slog.Warn("error reading folded spans", "spans", len(ids), "err", err)
			lf.mu.Lock()
			lf.queued = append(ids, lf.queued...)
			lf.queuedReads += reads
			lf.mu.Unlock()
			failures++
			time.Sleep(min(time.Duration(1<<min(failures-1, 6))*foldReadBackoff, 10*time.Second))
			continue
		}
		failures = 0
		// done once dispatched: what's dispatched after runs after it (and
		// the console applies it with its next step)
		for range reads {
			lf.end()
		}
	}
}

// wait blocks until the reads in flight are dispatched for ingestion, or
// ctx ends.
func (lf *liveFold) wait(ctx context.Context) {
	lf.mu.Lock()
	if lf.inflight == 0 {
		lf.mu.Unlock()
		return
	}
	idle := lf.idle
	lf.mu.Unlock()
	select {
	case <-idle:
	case <-ctx.Done():
	}
}

func (fe *frontendPretty) SetSpanQuery(trace dagui.TraceID, query SpanQueryFunc) {
	fe.dispatch(func() {
		if fe.fold != nil {
			fe.fold.trace = trace
			fe.fold.query = query
		}
	})
}

func (fe *frontendPretty) folding() bool {
	return fe.fold != nil && fe.fold.query != nil
}

// readFoldedRowsLocked reads back the folded children of what the view
// shows expanded -- its rows and the span it's zoomed into -- after a
// recalculation, so that they render a round trip later.
func (fe *frontendPretty) readFoldedRowsLocked() {
	if !fe.folding() {
		return
	}
	f := fe.db.Fold()
	for _, id := range fe.foldedRowsLocked(nil) {
		f.Expand(id)
	}
}

// foldedRowsLocked appends the spans whose folded children the view would
// show (see dagui.RowsView.FoldedPlacements).
func (fe *frontendPretty) foldedRowsLocked(ids []dagui.SpanID) []dagui.SpanID {
	if zoomed := fe.db.Spans.Map[fe.ZoomedSpan]; zoomed != nil {
		ids = fe.rowsView.FoldedPlacements(zoomed, ids)
	}
	for _, row := range fe.rows.Order {
		if row.Expanded {
			ids = fe.rowsView.FoldedPlacements(row.Span, ids)
		}
	}
	return ids
}

// SettleFold reads back, round after round, the folded children of the rows
// the final render shows expanded, until there are none, the reads fail, or
// ctx ends.
func (fe *frontendPretty) SettleFold(ctx context.Context) {
	lf := fe.fold
	if lf == nil {
		return
	}
	for range foldSettleRounds {
		lf.wait(ctx)
		if ctx.Err() != nil {
			return
		}
		var asked bool
		done := make(chan struct{})
		fe.dispatch(func() {
			defer close(done)
			if !fe.folding() {
				return
			}
			f := fe.db.Fold()
			for _, id := range fe.finalFoldedRowsLocked() {
				if f.Expand(id) {
					asked = true
				}
			}
		})
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
		if !asked {
			return
		}
	}
}

// finalFoldedRowsLocked lists the spans whose folded children the final
// render would show, as laid out by a throwaway frontend over the same DB
// and options, so the live view is left as it is.
func (fe *frontendPretty) finalFoldedRowsLocked() []dagui.SpanID {
	final := newWithTerminalProfile(io.Discard, fe.db, tuist.NewHeadlessTerminal(consoleWidth, consoleHeight), termenv.Ascii)
	final.reportOnly = true
	final.FrontendOpts = fe.FrontendOpts
	final.SpanExpanded = maps.Clone(fe.SpanExpanded)
	final.SpanVerbosity = maps.Clone(fe.SpanVerbosity)
	final.pinnedZoom = fe.pinnedZoom
	final.reportScopedSubtree = fe.reportScopedSubtree
	final.setupFinalRenderLocked()
	ids := final.foldedRowsLocked(nil)
	// what the live view shows expanded, too: it renders until the end
	return fe.foldedRowsLocked(ids)
}

// readFoldedMatchesLocked reads back the folded spans whose name matches a
// search's query, so they take part in it once they arrive, and returns how
// many more match than it reads (foldSearchLimit).
func (fe *frontendPretty) readFoldedMatchesLocked(query string) int {
	if !fe.folding() {
		return 0
	}
	query = strings.ToLower(query)
	f := fe.db.Fold()
	ids, total := f.MatchNames(func(name string) bool {
		return strings.Contains(strings.ToLower(name), query)
	}, foldSearchLimit)
	f.Retain(ids...)
	return total - len(ids)
}

// readFoldedTargetLocked reads back a folded span a source's log search
// found a match in, reporting whether it's being read.
func (fe *frontendPretty) readFoldedTargetLocked(id dagui.SpanID) bool {
	if !fe.folding() || !fe.db.Fold().IsFolded(id) {
		return false
	}
	fe.db.Fold().Retain(id)
	return true
}
