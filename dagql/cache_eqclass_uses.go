package dagql

import (
	"errors"
	"fmt"
)

// The classes' uses, kept, and the classes freed as they end.
//
// A class is live while a live term or an entry uses it: a term uses the
// class of each of its inputs and of its output, and an entry uses each of
// its output classes. A class ends when its last use goes, or when a union
// joins it into another; one made and never used has ended too. The
// operation that ends a class notes it, and the collector frees the noted
// classes that ended as it finishes (freeEndedEqClassesLocked), so that no
// table holds a dead class, and freeing costs what ended, never a walk of
// the graph.
//
// egraphUses counts, at each root, the uses of its class: one for each
// input of each live term whose class it is, one for each live term whose
// output class it is, and one for each output class of an entry under it.
// liveEqClasses counts the roots whose uses are above 0. Every write of what
// that reads keeps it current: a class made (allocEqClassLocked), a union
// (mergeEqClassesNoRepairLocked), a term made or imported (useTermLocked), a
// term removed (unuseTermLocked), a term given another output class
// (setTermOutputLocked), an entry's output classes added
// (addResultOutputEqClassLocked) or removed
// (removeResultOutputEqClassesLocked), a class freed, and a reset. A repair
// moves a term's inputs to their roots, which changes no class's uses.
// verifyEqClassUses checks the kept uses against a walk.

// useEqClassLocked counts one use of id's class. Requires egraphMu for
// writing.
func (c *Cache) useEqClassLocked(id eqClassID) {
	root := c.findEqClassLocked(id)
	if root == 0 {
		return
	}
	if c.egraphUses[root] == 0 {
		c.liveEqClasses++
	}
	c.egraphUses[root]++
}

// unuseEqClassLocked counts one use of id's class fewer, and notes the class
// as ended when that was its last. Requires egraphMu for writing.
func (c *Cache) unuseEqClassLocked(id eqClassID) {
	root := c.findEqClassLocked(id)
	if root == 0 {
		return
	}
	c.egraphUses[root]--
	if c.egraphUses[root] == 0 {
		c.liveEqClasses--
		c.endedEqClasses = append(c.endedEqClasses, root)
	}
}

// useTermLocked counts the uses of a term made or imported: its inputs' and
// its output's classes. Requires egraphMu for writing.
func (c *Cache) useTermLocked(term *egraphTerm) {
	c.noteSavedTermLocked(term.id)
	for _, in := range term.inputEqIDs {
		c.useEqClassLocked(in)
	}
	c.useEqClassLocked(term.outputEqID)
}

// unuseTermLocked takes away a removed term's uses. Requires egraphMu for
// writing.
func (c *Cache) unuseTermLocked(term *egraphTerm) {
	c.noteSavedTermLocked(term.id)
	for _, in := range term.inputEqIDs {
		c.unuseEqClassLocked(in)
	}
	c.unuseEqClassLocked(term.outputEqID)
}

// setTermOutputLocked gives a term the output class root, a root, moving
// its use. Requires egraphMu for writing.
func (c *Cache) setTermOutputLocked(term *egraphTerm, root eqClassID) {
	if old := c.findEqClassLocked(term.outputEqID); old != root {
		c.unuseEqClassLocked(old)
		c.useEqClassLocked(root)
	}
	term.outputEqID = root
	c.noteSavedTermLocked(term.id)
}

// joinEqClassUsesLocked moves the uses of rb, a root a union has just put
// under ra, to ra: two live classes joined are one. Requires egraphMu for
// writing.
func (c *Cache) joinEqClassUsesLocked(ra, rb eqClassID) {
	moved := c.egraphUses[rb]
	if moved == 0 {
		return
	}
	c.egraphUses[rb] = 0
	if c.egraphUses[ra] > 0 {
		c.liveEqClasses--
	}
	c.egraphUses[ra] += moved
}

