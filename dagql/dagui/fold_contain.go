package dagui

// Contained waits.
//
// A row's waiting (resolvedWaits) counts a blocker inside the row's own
// subtree as the row's own work. A wait of a span C on a span T beneath it
// whose chain can't leave T -- no edge, wait or cause link, crosses T's
// subtree boundary -- resolves, wherever it's read from a row holding T, to
// blockers inside that row: it tells the row nothing but the stretch it
// covers, which merges with the row's other waits (mergeWaitWindows). Such a
// wait is CONTAINED: T need not be retained for it, and the DB reads it as a
// window on work inside the row (waitWindow.inside) instead of on T. What
// reads C's waits:
//
//   - C's row, the rows holding C, and C's same-name parent, whose row
//     merges C's waits with its own: all hold T;
//   - a span outside C waiting on C reads them through its wait chain, for
//     its own row, which doesn't hold T: C is READ, and so is what C's
//     waits reach, and C's twins (its same-name children, whose waits are
//     C's);
//   - a call C is the deferred evaluation of reads C's waits as its own:
//     C has a cause link then, and a span with one is read too.
//
// So a wait of C on a span T beneath it is contained while C and its twin
// parents aren't read, T is SEALED -- no edge crosses its boundary, nor that
// of its parent, whose waits the chain reads too when T is a call's
// execution (rowWaitsState.chainWaits) -- and T isn't a lazy evaluation,
// which resolves to the call it evaluates. An edge arriving later that
// breaks this breaks the wait (breakWaits, breakWaitsOn): C and T are
// retained, and read back, from then on.
//
// A folded span whose waits are all contained or on its own execution
// (noopWait) is plain work, as its summary is: nothing it waits on is
// outside it. One under a twin parent is retained, as the parent's row
// reads its waits (neededForWaits). A wait on a span not received yet is
// sorted once that span arrives (resolvePending).

// noteWait notes span i's wait on target.
func (f *Fold) noteWait(i, target uint32) {
	if i == target {
		return
	}
	lca := f.commonAncestor(i, target)
	f.markEscape(i, lca)
	if lca != i {
		// a wait on a span outside i reads that span's waits
		f.markRead(target)
		return
	}
	if f.reads == nil {
		f.reads = map[uint32][]uint32{}
	}
	f.reads[i] = append(f.reads[i], target)
	if f.state[i]&foldRead != 0 {
		f.markRead(target)
	}
}

// noteCause notes span i's cause link to cause: both ends' chains may leave
// their subtrees through it (resolveBlocker, inferred waits).
func (f *Fold) noteCause(i, cause uint32) {
	if i == cause {
		return
	}
	lca := f.commonAncestor(i, cause)
	f.markEscape(i, lca)
	f.markEscape(cause, lca)
	// the row of a call i is an evaluation of reads i's waits as its own,
	// and its twins'
	f.markRead(i)
	f.state[i] |= foldCauseLinks
	for c := f.firstChild[i]; c != 0; c = f.nextSib[c-1] {
		if f.name[c-1] == f.name[i] && f.state[c-1]&foldNoopWaits != 0 {
			f.breakNoops(c - 1)
		}
	}
}

// breakNoops retains span s, whose waits on its own execution a row now
// reads, and their targets, reading back those folded.
func (f *Fold) breakNoops(s uint32) {
	f.state[s] = f.state[s]&^foldNoopWaits | foldWaitLinks
	f.Broken++
	f.BrokenTargets += len(f.noops[s])
	f.promote(s, f.have(s))
	for _, t := range f.noops[s] {
		f.promote(t, noSpan)
	}
}

// have is the span promote may take the arriving snapshot of: s itself if
// it's the one being ingested.
func (f *Fold) have(s uint32) uint32 {
	if s == f.ingesting {
		return s
	}
	return noSpan
}

// foldWait is a wait link of an arriving span: its target, and whether
// it's a call's wait on an execution (reason call_exec or singleflight).
type foldWait struct {
	target SpanID
	exec   bool
}

