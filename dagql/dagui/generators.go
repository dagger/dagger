package dagui

// GeneratorNode is a surfaced trace-level generator run (deduped by generator
// name), with any nested child generators beneath it. Its status is its
// representative span's.
type GeneratorNode struct {
	Name     string
	Span     *Span // representative span (a failed one when the generator failed)
	Children []*GeneratorNode
}

// Failed reports whether the generator failed: its representative span is a
// failed one whenever any of its spans failed.
func (n *GeneratorNode) Failed() bool { return n.Span.IsFailedOrCausedFailure() }

// GeneratorChanges is what a `dagger generate` generator changed: its
// changeset's diff stat, as telemetryattrs.GeneratorChangesAttr carries it on
// the generator's span.
type GeneratorChanges struct {
	// Files, Added and Removed total every changed path, Entries or not.
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// Entries are the first changed paths, at most MaxGeneratorChangeEntries.
	Entries []GeneratorChange `json:"entries,omitempty"`
}

// GeneratorChange is one changed path of a generator's changeset.
type GeneratorChange struct {
	Path string `json:"path"`
	// OldPath is the path a renamed file had.
	OldPath string `json:"oldPath,omitempty"`
	// Kind is ADDED, MODIFIED, REMOVED or RENAMED.
	Kind    string `json:"kind"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
}

// MaxGeneratorChangeEntries bounds GeneratorChanges.Entries, so a generator
// that rewrites thousands of files doesn't put them all on its span.
const MaxGeneratorChangeEntries = 50

// Changes returns what the generator changed, or nil when it produced no
// changes, or failed.
func (n *GeneratorNode) Changes() *GeneratorChanges {
	if n.Failed() {
		return nil
	}
	return n.Span.GeneratorChanges
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
	return db.surfacedGenerators.get(db, db.surfaceRoot(root), isGeneratorSpan, buildSurfacedGenerators)
}

func generatorNameOf(s *Span) string { return s.GeneratorName }

func isGeneratorSpan(s *Span) bool { return s.GeneratorName != "" }

func buildSurfacedGenerators(candidates []*Span, root *Span) []*GeneratorNode {
	return buildSurfacedTree(candidates, root,
		generatorNameOf,
		func(name string, span *Span) *GeneratorNode {
			return &GeneratorNode{Name: name, Span: span}
		},
		func(n *GeneratorNode) *[]*GeneratorNode { return &n.Children },
		func(n *GeneratorNode) (bool, string) { return n.Failed(), n.Name },
	)
}

// HasFailedChild reports whether any descendant generator failed, so a failing
// parent generator can defer its own error detail to the children that explain
// it.
func (n *GeneratorNode) HasFailedChild() bool {
	for _, c := range n.Children {
		if c.Failed() || c.HasFailedChild() {
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
			parent.AddRevealedSpan(node.Span)
			wire(node.Span, node.Children)
		}
	}
	wire(host, db.SurfacedGenerators())
}
