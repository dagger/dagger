package dagui

import (
	"maps"
	"slices"
)

// TimeBreakdownSpans walks what a row's time breakdown draws on: its waits,
// their targets' own waits and execution twins, the deferred work it ran,
// down to waitChainMaxDepth. A fan-out row of a large trace (an HTTP request
// running hundreds of calls, waiting on hundreds of others) reaches thousands
// of spans, and a remote frontend asks again every time the row changes,
// which is once per batch while its children stream in; a local one asks of
// every row it renders, every frame.
//
// So for such a row the DB keeps the walk (a breakdownWalker with a record),
// and brings it up to date from what changed since: the walk records, for
// each step (a span walked or expanded, at a depth), the spans it read, and
// the DB logs the changes to the structure of the spans kept walks read
// (touchStructure). A change that can only add to what the walk reaches, as
// most do (children, effects or wait links added, a placeholder received, a
// span looked up turning up), re-runs the steps that read the span; anything
// else starts the walk over. The steps reach the same spans whichever order
// they run in, as each span is walked and expanded at the least depth any
// path reaches it at, so the visits come out the same as a fresh walk's, in
// another order.

// breakdownMemoMinEdges is the fewest waits, children and effects a row has
// for the DB to keep its walk: a smaller row's walk is cheaper than keeping
// it. A variable for tests, which keep every walk.
var breakdownMemoMinEdges = 64

// maxStructLog bounds the DB's log of structure changes. A kept walk that
// falls further behind starts over.
const maxStructLog = 1 << 16

// structTouchKind says how a span's structure changed.
type structTouchKind uint8

const (
	// touchEdges: children, effects or wait links were added. Only the
	// steps that read those edges can reach more.
	touchEdges structTouchKind = iota
	// touchReceived: a placeholder was received. Whatever was read of it
	// changed, but only so that a walk reaches more: it was no one's child
	// and resolved to itself, and its ancestry ended at it.
	touchReceived
	// touchFull: anything else, such as a name or parent changing, or a
	// resolution moving. A walk that read the span starts over.
	touchFull
	// touchRowWaitsGrew: the span's published waiting (RowWaits) names
	// blockers it didn't, and still those it did. Only the span's own walk
	// reads it, and only reaches more.
	touchRowWaitsGrew
	// touchRowWaitsChanged: the span's published waiting no longer names a
	// blocker it did. The span's own walk starts over.
	touchRowWaitsChanged
)

type structTouch struct {
	span *Span
	kind structTouchKind
}

// touchStructure logs a change to what TimeBreakdownSpans reads of span, if
// a walk the DB keeps read it.
func (db *DB) touchStructure(span *Span, kind structTouchKind) {
	if _, read := db.breakdownReads[span]; !read {
		return
	}
	if len(db.structLog) >= maxStructLog {
		n := len(db.structLog) / 2
		db.structLog = slices.Clone(db.structLog[n:])
		db.structLogBase += uint64(n)
	}
	db.structLog = append(db.structLog, structTouch{span: span, kind: kind})
}

// structPos is the position of the end of the structure log.
func (db *DB) structPos() uint64 {
	return db.structLogBase + uint64(len(db.structLog))
}

// spanStructure is what TimeBreakdownSpans reads of a span's own snapshot,
// captured before a new one replaces it (noteStructure).
type spanStructure struct {
	received, internal, passthrough bool
	name                            string
	parentID                        SpanID
	links                           []SpanLink
	causes, installs                int
	rowWaits                        *RowWaits
}

func (span *Span) structure() spanStructure {
	return spanStructure{
		received:    span.Received,
		internal:    span.Internal,
		passthrough: span.Passthrough,
		name:        span.Name,
		parentID:    span.ParentID,
		links:       span.Links,
		causes:      span.causesViaLinks.Len(),
		installs:    span.installSites.Len(),
		rowWaits:    span.RowWaits,
	}
}

// noteStructure logs how integrating span changed its structure from prev.
func (db *DB) noteStructure(span *Span, prev spanStructure) {
	if _, read := db.breakdownReads[span]; !read {
		return
	}
	// a remote frontend receives the waiting the DB serving it computed
	db.noteRowWaits(span, prev.rowWaits)
	if !prev.received {
		if isLazyEvaluationName(span.Name) {
			// it resolves on through its causes now
			db.touchStructure(span, touchFull)
		} else {
			db.touchStructure(span, touchReceived)
		}
		return
	}
	cur := span.structure()
	if cur.received != prev.received || cur.internal != prev.internal ||
		cur.passthrough != prev.passthrough || cur.name != prev.name ||
		cur.parentID != prev.parentID || cur.causes != prev.causes ||
		cur.installs != prev.installs ||
		len(cur.links) < len(prev.links) ||
		!slices.EqualFunc(cur.links[:len(prev.links)], prev.links, sameLinkStructure) {
		db.touchStructure(span, touchFull)
		return
	}
	if len(cur.links) > len(prev.links) {
		db.touchStructure(span, touchEdges)
	}
}