// classifyWaits records span i's waits, and sorts them into those no row
// reads -- its waits on its own execution (noopWait) --, contained ones, and
// the others, which it appends to linked for the DB to retain with i.
func (f *Fold) classifyWaits(i uint32, waits []foldWait, linked []SpanID) []SpanID {
	// the waits a span carries come whole with each snapshot of it
	delete(f.reads, i)
	delete(f.noops, i)
	if f.waitGen == nil {
		f.waitGen = map[uint32]uint32{}
	}
	f.waitGen[i]++
	targets := make([]uint32, len(waits))
	skip := make([]bool, len(waits))
	var noops []uint32
	any, open := false, false
	for k, w := range waits {
		t := f.intern(w.target)
		targets[k] = t
		if t != i && f.state[t]&foldReceived == 0 {
			// a wait on a span still to come, whose place isn't known
			// yet: sorted once it arrives (see resolvePending)
			skip[k] = true
			f.markEscape(i, noSpan)
			if f.pending == nil {
				f.pending = map[uint32][]pendingWait{}
			}
			f.pending[t] = append(f.pending[t], pendingWait{carrier: i, exec: w.exec, gen: f.waitGen[i]})
			f.Pended++
			if p := f.parent[i]; p != 0 && f.name[p-1] == f.name[i] && f.name[i] != 0 {
				// its twin parent's row reads its waits as its own
				open = true
			}
			continue
		}
		if f.noopWait(i, t, w.exec) {
			skip[k] = true
			noops = append(noops, t)
			continue
		}
		f.noteWait(i, t)
	}
	unread := f.unread(i)
	for k, w := range waits {
		if skip[k] {
			continue
		}
		if t := targets[k]; unread && f.containable(i, t) {
			f.state[t] |= foldWaitTarget
			any = true
			continue
		}
		open = true
		linked = append(linked, w.target)
	}
	if open {
		f.state[i] |= foldWaitLinks
	}
	if any {
		f.state[i] |= foldContainedWaits
	} else {
		f.state[i] &^= foldContainedWaits
	}
	if len(noops) > 0 {
		if f.noops == nil {
			f.noops = map[uint32][]uint32{}
		}
		f.noops[i] = noops
		f.state[i] |= foldNoopWaits
	} else {
		f.state[i] &^= foldNoopWaits
	}
	return linked
}

// pendingWait is a wait on a span not received yet.
type pendingWait struct {
	carrier uint32
	exec    bool
	// gen is the carrier's waitGen when it carried it.
	gen uint32
}

// resolvePending sorts the waits on t, which just arrived, as classifyWaits
// would have if it had been there: on t's own execution, contained, or a
// wait the carrier is retained for, with t.
//
// Until t arrived the waits were unresolved for every row reading them, as
// the full load's are on a span it holds only a reference to: one the DB
// holds as well, for a retained carrier. The chains of the carrier's
// readers read them so too, which classifyWaits keeps exact by unsealing
// the carrier (markEscape), as it may be waiting outside itself.
func (f *Fold) resolvePending(t uint32) {
	waits, ok := f.pending[t]
	if !ok {
		return
	}
	delete(f.pending, t)
	for _, w := range waits {
		i := w.carrier
		if w.gen != f.waitGen[i] {
			// a later snapshot of the carrier replaced it
			continue
		}
		if f.state[i]&foldRetained != 0 {
			// the waiting below its row, unresolved, may resolve now
			f.touch(i)
		}
		if f.noopWait(i, t, w.exec) {
			if f.noops == nil {
				f.noops = map[uint32][]uint32{}
			}
			f.noops[i] = append(f.noops[i], t)
			f.state[i] |= foldNoopWaits
			continue
		}
		f.noteWait(i, t)
		if f.unread(i) && f.containable(i, t) {
			f.state[t] |= foldWaitTarget
			f.state[i] |= foldContainedWaits
			continue
		}
		f.state[i] |= foldWaitLinks
		f.promote(i, noSpan)
		f.promote(t, t)
	}
}

