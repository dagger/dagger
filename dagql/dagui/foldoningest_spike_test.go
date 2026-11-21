package dagui

// Spike: fold-on-ingest (see hack/designs/fold-on-ingest.md).
//
// Instead of keeping every span of a live session in the DB, the CLI keeps
// full state (a *Span in a DB) only for the spans it may render -- the
// "retained" spans -- and folds every other span into its retained
// ancestors' aggregates as it arrives, keeping only a compact stub: its place
// in the tree, its last roll-up category and a few flags. A folded span is
// loaded again (from the engine's store, at its latest snapshot) when
// something needs it in full: here, a retained descendant.
//
// This file measures what a stub costs per span and prototypes one aggregate
// end to end -- the roll-up counts (Span.updateRollUpAncestors) -- to surface
// the ordering problems for real: start/end updates, children before
// parents, and promotion mid-stream.

import (
	"context"
	"encoding/binary"
	"flag"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"unsafe"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/dagger/dagger/dagql/internal/spanfootprint"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// foldState packs a stub's state: its roll-up category in the low 3 bits,
// then flags.
type foldState uint16

const (
	foldCatMask     foldState = 0x7 // spanStateCategory
	foldReceived    foldState = 1 << 3
	foldRetained    foldState = 1 << 4
	foldBoundary    foldState = 1 << 5
	foldEncapsulate foldState = 1 << 6
	// foldUIPassthrough is dagui's existing UI passthrough flag
	// (SpanSnapshot.Passthrough), which countChildren flattens through.
	foldUIPassthrough foldState = 1 << 7
	foldInternal      foldState = 1 << 8
	foldRollUpLogs    foldState = 1 << 9
	foldHasLogs       foldState = 1 << 10
	foldKind          foldState = 1 << 11 // a surfaced kind (see isIndexedKind)
	foldReveal        foldState = 1 << 12
)

func (s foldState) cat() spanStateCategory { return spanStateCategory(s & foldCatMask) }

// foldPolicy decides which spans are retained when they arrive.
type foldPolicy int

const (
	// foldRetainTop retains the root and its direct children only: the rows
	// of a collapsed session. Everything else is folded, so it measures the
	// cost of a folded span.
	foldRetainTop foldPolicy = iota
	// foldRetainKinds also retains failed and revealed spans and surfaced
	// kinds (checks, tests, LLM messages, agents, services) that may roll up
	// to the root (no boundary or encapsulating stub above them), and the
	// ancestors of every retained span. (Reveal really stops at the nearest
	// boundary rather than the root; close enough for measuring.)
	foldRetainKinds
)

func (p foldPolicy) String() string {
	return map[foldPolicy]string{foldRetainTop: "top", foldRetainKinds: "kinds"}[p]
}

// foldIndex is the fold-on-ingest layer: a stub per span, struct-of-arrays,
// indexed by a dense uint32; full state for retained spans in db.
type foldIndex struct {
	ids   foldIDTable
	spans []SpanID // idx -> span ID
	// idx+1 of the parent, first child and next sibling; 0 for none.
	parent, firstChild, nextSib []uint32
	state                       []foldState

	// agg holds the roll-up counts of the retained spans, over ALL their
	// descendants, retained or folded.
	agg map[uint32]*RollUpState

	// db holds the retained spans in full.
	db      *DB
	pending []sdktrace.ReadOnlySpan

	policy foldPolicy
	// load reads a span's latest snapshot from the engine's store: done
	// reports whether its end was seen. It stands for a span query round
	// trip (POST /v1/traces/query in the design).
	load func(id SpanID, done bool) sdktrace.ReadOnlySpan

	loads  int // promotions that read a folded span back
	walked int // stubs visited summing subtrees (promotion, late links)
}

func newFoldIndex(policy foldPolicy, load func(SpanID, bool) sdktrace.ReadOnlySpan) *foldIndex {
	return &foldIndex{
		agg:    map[uint32]*RollUpState{},
		db:     NewDB(),
		policy: policy,
		load:   load,
	}
}

func (fi *foldIndex) Shutdown(context.Context) error { return nil }

func (fi *foldIndex) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, s := range spans {
		fi.ingest(s)
	}
	if len(fi.pending) > 0 {
		if err := fi.db.ExportSpans(ctx, fi.pending); err != nil {
			return err
		}
		clear(fi.pending)
		fi.pending = fi.pending[:0]
	}
	return nil
}

