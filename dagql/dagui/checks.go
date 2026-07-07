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

// SurfacedChecks returns the trace's checks as a tree, independent of the
// `reveal` mechanism.
//
// A span with a CheckName is surfaced only if its ancestor chain reaches the
// trace root with no Boundary or Encapsulate span in between. That drops checks
// a test intentionally runs (e.g. fixtures asserting that a check fails), which
// are wrapped in a boundary -- the same containment the reveal bubbling applies,
// minus the reveal stop that hides legitimate checks nested under another check.
//
// Requiring the chain to *reach the root* matters because the boundary span is
// often not loaded: a fixture check reaches the outer trace through a nested
// `dagger check` invocation, so its chain dead-ends at the reparenting seam (the
// spawning withExec) -- below the test's Boundary span -- which the incremental
// fetch never pulls in. A severed chain can't be proven boundary-free, so it's
// treated as contained too; a legitimate trace-level check always reaches root,
// since the priority fetch loads its full ancestor chain.
//
// Checks are deduped by name (a check is failed if any of its spans failed) and
// nested under the nearest surfaced ancestor check. Roots and children are
// ordered failed-first, then by name.
//
// The result is cached per DB mutation: every input (check names, ancestor
// chains, boundaries, statuses, the root span) only changes when a span is
// added or updated, and a render frame re-reads the tree for every check row.
// Callers must treat the returned nodes as read-only.
func (db *DB) SurfacedChecks() []*CheckNode {
	if db.surfacedChecksInit && db.surfacedChecksAt == db.mutations {
		return db.surfacedChecks
	}
	db.surfacedChecks = db.buildSurfacedChecks()
	db.surfacedChecksAt = db.mutations
	db.surfacedChecksInit = true
	return db.surfacedChecks
}

func (db *DB) buildSurfacedChecks() []*CheckNode {
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
		// Walk ancestors toward the root: a Boundary/Encapsulate between this check
		// and the root contains it (hide it); otherwise remember the nearest
		// ancestor check to nest under, and note whether we reach the root at all.
		contained := false
		parentName := ""
		reachedRoot := span == db.RootSpan
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			if p.Boundary || p.Encapsulate {
				contained = true
				break
			}
			if parentName == "" && p.CheckName != "" && p.CheckName != span.CheckName {
				parentName = p.CheckName
			}
			if p == db.RootSpan {
				reachedRoot = true
				break
			}
		}
		// A check whose ancestor chain is severed before it reaches the trace
		// root can't be proven boundary-free: a check a test runs as a fixture
		// reaches the outer trace through a nested `dagger check` invocation (a
		// reparenting seam at the spawning withExec), so its chain dead-ends at
		// that seam -- or at an unreceived placeholder -- below the test's
		// Boundary span, which the incremental fetch never loaded. Treat that
		// severance as containment, so fixtures stay hidden just like checks with
		// a loaded Boundary ancestor.
		if !contained && db.RootSpan != nil && !reachedRoot {
			contained = true
		}
		if contained {
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