// noteRowWaits logs how the span's published waiting changed from prev, if
// it changed the blockers it names: a republish moving only the times, as
// every one of a running row's does, changes nothing the walk reads.
func (db *DB) noteRowWaits(span *Span, prev *RowWaits) {
	if span.RowWaits == prev {
		return
	}
	if _, read := db.breakdownReads[span]; !read {
		return
	}
	was, is := rowWaitsBlockers(prev), rowWaitsBlockers(span.RowWaits)
	if maps.Equal(was, is) {
		return
	}
	for id := range was {
		if _, ok := is[id]; !ok {
			db.touchStructure(span, touchRowWaitsChanged)
			return
		}
	}
	db.touchStructure(span, touchRowWaitsGrew)
}

// rowWaitsBlockers returns the blockers the waiting names, as the walk
// looks them up (breakdownWalker.rowWaits).
func rowWaitsBlockers(rw *RowWaits) map[SpanID]struct{} {
	if rw == nil {
		return nil
	}
	ids := map[SpanID]struct{}{}
	for _, seg := range rw.Segments {
		if seg.Target.IsValid() {
			ids[seg.Target] = struct{}{}
		}
		for _, b := range seg.Blockers {
			ids[b.Target] = struct{}{}
		}
	}
	return ids
}

// sameLinkStructure reports whether two links read the same to
// TimeBreakdownSpans.
func sameLinkStructure(a, b SpanLink) bool {
	return a.SpanContext.SpanID == b.SpanContext.SpanID &&
		a.Purpose == b.Purpose &&
		a.IsWait() == b.IsWait()
}

type breakdownStepKind uint8

const (
	stepWalk breakdownStepKind = iota
	stepEmit
	stepRowWaits
)

type breakdownStepKey struct {
	kind breakdownStepKind
	span *Span
}

// breakdownStep is a step of a kept walk: a span walked or expanded at the
// least depth it was reached at.
type breakdownStep struct {
	breakdownStepKey
	depth int
	runs  int32
	// walked is how far a walk step got through its span's edges.
	walked walkProgress
}

// walkProgress is how far a walk step got through its span's links,
// children and effects, so that one run again only because they grew picks
// up where it left off. Children and effects are kept in start order, so
// they grew only at the end if the last of them is where it was.
type walkProgress struct {
	links, children, effects int
	lastChild, lastEffect    *Span
}

// walkProgress returns how far a walk of the span's edges as they are gets.
func (span *Span) walkProgress() walkProgress {
	p := walkProgress{
		links:    len(span.Links),
		children: span.ChildSpans.Len(),
		effects:  span.effectsViaLinks.Len(),
	}
	if p.children > 0 {
		p.lastChild = span.ChildSpans.Spans()[p.children-1]
	}
	if p.effects > 0 {
		p.lastEffect = span.effectsViaLinks.Spans()[p.effects-1]
	}
	return p
}

// grewAtEnd reports whether s's edges are those p was taken of, plus more
// after them.
func (p walkProgress) grewAtEnd(s *Span) bool {
	children, effects := s.ChildSpans.Spans(), s.effectsViaLinks.Spans()
	return p.links <= len(s.Links) &&
		p.children <= len(children) &&
		(p.children == 0 || children[p.children-1] == p.lastChild) &&
		p.effects <= len(effects) &&
		(p.effects == 0 || effects[p.effects-1] == p.lastEffect)
}

// breakdownRecord is what a kept walk records beyond the walk's own state.
type breakdownRecord struct {
	// pos is the position in the DB's structure log the walk is up to date
	// with.
	pos uint64
	// ancestors are the row's ancestors as it was walked: what an ancestry
	// walk stops at depends on them (blockerAncestry), so the walk starts
	// over when they change.
	ancestors []*Span
	// visits lists the spans visited, in the order they were.
	visits []*Span
	steps  []breakdownStep
	index  map[breakdownStepKey]int32
	// readers lists the steps that read each span, and edgeReaders those
	// that read its children, effects or links.
	readers, edgeReaders map[*Span][]int32
	// missing holds the IDs the walk looked up and didn't find, with the
	// steps that did: one that turns up only adds to what they reach.
	missing map[SpanID][]int32
	// step is the step running.
	step int32
}

