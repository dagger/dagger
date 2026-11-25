package dagql

import (
	"errors"
	"fmt"
)

// The classes' uses, kept.
//
// A class is live while a live term or an entry uses it: a term uses the
// class of each of its inputs and of its output, and an entry uses each of
// its output classes. Compaction keeps the live classes and frees every
// other slot (compactEqClassesModeLocked), once the slots are at least twice
// the live classes. The cache keeps the uses as they change, so that knowing
// how many classes are live walks nothing, and a check that frees nothing
// costs nothing.
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
// (removeResultOutputEqClassesLocked), a compaction, which counts them again
// from its own walk, and a reset. A repair moves a term's inputs to their
// roots, which changes no class's uses. verifyEqClassUses checks the kept
// uses against a walk.

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

// unuseEqClassLocked counts one use of id's class fewer. Requires egraphMu
// for writing.
func (c *Cache) unuseEqClassLocked(id eqClassID) {
	root := c.findEqClassLocked(id)
	if root == 0 {
		return
	}
	c.egraphUses[root]--
	if c.egraphUses[root] == 0 {
		c.liveEqClasses--
	}
}

// useTermLocked counts the uses of a term made or imported: its inputs' and
// its output's classes. Requires egraphMu for writing.
func (c *Cache) useTermLocked(term *egraphTerm) {
	for _, in := range term.inputEqIDs {
		c.useEqClassLocked(in)
	}
	c.useEqClassLocked(term.outputEqID)
}

// unuseTermLocked takes away a removed term's uses. Requires egraphMu for
// writing.
func (c *Cache) unuseTermLocked(term *egraphTerm) {
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

// walkEqClassUsesLocked counts each root's uses by walking every live term
// and every entry's output classes, as compaction counted the live classes
// before the cache kept them. Requires egraphMu for writing: it compresses
// the union-find's paths.
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
