package idtui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
)

// Trace inspection renderers: plain-text views over a dagui.DB that answer
// the questions a person (or an agent) asks while debugging a trace -- "which
// spans are named X", "why is this span hidden / what's above and below it",
// "where did the time go beneath this span".
//
// They are pure functions of the DB so the same views serve two very
// different readers: the TUI console (DAGGER_TUI_CONSOLE, see
// frontend_console.go), whose DB is the frontend's live one, and the engine's
// LLM builtin tools (core/trace_inspect.go), which materialize a throwaway DB
// from the client's telemetry store per call. Keeping one renderer per view is
// what keeps the two vocabularies identical, so a workflow learned in one
// transfers to the other.

// InspectMaxChildren bounds how many direct children a span detail lists.
// The list is a navigation aid -- enough to pick the next span to inspect --
// not an enumeration; RenderSpanTimings is the tool for the whole subtree.
const InspectMaxChildren = 20

// SpanStatus classifies a span with a short, stable vocabulary: ERROR (the
// span itself errored), FAIL (it passed its own OTel status through but a
// failure rides on a link -- a test or check whose error is on a descendant
// or linked span), run (still running), or ok.
func SpanStatus(sp *dagui.Span) string {
	switch {
	case sp.IsFailed():
		return "ERROR"
	case sp.IsFailedOrCausedFailure():
		return "FAIL"
	case sp.IsRunning():
		return "run"
	default:
		return "ok"
	}
}

// SpanFlags lists the dagui flags set on a span that shape how the UI treats
// it (visibility, encapsulation, log/span roll-up) -- only the ones that are
// actually set, so ancestor-chain lines stay compact.
func SpanFlags(sp *dagui.Span) []string {
	var flags []string
	set := func(on bool, name string) {
		if on {
			flags = append(flags, name)
		}
	}
	set(sp.Internal, "internal")
	set(sp.Boundary, "boundary")
	set(sp.Encapsulate, "encapsulate")
	set(sp.Encapsulated, "encapsulated")
	set(sp.Passthrough, "passthrough")
	set(sp.Ignore, "ignore")
	set(sp.Reveal, "reveal")
	set(sp.RollUpLogs, "rollUpLogs")
	set(sp.RollUpSpans, "rollUpSpans")
	set(sp.Cached, "cached")
	set(sp.Canceled, "canceled")
	switch {
	case sp.Service && sp.ServiceName != "":
		flags = append(flags, "service="+sp.ServiceName)
	case sp.Service:
		flags = append(flags, "service")
	case sp.ServiceName != "":
		flags = append(flags, "serviceName="+sp.ServiceName)
	}
	if sp.CheckName != "" {
		flags = append(flags, "check="+sp.CheckName)
	}
	if sp.LLMRole != "" {
		flags = append(flags, "llmRole="+sp.LLMRole)
	}
	if sp.LLMTool != "" {
		flags = append(flags, "llmTool="+sp.LLMTool)
	}
	return flags
}

