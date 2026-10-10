package dagui

import "slices"

// spanWalker builds the TraceTrees of a RowsView.
//
// Where a span lands in the tree is decided by rules over the DB and the
// view's options alone, never by the order the walk happens to visit spans
// in: the trees built beneath a span are the same whether the walk builds
// the whole view or only that span's own path down from the top. (The
// TestWalkIsLocal property test holds the walk to that.)
//
// A span can be placed in the tree in more than one way:
//
//   - under a cause: a cause-linked span is one of its cause's ChildSpans
//     (see DB.integrateSpan and DB.linkResumedOutput), so a resumed span
//     shows beneath the call that created it;
//   - under its real parent;
//   - at the top level, when the view lists spans rather than one span's
//     children;
//   - inline: shown just before an effect of it, holding the effect, so a
//     span shows what produced its input even when that happened outside
//     the zoomed span.
//
// Each span gets exactly one home, chosen in that order of preference
// first among the places the view reaches without inlining anything (see
// reachable). Only a span with no such place is inlined, beside a single
// effect chosen by start time (see inliner), so where a cause shows never
// depends on which of its effects the walk got to first, nor on which rows
// are expanded. A span with no place of its own that isn't inlined either
// goes with its parent, wherever that went.
type spanWalker struct {
	db   *DB
	opts FrontendOpts

	// scopeRoot confines the walk to its real-parentage subtree; see
	// FrontendOpts.StrictSubtree.
	scopeRoot *Span

	// container is the span whose children are the view's top level, when
	// the view is one span's children.
	container *Span

	// roots are the view's top level, when the view lists spans instead:
	// a zoomed passthrough span's revealed spans, a root filter's spans, or
	// every span when nothing is zoomed. allRoots is set for the last.
	roots    []*Span
	rootSet  map[*Span]bool
	allRoots bool

	// modes memoizes mode for every span the walk considers, so a span
	// whose visibility depends on the clock (FrontendOpts.GCThreshold)
	// gets one answer throughout a walk.
	modes map[*Span]spanMode

	// infos memoizes placement for the spans placement rules ask about:
	// spans with cause links and the ancestors of their causes.
	infos map[*Span]*walkInfo

	// emitted is called with each tree the walk builds, parents first, and
	// whether it's a copy built for a span that reveals it (see
	// TraceTree.Revealed).
	emitted func(tree *TraceTree, revealedCopy bool)

	// descend, if set, decides whether the walk builds a tree's children;
	// without it, it builds them all.
	descend func(*TraceTree) bool

	// lazy builds only the trees the view's rows show: see DB.rowsView and
	// walk_lazy.go.
	lazy bool
	// shallow stops a lazy walk from building beneath the trees it builds,
	// for building one list on demand.
	shallow bool
	// running is, for each span the walk may place, the spans placed beneath
	// it on the way to a running span (see runningEdges); built on first use.
	running map[*Span][]runningEdge
}

// spanMode is how the walk treats a span.
type spanMode uint8

const (
	// modeDrop: the span isn't shown, and neither is anything placed
	// beneath it.
	modeDrop spanMode = iota + 1
	// modeThrough: the span isn't shown, but its children are, in its
	// place (passthrough spans, spans never received).
	modeThrough
	// modeBuild: the span is shown as a tree.
	modeBuild
)

type reachState uint8

const (
	reachUnknown reachState = iota
	reachVisiting
	reachNo
	reachYes
)

type walkInfo struct {
	// natural: whether the view reaches the span without inlining any.
	// reach: whether it reaches it at all.
	natural, reach reachState

	// naturalHost is the cause holding the span when nothing is inlined;
	// host the one holding it in the view. Nil when its parent (or the
	// top level, or an inlining effect) does.
	naturalHost, host         *Span
	naturalHostDone, hostDone bool

	// inliner is the effect beside which the span is inlined, if it is.
	inliner     *Span
	inlinerDone bool
}

