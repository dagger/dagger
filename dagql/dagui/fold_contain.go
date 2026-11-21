package dagui

// Contained waits.
//
// A row's waiting (resolvedWaits) counts a blocker inside the row's own
// subtree as the row's own work. So when no edge -- a wait or cause link --
// leaves a span C's subtree, every wait chain starting at C's waits stays in
// it, every blocker it resolves to is inside C, and C's waits tell C's row,
// and any row holding C, nothing. They're CONTAINED: neither C nor their
// targets need be retained for them, and C folds as plain work (foldedWork),
// as its summary is. What else reads C's own waits:
//
//   - a span outside C waiting on C reads them through its wait chain, for
//     its own row, which doesn't hold C's blockers: C is READ, and so is
//     what C's waits reach, and C's twins (its same-name children, whose
//     waits are C's);
//   - C's same-name parent, whose row merges C's waits with its own (the
//     longest contributor names a merged stretch): C's waits are contained
//     only while its twin parent's are too;
//   - a call C is the deferred evaluation of reads C's waits as its own:
//     C has a cause link then, and a span with one is read too.
//
// So a wait of C on a span beneath it is contained while no edge escapes
// C's subtree, C isn't read, and the same holds for C's twin parents. An
// edge arriving later that breaks this breaks C's contained waits
// (breakWaits): C and their targets are retained, and read back, from then
// on.

// noteWait notes span i's wait on target, and reports whether it's
// contained.
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
	have := noSpan
	if s == f.ingesting {
		have = s
	}
	f.promote(s, have)
	for _, t := range f.noops[s] {
		f.promote(t, noSpan)
	}
}

// foldWait is a wait link of an arriving span: its target, and whether
// it's a call's wait on an execution (reason call_exec or singleflight).
type foldWait struct {
	target SpanID
	exec   bool
}

// classifyWaits records span i's waits, and sorts them into those no row
// reads -- its waits on its own execution (noopWait), contained ones -- and
// the others, which it appends to linked for the DB to retain with i.
func (f *Fold) classifyWaits(i uint32, waits []foldWait, linked []SpanID) []SpanID {
	// the waits a span carries come whole with each snapshot of it
	delete(f.reads, i)
	delete(f.noops, i)
	targets := make([]uint32, len(waits))
	noop := make([]bool, len(waits))
	var noops []uint32
	for k, w := range waits {
		targets[k] = f.intern(w.target)
		if f.noopWait(i, targets[k], w.exec) {
			noop[k] = true
			noops = append(noops, targets[k])
			continue
		}
		f.noteWait(i, targets[k])
	}
	contained := f.waitsContained(i)
	any := false
	for k, w := range waits {
		if noop[k] {
			continue
		}
		if contained && f.isAncestor(i, targets[k]) && targets[k] != i {
			any = true
			continue
		}
		f.state[i] |= foldWaitLinks
		linked = append(linked, w.target)
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

// waitsContained reports whether span i's waits on spans beneath it are
// contained: no edge leaves its subtree, it isn't read, and the same holds
// for its twin parents.
func (f *Fold) waitsContained(i uint32) bool {
	for depth := 0; depth < 4096; depth++ {
		if f.state[i]&(foldEscapes|foldRead) != 0 {
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

// markEscape marks s and its ancestors below lca (noSpan: all of them) as
// spans an edge leaves the subtree of, breaking their contained waits and
// their twins'.
func (f *Fold) markEscape(s, lca uint32) {
	for depth := 0; s != lca && depth < 4096; depth++ {
		if f.state[s]&foldEscapes == 0 {
			f.state[s] |= foldEscapes
			f.breakWaits(s)
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
// reading back those folded; and so its twins' (see waitsContained).
func (f *Fold) breakWaits(s uint32) {
	stack := []uint32{s}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.state[s]&foldContainedWaits != 0 {
			f.state[s] = f.state[s]&^foldContainedWaits | foldWaitLinks
			f.Broken++
			f.BrokenTargets += len(f.reads[s])
			have := noSpan
			if s == f.ingesting {
				have = s
			}
			f.promote(s, have)
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

// skipsWait reports whether the waiting below rows is to skip carrier's
// wait on target: a contained wait on a folded span, which the DB doesn't
// hold even a reference to, and which tells nothing (see above).
func (f *Fold) skipsWait(carrier, target SpanID) bool {
	if f == nil {
		return false
	}
	c, ok := f.lookup(carrier)
	if !ok || f.state[c]&(foldContainedWaits|foldNoopWaits) == 0 {
		return false
	}
	t, ok := f.lookup(target)
	if !ok || t == c || f.state[t]&(foldReceived|foldRetained) != foldReceived {
		return false
	}
	if f.state[c]&foldContainedWaits == 0 && f.name[t] != f.name[c] {
		// only its waits on its own execution go unread
		return false
	}
	return f.isAncestor(c, t)
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