// RenderSpanList lists the spans loaded in db -- one "id  status  name" line
// each, in arrival order -- optionally filtered by a name (or service name)
// substring, so a caller can find a span ID to zoom to, inspect, or read
// logs beneath. Service-instance spans are tagged with their hostname so
// running services (whose logs live beneath them) are cheap to find.
//
// limit > 0 keeps only the NEWEST limit matches -- the most recent spans are
// the ones a reader asking "what just ran" means -- and says how many earlier
// matches were dropped. 0 lists them all.
func RenderSpanList(db *dagui.DB, query string, limit int) string {
	var lines []string
	for _, sp := range db.Spans.Order {
		if !sp.Received {
			// A placeholder allocated for a parent pointer, not a span
			// anyone can act on.
			continue
		}
		if query != "" && !strings.Contains(sp.Name, query) && !strings.Contains(sp.ServiceName, query) {
			continue
		}
		name := sp.Name
		if sp.Service {
			tag := "service"
			if sp.ServiceName != "" {
				tag += " " + sp.ServiceName
			}
			name += "  [" + tag + "]"
		}
		lines = append(lines, fmt.Sprintf("%s  %-5s  %s", sp.ID, SpanStatus(sp), name))
	}
	var b strings.Builder
	if limit > 0 && len(lines) > limit {
		fmt.Fprintf(&b, "... %d earlier matching spans omitted (narrow the query, or raise the limit) ...\n", len(lines)-limit)
		lines = lines[len(lines)-limit:]
	}
	for _, line := range lines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderSpanDetail reports one span in depth: status, timing, the error it
// carries, the UI-shaping flags, the parent chain up to the root -- each
// ancestor with its own set flags, which is what debugging "why is this span
// hidden / why didn't its logs roll up" needs -- and its direct children, as
// the way down. Returns false if the span isn't loaded in the DB.
func RenderSpanDetail(db *dagui.DB, id dagui.SpanID) (string, bool) {
	sp, ok := db.Spans.Map[id]
	if !ok || sp == nil || !sp.Received {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "span:     %s  %s\n", sp.ID, sp.Name)
	if sp.CallDigest != "" {
		// The bridge to recipe inspection (InspectCall / the console's /id):
		// the digest of the dagql call this span reports on.
		fmt.Fprintf(&b, "call:     %s\n", sp.CallDigest)
	}
	fmt.Fprintf(&b, "status:   %s\n", SpanStatus(sp))
	if sp.IsFailed() && sp.Status.Description != "" {
		fmt.Fprintf(&b, "error:    %s\n", strings.ReplaceAll(sp.Status.Description, "\n", "\n          "))
	}
	if origins := sp.ErrorOrigins.Order; len(origins) > 0 {
		fmt.Fprintf(&b, "error origins:\n")
		for _, origin := range origins {
			fmt.Fprintf(&b, "  %s  %s\n", origin.ID, origin.Name)
		}
	}
	if sp.StartTime.IsZero() {
		fmt.Fprintf(&b, "started:  (unknown)\n")
	} else {
		fmt.Fprintf(&b, "started:  %s\n", sp.StartTime.Format(time.RFC3339Nano))
		if sp.IsRunning() {
			// Running spans have no end time yet (dagui encodes that as
			// EndTime < StartTime); show elapsed time instead.
			fmt.Fprintf(&b, "ended:    (still running)\n")
			fmt.Fprintf(&b, "duration: %s (so far)\n", time.Since(sp.StartTime).Truncate(time.Millisecond))
		} else {
			fmt.Fprintf(&b, "ended:    %s\n", sp.EndTime.Format(time.RFC3339Nano))
			fmt.Fprintf(&b, "duration: %s\n", sp.EndTime.Sub(sp.StartTime).Truncate(time.Millisecond))
		}
	}
	if flags := SpanFlags(sp); len(flags) > 0 {
		fmt.Fprintf(&b, "flags:    %s\n", strings.Join(flags, " "))
	} else {
		fmt.Fprintf(&b, "flags:    (none)\n")
	}
	if sp.HasLogs {
		fmt.Fprintf(&b, "logs:     yes\n")
	}
	fmt.Fprintf(&b, "parents (nearest first):\n")
	if sp.ParentSpan == nil {
		fmt.Fprintf(&b, "  (none — root span)\n")
	}
	for parent := sp.ParentSpan; parent != nil; parent = parent.ParentSpan {
		line := fmt.Sprintf("  %s  %s", parent.ID, parent.Name)
		if flags := SpanFlags(parent); len(flags) > 0 {
			line += "  [" + strings.Join(flags, " ") + "]"
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	// ChildSpans folds cause-linked children in (dagui treats a cause link
	// as a parent→child edge), so this is the same containment the tree
	// renders; only loaded children are known.
	children := make([]*dagui.Span, 0, len(sp.ChildSpans.Order))
	for _, child := range sp.ChildSpans.Order {
		if child.Received {
			children = append(children, child)
		}
	}
	fmt.Fprintf(&b, "children (loaded): %d\n", len(children))
	for i, child := range children {
		if i >= InspectMaxChildren {
			fmt.Fprintf(&b, "  ... %d more\n", len(children)-i)
			break
		}
		line := fmt.Sprintf("  %s  %-5s  %s", child.ID, SpanStatus(child), child.Name)
		if flags := SpanFlags(child); len(flags) > 0 {
			line += "  [" + strings.Join(flags, " ") + "]"
		}
		fmt.Fprintf(&b, "%s\n", line)
	}
	return b.String(), true
}

// RenderSpanTimings lists root's loaded subtree chronologically -- span ID,
// parent ID, start offset from root, wall duration, name -- including
// internal spans. It reports raw parent/child timing, not the UI's filtered or
// linked tree, and never fetches telemetry or logs. now is shared by all
// running rows so their elapsed durations describe one snapshot. Returns
// false if root isn't loaded in the DB.
func RenderSpanTimings(db *dagui.DB, id dagui.SpanID, minDuration time.Duration, limit int, now time.Time) (string, bool) {
	root, ok := db.Spans.Map[id]
	if !ok || root == nil || !root.Received {
		return "", false
	}
	children := make(map[dagui.SpanID][]*dagui.Span)
	for _, sp := range db.Spans.Order {
		if sp.Received {
			children[sp.ParentID] = append(children[sp.ParentID], sp)
		}
	}
	var spans []*dagui.Span
	seen := make(map[dagui.SpanID]bool)
	pending := []*dagui.Span{root}
	for len(pending) > 0 {
		sp := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		spans = append(spans, sp)
		pending = append(pending, children[sp.ID]...)
	}
	slices.SortFunc(spans, func(a, b *dagui.Span) int {
		// Unknown starts sort last; equal starts have a stable ID tie-break.
		if a.StartTime.IsZero() != b.StartTime.IsZero() {
			if a.StartTime.IsZero() {
				return 1
			}
			return -1
		}
		if cmp := a.StartTime.Compare(b.StartTime); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	var b strings.Builder
	fmt.Fprintf(&b, "root: %s  %q\n", root.ID, root.Name)
	fmt.Fprintln(&b, "Loaded spans only (including internal); incomplete if telemetry is missing or not fetched. No logs fetched.")
	fmt.Fprintln(&b, "Durations are wall time, may overlap, and are not CPU self time; 'so far' uses the current clock. Unknown timings survive the duration filter.")
	fmt.Fprintln(&b, "span_id  parent_id  start_offset  duration  name")
	shown, filtered, capped := 0, 0, 0
	for _, sp := range spans {
		offset, duration := "unknown", "unknown"
		if !sp.StartTime.IsZero() {
			elapsed := sp.EndTime.Sub(sp.StartTime)
			if sp.IsRunning() {
				elapsed = now.Sub(sp.StartTime)
			}
			if elapsed < minDuration {
				filtered++
				continue
			}
			duration = elapsed.String()
			if sp.IsRunning() {
				duration += " (so far)"
			}
			if !root.StartTime.IsZero() {
				offset = sp.StartTime.Sub(root.StartTime).String()
			}
		}
		if limit > 0 && shown >= limit {
			capped++
			continue
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s  %q\n", sp.ID, sp.ParentID, offset, duration, sp.Name)
		shown++
	}
	fmt.Fprintf(&b, "shown: %d; omitted: %d (%d below minDuration, %d over limit); loaded subtree: %d\n", shown, filtered+capped, filtered, capped, len(spans))
	return b.String(), true
}