// newSpanWalker sets up the walk of the view opts selects, returning the
// zoomed span, if any. It reports false when the zoomed span hasn't been
// received yet.
func (db *DB) newSpanWalker(opts FrontendOpts) (*spanWalker, *Span, bool) {
	w := &spanWalker{
		db:    db,
		opts:  opts,
		infos: map[*Span]*walkInfo{},
	}
	var zoomed *Span
	if opts.ZoomedSpan.IsValid() {
		var ok bool
		zoomed, ok = db.Spans.Map[opts.ZoomedSpan]
		if !ok {
			return nil, nil, false
		}
		if zoomed.RevealedSpans.Len() > 0 &&
			// Revealed spans bubble up all the way to the root span. By default, we
			// want to preserve the top-level context (i.e. spans immediately beneath
			// root). So, we only prioritize revealed spans if the zoomed span is also
			// marked Passthrough. That's how shell mode is able to take over the
			// top-level UI: it creates a `shell` span with `passthrough: true` and
			// zooms it.
			//
			// We could consider making this default later even for the root span.
			// Maybe it's slick to see only the intentionally revealed stuff? But you
			// probably wouldn't that for Errored spans which are auto-revealed.
			zoomed.Passthrough {
			w.setRoots(zoomed.RevealedSpans.Spans())
		} else {
			w.container = zoomed
		}
	} else {
		w.roots = db.Spans.Order
		w.allRoots = true
	}
	if opts.RootFilter != nil && (!opts.ZoomedSpan.IsValid() || opts.ZoomedSpan == db.PrimarySpan) {
		if roots := opts.RootFilter(db, zoomed); len(roots) > 0 {
			w.container = nil
			w.allRoots = false
			w.setRoots(roots)
		}
	}
	// Strict scoping: the walk root a span must descend from by real
	// parentage. See FrontendOpts.StrictSubtree -- this is what makes a scoped
	// report render exactly the root span's own subtree.
	if opts.StrictSubtree && zoomed != nil {
		w.scopeRoot = zoomed
	}
	// Walking every span considers every span, so size the memo of their
	// modes up front rather than growing it through rehashes.
	modesHint := 0
	if w.allRoots {
		modesHint = len(db.Spans.Order)
	}
	w.modes = make(map[*Span]spanMode, modesHint)
	return w, zoomed, true
}

func (w *spanWalker) info(span *Span) *walkInfo {
	in := w.infos[span]
	if in == nil {
		in = &walkInfo{}
		w.infos[span] = in
	}
	return in
}

func (w *spanWalker) inScope(span *Span) bool {
	if w.scopeRoot == nil {
		return true
	}
	for p := span; p != nil; p = p.ParentSpan {
		if p == w.scopeRoot {
			return true
		}
	}
	return false
}

func (w *spanWalker) mode(span *Span) spanMode {
	if mode, ok := w.modes[span]; ok {
		return mode
	}
	mode := w.computeMode(span)
	w.modes[span] = mode
	return mode
}

func (w *spanWalker) computeMode(span *Span) spanMode {
	// Strictly-scoped walks never leave the root's own subtree: a span
	// attached by a link (cause/effect) or inlined as a cause can live
	// anywhere in the trace.
	if !w.inScope(span) {
		return modeDrop
	}
	// Hidden spans aren't collected into the tree at all, so relationships
	// between the rows that are (e.g. chained pipeline calls) stay accurate.
	if !w.opts.ShouldShow(w.db, span) {
		return modeDrop
	}
	if (span.Passthrough && !w.opts.Debug) ||
		// We inserted a stub for this span, but never received data for it.
		// This can happen if we're within a larger trace - we'll allocate our
		// parent, but not actually see it, so just move along to its
		// children.
		!span.Received {
		return modeThrough
	}
	if w.opts.Filter != nil {
		switch w.opts.Filter(span) {
		case WalkSkip, WalkStop:
			return modeDrop
		case WalkPassthrough:
			return modeThrough
		}
	}
	return modeBuild
}

func (w *spanWalker) isRoot(span *Span) bool {
	return w.allRoots || w.rootSet[span]
}

// setRoots makes the view list spans at its top level.
func (w *spanWalker) setRoots(roots []*Span) {
	w.roots = roots
	w.rootSet = make(map[*Span]bool, len(roots))
	for _, root := range roots {
		w.rootSet[root] = true
	}
}

