package clientdb

import (
	"bytes"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
)

// refSpanLookup is the span index as it was before its span IDs were
// interned: string-keyed maps throughout. It is kept as the reference the
// interned index must answer identically to (TestSpanLookupMatchesReference).

type refSpanLookupKey struct {
	traceID string
	spanID  string
}

type refSpanLookup struct {
	mu  sync.RWMutex
	gen atomic.Uint64

	firstRow       map[refSpanLookupKey]int64
	lastRow        map[string]int64
	parent         map[string]string
	parentOf       map[string][]string
	arrivals       []string
	checkSpans     map[string]struct{}
	testSpans      map[string]struct{}
	agentSpans     map[string]struct{}
	children       map[string][]string
	causalIDs      map[string]uint32
	causalName     []string
	linkSets       []causalLinkSet
	linkSetIDs     map[string]uint32
	spanLinkSet    map[uint32]uint32
	targetLinkSets map[uint32][]uint32
	freeLinkSets   []uint32
}

func newRefSpanLookup() *refSpanLookup {
	return &refSpanLookup{
		firstRow:       make(map[refSpanLookupKey]int64),
		lastRow:        make(map[string]int64),
		parent:         make(map[string]string),
		parentOf:       make(map[string][]string),
		checkSpans:     make(map[string]struct{}),
		testSpans:      make(map[string]struct{}),
		agentSpans:     make(map[string]struct{}),
		children:       make(map[string][]string),
		causalIDs:      make(map[string]uint32),
		linkSetIDs:     make(map[string]uint32),
		spanLinkSet:    make(map[uint32]uint32),
		targetLinkSets: make(map[uint32][]uint32),
	}
}

func (l *refSpanLookup) generation() uint64 { return l.gen.Load() }

func (l *refSpanLookup) addLocked(row Span, causalTargets []string) {
	linked := l.addCauseLinksLocked(row.SpanID, causalTargets)
	_, seen := l.lastRow[row.SpanID]
	l.lastRow[row.SpanID] = row.ID
	if _, marked := l.checkSpans[row.SpanID]; !marked && bytes.Contains(row.Attributes, checkNameAttrMarker) {
		l.checkSpans[row.SpanID] = struct{}{}
	}
	if _, marked := l.testSpans[row.SpanID]; !marked &&
		(bytes.Contains(row.Attributes, testCaseNameAttrMarker) || bytes.Contains(row.Attributes, testSuiteNameAttrMarker)) {
		l.testSpans[row.SpanID] = struct{}{}
	}
	if _, marked := l.agentSpans[row.SpanID]; !marked && bytes.Contains(row.Attributes, agentIDAttrMarker) {
		l.agentSpans[row.SpanID] = struct{}{}
	}
	var parentKnown bool
	if row.ParentSpanID.Valid {
		_, parentKnown = l.parent[row.SpanID]
		if !parentKnown {
			l.parent[row.SpanID] = row.ParentSpanID.String
			l.parentOf[row.ParentSpanID.String] = append(l.parentOf[row.ParentSpanID.String], row.SpanID)
		}
	}
	if !seen || linked || (row.ParentSpanID.Valid && !parentKnown) {
		l.arrivals = append(l.arrivals, row.SpanID)
	}
	key := refSpanLookupKey{traceID: row.TraceID, spanID: row.SpanID}
	if _, exists := l.firstRow[key]; exists {
		return
	}
	l.firstRow[key] = row.ID
	if row.ParentSpanID.Valid {
		children := l.children[row.ParentSpanID.String]
		if parentKnown && slices.Contains(children, row.SpanID) {
			return
		}
		l.children[row.ParentSpanID.String] = append(children, row.SpanID)
	}
}

