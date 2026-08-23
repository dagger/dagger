package dagui

import (
	"sort"
)

// CheckNode is a surfaced trace-level check (deduped by check name), with any
// nested child checks beneath it.
type CheckNode struct {
	Name     string
	Span     *Span // representative span (a failed one when the check failed)
	Failed   bool
	Children []*CheckNode
}

// SurfacedChecks returns the whole trace's checks as a tree, independent of
// the `reveal` mechanism. It is SurfacedChecksForSpan relative to the trace
// root.
func (db *DB) SurfacedChecks() []*CheckNode {
	return db.SurfacedChecksForSpan(nil)
}

// SurfacedChecksForSpan returns the checks that ran beneath root as a tree,
// independent of the `reveal` mechanism. A nil root means the trace root, i.e.
// the whole trace.
//
// Surfacing is zoom-relative: it answers "what checks ran beneath THIS span".
// A span with a CheckName is surfaced only if its ancestor chain reaches root
// with no Boundary or Encapsulate span in between. That drops checks a test
// intentionally runs (e.g. fixtures asserting that a check fails), which are
// wrapped in a boundary -- the same containment the reveal bubbling applies,
// minus the reveal stop that hides legitimate checks nested under another
// check. The walk STOPS at root, so a Boundary on or above it (a zoom to an
// LLM tool-call display span, which is itself a boundary) says nothing about
// containment *within* the subtree being asked about.
//
// Requiring the chain to *reach root* matters because the boundary span is
// often not loaded: a fixture check reaches the outer trace through a nested
// `dagger check` invocation, so its chain dead-ends at the reparenting seam (the
// spawning withExec) -- below the test's Boundary span -- which the incremental
// fetch never pulls in. A severed chain can't be proven boundary-free, so it's
// treated as contained too; a legitimate trace-level check always reaches root,
// since the priority fetch loads its full ancestor chain. The same rule is what
// excludes checks that ran OUTSIDE the given root: their chains reach the trace
// root without ever passing through it.
//
// Checks are deduped by name (a check is failed if any of its spans failed) and
// nested under the nearest surfaced ancestor check. Roots and children are
// ordered failed-first, then by name.
//
// The result is cached per DB mutation AND per root: every other input (check
// names, ancestor chains, boundaries, statuses) only changes when a span is
// added or updated, and a render frame re-reads the tree for every check row.
// Callers must treat the returned nodes as read-only.
func (db *DB) SurfacedChecksForSpan(root *Span) []*CheckNode {
	r := db.surfaceRoot(root)
	key := surfaceRootID(r)
	if db.surfacedChecksInit && db.surfacedChecksAt == db.mutations && db.surfacedChecksRoot == key {
		return db.surfacedChecks
	}
	db.surfacedChecks = db.buildSurfacedChecks(r)
	db.surfacedChecksAt = db.mutations
	db.surfacedChecksRoot = key
	db.surfacedChecksInit = true
	return db.surfacedChecks
}

func (db *DB) buildSurfacedChecks(root *Span) []*CheckNode {
	type info struct {
		span       *Span
		parentName string
		failed     bool
	}
	byName := map[string]*info{}
	for span := range db.Spans.Iter() {
		if span.CheckName == "" {
			continue
		}
		// Remember the nearest ancestor check while the shared walk decides
		// whether Boundary/Encapsulate contains this span relative to root.
		parentName := ""
		if !spanMayRollUp(span, root, func(parent *Span) {
			if parentName == "" && parent.CheckName != "" && parent.CheckName != span.CheckName {
				parentName = parent.CheckName
			}
		}) {
			continue
		}
		failed := span.IsFailedOrCausedFailure()
		cur, ok := byName[span.CheckName]
		switch {
		case !ok:
			byName[span.CheckName] = &info{span: span, parentName: parentName, failed: failed}
		case failed && !cur.failed:
			// prefer a failed representative so the rendered detail points at the
			// failure
			cur.span = span
			cur.failed = true
			cur.parentName = parentName
		default:
			cur.failed = cur.failed || failed
		}
	}

	nodes := make(map[string]*CheckNode, len(byName))
	for name, in := range byName {
		nodes[name] = &CheckNode{Name: name, Span: in.span, Failed: in.failed}
	}
	var roots []*CheckNode
	for name, in := range byName {
		node := nodes[name]
		if parent, ok := nodes[in.parentName]; ok && in.parentName != "" {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	var sortNodes func(ns []*CheckNode)
	sortNodes = func(ns []*CheckNode) {
		sort.SliceStable(ns, func(i, j int) bool {
			if ns[i].Failed != ns[j].Failed {
				return ns[i].Failed // failed first
			}
			return ns[i].Name < ns[j].Name
		})
		for _, n := range ns {
			sortNodes(n.Children)
		}
	}
	sortNodes(roots)
	return roots
}

// HasFailedChild reports whether any descendant check failed, so a failing
// parent check can defer its own error detail to the children that explain it.
func (n *CheckNode) HasFailedChild() bool {
	for _, c := range n.Children {
		if c.Failed || c.HasFailedChild() {
			return true
		}
	}
	return false
}
