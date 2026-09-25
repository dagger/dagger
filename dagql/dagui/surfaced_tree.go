package dagui

import (
	"sort"
)

// surfacedTreeMemo caches a surfaced tree (checks, generators, services) per
// root for the current DB mutation. Surfacing is asked about many roots in one
// render frame -- the zoomed span for the report sections, plus every LLM tool
// call row for its inline rollups -- so a single-entry memo keyed by root would
// thrash. The candidate spans are collected once per mutation, so each
// additional root costs a walk of just those spans' ancestor chains rather
// than a scan of the whole trace.
type surfacedTreeMemo[N any] struct {
	init       bool
	at         uint64
	candidates []*Span
	byRoot     map[SpanID][]*N
}

func (m *surfacedTreeMemo[N]) get(db *DB, root *Span, isCandidate func(*Span) bool, build func(candidates []*Span, root *Span) []*N) []*N {
	if !m.init || m.at != db.mutations {
		m.candidates = m.candidates[:0]
		for span := range db.Spans.Iter() {
			if isCandidate(span) {
				m.candidates = append(m.candidates, span)
			}
		}
		m.byRoot = map[SpanID][]*N{}
		m.at = db.mutations
		m.init = true
	}
	key := surfaceRootID(root)
	if nodes, ok := m.byRoot[key]; ok {
		return nodes
	}
	nodes := build(m.candidates, root)
	m.byRoot[key] = nodes
	return nodes
}

// buildSurfacedTree is the shared shape of buildSurfacedChecks and
// buildSurfacedGenerators: walk the candidate spans, keep those with a name (as
// told by nameOf) that the Boundary/Encapsulate rules let roll up to root, dedupe
// them by name preferring a failed representative, hang each under its
// nearest named ancestor, and sort failed-first then by name at every level.
//
// nodeOf constructs a node; kids and key expose its children slice and its
// (failed, name) sort key so the tree can be built without reflection.
func buildSurfacedTree[N any](
	candidates []*Span,
	root *Span,
	nameOf func(*Span) string,
	nodeOf func(name string, span *Span, failed bool) *N,
	kids func(*N) *[]*N,
	key func(*N) (failed bool, name string),
) []*N {
	type info struct {
		span       *Span
		parentName string
		failed     bool
	}
	byName := map[string]*info{}
	for _, span := range candidates {
		name := nameOf(span)
		if name == "" {
			continue
		}
		// Remember the nearest named ancestor while the shared walk decides
		// whether Boundary/Encapsulate contains this span relative to root.
		parentName := ""
		if !spanMayRollUp(span, root, func(parent *Span) {
			if pn := nameOf(parent); parentName == "" && pn != "" && pn != name {
				parentName = pn
			}
		}) {
			continue
		}
		failed := span.IsFailedOrCausedFailure()
		cur, ok := byName[name]
		switch {
		case !ok:
			byName[name] = &info{span: span, parentName: parentName, failed: failed}
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

	nodes := make(map[string]*N, len(byName))
	for name, in := range byName {
		nodes[name] = nodeOf(name, in.span, in.failed)
	}
	var roots []*N
	for name, in := range byName {
		node := nodes[name]
		if parent, ok := nodes[in.parentName]; ok && in.parentName != "" {
			ch := kids(parent)
			*ch = append(*ch, node)
		} else {
			roots = append(roots, node)
		}
	}

	var sortNodes func(ns []*N)
	sortNodes = func(ns []*N) {
		sort.SliceStable(ns, func(i, j int) bool {
			fi, ni := key(ns[i])
			fj, nj := key(ns[j])
			if fi != fj {
				return fi // failed first
			}
			return ni < nj
		})
		for _, n := range ns {
			sortNodes(*kids(n))
		}
	}
	sortNodes(roots)
	return roots
}
