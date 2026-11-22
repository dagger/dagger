package dagui

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// Self-time decomposition.
//
// A row's time decomposes into the time the op actually spent executing
// (self time) vs the time it was provably blocked on other ops (waiting),
// using the
// explicit wait edges the engine emits as span links (wcprof × OTel). Time
// with no recorded activity at all stays an unpainted gap: a lazy op that
// returned its shell and sits dormant until forced is not waiting on
// anything — it is simply not running.
//
// Waiting is resolved transitively and per-instant: if C waits on B, B waits
// on A, and A is the one actually working, C's waiting reads "waiting on A"
// for the stretch A was working — users shouldn't have to walk the chain
// themselves. Segments whose blocker was reached through the chain are
// marked Indirect (with Via naming the direct hop).

// TimeBlocker is one operation in a resolved wait chain.
type TimeBlocker struct {
	Target SpanID
	Label  string
}

// TimeSegment is one contiguous stretch of a row's painted time.
type TimeSegment struct {
	Start, End time.Time
	// Waiting marks the stretch as blocked-on-another-op. Target identifies
	// the blocker actually working during the stretch when it is in the
	// trace (zero otherwise); Label names it either way.
	Waiting bool
	Target  SpanID
	Label   string
	// Blockers contains the resolved wait chain, ordered from the direct
	// blocker to the fundamental blocker identified by Target. Operations
	// outside the trace have no entry.
	Blockers []TimeBlocker
	// Indirect marks a waiting segment whose blocker was reached by
	// following the wait chain rather than being the row's direct target.
	Indirect bool
	// Via names the row's direct wait target for an Indirect segment.
	Via string

	// unplaced marks a stretch whose blocker is a span of the trace not
	// loaded, rather than outside the trace.
	unplaced bool
}

func (seg TimeSegment) Duration() time.Duration {
	return seg.End.Sub(seg.Start)
}

// TimeBreakdown is the full decomposition for one row.
type TimeBreakdown struct {
	// Segments tile the row's painted intervals in time order. Gaps between
	// segments are dormant time.
	Segments []TimeSegment
	Self     time.Duration
	Waiting  time.Duration
	// Material reports whether the waiting is significant enough to change
	// the rendering (calm by default: immaterial rows render as always).
	Material bool
	// DominantTarget/DominantLabel identify the single largest blocker.
	DominantTarget SpanID
	DominantLabel  string

	// unresolved marks a breakdown that reads as working because what the
	// row waited on isn't loaded.
	unresolved bool
}

// Materiality thresholds: decompose only when the row waited at least
// materialWaitMin and waiting covers at least materialWaitFrac of its painted
// time.
const (
	materialWaitMin  = 1 * time.Second
	materialWaitFrac = 0.2

	// waitChainMaxDepth bounds transitive blocker resolution.
	waitChainMaxDepth = 8
)

type waitWindow struct {
	start, end time.Time
	target     *Span // raw wait target (often an internal resume span)
	reason     string
	// below is set on a wait recorded below the target (rowWaitsState
	// chainWaits): it is resolved already, to the op working during it.
	below *waitStretch
	// inside marks a contained wait on a span a folding DB holds as a stub
	// (see fold_contain.go): whatever the row reading it, it resolves to
	// work inside the row.
	inside bool
}

// resolvedWait is a wait stretch attributed to the op actually working
// during it.
type resolvedWait struct {
	start, end time.Time
	blocker    *Span // nil when not in the trace
	blockers   []*Span
	indirect   bool
	via        string
	// unplaced marks a blocker of the trace not loaded (see TimeSegment)
	unplaced bool
	// inside marks the resolution of a contained wait (waitWindow.inside):
	// blockers inside the row
	inside bool
}

// breakdownClock is the time a TimeBreakdown is computed at. It records
// whether the computation used it: an interval still open, a wait reaching
// past it, cut off at now, or published waiting extended to it. A breakdown
// that did not is the same at any later time, until the DB changes.
type breakdownClock struct {
	now  time.Time
	live bool
}

// clip returns t, or now if t is later.
func (clk *breakdownClock) clip(t time.Time) time.Time {
	if t.After(clk.now) {
		clk.live = true
		return clk.now
	}
	return t
}

// observed returns the span's interval, ending now while it is running.
func (clk *breakdownClock) observed(span *Span) Interval {
	if span.EndTime.IsZero() || span.EndTime.Before(span.StartTime) {
		clk.live = true
	}
	return span.observedInterval(clk.now)
}

// breakdownMemo is a span's TimeBreakdown as of db.breakdownsAt, computed at
// now; live when it depends on now (see breakdownClock).
type breakdownMemo struct {
	hb   *TimeBreakdown
	now  time.Time
	live bool
}