// freeEndedEqClassesLocked frees each class noted since it last ran that
// has ended: one a union joined into another, and one no term and no entry
// uses. A noted class that is live again, or already free, stays as it is.
// The collector runs it as it finishes (collectUnownedResultsLocked): no
// operation keeps a class number across a hold of the cache, nor across a
// collection within one, but the change marks' joins (changeMarks.joined),
// which it moves first to the roots they were joined into, while the
// parents still lead there, dropping one whose root nothing uses, which is
// freed with nothing to stamp. So nothing that names a noted class outlives
// it. It costs one look at each noted class and each pending join, and a
// delete for each digest of a freed class. Requires egraphMu for writing.
func (c *Cache) freeEndedEqClassesLocked() {
	if c.changes != nil {
		joined := c.changes.joined[:0]
		for _, id := range c.changes.joined {
			if root := c.findEqClassLocked(id); root != 0 && c.egraphUses[root] > 0 {
				joined = append(joined, root)
			}
		}
		c.changes.joined = joined
	}
	ended := c.endedEqClasses
	if c.testFreeEndedEqClasses != nil {
		c.testFreeEndedEqClasses()
		c.endedEqClasses = ended[:0]
		return
	}
	for _, id := range ended {
		if int(id) >= len(c.egraphParents) {
			continue
		}
		parent := c.egraphParents[id]
		if parent == 0 || parent == id && c.egraphUses[id] > 0 {
			continue
		}
		c.freeEqClassLocked(id)
	}
	c.endedEqClasses = ended[:0]
}

// freeEqClassLocked frees the class numbered id, which has ended: a root no
// term and no entry uses, or a class a union joined into another, which
// moved its digests, terms and entries to the class it joined and left at
// most empty sets under its number. A dead class's digests have no class
// from now on, so a kept call that takes one keys its term anew
// (forgetCallTermsLocked). Its number is taken by a new class
// (allocEqClassLocked). Requires egraphMu for writing.
func (c *Cache) freeEqClassLocked(id eqClassID) {
	for dig := range c.eqClassToDigests[id].All() {
		delete(c.egraphDigestToClass, dig)
		c.forgetCallTermsLocked(dig)
	}
	delete(c.eqClassToDigests, id)
	delete(c.eqClassExtraDigests, id)
	delete(c.inputEqClassToTerms, id)
	delete(c.outputEqClassToTerms, id)
	delete(c.outputEqClassResults, id)
	c.egraphParents[id] = 0
	c.egraphRanks[id] = 0
	c.noteSavedClassLocked(id)
	if c.changes != nil {
		delete(c.changes.left, id)
		delete(c.changes.leftNamed, id)
	}
	c.freeEqClassIDs = append(c.freeEqClassIDs, id)
}

// walkEqClassUsesLocked counts each root's uses by walking every live term
// and every entry's output classes, for a check of the uses the cache keeps
// (verifyEqClassUses). Requires egraphMu for writing: it compresses the
// union-find's paths.
func (c *Cache) walkEqClassUsesLocked() map[eqClassID]uint32 {
	uses := make(map[eqClassID]uint32)
	use := func(id eqClassID) {
		if root := c.findEqClassLocked(id); root != 0 {
			uses[root]++
		}
	}
	for _, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		for _, in := range term.inputEqIDs {
			use(in)
		}
		use(term.outputEqID)
	}
	for _, outputEqIDs := range c.resultOutputEqClasses {
		for outputEqID := range outputEqIDs.All() {
			use(outputEqID)
		}
	}
	return uses
}

// verifyEqClassUses checks the kept uses and live classes against a walk
// (walkEqClassUsesLocked).
func (c *Cache) verifyEqClassUses() error {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	walked := c.walkEqClassUsesLocked()
	var errs []error
	if c.liveEqClasses != len(walked) {
		errs = append(errs, fmt.Errorf("live classes: kept %d, walked %d", c.liveEqClasses, len(walked)))
	}
	for id, kept := range c.egraphUses {
		if want := walked[eqClassID(id)]; kept != want {
			errs = append(errs, fmt.Errorf("class %d: kept %d uses, walked %d", id, kept, want))
			break
		}
	}
	for root := range walked {
		if int(root) >= len(c.egraphUses) {
			errs = append(errs, fmt.Errorf("class %d: used, past the kept uses' %d slots", root, len(c.egraphUses)))
			break
		}
	}
	return errors.Join(errs...)
}

