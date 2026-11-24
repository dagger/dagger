package clientdb

import (
	"bytes"
	"encoding/binary"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
)

// spanLookup indexes a store's span rows, answering a span's place in the
// trace (parent, children, cause links), its newest and first snapshot rows,
// and the spans marked as checks, tests and agent loops, from memory.
//
// It holds every span of a session for the session's life, so it keeps each
// span ID once: interned (see idTable) as a dense uint32, which indexes
// the per-span slices below. A string-keyed map per relation kept a span's ID
// in up to eight maps, at ~400 B per span.
type spanLookup struct {
	mu sync.RWMutex
	// gen counts the batches of span rows added. Readers that memoize an
	// answer that more spans or links could change (see place) revalidate it
	// when gen moves. Written under mu; read without it.
	gen atomic.Uint64

	ids idTable
	// Interned trace IDs, for firstRow's trace + span key.
	traceIDs   map[string]uint32
	traceNames []string

	// Per span ID, indexed by its interned ID; grown together by grow.
	//
	// lastRow is each span's NEWEST snapshot row, 0 for none. OTel span
	// snapshots are cumulative -- a later row carries the span's full
	// current state -- so the last row alone reconstructs the state that
	// sequential processing of every snapshot would end with. This is what
	// lets a scoped reader materialize a span subtree without reprocessing
	// the whole stream.
	lastRow []int64
	// firstRow is the first row of the span in firstTrace (the first trace
	// it was seen in); otherFirstRows holds its first row in any other
	// trace. The trace + span key preserves SelectSpan's predicate.
	firstRow       []int64
	firstTrace     []uint32
	otherFirstRows map[spanTraceKey]int64
	// parent is each span's parent (its ID + 1, 0 for none), from the first
	// snapshot that carries one. Together with the children it makes
	// ancestor chains answerable from the index alone.
	parent []uint32
	// parentOf inverts parent exactly, as a list per parent threaded
	// through the spans (head, tail, next): unlike children, which follows
	// each span's first snapshot per trace, it lists a span under whichever
	// parent parent holds for it, so that walking it down finds exactly the
	// spans place finds walking up (see closeBeneath).
	parentOfHead, parentOfTail, parentOfNext []uint32
	// children lists, per parent, the spans whose first snapshot in a trace
	// names it, once each, threaded the same way. A span is on at most one
	// such list by childOf (its parent + 1); the rare second parent, of the
	// same span ID in another trace, keeps it in extraChildren.
	childHead, childTail, childNext, childOf []uint32
	extraChildren                            map[uint32][]uint32
	// marks holds markCheck, markTest and markAgent: spans whose attributes
	// mark them as named checks or tests (telemetry.CheckNameAttr, semconv
	// test.case.name / test.suite.name) or agent loops (telemetryattrs
	// AgentIDAttr). Detected with a raw byte scan of the encoded attributes
	// -- no decode -- so check/test span selection answers from the index
	// instead of scanning the stream. A false positive (marker bytes inside a
	// value) merely loads one extra span. The lists hold the marked spans in
	// marking order.
	marks                         []uint8
	checkSpans, testSpans, agents []uint32

	// arrivals lists, in order, the spans whose snapshot could place them
	// somewhere new: a span's first snapshot, and one that gives it a parent
	// or more cause links. A reader that placed spans as of some length of it
	// need only place the arrivals since (see placeArrivals).
	arrivals []uint32

	// The cause-purpose span links index both ways. dagui treats a cause
	// link as a parent→child edge (the linking span joins the linked span's
	// ChildSpans; see dagql/dagui/db.go), so the descendant walk follows them
	// from linked (target) span to linking span — e.g. a service's
	// long-lived exec span cause-links to the API spans that installed the
	// Service value (Container.asService and friends), and the service's
	// logs belong beneath them. The log-scope walk also seeds from the other
	// end, linking span to target: the service's stdio log records are
	// deliberately attributed to those install spans (core/service.go routes
	// them there), so reading logs "beneath" the exec span must reach the
	// install spans' rows too.
	//
	// Unlike children, links are fed from EVERY snapshot row of a span, not
	// just the first: links are added to live spans over time
	// (RunningService.addOriginSpanContexts), so later snapshots carry edges
	// the first row lacked.
	//
	// Many spans carry the same set of cause links — every lazy evaluation
	// of a result links the API spans that installed it — so each distinct
	// set is stored once, with the spans that carry it. The index grows with
	// the spans and the distinct sets, not with the links: thousands of
	// spans sharing hundreds of links cost thousands of entries, not
	// millions.
	linkSets   []causalLinkSet
	linkSetIDs map[string]uint32
	// spanLinkSet maps a linking span to the set it carries.
	spanLinkSet map[uint32]uint32
	// targetLinkSets maps a linked span to the sets that contain it.
	targetLinkSets map[uint32][]uint32
	// freeLinkSets are the slots of sets emptied by spans whose links grew,
	// for reuse.
	freeLinkSets []uint32
}