// reachable reports whether the view reaches span: whether some placement
// rule puts it in the tree. With viaInline false, inlined spans don't count,
// nor anything placed beneath them.
//
// This is a least fixed point over parent and cause edges, which point back
// in time; a trace can't link them into a cycle, but a cycle is guarded
// against all the same, as unreachable.
func (w *spanWalker) reachable(span *Span, viaInline bool) bool {
	if span == w.container {
		return true
	}
	if w.allRoots {
		// every span is at the top level, unless it's dropped
		return w.mode(span) != modeDrop
	}
	in := w.info(span)
	state := &in.natural
	if viaInline {
		state = &in.reach
	}
	switch *state {
	case reachYes:
		return true
	case reachNo, reachVisiting:
		return false
	}
	*state = reachVisiting
	ok := w.computeReachable(span, viaInline)
	if ok {
		*state = reachYes
	} else {
		*state = reachNo
	}
	return ok
}

func (w *spanWalker) computeReachable(span *Span, viaInline bool) bool {
	if w.mode(span) == modeDrop {
		return false
	}
	if w.isRoot(span) {
		return true
	}
	if viaInline {
		if w.reachable(span, false) {
			return true
		}
		if w.inliner(span) != nil {
			// inlined, or held by a cause inlined beside the same effect
			return true
		}
	} else {
		for _, cause := range span.causesViaLinks.Spans() {
			if w.canHost(span, cause) && w.reachable(cause, false) {
				return true
			}
		}
	}
	if parent := span.ParentSpan; parent != nil && w.reachable(parent, viaInline) {
		return true
	}
	return false
}

// canHost reports whether cause can hold span beneath it as one of its
// ChildSpans: it's shown as a tree (or is the view's container), and it is
// neither an ancestor nor a descendant of span.
func (w *spanWalker) canHost(span, cause *Span) bool {
	if cause == span {
		return false
	}
	if cause != w.container && w.mode(cause) != modeBuild {
		return false
	}
	return !span.HasParent(cause) && !cause.HasParent(span)
}

// host returns the cause holding span in the view, or nil if none does.
// That's its first cause that can hold it (see canHost) and that the view
// reaches without inlining, or that is inlined beside span's own slot: the
// effect span is, or the effect span is itself inlined beside. An inlined
// cause holds nothing else, which would move what it holds beside the
// effect, and could move the effect's own slot beneath itself.
func (w *spanWalker) host(span *Span) *Span {
	if span.causesViaLinks.Len() == 0 {
		return nil
	}
	in := w.info(span)
	if !in.hostDone {
		in.hostDone = true
		slot := span
		if !w.reachable(span, false) {
			slot = w.inliner(span)
		}
		for _, cause := range span.causesViaLinks.Spans() {
			if !w.canHost(span, cause) {
				continue
			}
			if w.reachable(cause, false) ||
				(slot != nil && w.inliner(cause) == slot) {
				in.host = cause
				break
			}
		}
	}
	return in.host
}

// naturalHost is host as it would be with nothing inlined: it decides the
// span's own slot, beside which its causes may be inlined.
func (w *spanWalker) naturalHost(span *Span) *Span {
	if span.causesViaLinks.Len() == 0 {
		return nil
	}
	in := w.info(span)
	if !in.naturalHostDone {
		in.naturalHostDone = true
		for _, cause := range span.causesViaLinks.Spans() {
			if w.canHost(span, cause) && w.reachable(cause, false) {
				in.naturalHost = cause
				break
			}
		}
	}
	return in.naturalHost
}