func (fi *foldIndex) intern(id SpanID) uint32 {
	if i, ok := fi.ids.get(id, fi.spans); ok {
		return i
	}
	i := uint32(len(fi.spans))
	fi.spans = append(fi.spans, id)
	fi.parent = append(fi.parent, 0)
	fi.firstChild = append(fi.firstChild, 0)
	fi.nextSib = append(fi.nextSib, 0)
	fi.state = append(fi.state, 0)
	fi.ids.put(id, i, fi.spans)
	return i
}

func (fi *foldIndex) ingest(s sdktrace.ReadOnlySpan) {
	i := fi.intern(SpanID{s.SpanContext().SpanID()})
	first := fi.state[i]&foldReceived == 0
	fi.state[i] |= foldReceived | foldFlags(s)
	if first {
		if p := s.Parent().SpanID(); p.IsValid() {
			pi := fi.intern(SpanID{p})
			fi.link(i, pi)
			if fi.state[i]&foldRetained != 0 {
				// i was retained before it arrived, for a retained
				// descendant: so are its newly known ancestors.
				fi.promote(pi, nil)
			}
		}
	}
	fi.setCategory(i, foldCategory(s))
	switch {
	case fi.state[i]&foldRetained != 0:
		fi.pending = append(fi.pending, s)
	case fi.shouldRetain(i):
		fi.promote(i, s)
	}
}

// link places i under p. Spans that arrived beneath i before i did only
// reached the aggregates up to i: credit the new ancestors with them.
func (fi *foldIndex) link(i, p uint32) {
	fi.parent[i] = p + 1
	fi.nextSib[i] = fi.firstChild[p]
	fi.firstChild[p] = i + 1
	if fi.firstChild[i] == 0 {
		return
	}
	sub := fi.subtreeCounts(i)
	for a := p + 1; a != 0; a = fi.parent[a-1] {
		if agg := fi.agg[a-1]; agg != nil {
			agg.PendingCount += sub.PendingCount
			agg.RunningCount += sub.RunningCount
			agg.CachedCount += sub.CachedCount
			agg.SuccessCount += sub.SuccessCount
			agg.FailedCount += sub.FailedCount
			agg.CanceledCount += sub.CanceledCount
		}
	}
}

// setCategory is updateRollUpAncestors over stubs: O(depth), touching only
// the retained ancestors' counts.
func (fi *foldIndex) setCategory(i uint32, c spanStateCategory) {
	old := fi.state[i].cat()
	if old == c {
		return
	}
	fi.state[i] = fi.state[i]&^foldCatMask | foldState(c)
	for a := fi.parent[i]; a != 0; a = fi.parent[a-1] {
		if fi.state[a-1]&foldRetained == 0 {
			continue
		}
		agg := fi.agg[a-1]
		if old != stateUnknown {
			agg.decrementCategory(old)
		}
		agg.incrementCategory(c)
	}
}

// subtreeCounts sums the categories of i's descendants over the stub tree.
func (fi *foldIndex) subtreeCounts(i uint32) RollUpState {
	var st RollUpState
	stack := []uint32{fi.firstChild[i]}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for ; c != 0; c = fi.nextSib[c-1] {
			fi.walked++
			st.incrementCategory(fi.state[c-1].cat())
			if fi.firstChild[c-1] != 0 {
				stack = append(stack, fi.firstChild[c-1])
			}
		}
	}
	return st
}

func (fi *foldIndex) shouldRetain(i uint32) bool {
	p := fi.parent[i]
	if p == 0 || fi.parent[p-1] == 0 && fi.state[p-1]&foldReceived != 0 {
		// the root, or one of its children
		return true
	}
	if fi.policy == foldRetainTop {
		return false
	}
	// A failure below a boundary or encapsulation (e.g. an integration
	// test's expected failure in a nested engine) only counts in its
	// ancestors' roll-ups; an error origin is loaded when the failed row
	// naming it renders its error.
	st := fi.state[i]
	return (st.cat() == stateFailed || st&(foldReveal|foldKind) != 0) && fi.mayRollUp(i)
}