// Span marks.
const (
	markCheck uint8 = 1 << iota
	markTest
	markAgent
)

// spanTraceKey is a span in a trace, by interned IDs.
type spanTraceKey struct {
	trace, span uint32
}

// causalLinkSet is one distinct set of cause-link targets and the spans
// whose links are exactly that set.
type causalLinkSet struct {
	targets []uint32 // sorted
	members []uint32
}

func newSpanLookup() *spanLookup {
	return &spanLookup{
		traceIDs:       make(map[string]uint32),
		otherFirstRows: make(map[spanTraceKey]int64),
		extraChildren:  make(map[uint32][]uint32),
		linkSetIDs:     make(map[string]uint32),
		spanLinkSet:    make(map[uint32]uint32),
		targetLinkSets: make(map[uint32][]uint32),
	}
}

// internLocked interns id, growing the per-span slices to hold it.
func (l *spanLookup) internLocked(id string) uint32 {
	n := l.ids.intern(id)
	if int(n) >= len(l.lastRow) {
		l.lastRow = append(l.lastRow, 0)
		l.firstRow = append(l.firstRow, 0)
		l.firstTrace = append(l.firstTrace, 0)
		l.parent = append(l.parent, 0)
		l.parentOfHead = append(l.parentOfHead, 0)
		l.parentOfTail = append(l.parentOfTail, 0)
		l.parentOfNext = append(l.parentOfNext, 0)
		l.childHead = append(l.childHead, 0)
		l.childTail = append(l.childTail, 0)
		l.childNext = append(l.childNext, 0)
		l.childOf = append(l.childOf, 0)
		l.marks = append(l.marks, 0)
	}
	return n
}

// lookupLocked returns id's interned ID, if any row or edge named it.
func (l *spanLookup) lookupLocked(id string) (uint32, bool) {
	return l.ids.lookup(id)
}

func (l *spanLookup) traceLocked(traceID string) uint32 {
	if n, ok := l.traceIDs[traceID]; ok {
		return n
	}
	n := uint32(len(l.traceNames))
	l.traceIDs[traceID] = n
	l.traceNames = append(l.traceNames, traceID)
	return n
}

func (l *spanLookup) addAll(rows []Span) {
	targets := make([][]string, len(rows))
	for i, row := range rows {
		targets[i] = causalLinkTargets(row)
	}
	l.mu.Lock()
	for i, row := range rows {
		l.addLocked(row, targets[i])
	}
	l.gen.Add(1)
	l.mu.Unlock()
}

// generation is the current gen.
func (l *spanLookup) generation() uint64 { return l.gen.Load() }