// breakdownWalker walks a row's time-breakdown spans (TimeBreakdownSpans):
// each span is walked and expanded at the least depth it's reached at, and
// visited once.
type breakdownWalker struct {
	row        *Span
	visit      func(*Span)
	seen       map[*Span]bool
	walkedAt   map[*Span]int
	expandedAt map[*Span]int
	// rec records the walk for the DB to keep; nil for a one-off walk.
	rec *breakdownRecord

	readFn, readAndVisitFn func(*Span)
}

func newBreakdownWalker(row *Span, visit func(*Span), rec *breakdownRecord) *breakdownWalker {
	w := &breakdownWalker{
		row:        row,
		visit:      visit,
		seen:       map[*Span]bool{},
		walkedAt:   map[*Span]int{},
		expandedAt: map[*Span]int{},
		rec:        rec,
	}
	if row != nil {
		w.seen[row] = true
	}
	w.readFn = w.read
	w.readAndVisitFn = func(s *Span) {
		w.read(s)
		w.visitOnce(s)
	}
	return w
}

func (w *breakdownWalker) run() {
	w.walk(w.row, 0)
	w.rowWaits()
}

func (w *breakdownWalker) visitOnce(c *Span) {
	if c == nil || w.seen[c] {
		return
	}
	w.seen[c] = true
	if w.rec != nil {
		w.rec.visits = append(w.rec.visits, c)
	}
	if w.visit != nil {
		w.visit(c)
	}
}

// enter starts a step, returning the one to go back to.
func (w *breakdownWalker) enter(kind breakdownStepKind, span *Span, depth int) int32 {
	rec := w.rec
	if rec == nil {
		return 0
	}
	key := breakdownStepKey{kind, span}
	idx, ok := rec.index[key]
	if !ok {
		idx = int32(len(rec.steps))
		rec.steps = append(rec.steps, breakdownStep{breakdownStepKey: key, depth: depth})
		rec.index[key] = idx
	} else if depth < rec.steps[idx].depth {
		rec.steps[idx].depth = depth
	}
	rec.steps[idx].runs++
	prev := rec.step
	rec.step = idx
	return prev
}

func (w *breakdownWalker) leave(prev int32) {
	if w.rec != nil {
		w.rec.step = prev
	}
}

// read records that the step running read t.
func (w *breakdownWalker) read(t *Span) {
	if w.rec == nil || t == nil {
		return
	}
	steps, ok := w.rec.readers[t]
	if !ok {
		db := w.row.db
		if db.breakdownReads == nil {
			db.breakdownReads = map[*Span]struct{}{}
		}
		db.breakdownReads[t] = struct{}{}
	}
	w.rec.readers[t] = w.rec.addStep(steps)
}

// readEdges records that the step running read t, its children, effects
// and links.
func (w *breakdownWalker) readEdges(t *Span) {
	if w.rec == nil || t == nil {
		return
	}
	w.read(t)
	w.rec.edgeReaders[t] = w.rec.addStep(w.rec.edgeReaders[t])
}

// addStep adds the step running to steps, unless it's there.
func (rec *breakdownRecord) addStep(steps []int32) []int32 {
	if n := len(steps); n > 0 && steps[n-1] == rec.step {
		return steps
	}
	// A step's first run can only have added itself while it ran, since;
	// one that ran before may be anywhere.
	if rec.steps[rec.step].runs > 1 && slices.Contains(steps, rec.step) {
		return steps
	}
	return append(steps, rec.step)
}

func (w *breakdownWalker) lookup(id SpanID) *Span {
	span := w.row.db.Spans.Map[id]
	if span == nil && w.rec != nil {
		if w.rec.missing == nil {
			w.rec.missing = map[SpanID][]int32{}
		}
		w.rec.missing[id] = w.rec.addStep(w.rec.missing[id])
	}
	return span
}

func (w *breakdownWalker) walk(s *Span, depth int) {
	if s == nil || depth > waitChainMaxDepth {
		return
	}
	if prev, ok := w.walkedAt[s]; ok && prev <= depth {
		return
	}
	w.walkedAt[s] = depth
	w.walkBody(s, depth, false)
}