// inliner returns the effect beside which span is inlined, or nil if it
// isn't. A span the view reaches without inlining is never inlined.
// Otherwise it's inlined beside its first effect (by start time) that is
// shown as a tree and that the view reaches without inlining, or failing
// that, beside the same effect as its first effect that is inlined itself,
// so a chain of causes is inlined together. Never beside the zoomed span,
// which isn't in its own view, nor beside its own descendant: it would hold
// its own slot.
//
// An inlined span goes just before that effect's slot (see inlineCauses),
// unless another cause inlined there holds it (see host).
func (w *spanWalker) inliner(span *Span) *Span {
	if w.allRoots || span.effectsViaLinks.Len() == 0 {
		return nil
	}
	in := w.info(span)
	if in.inlinerDone {
		return in.inliner
	}
	// guards against cycles of cause links, which a trace can't form
	in.inlinerDone = true
	if w.mode(span) == modeDrop || w.reachable(span, false) {
		return nil
	}
	for _, effect := range span.effectsViaLinks.Spans() {
		if effect == w.container || w.mode(effect) != modeBuild ||
			effect.HasParent(span) || span.HasParent(effect) {
			continue
		}
		slot := effect
		if !w.reachable(effect, false) {
			slot = w.inliner(effect)
		}
		if slot != nil && !slot.HasParent(span) {
			in.inliner = slot
			break
		}
	}
	return in.inliner
}

// parentHolds reports whether span's parent holds it, provided no cause
// does.
//
// A span the view reaches without inlining stays where it would be without
// any, so its parent holds it only if the view reaches the parent without
// inlining too. An inlined span stays beside its effect, apart from its
// parent. Only a span with neither goes with its parent, wherever inlining
// put that. So an inlined span never pulls in a span with a place of its
// own, which could be the very slot it's inlined beside.
func (w *spanWalker) parentHolds(span *Span) bool {
	parent := span.ParentSpan
	if parent == nil {
		return false
	}
	if w.reachable(span, false) {
		return w.reachable(parent, false)
	}
	return w.inliner(span) == nil && w.reachable(parent, true)
}

// homeIsParent reports whether span's home is under its parent, which the
// walk has placed.
func (w *spanWalker) homeIsParent(span, parent *Span) bool {
	if w.allRoots {
		// every span the walk places is reached without inlining
		return w.host(span) == nil
	}
	if span.causesViaLinks.Len() == 0 && span.effectsViaLinks.Len() == 0 {
		// no cause holds it, and it's not inlined; it has a place of its
		// own only at the top level
		return !w.isRoot(span) || w.reachable(parent, false)
	}
	return w.host(span) == nil && w.parentHolds(span)
}

// homeIsRoot reports whether span's home is the top level.
func (w *spanWalker) homeIsRoot(span *Span) bool {
	return w.isRoot(span) && w.host(span) == nil && !w.parentHolds(span)
}

// treeList collects one sibling list of trees.
type treeList struct {
	parent *TraceTree
	trees  []*TraceTree
	// lastCall is the last call tree in the list, for Chained.
	lastCall *TraceTree
	// revealed is set within revealed copies.
	revealed bool
	// probe makes the list build nothing, only note in found whether it
	// would hold any tree.
	probe, found bool
}

// walk builds the view's top-level trees.
func (w *spanWalker) walk() []*TraceTree {
	top := &treeList{}
	if w.container != nil {
		w.fill(top, w.container)
		return top.trees
	}
	for _, span := range w.roots {
		if span.causesViaLinks.Len() > 0 && w.slotAtRoot(span) {
			w.inlineCauses(top, span)
		}
		if w.homeIsRoot(span) {
			w.emit(top, span)
		}
	}
	return top.trees
}

// slotAtRoot reports whether span's own slot (see inliner) is at the top
// level, so its inlined causes go there.
func (w *spanWalker) slotAtRoot(span *Span) bool {
	return w.naturalHost(span) == nil &&
		!w.parentHolds(span) &&
		w.isRoot(span)
}

// fill places the spans held by span into list: its children, and its
// effects that it hosts, each preceded by any causes inlined beside it.
func (w *spanWalker) fill(list *treeList, span *Span) {
	for _, child := range span.ChildSpans.Spans() {
		if list.found {
			return
		}
		if child == w.container {
			// reached when an ancestor of the zoomed span is inlined; what it
			// holds is the top level
			continue
		}
		if child.ParentSpan == span {
			// the child's slot is here if no cause holds it naturally, and
			// the view reaches this span without inlining
			if child.causesViaLinks.Len() > 0 &&
				w.naturalHost(child) == nil && w.reachable(span, false) {
				w.inlineCauses(list, child)
			}
			if w.homeIsParent(child, span) {
				w.emit(list, child)
			}
		} else {
			// span is one of the child's causes
			if w.naturalHost(child) == span {
				w.inlineCauses(list, child)
			}
			if w.host(child) == span {
				w.emit(list, child)
			}
		}
	}
}