func (l *spanLookup) addLocked(row Span, causalTargets []string) {
	span := l.internLocked(row.SpanID)
	// Link edges are indexed before (and regardless of) the duplicate-snapshot
	// suppression below: a later snapshot of an already-seen span may carry
	// links its first row lacked.
	linked := l.addCauseLinksLocked(span, causalTargets)
	// Likewise per-row, not per-span: the newest snapshot is the span's
	// current state, and check/test markers may only appear on a snapshot
	// taken after the attribute was set.
	seen := l.lastRow[span] != 0
	l.lastRow[span] = row.ID
	l.markLocked(span, row.Attributes)
	var parentKnown bool
	var parent uint32
	if row.ParentSpanID.Valid {
		parent = l.internLocked(row.ParentSpanID.String)
		parentKnown = l.parent[span] != 0
		if !parentKnown {
			l.parent[span] = parent + 1
			l.appendList(l.parentOfHead, l.parentOfTail, l.parentOfNext, parent, span)
		}
	}
	if !seen || linked || (row.ParentSpanID.Valid && !parentKnown) {
		l.arrivals = append(l.arrivals, span)
	}
	trace := l.traceLocked(row.TraceID)
	if !l.markFirstLocked(trace, span, row.ID) {
		return
	}
	if row.ParentSpanID.Valid {
		// New span IDs cannot already be in a child list. Only a repeated
		// ID in another trace needs the span-ID-only uniqueness check.
		if parentKnown && l.isChildLocked(parent, span) {
			return
		}
		if l.childOf[span] == 0 {
			l.childOf[span] = parent + 1
			l.appendList(l.childHead, l.childTail, l.childNext, parent, span)
		} else {
			l.extraChildren[parent] = append(l.extraChildren[parent], span)
		}
	}
}

// markLocked records the marks attrs carry for span.
func (l *spanLookup) markLocked(span uint32, attrs []byte) {
	marks := l.marks[span]
	if marks&markCheck == 0 && bytes.Contains(attrs, checkNameAttrMarker) {
		marks |= markCheck
		l.checkSpans = append(l.checkSpans, span)
	}
	if marks&markTest == 0 &&
		(bytes.Contains(attrs, testCaseNameAttrMarker) || bytes.Contains(attrs, testSuiteNameAttrMarker)) {
		marks |= markTest
		l.testSpans = append(l.testSpans, span)
	}
	if marks&markAgent == 0 && bytes.Contains(attrs, agentIDAttrMarker) {
		marks |= markAgent
		l.agents = append(l.agents, span)
	}
	l.marks[span] = marks
}

// markFirstLocked records row as span's first in trace, reporting false if
// it already had one there.
func (l *spanLookup) markFirstLocked(trace, span uint32, row int64) bool {
	switch {
	case l.firstRow[span] == 0:
		l.firstRow[span], l.firstTrace[span] = row, trace
		return true
	case l.firstTrace[span] == trace:
		return false
	}
	key := spanTraceKey{trace: trace, span: span}
	if _, exists := l.otherFirstRows[key]; exists {
		return false
	}
	l.otherFirstRows[key] = row
	return true
}

// appendList appends span to parent's list threaded through head, tail and
// next (IDs + 1, 0 for none).
func (l *spanLookup) appendList(head, tail, next []uint32, parent, span uint32) {
	if tail[parent] == 0 {
		head[parent] = span + 1
	} else {
		next[tail[parent]-1] = span + 1
	}
	tail[parent] = span + 1
}

// eachInList calls f with each span on parent's list, in order, until f
// returns false.
func eachInList(head, next []uint32, parent uint32, f func(uint32) bool) bool {
	for n := head[parent]; n != 0; n = next[n-1] {
		if !f(n - 1) {
			return false
		}
	}
	return true
}

// eachChildLocked calls f with each of parent's children until f returns
// false: those first added under it in the order they were, then those it
// gained as the same span ID in another trace. Every caller treats the
// children as a set.
func (l *spanLookup) eachChildLocked(parent uint32, f func(uint32) bool) {
	if !eachInList(l.childHead, l.childNext, parent, f) {
		return
	}
	for _, kid := range l.extraChildren[parent] {
		if !f(kid) {
			return
		}
	}
}

// isChildLocked reports whether span is on parent's children list.
func (l *spanLookup) isChildLocked(parent, span uint32) bool {
	if l.childOf[span] == parent+1 {
		return true
	}
	return slices.Contains(l.extraChildren[parent], span)
}

// childrenLocked lists parent's children by name, in eachChildLocked's
// order.
func (l *spanLookup) childrenLocked(parent string) []string {
	n, ok := l.lookupLocked(parent)
	if !ok {
		return nil
	}
	var kids []string
	l.eachChildLocked(n, func(kid uint32) bool {
		kids = append(kids, l.ids.name(kid))
		return true
	})
	return kids
}

