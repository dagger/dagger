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
// A span with a CheckName is surfaced only if no Boundary or Encapsulate
// ancestor sits between it and the root. That drops checks a test intentionally
// runs (e.g. fixtures asserting that a check fails), which are wrapped in a
// boundary -- the same containment the reveal bubbling applies, minus the
// reveal stop that hides legitimate checks nested under another check.
//
// Checks are deduped by name (a check is failed if any of its spans failed) and
// nested under the nearest surfaced ancestor check. Roots and children are
// ordered failed-first, then by name.
func (db *DB) SurfacedChecks() []*CheckNode {
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
		// Walk ancestors: a Boundary/Encapsulate between this check and the root
		// contains it (hide it); otherwise remember the nearest ancestor check to
		// nest under.
		contained := false
		parentName := ""
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			if p.Boundary || p.Encapsulate {
				contained = true
				break
			}
			if parentName == "" && p.CheckName != "" && p.CheckName != span.CheckName {
				parentName = p.CheckName
			}
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