// TimeBreakdown computes the own-vs-waiting decomposition for the span's
// row at the given time. Once the DB computes the waiting below rows
// (UpdateRowWaits), the row's waiting includes it. The result is shared:
// callers must not modify it.
//
// Every frame asks it for every row it renders, so it is memoized per span
// until the DB next changes: a row whose breakdown did not depend on the time
// (nothing in it still running) is reused at any later time, and a running
// one only at the same time.
func (span *Span) TimeBreakdown(now time.Time) *TimeBreakdown {
	if span == nil || span.db == nil {
		return &TimeBreakdown{}
	}
	db := span.db
	db.syncBreakdownMemos()
	if memo, ok := db.breakdowns[span]; ok &&
		(now.Equal(memo.now) || (!memo.live && now.After(memo.now))) {
		return memo.hb
	}
	clk := &breakdownClock{now: now}
	hb := span.timeBreakdown(clk, nil)
	db.breakdowns[span] = breakdownMemo{hb: hb, now: now, live: clk.live}
	return hb
}

// syncBreakdownMemos empties the breakdown and blocker label memos when the
// DB has changed since they were filled.
func (db *DB) syncBreakdownMemos() {
	if db.breakdowns != nil && db.breakdownsAt == db.mutations {
		return
	}
	if db.breakdowns == nil {
		db.breakdowns = map[*Span]breakdownMemo{}
		db.blockerLabels = map[*Span]string{}
	} else {
		clear(db.breakdowns)
		clear(db.blockerLabels)
	}
	db.breakdownsAt = db.mutations
}

// timeBreakdown computes the row's breakdown at clk. While the waiting below
// rows is being computed, below holds it: a wait chain continues through the
// waits recorded below a step, and the row's published waiting is left out.
func (span *Span) timeBreakdown(clk *breakdownClock, below *rowWaitsState) *TimeBreakdown {
	hb := &TimeBreakdown{}
	if span == nil || span.db == nil {
		return hb
	}

	painted := span.paintedIntervals(clk)
	if len(painted) == 0 {
		return hb
	}
	var paintedTotal time.Duration
	for _, p := range painted {
		paintedTotal += p.End.Sub(p.Start)
	}

	var waiting []TimeSegment
	if resolved, ok := span.resolvedWaits(clk, paintedTotal, below); ok {
		waiting = span.waitingSegments(resolved, below)
	} else {
		hb.unresolved = true
	}
	if below == nil {
		// The waiting computed from the row's whole subtree holds its own
		// waits as well: it wins where both cover.
		waiting = overlaySegments(span.rowWaitSegments(clk), waiting)
	}

	// Tile each painted interval into own/waiting segments; waiting wins
	// where they overlap.
	perTarget := map[SpanID]time.Duration{}
	targetLabels := map[SpanID]string{}
	perLabel := map[string]time.Duration{}
	for _, p := range painted {
		cursor := p.Start
		for _, w := range waiting {
			if !w.End.After(p.Start) || !w.Start.Before(p.End) {
				continue
			}
			seg := w
			if seg.Start.Before(cursor) {
				seg.Start = cursor
			}
			if seg.End.After(p.End) {
				seg.End = p.End
			}
			if !seg.End.After(seg.Start) {
				continue
			}
			if seg.Start.After(cursor) {
				hb.addSegment(TimeSegment{Start: cursor, End: seg.Start})
			}
			if seg.Target.IsValid() {
				perTarget[seg.Target] += seg.Duration()
				targetLabels[seg.Target] = seg.Label
			}
			perLabel[seg.Label] += seg.Duration()
			hb.addSegment(seg)
			cursor = seg.End
		}
		if cursor.Before(p.End) {
			hb.addSegment(TimeSegment{Start: cursor, End: p.End})
		}
	}

	for _, seg := range hb.Segments {
		if seg.Waiting {
			hb.Waiting += seg.Duration()
		} else {
			hb.Self += seg.Duration()
		}
	}

	total := hb.Self + hb.Waiting
	hb.Material = hb.Waiting >= materialWaitMin &&
		total > 0 &&
		float64(hb.Waiting) >= materialWaitFrac*float64(total)

	var dominantDur time.Duration
	for target, dur := range perTarget {
		if dur > dominantDur {
			hb.DominantTarget, dominantDur = target, dur
		}
	}
	if hb.DominantTarget.IsValid() {
		hb.DominantLabel = targetLabels[hb.DominantTarget]
	} else {
		var labelDur time.Duration
		for label, dur := range perLabel {
			if dur > labelDur {
				hb.DominantLabel, labelDur = label, dur
			}
		}
	}

	return hb
}

// paintedIntervals returns the row's painted intervals: its activity (its
// own interval plus deferred executions attributed back via causal links)
// plus children that ran outside the span's own interval (deferred work
// recorded under the original call, e.g. a withExec's container run
// happening when forced).
func (span *Span) paintedIntervals(clk *breakdownClock) []Interval {
	var painted []Interval
	if !span.Activity.EarliestRunning.IsZero() {
		clk.live = true
	}
	for ival := range span.Activity.Intervals(clk.now) {
		painted = append(painted, ival)
	}
	for _, child := range span.ChildSpans.Spans() {
		if child.installedAt(span) {
			// a lazy evaluation of a result this row only installed: its
			// time is the evaluated call's
			continue
		}
		if ival := clk.observed(child); ival.End.After(ival.Start) {
			painted = append(painted, ival)
		}
	}
	if span.db != nil {
		// and the children a folding DB holds as stubs
		painted = span.db.fold.foldedWork(span, clk, false, painted)
	}
	return mergeSpanIntervals(painted)
}