// parentLocked returns span's parent, if it has one.
func (l *spanLookup) parentLocked(span uint32) (uint32, bool) {
	p := l.parent[span]
	return p - 1, p != 0
}

// addCauseLinksLocked adds targets to the set of spans span cause-links,
// reporting whether the set grew.
// A span keeps one set: links that grow it move the span to the larger set.
func (l *spanLookup) addCauseLinksLocked(span uint32, targets []string) bool {
	if len(targets) == 0 {
		return false
	}
	old, linked := l.spanLinkSet[span]
	var current []uint32
	if linked {
		current = l.linkSets[old].targets
	}
	next := current
	for _, target := range targets {
		id := l.internLocked(target)
		i, found := slices.BinarySearch(next, id)
		if found {
			continue
		}
		if len(next) == len(current) {
			next = slices.Clone(current)
		}
		next = slices.Insert(next, i, id)
	}
	if linked && len(next) == len(current) {
		return false
	}
	if linked {
		members := l.linkSets[old].members
		i := slices.Index(members, span)
		l.linkSets[old].members = slices.Delete(members, i, i+1)
		if len(l.linkSets[old].members) == 0 {
			l.dropLinkSetLocked(old)
		}
	}
	set := l.linkSetLocked(next)
	l.linkSets[set].members = append(l.linkSets[set].members, span)
	l.spanLinkSet[span] = set
	return true
}

// linkSetLocked returns the set with exactly targets (sorted), adding it.
func (l *spanLookup) linkSetLocked(targets []uint32) uint32 {
	key := linkSetKey(targets)
	if set, ok := l.linkSetIDs[key]; ok {
		return set
	}
	var set uint32
	if n := len(l.freeLinkSets); n > 0 {
		set = l.freeLinkSets[n-1]
		l.freeLinkSets = l.freeLinkSets[:n-1]
		l.linkSets[set] = causalLinkSet{targets: targets}
	} else {
		set = uint32(len(l.linkSets))
		l.linkSets = append(l.linkSets, causalLinkSet{targets: targets})
	}
	l.linkSetIDs[key] = set
	for _, target := range targets {
		l.targetLinkSets[target] = append(l.targetLinkSets[target], set)
	}
	return set
}

// dropLinkSetLocked removes a set no span carries any more, so that a span
// whose links grow leaves nothing behind.
func (l *spanLookup) dropLinkSetLocked(set uint32) {
	targets := l.linkSets[set].targets
	delete(l.linkSetIDs, linkSetKey(targets))
	for _, target := range targets {
		sets := l.targetLinkSets[target]
		i := slices.Index(sets, set)
		if sets = slices.Delete(sets, i, i+1); len(sets) == 0 {
			delete(l.targetLinkSets, target)
		} else {
			l.targetLinkSets[target] = sets
		}
	}
	l.linkSets[set] = causalLinkSet{}
	l.freeLinkSets = append(l.freeLinkSets, set)
}

func linkSetKey(targets []uint32) string {
	key := make([]byte, 0, 4*len(targets))
	for _, id := range targets {
		key = binary.LittleEndian.AppendUint32(key, id)
	}
	return string(key)
}

// causalChildrenLocked calls f with each span that cause-links to target.
func (l *spanLookup) causalChildrenLocked(target uint32, f func(uint32) bool) {
	for _, set := range l.targetLinkSets[target] {
		for _, member := range l.linkSets[set].members {
			if !f(member) {
				return
			}
		}
	}
}

// causalParentsLocked returns the spans span cause-links to.
func (l *spanLookup) causalParentsLocked(span uint32) []uint32 {
	set, ok := l.spanLinkSet[span]
	if !ok {
		return nil
	}
	return l.linkSets[set].targets
}

func (l *spanLookup) first(traceID, spanID string) (int64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	span, ok := l.lookupLocked(spanID)
	if !ok || l.firstRow[span] == 0 {
		return 0, false
	}
	trace, ok := l.traceIDs[traceID]
	if !ok {
		return 0, false
	}
	if l.firstTrace[span] == trace {
		return l.firstRow[span], true
	}
	row, found := l.otherFirstRows[spanTraceKey{trace: trace, span: span}]
	return row, found
}

