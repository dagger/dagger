package dagui

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
	return buildSurfacedTree(db, root,
		func(s *Span) string { return s.GeneratorName },
		func(name string, span *Span, failed bool) *GeneratorNode {
			return &GeneratorNode{Name: name, Span: span, Failed: failed}
		},
		func(n *GeneratorNode) *[]*GeneratorNode { return &n.Children },
		func(n *GeneratorNode) (bool, string) { return n.Failed, n.Name },
	)
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

// HasGenerators reports whether the trace's surfaced generator view is
// non-empty, so the live view can promote it to the top level (mirrors
// HasChecks).
func (db *DB) HasGenerators() bool {
	return db.HasGeneratorsForSpan(nil)
}

// HasGeneratorsForSpan reports whether the root-relative surfaced generator
// view is non-empty. A nil root means the live trace root.
func (db *DB) HasGeneratorsForSpan(root *Span) bool {
	return len(db.SurfacedGeneratorsForSpan(root)) > 0
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