// noopWait reports whether span i's wait on t is one no row reads: a call's
// wait on its own execution, its same-name descendant, which every row
// reading i's waits skips (collectOwnWaits) -- i's own, its twin parent's,
// a wait chain's through i -- unless i or its twin parent is the deferred
// evaluation of a call of another name, whose row reads it (a span with
// cause links).
func (f *Fold) noopWait(i, t uint32, exec bool) bool {
	if !exec || t == i || f.name[i] == 0 || f.name[t] != f.name[i] || f.state[i]&foldCauseLinks != 0 {
		return false
	}
	if p := f.parent[i]; p != 0 && f.name[p-1] == f.name[i] && f.state[p-1]&foldCauseLinks != 0 {
		return false
	}
	return f.isAncestor(i, t)
}

// unread reports whether no row that doesn't hold span i reads its waits:
// it isn't read, nor are its twin parents.
func (f *Fold) unread(i uint32) bool {
	for depth := 0; depth < 4096; depth++ {
		if f.state[i]&foldRead != 0 {
			return false
		}
		p := f.parent[i]
		if p == 0 || f.name[p-1] != f.name[i] || f.name[i] == 0 {
			return true
		}
		i = p - 1
	}
	return false
}

// containable reports whether a wait of span c on t, if no row that
// doesn't hold c reads it, is contained: t is beneath c, sealed, and no
// lazy evaluation.
func (f *Fold) containable(c, t uint32) bool {
	if t == c || f.state[t]&foldReceived == 0 || !f.isAncestor(c, t) {
		return false
	}
	if f.state[t]&foldEscapes != 0 || isLazyEvaluationName(f.nameTab[f.name[t]]) {
		return false
	}
	if p := f.parent[t]; p != 0 && p-1 != c && f.state[p-1]&foldEscapes != 0 {
		return false
	}
	return true
}

// markEscape marks s and its ancestors below lca (noSpan: all of them) as
// spans an edge crosses the subtree boundary of, breaking the contained
// waits on them, and on their children, whose chains read theirs.
func (f *Fold) markEscape(s, lca uint32) {
	for depth := 0; s != lca && depth < 4096; depth++ {
		if f.state[s]&foldEscapes == 0 {
			f.state[s] |= foldEscapes
			if f.state[s]&foldWaitTarget != 0 {
				f.breakWaitsOn(s, noSpan)
			}
			for c := f.firstChild[s]; c != 0; c = f.nextSib[c-1] {
				if f.state[c-1]&foldWaitTarget != 0 {
					f.breakWaitsOn(c-1, s)
				}
			}
		}
		p := f.parent[s]
		if p == 0 {
			return
		}
		s = p - 1
	}
}

// markRead marks s as read by a row that doesn't hold it, and what reading
// it reads: what its waits reach beneath it, and its twins.
func (f *Fold) markRead(s uint32) {
	stack := []uint32{s}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.state[s]&foldRead != 0 {
			continue
		}
		f.state[s] |= foldRead
		f.breakWaits(s)
		stack = append(stack, f.reads[s]...)
		for c := f.firstChild[s]; c != 0; c = f.nextSib[c-1] {
			if f.name[c-1] == f.name[s] && f.name[s] != 0 {
				stack = append(stack, c-1)
			}
		}
	}
}

// breakWaits retains span s if it has contained waits, and their targets,
// reading back those folded; and so its twins' (see unread).
func (f *Fold) breakWaits(s uint32) {
	stack := []uint32{s}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.state[s]&foldContainedWaits != 0 {
			f.state[s] = f.state[s]&^foldContainedWaits | foldWaitLinks
			f.Broken++
			f.BrokenTargets += len(f.reads[s])
			f.promote(s, f.have(s))
			for _, t := range f.reads[s] {
				f.promote(t, noSpan)
			}
		}
		for c := f.firstChild[s]; c != 0; c = f.nextSib[c-1] {
			if f.name[c-1] == f.name[s] && f.name[s] != 0 && f.state[c-1]&foldContainedWaits != 0 {
				stack = append(stack, c-1)
			}
		}
	}
}