// causalChildrenOf returns the span IDs that cause-link to the given span, in
// no particular order.
func (l *spanLookup) causalChildrenOf(spanID string) []string {
	var kids []string
	l.mu.RLock()
	defer l.mu.RUnlock()
	if target, ok := l.lookupLocked(spanID); ok {
		l.causalChildrenLocked(target, func(kid uint32) bool {
			kids = append(kids, l.ids.name(kid))
			return true
		})
	}
	return kids
}

// causalParentsOf returns the span IDs the given span cause-links to.
func (l *spanLookup) causalParentsOf(spanID string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	span, ok := l.lookupLocked(spanID)
	if !ok {
		return nil
	}
	targets := l.causalParentsLocked(span)
	if targets == nil {
		return nil
	}
	parents := make([]string, len(targets))
	for i, target := range targets {
		parents[i] = l.ids.name(target)
	}
	return parents
}

// hasDescendants reports whether root has anything nested beneath it, over
// the downward edges logScope walks (child edges and cause-purpose link
// edges). It only ever inspects root's own direct edges -- a span with any
// child at all has a descendant -- so it is O(direct children) and never
// materializes the subtree. Note it deliberately ignores logScope's reverse
// seeds: a service exec span's install spans sit beside it, not beneath it,
// and don't make it "have descendants".
func (l *spanLookup) hasDescendants(root string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n, ok := l.lookupLocked(root)
	if !ok {
		return false
	}
	descendant := false
	// A malformed self-parented row would otherwise report itself as its
	// own descendant.
	l.eachChildLocked(n, func(kid uint32) bool {
		descendant = kid != n
		return !descendant
	})
	if descendant {
		return true
	}
	l.causalChildrenLocked(n, func(kid uint32) bool {
		descendant = kid != n
		return !descendant
	})
	return descendant
}

// logScope returns every span whose log rows belong to a capture rooted at
// root: root itself, the spans root cause-links to (its containing spans in
// dagui's model — e.g. the install spans a service exec span links, which
// carry the service's stdio records; see causalParents), and everything
// reachable from those seeds via child edges and cause-purpose link edges —
// the same containment dagui renders, where a cause-linking span (e.g. a
// service's exec span) appears as a child of the span it links to (e.g. the
// Container.asService install span). Seeding from both ends of the root's
// cause links is what keeps a capture rooted at either handle — the exec
// span or an install span — returning the same log lines.
//
// The reverse (linking→linked) edges are followed from the root ONLY: a span
// merely reached during the walk (e.g. a lazy resume span beneath a tool
// call, which cause-links the install spans of the value it resumes) must
// not drag unrelated subtrees into the capture.
func (l *spanLookup) logScope(root string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n, ok := l.lookupLocked(root)
	if !ok {
		return map[string]struct{}{root: {}}
	}
	seeds := append([]uint32{n}, l.causalParentsLocked(n)...)
	in := map[uint32]struct{}{}
	queue := make([]uint32, 0, len(seeds))
	for _, seed := range seeds {
		if _, exists := in[seed]; exists {
			continue
		}
		in[seed] = struct{}{}
		queue = append(queue, seed)
	}
	add := func(kid uint32) bool {
		if _, exists := in[kid]; !exists {
			in[kid] = struct{}{}
			queue = append(queue, kid)
		}
		return true
	}
	for i := 0; i < len(queue); i++ {
		l.eachChildLocked(queue[i], add)
		l.causalChildrenLocked(queue[i], add)
	}
	return l.namesLocked(queue)
}

// namesLocked is the set of spans' names.
func (l *spanLookup) namesLocked(spans []uint32) map[string]struct{} {
	names := make(map[string]struct{}, len(spans))
	for _, span := range spans {
		names[l.ids.name(span)] = struct{}{}
	}
	return names
}

