package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// traceTargetMaxNamesListed bounds how many names an "unknown name" error
// lists back. A trace can carry hundreds of checks and thousands of test
// cases; a handful of examples tells the reader what the vocabulary looks
// like, a full dump just burns its context.
const traceTargetMaxNamesListed = 30

// traceTarget is what ReadTrace was asked to render: at most one of these is
// set (the tool rejects an empty request).
type traceTarget struct {
	Span  string
	Check string
	Test  string
}

func (t traceTarget) empty() bool {
	return t.Span == "" && t.Check == "" && t.Test == ""
}

// resolveTraceTarget resolves a ReadTrace target to the span ID its report
// should be scoped to, against the session's (cached, incrementally loaded)
// trace DB.
func resolveTraceTarget(ctx context.Context, target traceTarget) (string, error) {
	var spanID string
	err := withTraceReportDB(ctx, func(db *dagui.DB) error {
		var err error
		spanID, err = resolveTraceTargetIn(db, target)
		return err
	})
	return spanID, err
}

// resolveTraceTargetIn is the pure half of resolveTraceTarget: everything it
// needs is in db, so it is directly testable.
//
// Resolution rules:
//   - span: must be a valid hex span ID that the trace actually contains.
//   - check: must name a check the trace contains (matched against span check
//     names directly, NOT against DB.SurfacedChecks: a check run inside an LLM
//     tool call always sits under the tool-call display span's Boundary, and a
//     name lookup has no business inheriting that containment); resolves to the
//     most recently *started* span carrying that check name.
//   - test: matched against test cases first, then suites (by full name or
//     leaf name, as TestView indexes both); resolves to the most recently
//     started matching span.
//
// Most-recent-wins because a name is how a reader refers to a check or test
// they just saw run: when a name matched several spans (a retried check, a
// test case run under more than one suite), the latest one is the one they
// mean.
func resolveTraceTargetIn(db *dagui.DB, target traceTarget) (string, error) {
	switch {
	case target.empty():
		return "", fmt.Errorf("ReadTrace needs a target: pass span (a hex span ID), " +
			"check (a check name, e.g. \"lint:check\"), or test (a test case or suite name)")

	case target.Span != "":
		id, err := trace.SpanIDFromHex(target.Span)
		if err != nil {
			return "", fmt.Errorf("invalid span ID %q: %w", target.Span, err)
		}
		if _, ok := db.Spans.Map[dagui.SpanID{SpanID: id}]; !ok {
			return "", fmt.Errorf("no span %q in this trace", target.Span)
		}
		return target.Span, nil

	case target.Check != "":
		// Resolve by scanning span check names directly, NOT via
		// DB.SurfacedChecks: surfacing answers "should this check be listed in
		// a report", which depends on Boundary/Encapsulate containment -- and a
		// check run inside an LLM tool call always has a Boundary ancestor (the
		// tool-call display span). A name lookup must not inherit that: the
		// reader is naming something it just saw run.
		names := map[string]bool{}
		var best *dagui.Span
		for span := range db.Spans.Iter() {
			if span.CheckName == "" {
				continue
			}
			names[span.CheckName] = true
			if span.CheckName != target.Check {
				continue
			}
			best = mostRecentSpan(best, span)
		}
		if best == nil {
			return "", fmt.Errorf("no check named %q in this trace%s",
				target.Check, listAvailable("checks", sortedKeys(names)))
		}
		return best.ID.SpanID.String(), nil

	default:
		view := db.TestView()
		nodes := view.CasesByName[target.Test]
		if len(nodes) == 0 {
			nodes = view.SuitesByName[target.Test]
		}
		if len(nodes) == 0 {
			return "", fmt.Errorf("no test case or suite named %q in this trace%s",
				target.Test, listAvailable("tests", testNames(view)))
		}
		var best *dagui.Span
		for _, node := range nodes {
			span := node.Span
			if span == nil {
				// A virtual suite is synthetic and has no span of its own; its
				// first real descendant stands in for it, exactly as the UI
				// uses it to focus the node.
				span = node.RepresentativeSpan
			}
			best = mostRecentSpan(best, span)
		}
		if best == nil {
			return "", fmt.Errorf("no span found for test %q in this trace", target.Test)
		}
		return best.ID.SpanID.String(), nil
	}
}

// mostRecentSpan returns whichever of the two started later, ignoring nils.
// Ties keep the incumbent, so the result is stable for spans with identical
// start times.
func mostRecentSpan(best, span *dagui.Span) *dagui.Span {
	if span == nil {
		return best
	}
	if best == nil || span.StartTime.After(best.StartTime) {
		return span
	}
	return best
}

// testNames is every name TestView can be looked up by (cases first, then
// suites), deduped.
func testNames(view *dagui.TestView) []string {
	if view == nil {
		return nil
	}
	seen := map[string]bool{}
	for name := range view.CasesByName {
		seen[name] = true
	}
	for name := range view.SuitesByName {
		seen[name] = true
	}
	return sortedKeys(seen)
}

func sortedKeys(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// listAvailable renders a bounded ", available <kind>: a, b, c" suffix for an
// unknown-name error, or "" when there are none to list.
func listAvailable(kind string, names []string) string {
	if len(names) == 0 {
		return fmt.Sprintf(" (no %s in this trace)", kind)
	}
	shown := names
	suffix := ""
	if len(shown) > traceTargetMaxNamesListed {
		shown = shown[:traceTargetMaxNamesListed]
		suffix = fmt.Sprintf(" (and %d more)", len(names)-traceTargetMaxNamesListed)
	}
	return fmt.Sprintf("; available %s: %s%s", kind, strings.Join(shown, ", "), suffix)
}