// walkBody walks s's waits, execution twins and deferred work. With resume,
// it picks up where it left off last time, as only s's own edges grew since.
func (w *breakdownWalker) walkBody(s *Span, depth int, resume bool) {
	defer w.leave(w.enter(stepWalk, s, depth))
	var from walkProgress
	if w.rec != nil {
		step := &w.rec.steps[w.rec.step]
		if resume && step.walked.grewAtEnd(s) {
			from = step.walked
		}
		step.walked = s.walkProgress()
	}
	w.readEdges(s)
	for i := from.links; i < len(s.Links); i++ {
		if s.Links[i].IsWait() {
			w.emit(w.lookup(s.Links[i].SpanContext.SpanID), depth)
		}
	}
	for _, child := range s.ChildSpans.Spans()[from.children:] {
		w.read(child)
		if child.installedAt(s) {
			continue
		}
		if child.Name == s.Name {
			w.readEdges(child)
			w.emit(child, depth)
			for _, gc := range child.ChildSpans.Spans() {
				w.read(gc)
				if !gc.installedAt(child) {
					w.emit(gc, depth)
				}
			}
		} else if resolved := resolveBlockerPath(child, w.readFn); resolved != nil && resolved != child {
			// collectInferredWaits inspects non-twin children which stand
			// in for another operation, including live resume markers.
			w.emit(child, depth)
		}
	}
	for _, effect := range s.effectsViaLinks.Spans()[from.effects:] {
		w.read(effect)
		if effect.installedAt(s) {
			continue
		}
		w.readEdges(effect)
		w.emit(effect, depth)
		for _, child := range effect.ChildSpans.Spans() {
			w.emit(child, depth)
		}
	}
}

func (w *breakdownWalker) emit(c *Span, depth int) {
	if c == nil {
		return
	}
	w.visitOnce(c)
	if prev, ok := w.expandedAt[c]; ok && prev <= depth {
		return
	}
	w.expandedAt[c] = depth
	w.emitBody(c, depth)
}

func (w *breakdownWalker) emitBody(c *Span, depth int) {
	prev := w.enter(stepEmit, c, depth)
	w.read(c)
	// Cause links and the parent fallback map resume markers to the
	// operations retained in TimeSegment.Blockers. Expand those operations
	// too: their waits form the next link and their descendants carry labels.
	for _, cause := range c.causesViaLinks.Spans() {
		// Preserve the existing dependency on every direct cause, even though
		// resolveBlocker selects only the evaluated one.
		w.visitOnce(cause)
	}
	resolved := resolveBlockerPath(c, w.readAndVisitFn)
	// shownBlocker names a hidden operation by its nearest drawn
	// ancestor, so the breakdown draws on that ancestry too.
	blockerAncestry(resolved, w.row, w.readAndVisitFn)
	w.labelSpans(resolved, 0)
	w.leave(prev)
	if resolved != nil && resolved != c {
		w.emit(resolved, depth)
	}
	w.walk(c, depth+1)
}

// labelSpans visits the two child levels blockerLabel searches for exec argv
// attributes.
func (w *breakdownWalker) labelSpans(c *Span, depth int) {
	if c == nil || depth >= 2 {
		return
	}
	w.readEdges(c)
	for _, child := range c.ChildSpans.Spans() {
		w.visitOnce(child)
		w.labelSpans(child, depth+1)
	}
}

// rowWaits visits the blockers the row's published waiting names, which its
// own waits may not reach, each with its ancestors: a view draws them, and
// jumps to them, from its tree.
func (w *breakdownWalker) rowWaits() {
	defer w.leave(w.enter(stepRowWaits, w.row, 0))
	w.read(w.row)
	rw := w.row.RowWaits
	if rw == nil {
		return
	}
	visitPlaced := func(id SpanID) {
		for s, depth := w.lookup(id), 0; s != nil && depth < 100; s, depth = s.ParentSpan, depth+1 {
			w.readAndVisitFn(s)
		}
	}
	for _, seg := range rw.Segments {
		if seg.Target.IsValid() {
			visitPlaced(seg.Target)
		}
		for _, b := range seg.Blockers {
			visitPlaced(b.Target)
		}
	}
}

// rerun runs a step of a kept walk again, after something it read changed:
// only the edges of the span it walks, with edgesOnly.
func (w *breakdownWalker) rerun(idx int32, edgesOnly bool) {
	step := w.rec.steps[idx]
	switch step.kind {
	case stepWalk:
		w.walkBody(step.span, step.depth, edgesOnly)
	case stepEmit:
		w.emitBody(step.span, step.depth)
	case stepRowWaits:
		w.rowWaits()
	}
}

