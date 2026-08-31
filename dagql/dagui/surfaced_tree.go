package dagui

import (
	"sort"
)

// buildSurfacedTree is the shared shape of buildSurfacedChecks and
// buildSurfacedGenerators: walk every span, keep those with a name (as told by
// nameOf) that the Boundary/Encapsulate rules let roll up to root, dedupe
// them by name preferring a failed representative, hang each under its
// nearest named ancestor, and sort failed-first then by name at every level.
//
// nodeOf constructs a node; kids and key expose its children slice and its
// (failed, name) sort key so the tree can be built without reflection.
func buildSurfacedTree[N any](
	db *DB,
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
	for span := range db.Spans.Iter() {
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