func (l *refSpanLookup) addCauseLinksLocked(spanID string, targets []string) bool {
	if len(targets) == 0 {
		return false
	}
	span := l.causalIDLocked(spanID)
	old, linked := l.spanLinkSet[span]
	var current []uint32
	if linked {
		current = l.linkSets[old].targets
	}
	next := current
	for _, target := range targets {
		id := l.causalIDLocked(target)
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

func (l *refSpanLookup) causalIDLocked(spanID string) uint32 {
	if id, ok := l.causalIDs[spanID]; ok {
		return id
	}
	id := uint32(len(l.causalName))
	l.causalIDs[spanID] = id
	l.causalName = append(l.causalName, spanID)
	return id
}

func (l *refSpanLookup) linkSetLocked(targets []uint32) uint32 {
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

func (l *refSpanLookup) dropLinkSetLocked(set uint32) {
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

func (l *refSpanLookup) causalChildrenLocked(spanID string, f func(string) bool) {
	target, ok := l.causalIDs[spanID]
	if !ok {
		return
	}
	for _, set := range l.targetLinkSets[target] {
		for _, member := range l.linkSets[set].members {
			if !f(l.causalName[member]) {
				return
			}
		}
	}
}

func (l *refSpanLookup) causalParentsLocked(spanID string) []string {
	span, ok := l.causalIDs[spanID]
	if !ok {
		return nil
	}
	set, ok := l.spanLinkSet[span]
	if !ok {
		return nil
	}
	targets := l.linkSets[set].targets
	parents := make([]string, len(targets))
	for i, target := range targets {
		parents[i] = l.causalName[target]
	}
	return parents
}

func (l *refSpanLookup) first(traceID, spanID string) (int64, bool) {
	l.mu.RLock()
	id, found := l.firstRow[refSpanLookupKey{traceID: traceID, spanID: spanID}]
	l.mu.RUnlock()
	return id, found
}

func (l *refSpanLookup) causalChildrenOf(spanID string) []string {
	var kids []string
	l.mu.RLock()
	l.causalChildrenLocked(spanID, func(kid string) bool {
		kids = append(kids, kid)
		return true
	})
	l.mu.RUnlock()
	return kids
}

func (l *refSpanLookup) causalParentsOf(spanID string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.causalParentsLocked(spanID)
}

func (l *refSpanLookup) hasDescendants(root string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, kid := range l.children[root] {
		if kid != root {
			return true
		}
	}
	descendant := false
	l.causalChildrenLocked(root, func(kid string) bool {
		descendant = kid != root
		return !descendant
	})
	return descendant
}

func (l *refSpanLookup) logScope(root string) map[string]struct{} {
	l.mu.RLock()
	seeds := append([]string{root}, l.causalParentsLocked(root)...)
	l.mu.RUnlock()

	scope := make(map[string]struct{}, len(seeds))
	queue := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		if _, exists := scope[seed]; exists {
			continue
		}
		scope[seed] = struct{}{}
		queue = append(queue, seed)
	}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]

		l.mu.RLock()
		children := append([]string(nil), l.children[parent]...)
		l.causalChildrenLocked(parent, func(kid string) bool {
			children = append(children, kid)
			return true
		})
		l.mu.RUnlock()

		for _, child := range children {
			if _, exists := scope[child]; exists {
				continue
			}
			scope[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return scope
}

func (l *refSpanLookup) ancestorClosure(ids map[string]struct{}) map[string]struct{} {
	closure := make(map[string]struct{}, len(ids))
	l.mu.RLock()
	defer l.mu.RUnlock()
	for id := range ids {
		for id != "" {
			if _, seen := closure[id]; seen {
				break
			}
			closure[id] = struct{}{}
			id = l.parent[id]
		}
	}
	return closure
}

func (l *refSpanLookup) parentSubtree(root string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	subtree := map[string]struct{}{root: {}}
	queue := []string{root}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range l.children[parent] {
			if _, seen := subtree[child]; seen {
				continue
			}
			subtree[child] = struct{}{}
			queue = append(queue, child)
		}
	}
	return subtree
}

func (l *refSpanLookup) latestRowIDs(ids map[string]struct{}) []int64 {
	rowIDs := make([]int64, 0, len(ids))
	l.mu.RLock()
	for id := range ids {
		if rowID, found := l.lastRow[id]; found {
			rowIDs = append(rowIDs, rowID)
		}
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}

func (l *refSpanLookup) hasSpan(spanID string) bool {
	l.mu.RLock()
	_, found := l.lastRow[spanID]
	l.mu.RUnlock()
	return found
}

type refPlacementMemo struct {
	gen     uint64
	out     map[string]struct{}
	walk    uint64
	seen    map[string]uint64
	visited []string
	queue   []string
}

func (l *refSpanLookup) place(spanID string, scope map[string]bool, memo *refPlacementMemo) (known, beneath bool, gen uint64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	gen = l.gen.Load()
	if _, known = l.lastRow[spanID]; !known {
		return false, false, gen
	}
	return true, l.beneathLocked(spanID, scope, memo), gen
}

func (l *refSpanLookup) beneathLocked(spanID string, scope map[string]bool, memo *refPlacementMemo) bool {
	if gen := l.gen.Load(); memo.out == nil || memo.gen != gen {
		memo.out, memo.gen = map[string]struct{}{}, gen
	}
	if memo.seen == nil {
		memo.seen = map[string]uint64{}
	}
	memo.walk++
	visited := memo.visited[:0]
	queue := memo.queue[:0]
	queue = append(queue, spanID)
	defer func() { memo.visited, memo.queue = visited[:0], queue[:0] }()
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if scope[id] {
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
		if parent, ok := l.parent[id]; ok {
			queue = append(queue, parent)
		}
		if span, ok := l.causalIDs[id]; ok {
			if set, ok := l.spanLinkSet[span]; ok {
				for _, target := range l.linkSets[set].targets {
					queue = append(queue, l.causalName[target])
				}
			}
		}
	}
	for _, id := range visited {
		memo.out[id] = struct{}{}
	}
	return false
}

func (l *refSpanLookup) closeBeneath(tree map[string]bool, unplaced []string) (arrivals int, still []string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	seeds := make([]string, 0, len(tree))
	for id := range tree {
		seeds = append(seeds, id)
	}
	l.closeFromLocked(tree, seeds)
	return len(l.arrivals), l.stillUnplacedLocked(unplaced)
}

func (l *refSpanLookup) placeArrivals(tree map[string]bool, arrivals int, memo *refPlacementMemo, unplaced []string) (added []string, covered int, still []string) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var seeds []string
	for _, id := range l.arrivals[arrivals:] {
		if !tree[id] && l.beneathLocked(id, tree, memo) {
			tree[id] = true
			seeds = append(seeds, id)
		}
	}
	seeds = append(seeds, l.closeFromLocked(tree, seeds)...)
	return seeds, len(l.arrivals), l.stillUnplacedLocked(unplaced)
}

func (l *refSpanLookup) closeFromLocked(tree map[string]bool, seeds []string) []string {
	queue := slices.Clone(seeds)
	add := func(kid string) bool {
		if !tree[kid] {
			tree[kid] = true
			queue = append(queue, kid)
		}
		return true
	}
	for i := 0; i < len(queue); i++ {
		for _, kid := range l.parentOf[queue[i]] {
			add(kid)
		}
		l.causalChildrenLocked(queue[i], add)
	}
	return queue[len(seeds):]
}

func (l *refSpanLookup) stillUnplacedLocked(unplaced []string) []string {
	var still []string
	for _, id := range unplaced {
		if _, known := l.lastRow[id]; !known {
			still = append(still, id)
		}
	}
	return still
}

func (l *refSpanLookup) allSpanIDs() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make(map[string]struct{}, len(l.lastRow))
	for id := range l.lastRow {
		ids[id] = struct{}{}
	}
	return ids
}

func (l *refSpanLookup) directChildren(spanID string) map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	kids := make(map[string]struct{}, len(l.children[spanID]))
	for _, kid := range l.children[spanID] {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
	}
	l.causalChildrenLocked(spanID, func(kid string) bool {
		if kid != spanID {
			kids[kid] = struct{}{}
		}
		return true
	})
	return kids
}

func (l *refSpanLookup) markedSpanIDs() (checks, tests map[string]struct{}) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	checks = make(map[string]struct{}, len(l.checkSpans))
	for id := range l.checkSpans {
		checks[id] = struct{}{}
	}
	tests = make(map[string]struct{}, len(l.testSpans))
	for id := range l.testSpans {
		tests[id] = struct{}{}
	}
	return checks, tests
}

func (l *refSpanLookup) agentSpanIDs() map[string]struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make(map[string]struct{}, len(l.agentSpans))
	for id := range l.agentSpans {
		ids[id] = struct{}{}
	}
	return ids
}