// breakWaitsOn breaks the contained waits on t, which is no longer sealed
// (or whose parent isn't, but for the waits of that parent, except):
// their carriers, t's ancestors, and t are retained, and read back.
func (f *Fold) breakWaitsOn(t, except uint32) {
	f.state[t] &^= foldWaitTarget
	for a, depth := f.parent[t], 0; a != 0 && depth < 4096; a, depth = f.parent[a-1], depth+1 {
		c := a - 1
		if c == except || f.state[c]&foldContainedWaits == 0 || !slicesContains(f.reads[c], t) {
			continue
		}
		f.state[c] |= foldWaitLinks
		f.Broken++
		f.BrokenTargets++
		f.promote(c, f.have(c))
		f.promote(t, noSpan)
	}
	if except != noSpan {
		// still a target of except's
		f.state[t] |= foldWaitTarget
	}
}

func slicesContains(s []uint32, v uint32) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// waitStandIn is how the waiting below rows reads a wait on a span a
// folding DB holds as a stub, which it doesn't hold even a reference to.
type waitStandIn int

const (
	// standInNone: the DB holds the target, or nothing does.
	standInNone waitStandIn = iota
	// standInSkip: a wait on the carrier's own execution, which no row
	// reads.
	standInSkip
	// standInInside: a contained wait, a window on work inside the row
	// (see above).
	standInInside
	// standInUnloaded: a wait on a span not received yet (resolvePending),
	// unresolved, as on a span the DB holds only a reference to.
	standInUnloaded
)

// waitStandIn reports how the waiting below rows is to read carrier's wait
// on target (exec: a wait for reason call_exec or singleflight). A folded
// span beneath carrier that carrier waits on is the target of a wait on its
// own execution or a contained one: the targets of its other waits are
// retained with it.
func (f *Fold) waitStandIn(carrier, target SpanID, exec bool) waitStandIn {
	if f == nil {
		return standInNone
	}
	t, ok := f.lookup(target)
	if !ok || f.state[t]&foldRetained != 0 {
		return standInNone
	}
	if f.state[t]&foldReceived == 0 {
		return standInUnloaded
	}
	c, ok := f.lookup(carrier)
	if !ok || t == c || f.state[c]&(foldContainedWaits|foldNoopWaits) == 0 || !f.isAncestor(c, t) {
		return standInNone
	}
	if f.state[c]&foldNoopWaits != 0 && exec && f.name[t] == f.name[c] {
		return standInSkip
	}
	if f.state[c]&foldContainedWaits != 0 {
		return standInInside
	}
	return standInNone
}

// holdsWaitTarget reports whether the stubs hold carrier's wait on target
// in the DB's stead (waitStandIn).
func (f *Fold) holdsWaitTarget(carrier, target SpanID, reason string) bool {
	return f.waitStandIn(carrier, target, isExecWait(reason)) != standInNone
}

func isExecWait(reason string) bool {
	return reason == "call_exec" || reason == "singleflight"
}

// commonAncestor returns the lowest common ancestor of x and y in the
// stubs, noSpan if their ancestries don't meet (yet).
func (f *Fold) commonAncestor(x, y uint32) uint32 {
	dx, tx := f.depthTop(x)
	dy, ty := f.depthTop(y)
	if tx != ty {
		return noSpan
	}
	for ; dx > dy; dx-- {
		x = f.parent[x] - 1
	}
	for ; dy > dx; dy-- {
		y = f.parent[y] - 1
	}
	for x != y {
		x, y = f.parent[x]-1, f.parent[y]-1
	}
	return x
}

// depthTop returns s's depth beneath the topmost ancestor the stubs place
// it under, and that ancestor.
func (f *Fold) depthTop(s uint32) (int, uint32) {
	d := 0
	for f.parent[s] != 0 && d < 4096 {
		s = f.parent[s] - 1
		d++
	}
	return d, s
}

// isAncestor reports whether a is s or one of its ancestors.
func (f *Fold) isAncestor(a, s uint32) bool {
	for depth := 0; depth < 4096; depth++ {
		if s == a {
			return true
		}
		p := f.parent[s]
		if p == 0 {
			return false
		}
		s = p - 1
	}
	return false
}