// inlineCauses places the causes inlined beside effect into list: its own,
// and those of its causes inlined beside it too, depth first.
func (w *spanWalker) inlineCauses(list *treeList, effect *Span) {
	var visited []*Span
	w.inlineCausesOf(list, effect, effect, &visited)
}

func (w *spanWalker) inlineCausesOf(list *treeList, slot, span *Span, visited *[]*Span) {
	for _, cause := range span.causesViaLinks.Spans() {
		if list.found {
			return
		}
		if w.inliner(cause) != slot ||
			// a diamond of links reaches a cause more than once
			slices.Contains(*visited, cause) {
			continue
		}
		*visited = append(*visited, cause)
		if w.host(cause) == nil {
			w.emit(list, cause)
		}
		w.inlineCausesOf(list, slot, cause, visited)
	}
}

// emit places span into list: as a tree, or by placing what it holds in
// its stead.
func (w *spanWalker) emit(list *treeList, span *Span) {
	switch w.mode(span) {
	case modeDrop:
		return
	case modeThrough:
		w.fill(list, span)
		return
	}
	if list.probe {
		list.found = true
		return
	}
	tree := &TraceTree{
		Span:             span,
		Parent:           list.parent,
		RevealedChildren: span.RevealedSpans.Len() > 0,
		revealedCopy:     list.revealed,
	}
	// A call chains onto the call before it in the same sibling list.
	if prev := list.lastCall; prev != nil {
		if base := span.Base(); base != nil {
			tree.Chained = base.Digest == prev.Span.CallDigest ||
				base.Digest == prev.Span.Output
		}
	}
	list.trees = append(list.trees, tree)
	if span.CallDigest != "" {
		list.lastCall = tree
	}
	if w.emitted != nil {
		w.emitted(tree, list.revealed)
	}
	if w.lazy {
		w.emitLazy(tree)
		return
	}
	if w.descend != nil && !w.descend(tree) {
		tree.IsRunningOrChildRunning = span.IsRunningOrEffectsRunning()
		return
	}

	w.buildChildren(tree)
	w.buildRevealed(tree)

	running := span.IsRunningOrEffectsRunning()
	for _, child := range tree.Children {
		if running {
			break
		}
		running = child.IsRunningOrChildRunning
	}
	for _, child := range tree.Revealed {
		if running {
			break
		}
		running = child.IsRunningOrChildRunning
	}
	tree.IsRunningOrChildRunning = running
}

// buildChildren builds the trees of the spans placed beneath tree's span.
func (w *spanWalker) buildChildren(tree *TraceTree) {
	tree.childrenBuilt = true
	if tree.revealedCopy && tree.ShouldShowRevealedSpans(w.opts) {
		// Within a revealed copy, a span showing its own revealed spans
		// doesn't need its children too: only rows show the copy, and they
		// show the revealed spans instead. (Elsewhere they're the home trees
		// of the spans beneath, which BySpan and a full-tree reader want.)
		return
	}
	children := treeList{parent: tree, revealed: tree.revealedCopy}
	w.fill(&children, tree.Span)
	tree.Children = children.trees
}

// buildRevealed builds copies of the span's revealed spans beneath tree, if
// it shows them in place of its children.
func (w *spanWalker) buildRevealed(tree *TraceTree) {
	tree.revealedBuilt = true
	if !tree.ShouldShowRevealedSpans(w.opts) {
		return
	}
	revealed := treeList{parent: tree, revealed: true}
	for _, span := range tree.Span.RevealedSpans.Spans() {
		if w.mode(span) == modeBuild && w.reachable(span, true) &&
			!revealedBy(tree, span) {
			w.emit(&revealed, span)
		}
	}
	tree.Revealed = revealed.trees
}

// revealedBy reports whether span is tree's span or an ancestor's, which
// would reveal itself without end.
func revealedBy(tree *TraceTree, span *Span) bool {
	for at := tree; at != nil; at = at.Parent {
		if at.Span == span {
			return true
		}
	}
	return false
}
