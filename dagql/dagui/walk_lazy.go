package dagui

// A lazy walk (see DB.rowsView) builds only the trees a view's rows show: the
// top level, and the lists beneath trees shown expanded. It builds the rest
// on demand, a list at a time (TraceTree.ChildTrees, RevealedTrees), or down
// the path to a span's home (RowsView.HomeTree).
//
// That's sound because placement is local (see spanWalker): the list built
// beneath a tree is the same whatever else is built. What a collapsed tree
// reports about its subtree, without building it, is worked out from the DB
// instead, to the same answer:
//
//   - whether it has children: a probe of the list beneath it, which stops
//     at the first tree it would build (hasChildren);
//   - whether anything beneath it is running: the trees on the way down to
//     each running span, found by walking up from it (runningEdges).

// emitLazy finishes a tree a lazy walk built, building beneath it if Rows
// will show it expanded.
func (w *spanWalker) emitLazy(tree *TraceTree) {
	tree.lazy = w
	tree.IsRunningOrChildRunning = w.runningBeneath(tree)
	if w.shallow || !tree.IsExpanded(w.opts) {
		return
	}
	if tree.ShouldShowRevealedSpans(w.opts) {
		w.buildRevealed(tree)
	} else {
		w.buildChildren(tree)
	}
}

// buildOnDemand builds one of a lazily built tree's lists: its Revealed
// copies, or its Children. It builds nothing beneath them.
func (w *spanWalker) buildOnDemand(tree *TraceTree, revealed bool) {
	shallow := w.shallow
	w.shallow = true
	defer func() { w.shallow = shallow }()
	if revealed {
		w.buildRevealed(tree)
	} else {
		w.buildChildren(tree)
	}
}

// hasChildren reports whether buildChildren would build any tree beneath
// tree, without building it.
func (w *spanWalker) hasChildren(tree *TraceTree) bool {
	if tree.revealedCopy && tree.ShouldShowRevealedSpans(w.opts) {
		return false
	}
	probe := treeList{probe: true}
	w.fill(&probe, tree.Span)
	return probe.found
}

// owner returns the span whose trees hold span's home among their Children,
// or nil if the top level does. It reports false if the view doesn't place
// span.
//
// It follows the placement rules emit is called by (see walk, fill and
// inlineCauses) backwards, one step up: past spans the walk goes through
// (passthrough spans, spans never received) to the tree holding them.
func (w *spanWalker) owner(span *Span) (*Span, bool) {
	if span == w.container || w.mode(span) == modeDrop || !w.reachable(span, true) {
		return nil, false
	}
	if host := w.host(span); host != nil {
		return w.fillOwner(host)
	}
	if w.reachable(span, false) {
		if w.parentHolds(span) {
			return w.fillOwner(span.ParentSpan)
		}
		return nil, w.isRoot(span)
	}
	if slot := w.inliner(span); slot != nil {
		return w.slotOwner(slot)
	}
	if w.parentHolds(span) {
		return w.fillOwner(span.ParentSpan)
	}
	return nil, false
}

// fillOwner returns the span whose trees hold what fill places for span.
func (w *spanWalker) fillOwner(span *Span) (*Span, bool) {
	if span == w.container {
		return nil, true
	}
	switch w.mode(span) {
	case modeBuild:
		return span, w.reachable(span, true)
	case modeThrough:
		return w.owner(span)
	}
	return nil, false
}

// slotOwner returns the span whose trees hold effect's own slot, which the
// causes inlined beside it go before (see inlineCauses).
func (w *spanWalker) slotOwner(effect *Span) (*Span, bool) {
	if host := w.naturalHost(effect); host != nil {
		return w.fillOwner(host)
	}
	if parent := effect.ParentSpan; parent != nil && w.reachable(parent, false) {
		return w.fillOwner(parent)
	}
	return nil, w.isRoot(effect)
}

// homeTree returns span's home tree, building the lists on the way down to
// it from body.
func (w *spanWalker) homeTree(body []*TraceTree, span *Span) *TraceTree {
	if w.mode(span) != modeBuild {
		return nil
	}
	// span and its owners, innermost first
	var path []*Span
	for at := span; at != nil; {
		if len(path) > len(w.db.Spans.Order) {
			// guards against a cycle, which placement can't form
			return nil
		}
		path = append(path, at)
		owner, ok := w.owner(at)
		if !ok {
			return nil
		}
		at = owner
	}
	trees := body
	var tree *TraceTree
	for i := len(path) - 1; i >= 0; i-- {
		tree = nil
		for _, candidate := range trees {
			if candidate.Span == path[i] && !candidate.revealedCopy {
				tree = candidate
				break
			}
		}
		if tree == nil {
			return nil
		}
		if i > 0 {
			trees = tree.ChildTrees()
		}
	}
	return tree
}