// mayRollUp is spanMayRollUp over stubs, against the whole trace: no
// boundary or encapsulating ancestor. An ancestor not received yet can't be
// ruled out, so it doesn't contain.
func (fi *foldIndex) mayRollUp(i uint32) bool {
	for a := fi.parent[i]; a != 0; a = fi.parent[a-1] {
		if fi.state[a-1]&(foldBoundary|foldEncapsulate) != 0 {
			return false
		}
	}
	return true
}

// promote retains i, whose current snapshot is s (nil to read it back), and
// its ancestors not retained yet, reading each received one back from the
// store.
func (fi *foldIndex) promote(i uint32, s sdktrace.ReadOnlySpan) {
	for j := i; ; {
		if fi.state[j]&foldRetained != 0 {
			return
		}
		fi.state[j] |= foldRetained
		sub := fi.subtreeCounts(j)
		fi.agg[j] = &sub
		switch {
		case j == i && s != nil:
			fi.pending = append(fi.pending, s)
		case fi.state[j]&foldReceived != 0:
			fi.loads++
			fi.pending = append(fi.pending, fi.load(fi.spans[j], fi.state[j].cat() != stateRunning))
		default:
			// not received yet: it is ingested in full when it arrives
		}
		if fi.parent[j] == 0 {
			return
		}
		j = fi.parent[j] - 1
	}
}

func foldCategory(s sdktrace.ReadOnlySpan) spanStateCategory {
	if s.EndTime().Before(s.StartTime()) {
		return stateRunning
	}
	var pending, cached, canceled bool
	for _, attr := range s.Attributes() {
		switch string(attr.Key) {
		case telemetry.PendingAttr:
			pending = attr.Value.AsBool()
		case telemetry.CachedAttr:
			cached = attr.Value.AsBool()
		case telemetry.CanceledAttr:
			canceled = attr.Value.AsBool()
		}
	}
	switch {
	case pending:
		return statePending
	case cached:
		return stateCached
	case canceled:
		return stateCanceled
	case s.Status().Code == codes.Error:
		return stateFailed
	default:
		return stateSuccess
	}
}

func foldFlags(s sdktrace.ReadOnlySpan) foldState {
	var st foldState
	for _, attr := range s.Attributes() {
		switch string(attr.Key) {
		case telemetry.UIBoundaryAttr:
			if attr.Value.AsBool() {
				st |= foldBoundary
			}
		case telemetry.UIEncapsulateAttr:
			if attr.Value.AsBool() {
				st |= foldEncapsulate
			}
		case telemetry.UIPassthroughAttr:
			if attr.Value.AsBool() {
				st |= foldUIPassthrough
			}
		case telemetry.UIInternalAttr:
			if attr.Value.AsBool() {
				st |= foldInternal
			}
		case telemetry.UIRollUpLogsAttr:
			if attr.Value.AsBool() {
				st |= foldRollUpLogs
			}
		case telemetry.UIRevealAttr:
			if attr.Value.AsBool() {
				st |= foldReveal
			}
		case telemetry.LLMRoleAttr, telemetryattrs.AgentAttr, telemetry.CheckNameAttr,
			telemetry.GeneratorNameAttr, string(semconv.TestCaseNameKey),
			string(semconv.TestSuiteNameKey), telemetryattrs.ServiceAttr,
			telemetryattrs.ServiceNameAttr:
			st |= foldKind
		}
	}
	return st
}

// foldIDTable maps span IDs to stub indexes: open addressing over the
// indexes alone (idx+1, 0 empty), comparing against the stubs' own span IDs,
// so a span ID is stored once.
type foldIDTable struct {
	slots []uint32
	n     int
}