// resolvedWaits returns the row's own waits, each resolved to the op working
// during it, or false when what the row waited on isn't loaded.
func (span *Span) resolvedWaits(clk *breakdownClock, paintedTotal time.Duration, below *rowWaitsState) ([]resolvedWait, bool) {
	// Gather the row's direct wait windows and merge them into disjoint
	// stretches, keeping the dominant direct target for each.
	var raw []waitWindow
	span.collectWaitWindows(&raw, span, clk)
	// Wait edges are only emitted when a wait ENDS, and they land on spans
	// that may stay open long after (a resume span's links only re-export on
	// heartbeat or its own end). So in addition to the explicit edges, infer
	// blocked stretches from the deferred-work markers themselves: external
	// deferred work hanging off this row's execution means the row was
	// blocked on it for that work's interval — including right now, when the
	// work is still running. Overlaps with explicit edges merge away.
	span.collectInferredWaits(&raw, clk)

	// A view holding part of a trace may not have loaded what the row
	// waited on. Until it does, whether the row waited on another operation
	// or ran nested work of its own is unknown, so it reads as working the
	// whole time. TimeBreakdownSpans names what to load. Each wait counts,
	// before merging keeps only the longest of those that adjoin.
	for _, w := range raw {
		if w.target.unloaded() || !blockerAncestry(resolveBlocker(w.target), span, nil) {
			return nil, false
		}
	}
	merged := mergeWaitWindows(raw)

	// Resolve each stretch transitively into per-instant blockers, then
	// smooth out sub-visual slivers.
	var resolved []resolvedWait
	for _, w := range merged {
		direct := resolveBlocker(w.target)
		via := ""
		if direct != nil {
			via = below.label(shownBlocker(direct, span))
		}
		visited := map[*Span]bool{}
		resolved = append(resolved, resolveWaitChain(w, via, nil, 0, visited, clk, below)...)
	}
	// A blocker rendered inside the row's own subtree is the row hosting its
	// own nested work (a module call running its function, a group span
	// wrapping its pieces) — that is own work, not waiting; the detail is
	// right below, expandable. Only blockers rendered elsewhere count as
	// "waiting on X".
	genuine := resolved[:0]
	for _, w := range resolved {
		if w.inside || w.blocker != nil && spanInSubtree(w.blocker, span) {
			continue
		}
		if w.blocker != nil && spanInSubtree(span, w.blocker) {
			// The blocker contains this row (an ancestor, ultimately the
			// session root): "waiting on" something that encloses you says
			// nothing. Chains that dead-end there carry no usable signal.
			continue
		}
		genuine = append(genuine, w)
	}
	resolved = genuine
	resolved = coalesceResolvedWaits(resolved)
	return absorbWaitSlivers(resolved, paintedTotal), true
}

// waitingSegments names the blockers of resolved waits by the rows the view
// draws for them, as waiting segments in time order.
func (span *Span) waitingSegments(resolved []resolvedWait, below *rowWaitsState) []TimeSegment {
	var segs []TimeSegment
	for _, w := range resolved {
		if !w.end.After(w.start) {
			continue
		}
		seg := TimeSegment{
			Start:    w.start,
			End:      w.end,
			Waiting:  true,
			Indirect: w.indirect,
			unplaced: w.unplaced,
		}
		for _, blocker := range w.blockers {
			blocker = shownBlocker(blocker, span)
			if n := len(seg.Blockers); n > 0 && seg.Blockers[n-1].Target == blocker.ID {
				continue
			}
			seg.Blockers = append(seg.Blockers, TimeBlocker{
				Target: blocker.ID,
				Label:  below.label(blocker),
			})
		}
		if w.indirect {
			seg.Via = w.via
		}
		if w.blocker != nil {
			blocker := shownBlocker(w.blocker, span)
			seg.Target = blocker.ID
			seg.Label = below.label(blocker)
		} else {
			seg.Label = "an operation outside this trace"
		}
		segs = append(segs, seg)
	}
	return segs
}

