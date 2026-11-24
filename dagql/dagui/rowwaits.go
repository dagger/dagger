package dagui

import (
	"slices"
	"sort"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Waits below a row.
//
// A row's own time breakdown counts the waits the row recorded itself. A row
// whose work is done by the steps under it, such as a module function call
// whose requests wait on a step another module is running, reads as working
// all the while those steps wait. UpdateRowWaits counts that as the row's
// waiting too: a row is waiting at an instant when something under it waits
// on work outside the row and nothing under it is working.
//
// That needs the row's whole subtree, so it is computed bottom-up over the
// DB, once, and kept up to date by dropping the summaries of what changed.
// Each row's result is published on its span (SpanSnapshot.RowWaits), where
// TimeBreakdown reads it, and remote frontends receive it with the span.
//
// The rules, at each span:
//
//   - A wait counts for a row only while the step it waited on is outside the
//     row: it rises to just below the lowest row holding both.
//   - Concurrent work wins: while any step under a span does its own work, the
//     span is working, and no waiting rises past it, its own included. A step
//     waiting on another step counts as whatever that step is doing, so a wait
//     chain continues through the waits recorded below the step it reaches.
//   - A lazy evaluation is the work of the call it evaluates, not of the row
//     it runs under.
//   - A module function's process runs for the whole call; it isn't work.
//     What the entrypoint does before starting it is.
//   - A wait on a step whose place in the tree isn't known yet stays where it
//     was recorded, until the step and its ancestors arrive.
//   - A view holding part of a trace (PartialTrace) leaves out a row until
//     its whole subtree is loaded.

// RowWaits is a row's waiting as UpdateRowWaits computed it from the row's
// whole subtree: its own waits, and the stretches where something under it
// waited on work outside it while nothing under it worked.
type RowWaits struct {
	// AsOf is the time it was computed as of. A stretch ending right at AsOf
	// was still going on then; on a row still running, it is taken to go on
	// until the next update.
	AsOf time.Time
	// Segments are the row's waiting stretches, in time order. A stretch
	// recorded below the row names that row in Via.
	Segments []TimeSegment `json:",omitempty"`
}

// rowWaitsState holds what UpdateRowWaits keeps between updates.
type rowWaitsState struct {
	partial bool
	// summaries holds each span's summary, dropped when it may have changed.
	summaries map[*Span]*waitSummary
	// changed holds the spans changed since the last update.
	changed map[*Span]struct{}
	// nextRound holds the spans changed while a round was under way, which
	// it computes as of a time before they changed: the next round computes
	// them again, though they may have finished, as running spans are.
	nextRound map[*Span]struct{}
	// waiters maps each span to the spans with a wait link on it.
	waiters map[*Span]*SpanSet
	// heldBack holds the spans whose summary held back a wait, because the
	// place of the step it waited on wasn't known yet.
	heldBack map[*Span]struct{}
	// publishing marks UpdateRowWaits' own span updates, which change
	// nothing a summary reads.
	publishing bool
	now        time.Time
	// labels caches blocker labels for one update (label).
	labels map[*Span]string
	// queue holds the spans whose summaries were dropped and are not yet
	// computed again, deepest first; pending maps each to its depth.
	queue   []*Span
	pending map[*Span]int
}

// waitSummary is one span's summary of the waiting in its subtree.
type waitSummary struct {
	// loaded reports whether the span's whole subtree is loaded. A summary
	// that isn't says nothing.
	loaded bool
	// resolved reports whether the span's own waits are known. A span
	// waiting on a step that isn't loaded reads as working, as its own
	// breakdown does.
	resolved bool
	painted  []Interval
	// waits are the row's waiting stretches: its own waits, and the waits
	// below it on work outside it, where nothing under it worked.
	waits []waitStretch
	// rising is what the span passes up to its parent: its waits where
	// nothing under it worked.
	rising []waitStretch
	// computing marks the stand-in for a summary being computed, read by a
	// wait chain that came back to it; provisional marks a summary computed
	// with one standing in for a child's, which isn't kept.
	computing, provisional bool
}

// waitStretch is a stretch during which a step waited on blocker.
type waitStretch struct {
	seg TimeSegment
	// blocker is the step waited on; nil when it is outside the trace.
	blocker *Span
	// origin is the span whose own breakdown recorded the wait.
	origin *Span
}

func (w waitStretch) start() time.Time { return w.seg.Start }
func (w waitStretch) end() time.Time   { return w.seg.End }

// resolved is the part of w within win, reached by a wait chain whose
// blockers so far are path.
func (w *waitStretch) resolved(win waitWindow, via string, path []*Span) resolvedWait {
	for _, b := range w.seg.Blockers {
		if w.origin != nil {
			path = appendResolvedBlocker(path, w.origin.db.Spans.Map[b.Target])
		}
	}
	path = appendResolvedBlocker(path, w.blocker)
	return resolvedWait{
		start:    win.start,
		end:      win.end,
		blocker:  w.blocker,
		blockers: path,
		indirect: true,
		via:      via,
		unplaced: w.seg.unplaced,
	}
}

// label names blocker as blockerLabel does, once per update: reading a
// command line from its exec's attributes is costly, and a busy blocker is
// named for many rows.
func (st *rowWaitsState) label(blocker *Span) string {
	if st == nil {
		return blockerLabel(blocker)
	}
	if l, ok := st.labels[blocker]; ok {
		return l
	}
	l := blockerLabel(blocker)
	st.labels[blocker] = l
	return l
}

// UpdateRowWaits brings every row's waiting (SpanSnapshot.RowWaits) up to
// date with the spans received so far, as of now. It returns the spans whose
// waiting changed, each updated as any changed span is, so that a DB serving
// a remote frontend sends it (UpdatedSnapshots).
//
// The first call computes it for the whole DB; later calls recompute only what
// changed since. With a budget above zero, a call stops once it has spent the
// budget and reports that work is left, and the next call carries on, so that
// a caller drawing a view or serving one is never held up for long; rows not
// recomputed yet keep their last result meanwhile. The calls carrying a round
// of work on compute it as of the now its first call was given. The DB keeps no extra state
// until the first call, so only a frontend that shows waiting calls it, and
// rows read their own breakdown until it does.
func (db *DB) UpdateRowWaits(now time.Time, budget time.Duration) (changed []*Span, more bool) {
	started := time.Now()
	st := db.rowWaits
	if st == nil || st.partial != db.PartialTrace {
		st = &rowWaitsState{
			partial:   db.PartialTrace,
			summaries: map[*Span]*waitSummary{},
			changed:   map[*Span]struct{}{},
			nextRound: map[*Span]struct{}{},
			waiters:   map[*Span]*SpanSet{},
			heldBack:  map[*Span]struct{}{},
			pending:   map[*Span]int{},
		}
		db.rowWaits = st
		for _, span := range db.Spans.Order {
			st.changed[span] = struct{}{}
		}
	}
	if len(st.queue) == 0 {
		// A new round, computed as of now throughout, so that a stretch
		// still going on ends at the same time in every summary of it,
		// whichever call computes the summary. Running spans have grown
		// since the last round.
		st.now = now
		for span := range st.nextRound {
			st.changed[span] = struct{}{}
		}
		clear(st.nextRound)
		for span := range db.runningSpans {
			st.changed[span] = struct{}{}
		}
	}
	if len(st.changed) > 0 {
		st.index()
		// a held-back wait may rise now that more spans have arrived
		for span := range st.heldBack {
			st.changed[span] = struct{}{}
		}
		clear(st.heldBack)
		for _, span := range st.drop() {
			if _, ok := st.pending[span]; !ok {
				st.pending[span] = depth(span)
				st.queue = append(st.queue, span)
			}
		}
		clear(st.changed)
		// deepest first, so that each summary reads its children's
		slices.SortStableFunc(st.queue, func(a, b *Span) int { return st.pending[b] - st.pending[a] })
	}
	if len(st.queue) == 0 {
		return nil, false
	}

	st.labels = map[*Span]string{}
	st.publishing = true
	defer func() { st.publishing = false }()
	done := 0
	for _, span := range st.queue {
		if budget > 0 && done > 0 && time.Since(started) >= budget {
			break
		}
		done++
		delete(st.pending, span)
		if st.publish(span, st.summary(span), st.now) {
			changed = append(changed, span)
		}
	}
	st.queue = slices.Delete(st.queue, 0, done)
	if budget <= 0 && len(st.queue) == 0 && len(st.nextRound) > 0 {
		// without a budget, settle what changed during the round just
		// finished too, as a final report needs
		settled, more := db.UpdateRowWaits(now, 0)
		return append(changed, settled...), more
	}
	return changed, len(st.queue) > 0
}

// index records the wait links of the changed spans in the reverse index.
func (st *rowWaitsState) index() {
	for span := range st.changed {
		for i := range span.Links {
			if !span.Links[i].IsWait() {
				continue
			}
			target := span.db.Spans.Map[span.Links[i].SpanContext.SpanID]
			if target == nil {
				continue
			}
			if st.waiters[target] == nil {
				st.waiters[target] = &SpanSet{}
			}
			st.waiters[target].add(span)
		}
	}
}

// depth is span's depth in the tree loaded.
func depth(span *Span) int {
	d := 0
	for s := span.ParentSpan; s != nil && d < 1000; s = s.ParentSpan {
		d++
	}
	return d
}

// noteRowWaitsChange records a span change for the next UpdateRowWaits.
func (db *DB) noteRowWaitsChange(span *Span) {
	if st := db.rowWaits; st != nil && !st.publishing {
		st.changed[span] = struct{}{}
		if len(st.queue) > 0 {
			st.nextRound[span] = struct{}{}
		}
	}
}

// drop drops the summaries of the changed spans and of everything that may
// read them, transitively: ancestors, cause and effect links, and the spans
// waiting on them or on a call execution span under them. It returns the
// spans dropped.
func (st *rowWaitsState) drop() []*Span {
	queue := make([]*Span, 0, len(st.changed))
	for span := range st.changed {
		queue = append(queue, span)
	}
	dropped := map[*Span]bool{}
	var out []*Span
	push := func(span *Span) {
		if !dropped[span] {
			queue = append(queue, span)
		}
	}
	for len(queue) > 0 {
		span := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for s := span; s != nil && !dropped[s]; s = s.ParentSpan {
			dropped[s] = true
			delete(st.summaries, s)
			out = append(out, s)
			for _, w := range st.waiters[s].Spans() {
				push(w)
			}
			for _, child := range s.ChildSpans.Spans() {
				if child.ParentSpan == s && child.isCallExec() {
					for _, w := range st.waiters[child].Spans() {
						push(w)
					}
				}
			}
			for _, linked := range s.causesViaLinks.Spans() {
				push(linked)
			}
			for _, linked := range s.effectsViaLinks.Spans() {
				push(linked)
			}
		}
	}
	return out
}

// summary returns span's summary, computing it if it was dropped.
func (st *rowWaitsState) summary(span *Span) *waitSummary {
	if sum, ok := st.summaries[span]; ok {
		return sum
	}
	// a wait chain coming back to a summary being computed reads nothing
	st.summaries[span] = &waitSummary{computing: true}
	sum := st.compute(span)
	if sum.provisional {
		delete(st.summaries, span)
	} else {
		st.summaries[span] = sum
	}
	return sum
}

func (st *rowWaitsState) compute(span *Span) *waitSummary {
	if !span.Received || span.db == nil {
		return &waitSummary{}
	}
	if st.partial && !span.childrenLoaded() {
		return &waitSummary{}
	}
	sum := &waitSummary{loaded: true, resolved: true}
	clk := &breakdownClock{now: st.now}
	sum.painted = span.paintedIntervals(clk)

	var own []waitStretch
	if span.mayWait() {
		hb := span.timeBreakdown(clk, st)
		sum.resolved = !hb.unresolved
		for _, seg := range hb.Segments {
			if !seg.Waiting {
				continue
			}
			own = append(own, waitStretch{
				seg:     seg,
				blocker: span.db.Spans.Map[seg.Target],
				origin:  span,
			})
		}
	}

	var fromChildren [][]waitStretch
	var work []Interval
	for _, child := range span.ChildSpans.Spans() {
		if child.ParentSpan != span {
			// an effect linked to span, not beneath it
			continue
		}
		csum := st.summary(child)
		if csum.computing || csum.provisional {
			// a wait chain from beneath child came up to span
			sum.provisional = true
			continue
		}
		if !csum.loaded {
			return &waitSummary{}
		}
		rising := st.placed(span, outside(csum.rising, span))
		fromChildren = append(fromChildren, rising)
		childWork := subtractIntervals(csum.painted, stretchIntervals(rising))
		if r := resolveBlocker(child); r != nil && r != child && !spanInSubtree(r, span) {
			// a lazy evaluation of a call outside span is that call's work
			childWork = nil
		}
		if child.FunctionPlumbing || child.isCallExec() && child.holdsFunctionPlumbing() {
			// While a module function's process runs, it is not work. What
			// runs before it, building the module and pulling its image, is.
			if proc := functionProcess(span, child, st.now); proc != nil {
				childWork = subtractIntervals(childWork, []Interval{proc.observedInterval(st.now)})
			}
		}
		work = append(work, childWork...)
	}
	// A folded child is plain work: its interval and its children's, with
	// nothing rising from it (see Fold.foldedWork).
	work = span.db.fold.foldedWork(span, clk, true, work)
	if !sum.resolved {
		// reads as working, as its own breakdown does
		return sum
	}
	work = mergeSpanIntervals(work)
	below := unionStretches(fromChildren...)
	below = subtractStretches(below, work)
	below = intersectStretches(below, sum.painted)
	sum.waits = overlayStretches(own, below)
	sum.rising = overlayStretches(subtractStretches(own, work), below)
	return sum
}

// chainWaits returns target's waits from below as wait windows, so that a
// wait chain reaching target continues through them. A call's execution span
// stands for the call: a module function's body runs its requests beneath the
// call's own span, beside the execution span. Each is resolved already, to
// the op working during it, so the chain ends there.
func (st *rowWaitsState) chainWaits(target *Span) []waitWindow {
	var out []waitWindow
	add := func(s *Span) {
		rising := st.summary(s).rising
		for i := range rising {
			w := &rising[i]
			out = append(out, waitWindow{start: w.start(), end: w.end(), target: w.blocker, below: w})
		}
	}
	add(target)
	if target.isCallExec() && target.ParentSpan != nil {
		add(target.ParentSpan)
	}
	return out
}

// placed keeps the stretches whose blocker's place in the tree is known, so
// whether it is outside a row is too, and holds back the rest, recording
// owner to be computed again when more spans arrive.
func (st *rowWaitsState) placed(owner *Span, in []waitStretch) []waitStretch {
	var out []waitStretch
	for _, w := range in {
		switch {
		case w.blocker == nil && !w.seg.unplaced, w.blocker != nil && isPlaced(w.blocker):
			out = append(out, w)
		default:
			st.heldBack[owner] = struct{}{}
		}
	}
	return out
}

// publish sets span's RowWaits from its summary, and reports whether they
// changed.
func (st *rowWaitsState) publish(span *Span, sum *waitSummary, now time.Time) bool {
	var segs []TimeSegment
	if sum.loaded && sum.resolved {
		for _, w := range sum.waits {
			seg := w.seg
			if w.origin != span {
				seg.Indirect = true
				seg.Via = st.label(w.origin)
			}
			segs = append(segs, seg)
		}
	}
	prev := span.RowWaits
	if prev == nil && len(segs) == 0 {
		return false
	}
	if prev != nil && sameSegments(prev.Segments, segs) && !reachesAsOf(prev) {
		return false
	}
	span.RowWaits = &RowWaits{AsOf: now, Segments: segs}
	span.db.update(span)
	return true
}

// reachesAsOf reports whether a stretch of rw was still going on when it was
// computed: it ends right at AsOf, where the intervals of running spans are
// cut. A stretch that ended later than AsOf ended for good, after the round
// began.
func reachesAsOf(rw *RowWaits) bool {
	n := len(rw.Segments)
	return n > 0 && rw.Segments[n-1].End.Equal(rw.AsOf)
}

func sameSegments(a, b []TimeSegment) bool {
	return slices.EqualFunc(a, b, func(x, y TimeSegment) bool {
		return x.Start.Equal(y.Start) && x.End.Equal(y.End) &&
			x.Waiting == y.Waiting && x.Target == y.Target && x.Label == y.Label &&
			x.Indirect == y.Indirect && x.Via == y.Via &&
			sameTimeBlockers(x.Blockers, y.Blockers)
	})
}

// rowWaitBlockers visits the blockers span's published waiting names, which
// its own waits may not reach, each with its ancestors: a view draws them,
// and jumps to them, from its tree (TimeBreakdownSpans).
func (span *Span) rowWaitBlockers(visit func(*Span)) {
	rw := span.RowWaits
	if rw == nil {
		return
	}
	visitPlaced := func(id SpanID) {
		for s, depth := span.db.Spans.Map[id], 0; s != nil && depth < 100; s, depth = s.ParentSpan, depth+1 {
			visit(s)
		}
	}
	for _, seg := range rw.Segments {
		if seg.Target.IsValid() {
			visitPlaced(seg.Target)
		}
		for _, b := range seg.Blockers {
			visitPlaced(b.Target)
		}
	}
}

// rowWaitSegments returns span's published waiting stretches as of clk: a
// stretch that reached the time they were computed goes on while the span is
// running.
func (span *Span) rowWaitSegments(clk *breakdownClock) []TimeSegment {
	rw := span.RowWaits
	if rw == nil || len(rw.Segments) == 0 {
		return nil
	}
	if !reachesAsOf(rw) || !span.IsRunningOrEffectsRunning() {
		return rw.Segments
	}
	// the last stretch goes on until now
	clk.live = true
	if !clk.now.After(rw.AsOf) {
		return rw.Segments
	}
	segs := slices.Clone(rw.Segments)
	segs[len(segs)-1].End = clk.now
	return segs
}

// childrenLoaded reports whether every child the trace's stream reported for
// span has arrived. A stream that reported no count leaves it unknown.
func (span *Span) childrenLoaded() bool {
	if span.childrenReported == 0 {
		return false
	}
	var n int32
	for _, child := range span.ChildSpans.Spans() {
		if child.ParentSpan == span && child.Received {
			n++
		}
	}
	return n >= span.childrenReported-1
}

// mayWait reports whether span's own breakdown could hold any waiting: it
// has wait links, on itself or its execution twins, deferred evaluations,
// or lazy evaluations beneath it.
func (span *Span) mayWait() bool {
	if span.effectsViaLinks.Len() > 0 || span.hasWaitLinks() {
		return true
	}
	for _, child := range span.ChildSpans.Spans() {
		if child.isLazyEvaluation() {
			return true
		}
		if child.Name != span.Name {
			continue
		}
		if child.hasWaitLinks() {
			return true
		}
		for _, gc := range child.ChildSpans.Spans() {
			if gc.isLazyEvaluation() {
				return true
			}
		}
	}
	return false
}

func (span *Span) hasWaitLinks() bool {
	for i := range span.Links {
		if span.Links[i].IsWait() {
			return true
		}
	}
	return false
}

func (span *Span) isCallExec() bool {
	kind, _ := span.ExtraAttributes.String(telemetryattrs.WcprofOpKindAttr)
	return kind == "call_exec"
}

// holdsFunctionPlumbing reports whether a call execution span holds the work
// that runs a module function.
func (span *Span) holdsFunctionPlumbing() bool {
	for _, child := range span.ChildSpans.Spans() {
		if child.ParentSpan == span && child.FunctionPlumbing {
			return true
		}
	}
	return false
}

// functionProcess finds the process that runs a module function, beneath
// host, the child of parent holding the function's plumbing: the exec whose
// run overlaps the function's own requests the most, or, when it made none,
// the exec that ended last.
func functionProcess(parent, host *Span, now time.Time) *Span {
	row, holder := parent, host
	if host.FunctionPlumbing && parent.isCallExec() && parent.ParentSpan != nil {
		row, holder = parent.ParentSpan, parent
	}
	var body []Interval
	for _, c := range row.ChildSpans.Spans() {
		if c.ParentSpan == row && c != holder && c.Received {
			body = append(body, c.observedInterval(now))
		}
	}
	body = row.db.fold.foldedWork(row, &breakdownClock{now: now}, false, body)
	body = mergeSpanIntervals(body)
	var best, last *Span
	var bestOverlap time.Duration
	var lastEnd time.Time
	var walk func(*Span)
	walk = func(s *Span) {
		for _, c := range s.ChildSpans.Spans() {
			if c.ParentSpan != s {
				continue
			}
			if kind, _ := c.ExtraAttributes.String(telemetryattrs.WcprofOpKindAttr); kind == "exec" {
				ival := c.observedInterval(now)
				var overlap time.Duration
				for _, b := range intersectIntervals(body, []Interval{ival}) {
					overlap += b.End.Sub(b.Start)
				}
				if overlap > bestOverlap {
					best, bestOverlap = c, overlap
				}
				if last == nil || ival.End.After(lastEnd) {
					last, lastEnd = c, ival.End
				}
			}
			walk(c)
		}
	}
	walk(host)
	if best != nil {
		return best
	}
	return last
}

// isPlaced reports whether span and all its ancestors have arrived, so its
// place in the tree is known.
func isPlaced(span *Span) bool {
	for s, depth := span, 0; s != nil && depth < 1000; s, depth = s.ParentSpan, depth+1 {
		if !s.Received {
			return false
		}
		if !s.ParentID.IsValid() {
			return true
		}
	}
	return false
}

// outside keeps the stretches whose blocker is outside row: neither beneath it
// nor one of its ancestors.
func outside(in []waitStretch, row *Span) []waitStretch {
	var out []waitStretch
	for _, w := range in {
		if w.blocker != nil && (spanInSubtree(w.blocker, row) || spanInSubtree(row, w.blocker)) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func stretchIntervals(in []waitStretch) []Interval {
	out := make([]Interval, 0, len(in))
	for _, w := range in {
		out = append(out, Interval{Start: w.start(), End: w.end()})
	}
	return mergeSpanIntervals(out)
}

// unionStretches merges lists of stretches into disjoint stretches in time
// order. Where stretches overlap, the one starting first keeps the time.
func unionStretches(lists ...[]waitStretch) []waitStretch {
	var all []waitStretch
	for _, l := range lists {
		all = append(all, l...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].start().Before(all[j].start()) })
	var out []waitStretch
	var covered time.Time
	for _, w := range all {
		if len(out) > 0 && w.start().Before(covered) {
			w.seg.Start = covered
		}
		if !w.end().After(w.start()) {
			continue
		}
		out = append(out, w)
		covered = w.end()
	}
	return out
}

// overlayStretches returns base plus the parts of extra base doesn't cover.
func overlayStretches(base, extra []waitStretch) []waitStretch {
	if len(extra) == 0 {
		return base
	}
	return unionStretches(base, subtractStretches(extra, stretchIntervals(base)))
}

func subtractStretches(in []waitStretch, cut []Interval) []waitStretch {
	if len(cut) == 0 {
		return in
	}
	var out []waitStretch
	for _, w := range in {
		for _, ival := range subtractIntervals([]Interval{{Start: w.start(), End: w.end()}}, cut) {
			part := w
			part.seg.Start, part.seg.End = ival.Start, ival.End
			out = append(out, part)
		}
	}
	return out
}

func intersectStretches(in []waitStretch, keep []Interval) []waitStretch {
	var out []waitStretch
	for _, w := range in {
		for _, ival := range intersectIntervals([]Interval{{Start: w.start(), End: w.end()}}, keep) {
			part := w
			part.seg.Start, part.seg.End = ival.Start, ival.End
			out = append(out, part)
		}
	}
	return out
}

// subtractIntervals returns ivals minus cut, both sorted and disjoint.
func subtractIntervals(ivals, cut []Interval) []Interval {
	var out []Interval
	j := 0
	for _, ival := range ivals {
		cur := ival.Start
		for j < len(cut) && !cut[j].End.After(cur) {
			j++
		}
		for k := j; k < len(cut) && cut[k].Start.Before(ival.End); k++ {
			if cut[k].Start.After(cur) {
				out = append(out, Interval{Start: cur, End: cut[k].Start})
			}
			if cut[k].End.After(cur) {
				cur = cut[k].End
			}
		}
		if ival.End.After(cur) {
			out = append(out, Interval{Start: cur, End: ival.End})
		}
	}
	return out
}

// intersectIntervals returns the overlap of a and b, both sorted and disjoint.
func intersectIntervals(a, b []Interval) []Interval {
	var out []Interval
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start, end := a[i].Start, a[i].End
		if b[j].Start.After(start) {
			start = b[j].Start
		}
		if b[j].End.Before(end) {
			end = b[j].End
		}
		if end.After(start) {
			out = append(out, Interval{Start: start, End: end})
		}
		if a[i].End.Before(b[j].End) {
			i++
		} else {
			j++
		}
	}
	return out
}