func foldHash(id SpanID) uint64 {
	h := binary.LittleEndian.Uint64(id.SpanID[:])
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

func (t *foldIDTable) get(id SpanID, spans []SpanID) (uint32, bool) {
	if len(t.slots) == 0 {
		return 0, false
	}
	mask := uint64(len(t.slots) - 1)
	for h := foldHash(id) & mask; ; h = (h + 1) & mask {
		v := t.slots[h]
		if v == 0 {
			return 0, false
		}
		if spans[v-1] == id {
			return v - 1, true
		}
	}
}

func (t *foldIDTable) put(id SpanID, i uint32, spans []SpanID) {
	if (t.n+1)*4 > len(t.slots)*3 {
		old := t.slots
		t.slots = make([]uint32, max(1024, 2*len(old)))
		t.n = 0
		for _, v := range old {
			if v != 0 {
				t.insert(spans[v-1], v-1)
			}
		}
	}
	t.insert(id, i)
}

func (t *foldIDTable) insert(id SpanID, i uint32) {
	mask := uint64(len(t.slots) - 1)
	h := foldHash(id) & mask
	for t.slots[h] != 0 {
		h = (h + 1) & mask
	}
	t.slots[h] = i + 1
	t.n++
}

// stubBytes is the stub table's own footprint: what a folded span costs.
func (fi *foldIndex) stubBytes() int {
	return cap(fi.ids.slots)*4 +
		cap(fi.spans)*int(unsafe.Sizeof(SpanID{})) +
		(cap(fi.parent)+cap(fi.firstChild)+cap(fi.nextSib))*4 +
		cap(fi.state)*2
}

func (fi *foldIndex) retained() int {
	n := 0
	for _, st := range fi.state {
		if st&foldRetained != 0 {
			n++
		}
	}
	return n
}

// footprintLoad reads span i of tr back, as the engine store would.
func footprintLoad(tr *spanfootprint.Trace) func(SpanID, bool) sdktrace.ReadOnlySpan {
	return func(id SpanID, done bool) sdktrace.ReadOnlySpan {
		return tr.Stub(int(binary.BigEndian.Uint64(id.SpanID[:]))-1, done)
	}
}

// noLinks strips span links, which the spike's roll-up does not follow (see
// the design doc: causal contributions need a side table).
type noLinks struct{ next sdktrace.SpanExporter }

func (e noLinks) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = stripLinks(s)
	}
	return e.next.ExportSpans(ctx, out)
}

func (e noLinks) Shutdown(ctx context.Context) error { return nil }

func stripLinks(s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	stub := tracetest.SpanStubFromReadOnlySpan(s)
	stub.Links = nil
	return stub.Snapshot()
}

// truthCounts counts the categories of span's descendants in a fully
// ingested DB, from scratch.
func truthCounts(span *Span) RollUpState {
	var st RollUpState
	var walk func(*Span)
	walk = func(s *Span) {
		for _, c := range s.ChildSpans.Spans() {
			if c.ParentSpan != s {
				continue
			}
			st.incrementCategory(c.currentStateCategory())
			walk(c)
		}
	}
	walk(span)
	return st
}

func rollUpOf(span *Span) RollUpState {
	if span.rollUpState == nil {
		return RollUpState{}
	}
	return *span.rollUpState
}

// compareFold checks the fold-on-ingest roll-up counts of every retained span
// against a from-scratch count over the fully ingested trace, and reports
// how many of today's incrementally maintained counts are off.
func compareFold(t *testing.T, fi *foldIndex, full *DB) (todayWrong int) {
	t.Helper()
	checked := 0
	for i, st := range fi.state {
		if st&foldRetained == 0 || st&foldReceived == 0 {
			continue
		}
		span := full.Spans.Map[fi.spans[i]]
		if span == nil {
			t.Fatalf("retained span %s not in the full DB", fi.spans[i])
		}
		want := truthCounts(span)
		if got := *fi.agg[uint32(i)]; got != want {
			t.Errorf("span %s (%s): fold-on-ingest counts %+v, want %+v", span.ID, span.Name, got, want)
		}
		if rollUpOf(span) != want {
			todayWrong++
		}
		checked++
		if retained := fi.db.Spans.Map[fi.spans[i]]; retained == nil || !retained.Received {
			t.Errorf("retained span %s not received in full by the fold-on-ingest DB", span.ID)
		}
		if p := fi.parent[i]; p != 0 && fi.state[p-1]&foldRetained == 0 {
			t.Errorf("retained span %s has a folded parent", span.ID)
		}
	}
	if checked == 0 {
		t.Fatal("no retained spans checked")
	}
	return todayWrong
}