// ancestorClosure returns ids plus every member's ancestor chain, walked over
// the parent index up to each trace root. Cycle-safe: a span already in the
// set ends its walk, so a (malformed) parent cycle cannot loop.
func (l *spanLookup) ancestorClosure(ids map[string]struct{}) map[string]struct{} {
	closure := make(map[string]struct{}, len(ids))
	l.mu.RLock()
	defer l.mu.RUnlock()
	in := map[uint32]struct{}{}
	for id := range ids {
		if id == "" {
			// The empty ID names no span: it ends a chain.
			continue
		}
		n, ok := l.lookupLocked(id)
		if !ok {
			closure[id] = struct{}{}
			continue
		}
		for {
			if _, seen := in[n]; seen {
				break
			}
			in[n] = struct{}{}
			closure[l.ids.name(n)] = struct{}{}
			parent, ok := l.parentLocked(n)
			if !ok || l.ids.empty(parent) {
				break
			}
			n = parent
		}
	}
	return closure
}

// parentSubtree returns root plus every span beneath it over parent→child
// edges alone -- no cause links, unlike logScope. Cycle-safe.
func (l *spanLookup) parentSubtree(root string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n, ok := l.lookupLocked(root)
	if !ok {
		return map[string]struct{}{root: {}}
	}
	in := map[uint32]struct{}{n: {}}
	queue := []uint32{n}
	for i := 0; i < len(queue); i++ {
		l.eachChildLocked(queue[i], func(kid uint32) bool {
			if _, seen := in[kid]; !seen {
				in[kid] = struct{}{}
				queue = append(queue, kid)
			}
			return true
		})
	}
	return l.namesLocked(queue)
}