// sync brings a kept walk up to date with the DB, reporting false when it
// has to start over instead.
func (w *breakdownWalker) sync() bool {
	db := w.row.db
	rec := w.rec
	if rec.pos < db.structLogBase || !sameAncestors(rec.ancestors, w.row) {
		return false
	}
	var rerun []int32
	// edgesOnly holds the steps queued, each true while only the edges of
	// the span it walks changed
	var edgesOnly map[int32]bool
	queue := func(steps []int32, touched *Span, edges bool) {
		for _, idx := range steps {
			if edgesOnly == nil {
				edgesOnly = map[int32]bool{}
			}
			own := edges && rec.steps[idx].span == touched
			if prev, ok := edgesOnly[idx]; ok {
				edgesOnly[idx] = prev && own
				continue
			}
			edgesOnly[idx] = own
			rerun = append(rerun, idx)
		}
	}
	for _, touch := range db.structLog[rec.pos-db.structLogBase:] {
		switch touch.kind {
		case touchFull:
			if _, read := rec.readers[touch.span]; read {
				return false
			}
		case touchReceived:
			queue(rec.readers[touch.span], nil, false)
		case touchEdges:
			queue(rec.edgeReaders[touch.span], touch.span, true)
		case touchRowWaitsGrew, touchRowWaitsChanged:
			idx, ok := rec.index[breakdownStepKey{stepRowWaits, touch.span}]
			if !ok {
				// another row's waiting
				continue
			}
			if touch.kind == touchRowWaitsChanged {
				return false
			}
			queue([]int32{idx}, nil, false)
		}
	}
	for id, steps := range rec.missing {
		if _, ok := db.Spans.Map[id]; ok {
			queue(steps, nil, false)
			delete(rec.missing, id)
		}
	}
	rec.pos = db.structPos()
	for _, idx := range rerun {
		w.rerun(idx, edgesOnly[idx])
	}
	return true
}

// walkBreakdownOnce walks row's time-breakdown spans without keeping the
// walk. A remote frontend walks every span it sends, so the DB reuses one
// walker's storage from one to the next.
func (db *DB) walkBreakdownOnce(row *Span, visit func(*Span)) {
	if len(row.Links) == 0 && row.ChildSpans.Len() == 0 &&
		row.effectsViaLinks.Len() == 0 && row.RowWaits == nil {
		// nothing to walk
		return
	}
	w := db.breakdownScratch
	if w == nil {
		w = newBreakdownWalker(nil, nil, nil)
	}
	// a visit walking another row gets a walker of its own
	db.breakdownScratch = nil
	w.row, w.visit = row, visit
	w.seen[row] = true
	w.run()
	clear(w.seen)
	clear(w.walkedAt)
	clear(w.expandedAt)
	w.row, w.visit = nil, nil
	db.breakdownScratch = w
}

// ancestors returns span's ancestors, as far as spanInSubtree looks.
func ancestors(span *Span) []*Span {
	var out []*Span
	for s, depth := span.ParentSpan, 1; s != nil && depth < 100; s, depth = s.ParentSpan, depth+1 {
		out = append(out, s)
	}
	return out
}

// sameAncestors reports whether span's ancestors are still those.
func sameAncestors(those []*Span, span *Span) bool {
	s := span.ParentSpan
	for _, ancestor := range those {
		if s != ancestor {
			return false
		}
		s = s.ParentSpan
	}
	return s == nil || len(those) >= 99
}

// keepsBreakdownWalk reports whether the DB keeps the row's
// TimeBreakdownSpans walk.
func (span *Span) keepsBreakdownWalk() bool {
	return len(span.Links)+span.ChildSpans.Len()+span.effectsViaLinks.Len() >= breakdownMemoMinEdges
}

// breakdownSpans returns the spans the row's kept walk visits, bringing it
// up to date, or walking anew.
func (span *Span) breakdownSpans() []*Span {
	db := span.db
	if w := db.breakdownWalks[span]; w != nil && w.sync() {
		return w.rec.visits
	}
	if db.breakdownWalks == nil {
		db.breakdownWalks = map[*Span]*breakdownWalker{}
	}
	w := newBreakdownWalker(span, nil, &breakdownRecord{
		pos:         db.structPos(),
		ancestors:   ancestors(span),
		index:       map[breakdownStepKey]int32{},
		readers:     map[*Span][]int32{},
		edgeReaders: map[*Span][]int32{},
	})
	w.run()
	db.breakdownWalks[span] = w
	return w.rec.visits
}
