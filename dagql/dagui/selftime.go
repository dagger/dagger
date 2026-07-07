package dagui

import (
	"encoding/json"
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

// TimeSegment is one contiguous stretch of a row's painted time.
type TimeSegment struct {
	Start, End time.Time
	// Waiting marks the stretch as blocked-on-another-op. Target identifies
	// the blocker actually working during the stretch when it is in the
	// trace (zero otherwise); Label names it either way.
	Waiting bool
	Target  SpanID
	Label   string
	// Indirect marks a waiting segment whose blocker was reached by
	// following the wait chain rather than being the row's direct target.
	Indirect bool
	// Via names the row's direct wait target for an Indirect segment.
	Via string
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
}

// resolvedWait is a wait stretch attributed to the op actually working
// during it.
type resolvedWait struct {
	start, end time.Time
	blocker    *Span // nil when not in the trace
	indirect   bool
	via        string
}

// TimeBreakdown computes the own-vs-waiting decomposition for the span's
// row at the given time.
//
//nolint:gocyclo // the decomposition is clearer as one flow.
func (span *Span) TimeBreakdown(now time.Time) *TimeBreakdown {
	hb := &TimeBreakdown{}
	if span == nil || span.db == nil {
		return hb
	}

	// Painted intervals: the row's activity (its own interval plus deferred
	// executions attributed back via causal links) plus children that ran
	// outside the span's own interval (deferred work recorded under the
	// original call, e.g. a withExec's container run happening when forced).
	var painted []Interval
	for ival := range span.Activity.Intervals(now) {
		painted = append(painted, ival)
	}
	for _, child := range span.ChildSpans.Order {
		if ival := child.observedInterval(now); ival.End.After(ival.Start) {
			painted = append(painted, ival)
		}
	}
	painted = mergeSpanIntervals(painted)
	if len(painted) == 0 {
		return hb
	}
	var paintedTotal time.Duration
	for _, p := range painted {
		paintedTotal += p.End.Sub(p.Start)
	}

	// Gather the row's direct wait windows and merge them into disjoint
	// stretches, keeping the dominant direct target for each.
	var raw []waitWindow
	span.collectWaitWindows(&raw, span, now)
	// Wait edges are only emitted when a wait ENDS, and they land on spans
	// that may stay open long after (a resume span's links only re-export on
	// heartbeat or its own end). So in addition to the explicit edges, infer
	// blocked stretches from the deferred-work markers themselves: external
	// deferred work hanging off this row's execution means the row was
	// blocked on it for that work's interval — including right now, when the
	// work is still running. Overlaps with explicit edges merge away.
	span.collectInferredWaits(&raw, now)
	merged := mergeWaitWindows(raw)

	// Resolve each stretch transitively into per-instant blockers, then
	// smooth out sub-visual slivers.
	var resolved []resolvedWait
	for _, w := range merged {
		direct := resolveBlocker(w.target)
		via := ""
		if direct != nil {
			via = blockerLabel(direct)
		}
		visited := map[*Span]bool{}
		resolved = append(resolved, resolveWaitChain(w, via, 0, visited, now)...)
	}
	// A blocker rendered inside the row's own subtree is the row hosting its
	// own nested work (a module call running its function, a group span
	// wrapping its pieces) — that is own work, not waiting; the detail is
	// right below, expandable. Only blockers rendered elsewhere count as
	// "waiting on X".
	genuine := resolved[:0]
	for _, w := range resolved {
		if w.blocker != nil && spanInSubtree(w.blocker, span) {
			continue
		}
		genuine = append(genuine, w)
	}
	resolved = genuine
	resolved = coalesceResolvedWaits(resolved)
	resolved = absorbWaitSlivers(resolved, paintedTotal)

	// Tile each painted interval into own/waiting segments; waiting wins
	// where they overlap.
	perTarget := map[*Span]time.Duration{}
	perLabel := map[string]time.Duration{}
	for _, p := range painted {
		cursor := p.Start
		for _, w := range resolved {
			if !w.end.After(p.Start) || !w.start.Before(p.End) {
				continue
			}
			start, end := w.start, w.end
			if start.Before(cursor) {
				start = cursor
			}
			if end.After(p.End) {
				end = p.End
			}
			if !end.After(start) {
				continue
			}
			if start.After(cursor) {
				hb.addSegment(TimeSegment{Start: cursor, End: start})
			}
			seg := TimeSegment{
				Start:    start,
				End:      end,
				Waiting:  true,
				Indirect: w.indirect,
			}
			if w.indirect {
				seg.Via = w.via
			}
			if w.blocker != nil {
				seg.Target = w.blocker.ID
				seg.Label = blockerLabel(w.blocker)
				perTarget[w.blocker] += end.Sub(start)
			} else {
				seg.Label = "an operation outside this trace"
			}
			perLabel[seg.Label] += end.Sub(start)
			hb.addSegment(seg)
			cursor = end
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

	var dominant *Span
	var dominantDur time.Duration
	for target, dur := range perTarget {
		if dur > dominantDur {
			dominant, dominantDur = target, dur
		}
	}
	if dominant != nil {
		hb.DominantTarget = dominant.ID
		hb.DominantLabel = blockerLabel(dominant)
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

// resolveWaitChain attributes the wait window to the ops actually working
// during it by recursing through the target's own waits. Stretches where the
// target itself was working resolve to the target; stretches where it was in
// turn waiting resolve deeper.
func resolveWaitChain(win waitWindow, viaLabel string, depth int, visited map[*Span]bool, now time.Time) []resolvedWait {
	self := resolvedWait{
		start:    win.start,
		end:      win.end,
		blocker:  resolveBlocker(win.target),
		indirect: depth > 0,
		via:      viaLabel,
	}
	if win.target == nil || depth >= waitChainMaxDepth || visited[win.target] {
		return []resolvedWait{self}
	}
	visited[win.target] = true
	defer delete(visited, win.target)

	// The target's own waits, including those recorded on its execution twins.
	var subs []waitWindow
	win.target.collectWaitWindows(&subs, win.target, now)
	for _, child := range win.target.ChildSpans.Order {
		if child.Name == win.target.Name {
			child.collectWaitWindows(&subs, win.target, now)
		}
	}
	// Live analog of the closed-edge descent: the window's target — and the
	// op it stands for, whose row carries the next deferred-work marker down
	// the chain — may itself be blocked right now with no wait edge emitted
	// yet. Descend through their inferred blocked state so live labels
	// resolve to the op actually working this instant, not the next hop.
	win.target.collectInferredWaits(&subs, now)
	if resolved := resolveBlocker(win.target); resolved != nil && resolved != win.target && !visited[resolved] {
		resolved.collectInferredWaits(&subs, now)
	}
	// Clip to this window.
	clipped := subs[:0]
	for _, s := range subs {
		if s.start.Before(win.start) {
			s.start = win.start
		}
		if s.end.After(win.end) {
			s.end = win.end
		}
		if s.end.After(s.start) {
			clipped = append(clipped, s)
		}
	}
	mergedSubs := mergeWaitWindows(clipped)
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
		out = append(out, resolveWaitChain(sub, viaLabel, depth+1, visited, now)...)
		cursor = sub.end
	}
	if cursor.Before(win.end) {
		tail := self
		tail.start = cursor
		out = append(out, tail)
	}
	return out
}

// coalesceResolvedWaits merges adjacent windows with the same blocker.
func coalesceResolvedWaits(wins []resolvedWait) []resolvedWait {
	if len(wins) == 0 {
		return nil
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i].start.Before(wins[j].start) })
	out := wins[:1]
	for _, w := range wins[1:] {
		last := &out[len(out)-1]
		if w.blocker == last.blocker && !w.start.After(last.end) {
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
				// too small to matter: extend the previous window over it
				if w.end.After(last.end) {
					last.end = w.end
				}
				continue
			}
			if contiguous && last.end.Sub(last.start) < epsilon {
				// previous was the sliver: let this window claim its space
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
			!seg.Start.After(prev.End) {
			if seg.End.After(prev.End) {
				prev.End = seg.End
			}
			return
		}
	}
	hb.Segments = append(hb.Segments, seg)
}

// collectWaitWindows appends span's wait edges as raw windows attributed to row.
func (span *Span) collectWaitWindows(dst *[]waitWindow, row *Span, now time.Time) {
	span.collectOwnWaits(dst, row, now)
	// Wait edges recorded on the row's execution twins (call_exec spans share
	// the caller's name and nest directly under it) and on its deferred
	// executions (effect spans, e.g. lazy resumes) belong to the row too.
	if span == row {
		for _, child := range span.ChildSpans.Order {
			if child.Name == span.Name {
				child.collectOwnWaits(dst, row, now)
			}
		}
		for _, effect := range span.effectsViaLinks.Order {
			effect.collectOwnWaits(dst, row, now)
			for _, child := range effect.ChildSpans.Order {
				if child.Name == effect.Name {
					child.collectOwnWaits(dst, row, now)
				}
			}
		}
	}
}

func (span *Span) collectOwnWaits(dst *[]waitWindow, row *Span, now time.Time) {
	for i := range span.Links {
		link := &span.Links[i]
		if !link.IsWait() {
			continue
		}
		target := span.db.Spans.Map[link.SpanContext.SpanID]

		// A caller blocked on its own resolver execution (its same-name
		// call_exec twin) is doing its own work, not waiting on another op.
		if (link.WaitReason == "call_exec" || link.WaitReason == "singleflight") &&
			target != nil && target.Name == row.Name && spanInSubtree(target, row) {
			continue
		}

		end := link.WaitEnd
		if end.After(now) {
			end = now
		}
		if !end.After(link.WaitStart) {
			continue
		}

		*dst = append(*dst, waitWindow{
			start:  link.WaitStart,
			end:    end,
			target: target,
			reason: link.WaitReason,
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
func (span *Span) collectInferredWaits(dst *[]waitWindow, now time.Time) {
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
		for _, child := range s.ChildSpans.Order {
			add(child)
			if child.Name == s.Name {
				for _, gc := range child.ChildSpans.Order {
					add(gc)
				}
			}
		}
		for _, effect := range s.effectsViaLinks.Order {
			add(effect)
			for _, child := range effect.ChildSpans.Order {
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
				end:    c.observedInterval(now).End,
				target: c,
			}
			if win.end.After(now) {
				win.end = now
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

// resolveBlocker maps internal plumbing spans (lazy "resume" markers)
// to the user-meaningful op whose deferred work they represent: the resume's
// causal source, falling back to its parent.
func resolveBlocker(target *Span) *Span {
	for depth := 0; target != nil && depth < 10; depth++ {
		if !strings.HasPrefix(target.Name, "resume ") {
			return target
		}
		var next *Span
		for _, cause := range target.causesViaLinks.Order {
			if cause != target {
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
	}
	return target
}

func blockerLabel(blocker *Span) string {
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

func truncateLabel(s string) string {
	if len(s) > 48 {
		return s[:47] + "…"
	}
	return s
}

// execArgv finds the wcprof exec argv stamped on the op's process-run
// span (a grandchild of the call span: call → exec.run → exec.processRun).
func (span *Span) execArgv(depth int) string {
	if raw, ok := span.ExtraAttributes[telemetryattrs.WcprofExecArgvAttr]; ok {
		var enc string
		if err := json.Unmarshal(raw, &enc); err == nil {
			var argv []string
			if err := json.Unmarshal([]byte(enc), &argv); err == nil && len(argv) > 0 {
				return truncateLabel(strings.Join(argv, " "))
			}
		}
	}
	if depth >= 2 {
		return ""
	}
	for _, child := range span.ChildSpans.Order {
		if argv := child.execArgv(depth + 1); argv != "" {
			return argv
		}
	}
	return ""
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