// runningEdge is a step down from a span's trees toward a running span: to
// the home tree of a span its trees hold, or to a copy of a span it reveals.
type runningEdge struct {
	span     *Span
	revealed bool
}

// runningEdges returns, for each span, the steps down from its trees that
// lead to a running span's tree: every way down to one, and nothing else.
//
// It walks up from each running span the view places (see DB.runningSpans):
// to the owner of its home (see owner), and to each span that reveals it, and
// so on up.
func (w *spanWalker) runningEdges() map[*Span][]runningEdge {
	if w.running != nil {
		return w.running
	}
	w.running = map[*Span][]runningEdge{}
	marked := map[*Span]bool{}
	var mark func(*Span)
	mark = func(span *Span) {
		if marked[span] {
			return
		}
		marked[span] = true
		if w.mode(span) != modeBuild || !w.reachable(span, true) {
			// no tree of its own
			return
		}
		if span != w.container {
			if owner, ok := w.owner(span); ok {
				if owner == nil {
					// The top level is the container's children, which
					// copies of the container hold too; nothing holds the
					// spans the view lists instead.
					owner = w.container
				}
				if owner != nil {
					w.running[owner] = append(w.running[owner], runningEdge{span: span})
					mark(owner)
				}
			}
		}
		for _, revealer := range w.db.revealersOf(span) {
			if w.mode(revealer) != modeBuild || !showsRevealedSpans(revealer, w.opts) {
				continue
			}
			w.running[revealer] = append(w.running[revealer], runningEdge{span: span, revealed: true})
			mark(revealer)
		}
	}
	for span := range w.db.runningSpans {
		if span.IsRunningOrEffectsRunning() {
			mark(span)
		}
	}
	return w.running
}

// runningBeneath is IsRunningOrChildRunning for a tree: whether its span or
// any tree beneath it, built or not, is running.
func (w *spanWalker) runningBeneath(tree *TraceTree) bool {
	return w.runningAt(tree.Span, tree.revealedCopy, &runningPath{span: tree.Span, tree: tree})
}

// runningPath is the path down to a tree runningAt considers, which a
// revealed span on it isn't copied beneath again (see revealedBy).
type runningPath struct {
	span *Span
	up   *runningPath
	// tree is the built tree the path starts from, whose ancestors carry
	// on the path up.
	tree *TraceTree
}

func (path *runningPath) has(span *Span) bool {
	for at := path; at != nil; at = at.up {
		if at.tree != nil {
			return revealedBy(at.tree, span)
		}
		if at.span == span {
			return true
		}
	}
	return false
}

// runningAt reports whether a tree of span at the end of path, a revealed
// copy or beneath one if revealedCopy is set, is running or holds one that
// is, following the trees emit would build beneath it (see buildChildren and
// buildRevealed).
func (w *spanWalker) runningAt(span *Span, revealedCopy bool, path *runningPath) bool {
	if span.IsRunningOrEffectsRunning() {
		return true
	}
	edges := w.runningEdges()[span]
	if len(edges) == 0 {
		return false
	}
	showsRevealed := span.RevealedSpans.Len() > 0 && showsRevealedSpans(span, w.opts)
	fills := !revealedCopy || !showsRevealed
	for _, edge := range edges {
		if edge.revealed {
			if !showsRevealed || path.has(edge.span) {
				continue
			}
			if w.runningAt(edge.span, true, &runningPath{span: edge.span, up: path}) {
				return true
			}
		} else if fills {
			if w.runningAt(edge.span, revealedCopy, &runningPath{span: edge.span, up: path}) {
				return true
			}
		}
	}
	return false
}

// noteRunning brings db.runningSpans up to date with a span whose running
// state may have changed.
func (db *DB) noteRunning(span *Span) {
	if span.IsRunningOrEffectsRunning() {
		if db.runningSpans == nil {
			db.runningSpans = map[*Span]struct{}{}
		}
		db.runningSpans[span] = struct{}{}
	} else {
		delete(db.runningSpans, span)
	}
}

// revealersOf returns the spans that reveal span (see Span.RevealedSpans).
func (db *DB) revealersOf(span *Span) []*Span {
	return db.revealers[span].Spans()
}

// noteRevealed brings db.revealers up to date with a change to revealer's
// RevealedSpans.
func (db *DB) noteRevealed(revealer, revealed *Span, added bool) {
	set := db.revealers[revealed]
	if added {
		if set == nil {
			if db.revealers == nil {
				db.revealers = map[*Span]*SpanSet{}
			}
			set = &SpanSet{}
			db.revealers[revealed] = set
		}
		set.add(revealer)
	} else if set.remove(revealer) && set.Len() == 0 {
		delete(db.revealers, revealed)
	}
}