// overlaySegments returns the waiting segments of base plus the parts of
// those of extra base doesn't cover, in time order.
func overlaySegments(base, extra []TimeSegment) []TimeSegment {
	if len(base) == 0 {
		return extra
	}
	if len(extra) == 0 {
		return base
	}
	covered := make([]Interval, 0, len(base))
	for _, seg := range base {
		covered = append(covered, Interval{Start: seg.Start, End: seg.End})
	}
	covered = mergeSpanIntervals(covered)
	out := slices.Clone(base)
	for _, seg := range extra {
		for _, ival := range subtractIntervals([]Interval{{Start: seg.Start, End: seg.End}}, covered) {
			part := seg
			part.Start, part.End = ival.Start, ival.End
			out = append(out, part)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// resolveWaitChain attributes the wait window to the ops actually working
// during it by recursing through the target's own waits. Stretches where the
// target itself was working resolve to the target; stretches where it was in
// turn waiting resolve deeper.
func resolveWaitChain(win waitWindow, viaLabel string, blockers []*Span, depth int, visited map[*Span]bool, clk *breakdownClock, below *rowWaitsState) []resolvedWait {
	if win.inside {
		return []resolvedWait{{start: win.start, end: win.end, inside: true}}
	}
	blocker := resolveBlocker(win.target)
	unplaced := false
	if blocker.unloaded() {
		// further down the chain than the row's own blockers, which
		// TimeBreakdown resolves first, it is as good as outside the trace
		blocker = nil
		unplaced = true
	}
	blockers = appendResolvedBlocker(blockers, blocker)
	self := resolvedWait{
		start:    win.start,
		end:      win.end,
		blocker:  blocker,
		blockers: append([]*Span(nil), blockers...),
		indirect: depth > 0,
		via:      viaLabel,
		unplaced: unplaced,
	}
	if win.target == nil || depth >= waitChainMaxDepth || visited[win.target] {
		return []resolvedWait{self}
	}
	visited[win.target] = true
	defer delete(visited, win.target)

	// The target's own waits, including those recorded on its execution twins.
	var subs []waitWindow
	win.target.collectWaitWindows(&subs, win.target, clk)
	for _, child := range win.target.ChildSpans.Spans() {
		if child.Name == win.target.Name {
			child.collectWaitWindows(&subs, win.target, clk)
		}
	}
	// Live analog of the closed-edge descent: the window's target — and the
	// op it stands for, whose row carries the next deferred-work marker down
	// the chain — may itself be blocked right now with no wait edge emitted
	// yet. Descend through their inferred blocked state so live labels
	// resolve to the op actually working this instant, not the next hop.
	win.target.collectInferredWaits(&subs, clk)
	if resolved := resolveBlocker(win.target); resolved != nil && resolved != win.target && !visited[resolved] {
		resolved.collectInferredWaits(&subs, clk)
	}
	mergedSubs := mergeWaitWindows(clipWaitWindows(subs, win))
	if below != nil {
		// A step that waited on work outside it from below, while nothing
		// under it worked, was waiting too: the chain goes on there, where
		// the step's own waits don't cover. Those stretches are resolved
		// already, each to its own blocker, so they aren't merged.
		mergedSubs = overlayWaitWindows(mergedSubs, clipWaitWindows(below.chainWaits(win.target), win))
	}
	if len(mergedSubs) == 0 {
		return []resolvedWait{self}
	}

	var out []resolvedWait
	cursor := win.start
	for _, sub := range mergedSubs {
		if sub.start.After(cursor) {
			// the target itself was working here
			gap := self
			gap.start, gap.end = cursor, sub.start
			out = append(out, gap)
		}
		if sub.below != nil {
			out = append(out, sub.below.resolved(sub, viaLabel, blockers))
		} else {
			out = append(out, resolveWaitChain(sub, viaLabel, blockers, depth+1, visited, clk, below)...)
		}
		cursor = sub.end
	}
	if cursor.Before(win.end) {
		tail := self
		tail.start = cursor
		out = append(out, tail)
	}
	return out
}

// appendResolvedBlocker copies path and adds blocker unless it is outside the
// trace or is an adjacent duplicate of the last resolved operation. Copying
// keeps sibling recursion from changing paths already stored on other slices.
func appendResolvedBlocker(path []*Span, blocker *Span) []*Span {
	out := append([]*Span(nil), path...)
	if blocker == nil || (len(out) > 0 && out[len(out)-1] == blocker) {
		return out
	}
	return append(out, blocker)
}

// coalesceResolvedWaits merges adjacent windows with the same blocker and
// complete causal path.
func coalesceResolvedWaits(wins []resolvedWait) []resolvedWait {
	if len(wins) == 0 {
		return nil
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].start.Before(wins[j].start) })
	out := wins[:1]
	for _, w := range wins[1:] {
		last := &out[len(out)-1]
		if w.blocker == last.blocker && w.unplaced == last.unplaced &&
			sameResolvedBlockers(w.blockers, last.blockers) &&
			!w.start.After(last.end) {
			if w.end.After(last.end) {
				last.end = w.end
			}
			// direct beats indirect when merging equal blockers
			if !w.indirect {
				last.indirect = false
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

func sameResolvedBlockers(a, b []*Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// absorbWaitSlivers folds windows too small to see (or click) into their
// larger neighbor so bars don't fragment into noise.
func absorbWaitSlivers(wins []resolvedWait, paintedTotal time.Duration) []resolvedWait {
	if len(wins) < 2 {
		return wins
	}
	epsilon := paintedTotal / 100
	if epsilon < 150*time.Millisecond {
		epsilon = 150 * time.Millisecond
	}
	var out []resolvedWait
	for _, w := range wins {
		if len(out) > 0 {
			last := &out[len(out)-1]
			contiguous := !w.start.After(last.end)
			if contiguous && w.end.Sub(w.start) < epsilon {
				// Too small to matter: the previous window claims this time
				// with its own complete blocker path.
				if w.end.After(last.end) {
					last.end = w.end
				}
				continue
			}
			if contiguous && last.end.Sub(last.start) < epsilon {
				// The previous window was the sliver: this window claims its
				// time with this window's complete blocker path.
				w.start = last.start
				out[len(out)-1] = w
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

func (hb *TimeBreakdown) addSegment(seg TimeSegment) {
	// Coalesce with the previous segment when contiguous and alike, so the
	// rendered bar doesn't fragment into slivers.
	if n := len(hb.Segments); n > 0 {
		prev := &hb.Segments[n-1]
		if prev.Waiting == seg.Waiting &&
			prev.Label == seg.Label &&
			prev.Target == seg.Target &&
			prev.Indirect == seg.Indirect &&
			prev.Via == seg.Via &&
			sameTimeBlockers(prev.Blockers, seg.Blockers) &&
			!seg.Start.After(prev.End) {
			if seg.End.After(prev.End) {
				prev.End = seg.End
			}
			return
		}
	}
	hb.Segments = append(hb.Segments, seg)
}

func sameTimeBlockers(a, b []TimeBlocker) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// collectWaitWindows appends span's wait edges as raw windows attributed to row.
func (span *Span) collectWaitWindows(dst *[]waitWindow, row *Span, clk *breakdownClock) {
	span.collectOwnWaits(dst, row, clk)
	// Wait edges recorded on the row's execution twins (call_exec spans share
	// the caller's name and nest directly under it) and on its deferred
	// executions (effect spans, e.g. lazy resumes) belong to the row too.
	if span == row {
		for _, child := range span.ChildSpans.Spans() {
			if child.Name == span.Name {
				child.collectOwnWaits(dst, row, clk)
			}
		}
		for effect := range span.evaluations {
			effect.collectOwnWaits(dst, row, clk)
			for _, child := range effect.ChildSpans.Spans() {
				if child.Name == effect.Name {
					child.collectOwnWaits(dst, row, clk)
				}
			}
		}
	}
}

func (span *Span) collectOwnWaits(dst *[]waitWindow, row *Span, clk *breakdownClock) {
	for i := range span.Links {
		link := &span.Links[i]
		if !link.IsWait() {
			continue
		}
		target := span.db.Spans.Map[link.SpanContext.SpanID]
		var inside bool
		if target == nil || !target.Received {
			// a span a folding DB holds as a stub (and as a reference,
			// if it hadn't arrived when span did): a wait on span's own
			// execution, or a contained one, on work inside every row
			// reading it (see fold_contain.go)
			var skip bool
			skip, inside = span.db.fold.waitStandIn(span.ID, link.SpanContext.SpanID, isExecWait(link.WaitReason))
			if skip {
				continue
			}
			if inside {
				target = nil
			}
		}

		// A caller blocked on its own resolver execution (its same-name
		// call_exec twin) is doing its own work, not waiting on another op.
		if (link.WaitReason == "call_exec" || link.WaitReason == "singleflight") &&
			target != nil && target.Name == row.Name && spanInSubtree(target, row) {
			continue
		}

		end := clk.clip(link.WaitEnd)
		if !end.After(link.WaitStart) {
			continue
		}

		*dst = append(*dst, waitWindow{
			start:  link.WaitStart,
			end:    end,
			target: target,
			reason: link.WaitReason,
			inside: inside,
		})
	}
}

// collectInferredWaits appends wait windows inferred from deferred-work
// markers hanging off the row's execution (direct children, twin children,
// effect resumes and their children): a marker resolving to an op rendered
// OUTSIDE the row's subtree means the row was blocked on that op while the
// marker ran. Only spans that stand in for another op qualify (a resume
// marker resolves to its origin; a plain child resolves to itself and never
// creates a window). Running markers block the row until now; completed ones
// cover their own interval, preserving past blocked stretches whose explicit
// wait edges haven't reached us yet.
func (span *Span) collectInferredWaits(dst *[]waitWindow, clk *breakdownClock) {
	seen := map[*Span]bool{span: true}
	var visit func(s *Span, depth int)
	visit = func(s *Span, depth int) {
		if depth > waitChainMaxDepth {
			return
		}
		var candidates []*Span
		add := func(c *Span) {
			if c != nil && !seen[c] {
				seen[c] = true
				candidates = append(candidates, c)
			}
		}
		for _, child := range s.ChildSpans.Spans() {
			if child.installedAt(s) {
				continue
			}
			add(child)
			if child.Name == s.Name {
				for _, gc := range child.ChildSpans.Spans() {
					if !gc.installedAt(child) {
						add(gc)
					}
				}
			}
		}
		for _, effect := range s.effectsViaLinks.Spans() {
			if effect.installedAt(s) {
				continue
			}
			add(effect)
			for _, child := range effect.ChildSpans.Spans() {
				add(child)
			}
		}
		for _, c := range candidates {
			resolved := resolveBlocker(c)
			if resolved == nil || resolved == c {
				continue
			}
			if resolved == span || spanInSubtree(resolved, span) {
				// The row's own deferred evaluation (or nested work) — that
				// alone is the row working, not waiting. But it may itself
				// be blocked deeper: descend to find a true external blocker
				// (the row is "executing its deferred eval, which is blocked
				// on X").
				visit(c, depth+1)
				continue
			}
			win := waitWindow{
				start:  c.StartTime,
				end:    clk.clip(clk.observed(c).End),
				target: c,
			}
			if win.end.After(win.start) {
				*dst = append(*dst, win)
			}
		}
	}
	visit(span, 0)
}

// WaitingNow reports whether the row is currently blocked on work rendered
// outside its own subtree, and on what (nil when the blocker is not in the
// trace). It is derived from the same decomposition the bars use: the row is
// blocked now iff its breakdown ends in a waiting segment that reaches now.
func (span *Span) WaitingNow(now time.Time) (*Span, bool) {
	if span == nil || span.db == nil || !span.IsRunningOrEffectsRunning() {
		return nil, false
	}
	if span.IsFailedOrCausedFailure() || span.IsCanceled() {
		// a failed or canceled row's story is its error, not its waits
		return nil, false
	}
	seg, ok := span.TimeBreakdown(now).BlockedNow(now)
	if !ok {
		return nil, false
	}
	return span.db.Spans.Map[seg.Target], true
}

// BlockedNow returns the waiting segment covering now, if any: the row is
// blocked right now on that segment's target.
func (hb *TimeBreakdown) BlockedNow(now time.Time) (TimeSegment, bool) {
	if n := len(hb.Segments); n > 0 {
		last := hb.Segments[n-1]
		if last.Waiting && !last.End.Before(now.Add(-time.Second)) {
			return last, true
		}
	}
	return TimeSegment{}, false
}

// TimeBreakdownSpans visits the spans a row's time breakdown draws on
// beyond the row itself: execution twins and their children, deferred-work
// markers (effects) and theirs, wait-edge targets, the cause chain that
// resolves markers to their ops, and the exec children that carry argv
// labels. Frontends that stream a filtered subset of spans (the Cloud
// websocket) must include these for visible rows or the live decomposition
// silently degrades to the wall-clock lie.
func (span *Span) TimeBreakdownSpans(visit func(*Span)) {
	if span == nil || span.db == nil {
		return
	}
	seen := map[*Span]bool{span: true}
	walkedAt := map[*Span]int{}
	expandedAt := map[*Span]int{}
	var walk func(s *Span, depth int)
	var emit func(c *Span, depth int)
	visitOnce := func(c *Span) {
		if c == nil || seen[c] {
			return
		}
		seen[c] = true
		visit(c)
	}
	// blockerLabel searches two child levels for exec argv attributes.
	var visitLabelSpans func(*Span, int)
	visitLabelSpans = func(c *Span, depth int) {
		if c == nil || depth >= 2 {
			return
		}
		for _, child := range c.ChildSpans.Spans() {
			visitOnce(child)
			visitLabelSpans(child, depth+1)
		}
	}
	emit = func(c *Span, depth int) {
		if c == nil {
			return
		}
		visitOnce(c)
		if prev, ok := expandedAt[c]; ok && prev <= depth {
			return
		}
		expandedAt[c] = depth

		// Cause links and the parent fallback map resume markers to the
		// operations retained in TimeSegment.Blockers. Expand those operations
		// too: their waits form the next link and their descendants carry labels.
		for _, cause := range c.causesViaLinks.Spans() {
			// Preserve the existing dependency on every direct cause, even though
			// resolveBlocker selects only the evaluated one.
			visitOnce(cause)
		}
		resolved := resolveBlockerPath(c, visitOnce)
		// shownBlocker names a hidden operation by its nearest drawn
		// ancestor, so the breakdown draws on that ancestry too.
		blockerAncestry(resolved, span, visitOnce)
		visitLabelSpans(resolved, 0)
		if resolved != nil && resolved != c {
			emit(resolved, depth)
		}
		walk(c, depth+1)
	}
	walk = func(s *Span, depth int) {
		if s == nil || depth > waitChainMaxDepth {
			return
		}
		if prev, ok := walkedAt[s]; ok && prev <= depth {
			return
		}
		walkedAt[s] = depth
		for i := range s.Links {
			if s.Links[i].IsWait() {
				emit(s.db.Spans.Map[s.Links[i].SpanContext.SpanID], depth)
			}
		}
		for _, child := range s.ChildSpans.Spans() {
			if child.installedAt(s) {
				continue
			}
			if child.Name == s.Name {
				emit(child, depth)
				for _, gc := range child.ChildSpans.Spans() {
					if !gc.installedAt(child) {
						emit(gc, depth)
					}
				}
			} else if resolved := resolveBlocker(child); resolved != nil && resolved != child {
				// collectInferredWaits inspects non-twin children which stand
				// in for another operation, including live resume markers.
				emit(child, depth)
			}
		}
		for _, effect := range s.effectsViaLinks.Spans() {
			if effect.installedAt(s) {
				continue
			}
			emit(effect, depth)
			for _, child := range effect.ChildSpans.Spans() {
				emit(child, depth)
			}
		}
	}
	walk(span, 0)
	span.rowWaitBlockers(visitOnce)
}

// resolveBlocker maps internal plumbing spans (lazy "resume" markers)
// to the user-meaningful op whose deferred work they represent: the call the
// resume evaluates, falling back to its parent. The sites the result was
// installed at (see installSites) only held the result, so a wait on its
// evaluation is not a wait on them.
func resolveBlocker(target *Span) *Span {
	return resolveBlockerPath(target, nil)
}

// resolveBlockerPath resolves target and optionally visits each operation used
// along the way. The callback lets TimeBreakdownSpans stream the exact inputs a
// filtered frontend needs to perform the same resolution after import.
func resolveBlockerPath(target *Span, visit func(*Span)) *Span {
	for depth := 0; target != nil && depth < 10; depth++ {
		// A resume resolves through its causes. So does any lazy evaluation
		// it reaches: a later evaluation of a lazy result may link an earlier
		// one rather than the call that returned the result.
		if !strings.HasPrefix(target.Name, "resume ") &&
			(depth == 0 || !target.isLazyEvaluation()) {
			return target
		}
		var next *Span
		for _, cause := range target.causesViaLinks.Spans() {
			if cause != target && !target.installedAt(cause) {
				next = cause
				break
			}
		}
		if next == nil {
			next = target.ParentSpan
		}
		if next == nil || next == target {
			return target
		}
		target = next
		if visit != nil {
			visit(target)
		}
	}
	return target
}

// shownBlocker names the operation a row waited on by the row the view draws
// for it wherever it draws row: op itself, or, when op is hidden there (it is
// internal, beneath an internal span row is not beneath, or passthrough), its
// nearest ancestor drawn there. An internal call often does the work of the
// public call around it, e.g. a Container.from the engine makes inside the
// user's Container.from. An ancestor that contains row would only say row
// waited on something enclosing it, so op keeps its own name then.
//
// Only loaded spans are read: an ancestor this DB holds only a reference to
// says nothing of whether it is drawn, and draws nothing itself, so op keeps
// its own name rather than take that. TimeBreakdown waits for the ancestry
// up to op's first drawn ancestor (blockerAncestry).
func shownBlocker(op, row *Span) *Span {
	shown := op
	for s, depth := op, 0; s != nil && s.Received && depth < 100; s, depth = s.ParentSpan, depth+1 {
		if spanInSubtree(row, s) {
			// whatever hides s hides row too
			break
		}
		if s.Internal {
			shown = s.ParentSpan
		}
	}
	for depth := 0; shown != nil && shown.Received && shown.Passthrough && depth < 100; depth++ {
		shown = shown.ParentSpan
	}
	if shown == nil || !shown.Received || spanInSubtree(row, shown) {
		return op
	}
	return shown
}

// blockerAncestry reports whether op, an operation row waited on, is loaded
// along with its ancestors up to the first one the view draws (neither
// internal nor passthrough) or that holds row: what shownBlocker needs to
// name op. With visit, it visits each of those on the way, up to and
// including the first one not loaded, for TimeBreakdownSpans. An operation
// outside the trace (nil) has nothing to load.
func blockerAncestry(op, row *Span, visit func(*Span)) bool {
	for s, depth := op, 0; s != nil && depth < 100; s, depth = s.ParentSpan, depth+1 {
		if visit != nil {
			visit(s)
		}
		if !s.Received {
			return false
		}
		if (!s.Internal && !s.Passthrough) || spanInSubtree(row, s) {
			return true
		}
	}
	return true
}

// blockerLabel names blocker in a breakdown, memoized like the breakdowns:
// it decodes exec argv JSON, and a breakdown asks for it per segment.
func blockerLabel(blocker *Span) string {
	db := blocker.db
	if db == nil {
		return computeBlockerLabel(blocker)
	}
	db.syncBreakdownMemos()
	if label, ok := db.blockerLabels[blocker]; ok {
		return label
	}
	label := computeBlockerLabel(blocker)
	db.blockerLabels[blocker] = label
	return label
}

func computeBlockerLabel(blocker *Span) string {
	// A container exec's real command beats its generic call name (three rows
	// all reading "Container.withExec" tell the user nothing).
	if argv := blocker.execArgv(0); argv != "" {
		return argv
	}
	// The exec argv attribute only exists once the blocker's exec actually
	// runs; the call's own args are known from chain-build time, so live
	// labels carry the real command before execution.
	if argv := blocker.callArgv(); argv != "" {
		return argv
	}
	return blocker.Name
}

// callArgv formats a withExec-style string-list "args" argument from
// the blocker's call, rendered the same way as the exec argv attribute.
func (span *Span) callArgv() string {
	call := span.Call()
	if call == nil {
		return ""
	}
	for _, arg := range call.Args {
		if arg.GetName() != "args" {
			continue
		}
		list := arg.GetValue().GetList()
		if list == nil {
			return ""
		}
		var parts []string
		for _, lit := range list.GetValues() {
			str, ok := lit.GetValue().(*callpbv1.Literal_String_)
			if !ok {
				return ""
			}
			parts = append(parts, str.Value())
		}
		if len(parts) == 0 {
			return ""
		}
		return truncateLabel(strings.Join(parts, " "))
	}
	return ""
}

// truncateLabel fits a command line on one short line: its arguments may
// hold a whole script, newlines and all.
func truncateLabel(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 48 {
		return s[:47] + "…"
	}
	return s
}

// execArgv finds the wcprof exec argv stamped on the op's process-run
// span (a grandchild of the call span: call → exec.run → exec.processRun).
func (span *Span) execArgv(depth int) string {
	if enc, ok := span.ExtraAttributes.String(telemetryattrs.WcprofExecArgvAttr); ok {
		var argv []string
		if err := json.Unmarshal([]byte(enc), &argv); err == nil && len(argv) > 0 {
			return truncateLabel(strings.Join(argv, " "))
		}
	}
	if depth >= 2 {
		return ""
	}
	for _, child := range span.ChildSpans.Spans() {
		if argv := child.execArgv(depth + 1); argv != "" {
			return argv
		}
	}
	return ""
}

// unloaded reports whether span is a placeholder for a span of the trace this
// DB holds only a reference to: a parent, link or wait target not received.
func (span *Span) unloaded() bool {
	return span != nil && !span.Received
}

func spanInSubtree(span, root *Span) bool {
	for depth := 0; span != nil && depth < 100; depth++ {
		if span == root {
			return true
		}
		span = span.ParentSpan
	}
	return false
}

func (span *Span) observedInterval(now time.Time) Interval {
	end := span.EndTime
	if end.IsZero() || end.Before(span.StartTime) {
		end = now
	}
	return Interval{Start: span.StartTime, End: end}
}

func mergeSpanIntervals(ivals []Interval) []Interval {
	if len(ivals) == 0 {
		return nil
	}
	sort.Slice(ivals, func(i, j int) bool { return ivals[i].Start.Before(ivals[j].Start) })
	out := ivals[:1]
	for _, ival := range ivals[1:] {
		last := &out[len(out)-1]
		if !ival.Start.After(last.End) {
			if ival.End.After(last.End) {
				last.End = ival.End
			}
			continue
		}
		out = append(out, ival)
	}
	return out
}

// clipWaitWindows clips wins to win, in place.
func clipWaitWindows(wins []waitWindow, win waitWindow) []waitWindow {
	clipped := wins[:0]
	for _, w := range wins {
		if w.start.Before(win.start) {
			w.start = win.start
		}
		if w.end.After(win.end) {
			w.end = win.end
		}
		if w.end.After(w.start) {
			clipped = append(clipped, w)
		}
	}
	return clipped
}

// overlayWaitWindows returns the disjoint windows base plus the parts of
// extra neither base nor an earlier window of extra covers, in time order.
func overlayWaitWindows(base, extra []waitWindow) []waitWindow {
	if len(extra) == 0 {
		return base
	}
	out := slices.Clone(base)
	covered := make([]Interval, 0, len(base)+len(extra))
	for _, w := range base {
		covered = append(covered, Interval{Start: w.start, End: w.end})
	}
	for _, w := range extra {
		covered = mergeSpanIntervals(covered)
		for _, ival := range subtractIntervals([]Interval{{Start: w.start, End: w.end}}, covered) {
			part := w
			part.start, part.end = ival.Start, ival.End
			out = append(out, part)
			covered = append(covered, ival)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}

// mergeWaitWindows merges overlapping wait windows into disjoint stretches,
// each keeping the contributor covering most of it.
func mergeWaitWindows(wins []waitWindow) []waitWindow {
	if len(wins) == 0 {
		return nil
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].start.Before(wins[j].start) })
	var merged []waitWindow
	cur := wins[0]
	contributors := []waitWindow{wins[0]}
	flush := func() {
		best := contributors[0]
		var bestDur time.Duration
		for _, c := range contributors {
			start, end := c.start, c.end
			if start.Before(cur.start) {
				start = cur.start
			}
			if end.After(cur.end) {
				end = cur.end
			}
			if d := end.Sub(start); d > bestDur {
				best, bestDur = c, d
			}
		}
		cur.target = best.target
		cur.reason = best.reason
		cur.inside = best.inside
		merged = append(merged, cur)
	}
	for _, w := range wins[1:] {
		if !w.start.After(cur.end) {
			if w.end.After(cur.end) {
				cur.end = w.end
			}
			contributors = append(contributors, w)
			continue
		}
		flush()
		cur = w
		contributors = []waitWindow{w}
	}
	flush()
	return merged
}