// verifyEqClassSlotsLocked checks the classes as a collection leaves them
// (freeEndedEqClassesLocked): every slot is free or its own root, a free one
// is on the free list once, every class is used, and no table, term or entry
// names a free slot or one that is not its own root. Requires egraphMu.
func (c *Cache) verifyEqClassSlotsLocked() error {
	errs := c.eqClassSlotErrorsLocked()
	bad := func(what string, id eqClassID) {
		if len(errs) < 10 {
			errs = append(errs, fmt.Errorf("%s names class %d, which is not a live root", what, id))
		}
	}
	live := func(id eqClassID) bool {
		return id != 0 && int(id) < len(c.egraphParents) && c.egraphParents[id] == id && c.egraphUses[id] > 0
	}
	for dig, id := range c.egraphDigestToClass {
		if !live(id) || !c.eqClassToDigests[id].Contains(dig) {
			bad("digest "+dig, id)
		}
	}
	checkClassKeys(bad, live, "the digests table", c.eqClassToDigests)
	checkClassKeys(bad, live, "the extra digests table", c.eqClassExtraDigests)
	checkClassKeys(bad, live, "the input terms table", c.inputEqClassToTerms)
	checkClassKeys(bad, live, "the output terms table", c.outputEqClassToTerms)
	checkClassKeys(bad, live, "the class entries table", c.outputEqClassResults)
	for termID, term := range c.egraphTerms {
		if term == nil {
			continue
		}
		for _, in := range term.inputEqIDs {
			// 0 is an input with no digest, which names no class.
			if in != 0 && !live(in) {
				bad(fmt.Sprintf("term %d's input", termID), in)
			}
		}
		if !live(term.outputEqID) {
			bad(fmt.Sprintf("term %d's output", termID), term.outputEqID)
		}
	}
	for resID, ids := range c.resultOutputEqClasses {
		for id := range ids.All() {
			if !live(id) {
				bad(fmt.Sprintf("entry %d", resID), id)
			}
		}
	}
	if c.changes != nil {
		checkClassKeys(bad, live, "the change marks", c.changes.left)
		checkClassKeys(bad, live, "the change marks", c.changes.leftNamed)
		for _, id := range c.changes.joined {
			if !live(id) {
				bad("the change marks' joins", id)
			}
		}
	}
	return errors.Join(errs...)
}

// eqClassSlotErrorsLocked is verifyEqClassSlotsLocked's check of the slots:
// every slot is free or its own root, a free one is on the free list once,
// and every class is used. Requires egraphMu.
func (c *Cache) eqClassSlotErrorsLocked() []error {
	var errs []error
	free := make(map[eqClassID]bool, len(c.freeEqClassIDs))
	for _, id := range c.freeEqClassIDs {
		if int(id) >= len(c.egraphParents) || c.egraphParents[id] != 0 || free[id] {
			errs = append(errs, fmt.Errorf("free list: class %d is in use or listed twice", id))
			continue
		}
		free[id] = true
	}
	for i := 1; i < len(c.egraphParents); i++ {
		id := eqClassID(i)
		switch parent := c.egraphParents[id]; {
		case parent == 0 && !free[id]:
			errs = append(errs, fmt.Errorf("class %d is free but not on the free list", id))
		case parent != 0 && parent != id:
			errs = append(errs, fmt.Errorf("class %d was joined into %d and not freed", id, parent))
		case parent == id && c.egraphUses[id] == 0:
			errs = append(errs, fmt.Errorf("class %d has no use and was not freed", id))
		}
	}
	return errs
}

// checkClassKeys reports to bad each key of table, named what, that is not
// a live class.
func checkClassKeys[V any](bad func(string, eqClassID), live func(eqClassID) bool, what string, table map[eqClassID]V) {
	for id := range table {
		if !live(id) {
			bad(what, id)
		}
	}
}