// TestFoldOnIngestRollUp feeds a live session (every span exported as it
// starts, then again as it ends) to both today's DB and the fold-on-ingest
// index, and checks the roll-up counts of every retained span.
func TestFoldOnIngestRollUp(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	for _, c := range foldCases {
		t.Run(c.name, func(t *testing.T) {
			full := NewDB()
			if err := tr.Export(t.Context(), c.exporter(noLinks{full}), 512, nil); err != nil {
				t.Fatal(err)
			}
			load := c.load(tr)
			fi := newFoldIndex(c.policy, func(id SpanID, done bool) sdktrace.ReadOnlySpan {
				return stripLinks(load(id, done))
			})
			if err := tr.Export(t.Context(), c.exporter(noLinks{fi}), 512, nil); err != nil {
				t.Fatal(err)
			}
			if wrong := compareFold(t, fi, full); wrong != 0 {
				t.Errorf("today's DB got %d retained spans' counts wrong in a live-order stream", wrong)
			}
			t.Logf("%s: %d/%d spans retained, %d loaded back, %d stub visits",
				c.name, fi.retained(), n, fi.loads, fi.walked)
		})
	}
}

// TestFoldOnIngestLateParents delivers each span once, completed, children
// before parents (as an import may): a span's subtree arrives before it
// links it to its ancestors.
func TestFoldOnIngestLateParents(t *testing.T) {
	const n = 30_000
	tr := spanfootprint.New(n)
	spans := make([]sdktrace.ReadOnlySpan, n)
	for i := range n {
		spans[i] = stripLinks(tr.Stub(i, true))
	}
	slices.Reverse(spans)
	full := NewDB()
	load := footprintLoad(tr)
	fi := newFoldIndex(foldRetainKinds, func(id SpanID, done bool) sdktrace.ReadOnlySpan {
		return stripLinks(load(id, done))
	})
	for chunk := range slices.Chunk(spans, 512) {
		if err := full.ExportSpans(t.Context(), chunk); err != nil {
			t.Fatal(err)
		}
		if err := fi.ExportSpans(t.Context(), chunk); err != nil {
			t.Fatal(err)
		}
	}
	wrong := compareFold(t, fi, full)
	// Not asserted: today's incremental counts miss the spans beneath a span
	// that joined its parent late (updateRollUpAncestors only propagates
	// the span's own category). Logged as evidence.
	t.Logf("today's DB: %d of %d retained spans' roll-up counts are wrong when children arrive first",
		wrong, fi.retained())
	t.Logf("%d spans retained, %d loaded back, %d stub visits", fi.retained(), fi.loads, fi.walked)
}

var foldFootprintIndex *foldIndex

var foldFootprintCase = flag.String("fold.case", "", "only measure this fold-on-ingest case (see foldCases)")

// BenchmarkFoldOnIngestFootprint reports the live heap the fold-on-ingest
// layer holds per span after a live session, next to BenchmarkSpanFootprint's
// numbers for today's DB:
//
//	go test ./dagql/dagui -run '^$' -bench 'FoldOnIngestFootprint|SpanFootprint' \
//	  -benchtime 1x -footprint.sizes 1000000
func BenchmarkFoldOnIngestFootprint(b *testing.B) {
	sizes, err := spanfootprint.Sizes(*footprintSizes)
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range sizes {
		for _, c := range foldCases {
			if *foldFootprintCase != "" && *foldFootprintCase != c.name {
				continue
			}
			b.Run(c.name+"/"+strconv.Itoa(n), func(b *testing.B) {
				var heap, stub float64
				var retained int
				for b.Loop() {
					foldFootprintIndex = nil
					tr := spanfootprint.New(n)
					var before, after runtime.MemStats
					runtime.GC()
					runtime.ReadMemStats(&before)
					fi := newFoldIndex(c.policy, c.load(tr))
					if err := tr.Export(context.Background(), c.exporter(fi), 512, nil); err != nil {
						b.Fatal(err)
					}
					runtime.GC()
					runtime.ReadMemStats(&after)
					foldFootprintIndex = fi
					heap = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n)
					stub = float64(fi.stubBytes()) / float64(n)
					retained = fi.retained()
				}
				b.ReportMetric(heap, "B/span")
				b.ReportMetric(stub, "stub-B/span")
				b.ReportMetric(float64(retained), "retained")
				b.ReportMetric(100*float64(retained)/float64(n), "retained-%")
			})
		}
	}
}

