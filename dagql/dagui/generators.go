package dagui

import (
	"sort"
)

// GeneratorNode is a surfaced trace-level generator run (deduped by generator
// name), with any nested child generators beneath it.
type GeneratorNode struct {
	Name     string
	Span     *Span // representative span (a failed one when the generator failed)
	Failed   bool
	Children []*GeneratorNode
}

// SurfacedGenerators returns the whole trace's `dagger generate` generator
// runs as a tree. It is SurfacedGeneratorsForSpan relative to the trace root.
func (db *DB) SurfacedGenerators() []*GeneratorNode {
	return db.SurfacedGeneratorsForSpan(nil)
}

// SurfacedGeneratorsForSpan returns the generator runs beneath root as a tree,
// independent of the `reveal` mechanism -- the generator analog of
// DB.SurfacedChecksForSpan. A nil root means the trace root.
//
// A span with a GeneratorName is surfaced only if its ancestor chain reaches
// root with no Boundary or Encapsulate span in between, and severed chains are
// treated as contained -- the same zoom-relative containment rules as checks
// (see SurfacedChecksForSpan for the full rationale), so generator runs a test
// drives as a fixture stay hidden.
//
// Generators are deduped by name (a generator is failed if any of its spans
// failed) and nested under the nearest surfaced ancestor generator. Roots and
// children are ordered failed-first, then by name.
//
// The result is cached per DB mutation and per root, like SurfacedChecks;
// callers must treat the returned nodes as read-only.
func (db *DB) SurfacedGeneratorsForSpan(root *Span) []*GeneratorNode {
	r := db.surfaceRoot(root)
	key := surfaceRootID(r)
	if db.surfacedGeneratorsInit && db.surfacedGeneratorsAt == db.mutations && db.surfacedGeneratorsRoot == key {
		return db.surfacedGenerators
	}
	db.surfacedGenerators = db.buildSurfacedGenerators(r)
	db.surfacedGeneratorsAt = db.mutations
	db.surfacedGeneratorsRoot = key
	db.surfacedGeneratorsInit = true
	return db.surfacedGenerators
}

func (db *DB) buildSurfacedGenerators(root *Span) []*GeneratorNode {
	type info struct {
		span       *Span
		parentName string
		failed     bool
	}
	byName := map[string]*info{}
	for span := range db.Spans.Iter() {
		if span.GeneratorName == "" {
			continue
		}
		// Walk ancestors toward root: a Boundary/Encapsulate between this
		// generator and root contains it (hide it); otherwise remember the
		// nearest ancestor generator to nest under, and note whether we reach
		// root at all.
		contained := false
		parentName := ""
		reachedRoot := span == root
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			atRoot := p == root
			if !atRoot && (p.Boundary || p.Encapsulate) {
				contained = true
				break
			}
			if parentName == "" && p.GeneratorName != "" && p.GeneratorName != span.GeneratorName {
				parentName = p.GeneratorName
			}
			if atRoot {
				// Stop at root: its own flags are outside the question, but its
				// name still nests (see SurfacedChecksForSpan).
				reachedRoot = true
				break
			}
		}
		// A chain severed before root can't be proven boundary-free; treat it
		// as contained, exactly as SurfacedChecksForSpan does.
		if !contained && root != nil && !reachedRoot {
			contained = true
		}
		if contained {
			continue
		}
		failed := span.IsFailedOrCausedFailure()
		cur, ok := byName[span.GeneratorName]
		switch {
		case !ok:
			byName[span.GeneratorName] = &info{span: span, parentName: parentName, failed: failed}
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

	nodes := make(map[string]*GeneratorNode, len(byName))
	for name, in := range byName {
		nodes[name] = &GeneratorNode{Name: name, Span: in.span, Failed: in.failed}
	}
	var roots []*GeneratorNode
	for name, in := range byName {
		node := nodes[name]
		if parent, ok := nodes[in.parentName]; ok && in.parentName != "" {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	var sortNodes func(ns []*GeneratorNode)
	sortNodes = func(ns []*GeneratorNode) {
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

// HasFailedChild reports whether any descendant generator failed, so a failing
// parent generator can defer its own error detail to the children that explain
// it.
func (n *GeneratorNode) HasFailedChild() bool {
	for _, c := range n.Children {
		if c.Failed || c.HasFailedChild() {
			return true
		}
	}
	return false
}

// HasGenerators reports whether the trace contains any generator spans, so the
// live view can promote them to the top level (mirrors HasChecks).
func (db *DB) HasGenerators() bool {
	return db.HasGeneratorsForSpan(nil)
}

// HasGeneratorsForSpan is HasGenerators restricted to root's subtree; a nil
// root means the whole trace.
func (db *DB) HasGeneratorsForSpan(root *Span) bool {
	for _, span := range db.Spans.Order {
		if span.GeneratorName != "" && underSurfaceRoot(span, root) {
			return true
		}
	}
	return false
}

// PromoteGeneratorsTo wires the surfaced generators into host.RevealedSpans
// (and each nested generator into its parent's RevealedSpans) so the live tree
// can surface a `dagger generate` run's generators at the top level without
// relying on the deprecated `reveal` attribute. It mirrors what reveal
// bubbling used to populate, derived from the reveal-independent
// SurfacedGenerators tree. Idempotent: re-adds are no-ops on the set.
//
// Callers mark host Passthrough (see promoteGeneratorsLocked) so RowsView
// iterates these revealed spans instead of host's raw children.
func (db *DB) PromoteGeneratorsTo(host *Span) {
	if host == nil {
		return
	}
	var wire func(parent *Span, nodes []*GeneratorNode)
	wire = func(parent *Span, nodes []*GeneratorNode) {
		for _, node := range nodes {
			parent.RevealedSpans.Add(node.Span)
			wire(node.Span, node.Children)
		}
	}
	wire(host, db.SurfacedGenerators())
}