// latestRowIDs returns the newest snapshot row ID of every span in ids that
// the index knows, in ascending row order — append order, which is the order
// sequential processing would have delivered them in.
func (l *spanLookup) latestRowIDs(ids map[string]struct{}) []int64 {
	rowIDs := make([]int64, 0, len(ids))
	l.mu.RLock()
	for id := range ids {
		if n, ok := l.lookupLocked(id); ok && l.lastRow[n] != 0 {
			rowIDs = append(rowIDs, l.lastRow[n])
		}
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

func (l *spanLookup) hasSpan(spanID string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.hasSpanLocked(spanID)
}

func (l *spanLookup) hasSpanLocked(spanID string) bool {
	n, ok := l.lookupLocked(spanID)
	return ok && l.lastRow[n] != 0
}

// placementMemo remembers, for one reader, the spans place found not beneath
// its scope, as of one generation of the index: more spans or links may
// place them later, but until the index changes they need no second walk.
type placementMemo struct {
	gen uint64
	out map[uint32]struct{}
	// Buffers reused across walks: seen holds the walk that last visited
	// each span, so it needs no clearing.
	walk    uint64
	seen    map[uint32]uint64
	visited []uint32
	queue   []uint32
}

// place reports whether spanID has a snapshot in the index and, if it does,
// whether it is in scope or reachable from it upward, over the reverse of
// logScope's downward edges: the parent edge and the span's own cause links.
// gen is the generation the answer holds for. A span found not beneath is
// remembered in memo, with every span its walk visited (their ancestry is
// part of its own), so that later walks through them stop there. Cycle-safe.
func (l *spanLookup) place(spanID string, scope map[string]bool, memo *placementMemo) (known, beneath bool, gen uint64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	gen = l.gen.Load()
	n, ok := l.lookupLocked(spanID)
	if !ok || l.lastRow[n] == 0 {
		return false, false, gen
	}
	return true, l.beneathLocked(n, scope, memo), gen
}

// beneathLocked is place's walk, for a span with a snapshot.
func (l *spanLookup) beneathLocked(span uint32, scope map[string]bool, memo *placementMemo) bool {
	if gen := l.gen.Load(); memo.out == nil || memo.gen != gen {
		memo.out, memo.gen = map[uint32]struct{}{}, gen
	}
	if memo.seen == nil {
		memo.seen = map[uint32]uint64{}
	}
	memo.walk++
	visited := memo.visited[:0]
	queue := memo.queue[:0]
	queue = append(queue, span)
	defer func() { memo.visited, memo.queue = visited[:0], queue[:0] }()
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if l.ids.in(id, scope) {
			return true
		}
		if memo.seen[id] == memo.walk {
			continue
		}
		memo.seen[id] = memo.walk
		visited = append(visited, id)
		if _, out := memo.out[id]; out {
			continue
		}
		if parent, ok := l.parentLocked(id); ok {
			queue = append(queue, parent)
		}
		queue = append(queue, l.causalParentsLocked(id)...)
	}
	for _, id := range visited {
		memo.out[id] = struct{}{}
	}
	return false
}

// closeBeneath adds to tree every span place would find beneath it: the
// spans reachable from it downward over the exact inverses of place's
// edges (parentOf, and the spans cause-linking each one). Every such span
// has a snapshot, so a scope may place them for good. It returns how many
// arrivals that covers (for placeArrivals), and the spans of unplaced that
// still have no snapshot, as of the same state of the index.
func (l *spanLookup) closeBeneath(tree map[string]bool, unplaced []string) (arrivals int, still []string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	seeds := make([]uint32, 0, len(tree))
	for id := range tree {
		if n, ok := l.lookupLocked(id); ok {
			seeds = append(seeds, n)
		}
	}
	l.closeFromLocked(tree, seeds)
	return len(l.arrivals), l.stillUnplacedLocked(unplaced)
}

// placeArrivals brings a tree closed by closeBeneath, as of arrivals, up to
// date: a span can only join it once it or a span above it arrives, so it
// places the arrivals since, then closes the tree beneath them. It returns
// the spans it added, the arrivals now covered, and the spans of unplaced
// that still have no snapshot.
func (l *spanLookup) placeArrivals(tree map[string]bool, arrivals int, memo *placementMemo, unplaced []string) (added []string, covered int, still []string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var seeds []uint32
	for _, id := range l.arrivals[arrivals:] {
		if !l.ids.in(id, tree) && l.beneathLocked(id, tree, memo) {
			tree[l.ids.name(id)] = true
			seeds = append(seeds, id)
		}
	}
	seeds = append(seeds, l.closeFromLocked(tree, seeds)...)
	for _, id := range seeds {
		added = append(added, l.ids.name(id))
	}
	return added, len(l.arrivals), l.stillUnplacedLocked(unplaced)
}

// closeFromLocked adds to tree the spans beneath seeds (see closeBeneath),
// returning them.
func (l *spanLookup) closeFromLocked(tree map[string]bool, seeds []uint32) []uint32 {
	queue := slices.Clone(seeds)
	add := func(kid uint32) bool {
		if !l.ids.in(kid, tree) {
			tree[l.ids.name(kid)] = true
			queue = append(queue, kid)
		}
		return true
	}
	for i := 0; i < len(queue); i++ {
		eachInList(l.parentOfHead, l.parentOfNext, queue[i], add)
		l.causalChildrenLocked(queue[i], add)
	}
	return queue[len(seeds):]
}

func (l *spanLookup) stillUnplacedLocked(unplaced []string) []string {
	var still []string
	for _, id := range unplaced {
		if !l.hasSpanLocked(id) {
			still = append(still, id)
		}
	}
	return still
}

// allSpanIDs snapshots every span ID the index has seen a snapshot of.
func (l *spanLookup) allSpanIDs() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make(map[string]struct{}, len(l.lastRow))
	for n, row := range l.lastRow {
		if row != 0 {
			ids[l.ids.name(uint32(n))] = struct{}{}
		}
	}
	return ids
}

// directChildren returns the spans one edge beneath spanID over the same
// downward edges logScope walks: child edges and cause-purpose link edges.
func (l *spanLookup) directChildren(spanID string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	kids := map[string]struct{}{}
	n, ok := l.lookupLocked(spanID)
	if !ok {
		return kids
	}
	add := func(kid uint32) bool {
		if kid != n {
			kids[l.ids.name(kid)] = struct{}{}
		}
		return true
	}
	l.eachChildLocked(n, add)
	l.causalChildrenLocked(n, add)
	return kids
}

// markedSpanIDs snapshots the check- and test-marked span ID sets.
func (l *spanLookup) markedSpanIDs() (checks, tests map[string]struct{}) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.namesLocked(l.checkSpans), l.namesLocked(l.testSpans)
}

// agentSpanIDs snapshots the agent-marked span ID set.
func (l *spanLookup) agentSpanIDs() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.namesLocked(l.agents)
}