// BenchmarkFoldOnIngestCPU compares the CPU cost of ingesting a live session
// into today's DB and into the fold-on-ingest layer.
func BenchmarkFoldOnIngestCPU(b *testing.B) {
	const n = 200_000
	tr := spanfootprint.New(n)
	perSpan := func(b *testing.B) {
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/span")
	}
	b.Run("today", func(b *testing.B) {
		for b.Loop() {
			if err := tr.Export(context.Background(), NewDB(), 512, nil); err != nil {
				b.Fatal(err)
			}
		}
		perSpan(b)
	})
	for _, c := range foldCases {
		b.Run("fold-"+c.name, func(b *testing.B) {
			for b.Loop() {
				fi := newFoldIndex(c.policy, c.load(tr))
				if err := tr.Export(context.Background(), c.exporter(fi), 512, nil); err != nil {
					b.Fatal(err)
				}
			}
			perSpan(b)
		})
	}
	b.Run("generator-only", func(b *testing.B) {
		for b.Loop() {
			if err := tr.Export(context.Background(), discardExporter{}, 512, nil); err != nil {
				b.Fatal(err)
			}
		}
		perSpan(b)
	})
}

// foldCases are the trace shapes and policies the benchmarks measure:
//
//   - top: only the root and its children retained; the cost of a folded span.
//   - kinds: spanfootprint's agent session as is, whose messages, tests and
//     checks mostly sit outside any boundary, so a quarter of it is retained.
//   - kinds-nested: the same trace with every call tree under an agent loop
//     made a boundary, like tool calls running nested engines (integration
//     tests, benchmarks): what they run no longer surfaces.
var foldCases = []foldCase{
	{"top", foldRetainTop, false},
	{"kinds", foldRetainKinds, false},
	{"kinds-nested", foldRetainKinds, true},
}

type foldCase struct {
	name   string
	policy foldPolicy
	nested bool
}

// exporter is what the case feeds the trace to.
func (c foldCase) exporter(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	if c.nested {
		return nestedTrees{next}
	}
	return next
}

// load reads a span back as the case's trace shows it.
func (c foldCase) load(tr *spanfootprint.Trace) func(SpanID, bool) sdktrace.ReadOnlySpan {
	load := footprintLoad(tr)
	if !c.nested {
		return load
	}
	return func(id SpanID, done bool) sdktrace.ReadOnlySpan {
		return nestTree(load(id, done))
	}
}

// nestedTrees makes every span whose parent is an agent loop a boundary.
type nestedTrees struct{ next sdktrace.SpanExporter }

func (e nestedTrees) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = nestTree(s)
	}
	return e.next.ExportSpans(ctx, out)
}

func (e nestedTrees) Shutdown(context.Context) error { return nil }

func nestTree(s sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	p := s.Parent().SpanID()
	if !p.IsValid() {
		return s
	}
	if pi := binary.BigEndian.Uint64(p[:]) - 1; pi == 0 || pi >= spanfootprint.LongLived {
		return s
	}
	stub := tracetest.SpanStubFromReadOnlySpan(s)
	stub.Attributes = append(slices.Clip(stub.Attributes), attribute.Bool(telemetry.UIBoundaryAttr, true))
	return stub.Snapshot()
}

type discardExporter struct{}

func (discardExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (discardExporter) Shutdown(context.Context) error                             { return nil }
