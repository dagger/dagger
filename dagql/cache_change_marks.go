package dagql

import (
	"cmp"
	"slices"
	"strings"
)

// changeMarks are the holdings and entries a blob-backed cache's writes have
// changed since the last write handed them back (takeChangesLocked), for its
// reader to ask again of only those (RemoteChange.Holdings and Entries).
//
// An entry is marked where anything a reader asks of it changes. What an
// entry's class mates answer changes too when the entry's value, record,
// stored parts or expiry change, or its class gains it: their part answers
// consider it (AvailablePart), and their classes' entries include it. Those
// marks (markEntryClassLocked) are widened to every entry of the entry's
// classes when the marks are handed back. Its holdings and recipes are its
// own (markEntryLocked). Its holdings would reach its class mates only by
// turning whether it has a value (noValueLocked), which never happens to an
// entry that stays: an entry with no value is owned by its holdings alone,
// as a prune drops only values that holdings alone keep and a value depends
// only on entries with values, so it goes with its last engine holding
// (markLeavingLocked); and one gains its first holding only as it is
// created, which marks its class. Classes only grow
// until an entry is removed, so widening then reaches every entry whose
// class the change touched, but for a removed entry's: its class mates are
// marked as it leaves its classes (markLeavingLocked), while the classes
// still name them.
//
// A holding is marked where any field of its description
// (RemoteHoldingInfo) changes, and where it is created or removed.
//
// Marks are appended, an entry or holding as often as it changes, and sorted
// and deduplicated once, when handed back: a write's marks cost an append
// each. Only a blob-backed cache marks: an engine's own cache has no reader
// to tell, and its changes is nil. Guarded by egraphMu.
type changeMarks struct {
	// entries are marked as they are, and classes widened to their classes'
	// entries when handed back.
	entries  []sharedResultID
	classes  []sharedResultID
	holdings []HolderKey
	// left are the classes whose entries a leaving entry has marked, so
	// collecting a whole class walks it once, not once per entry.
	left map[eqClassID]struct{}
}

// markEntryLocked marks the entry numbered id: its own facts or holdings
// changed. Requires egraphMu for writing.
func (c *Cache) markEntryLocked(id sharedResultID) {
	if c.changes == nil || id == 0 {
		return
	}
	c.changes.entries = append(c.changes.entries, id)
}

// markEntryClassLocked marks the entry numbered id and, when the marks are
// handed back, every entry of its classes: its value, record, stored parts
// or expiry changed, or it joined their class. Requires egraphMu for
// writing.
func (c *Cache) markEntryClassLocked(id sharedResultID) {
	if c.changes == nil || id == 0 {
		return
	}
	c.changes.classes = append(c.changes.classes, id)
}

// markLeavingLocked marks an entry that is leaving its classes, before it
// leaves them: the entry, and every other entry of its classes, whose part
// answers it may have served. Requires egraphMu for writing.
func (c *Cache) markLeavingLocked(id sharedResultID) {
	if c.changes == nil {
		return
	}
	c.changes.entries = append(c.changes.entries, id)
	c.stampEntryLocked(c.resultsByID[id])
	for eqID := range c.resultOutputEqClasses[id] {
		root := c.eqClassRootLocked(eqID)
		if root == 0 {
			continue
		}
		if _, ok := c.changes.left[root]; ok {
			continue
		}
		if c.changes.left == nil {
			c.changes.left = map[eqClassID]struct{}{}
		}
		c.changes.left[root] = struct{}{}
		for member := range c.outputEqClassResults[root] {
			c.changes.entries = append(c.changes.entries, member)
			c.stampEntryLocked(c.resultsByID[member])
		}
	}
}

// markHoldingLocked marks the holding key. Requires egraphMu for writing.
func (c *Cache) markHoldingLocked(key HolderKey) {
	if c.changes == nil {
		return
	}
	c.changes.holdings = append(c.changes.holdings, key)
}

// takeChangesLocked hands the marks back in change, sorted, and clears them:
// each marked holding and entry, and every entry of each class-marked
// entry's classes. A write takes them at its end, under the hold that made
// them, so each is handed back by the write that made it. Requires egraphMu
// for writing.
func (c *Cache) takeChangesLocked(change *RemoteChange) {
	if !c.changesPendingLocked() {
		return
	}
	marks := c.changes
	change.Entries = slices.Grow(change.Entries, len(marks.entries)+len(marks.classes))
	for _, id := range marks.entries {
		change.Entries = append(change.Entries, uint64(id))
	}
	var widened map[eqClassID]struct{}
	for _, id := range marks.classes {
		change.Entries = append(change.Entries, uint64(id))
		c.stampEntryLocked(c.resultsByID[id])
		for eqID := range c.resultOutputEqClasses[id] {
			root := c.eqClassRootLocked(eqID)
			if root == 0 {
				continue
			}
			if _, ok := widened[root]; ok {
				continue
			}
			if widened == nil {
				widened = map[eqClassID]struct{}{}
			}
			widened[root] = struct{}{}
			for member := range c.outputEqClassResults[root] {
				change.Entries = append(change.Entries, uint64(member))
				c.stampEntryLocked(c.resultsByID[member])
			}
		}
	}
	slices.Sort(change.Entries)
	change.Entries = slices.Compact(change.Entries)
	change.Holdings = append(change.Holdings, marks.holdings...)
	slices.SortFunc(change.Holdings, compareChangedHoldings)
	change.Holdings = slices.CompactFunc(change.Holdings, func(a, b HolderKey) bool { return a == b })
	marks.entries, marks.classes, marks.holdings = marks.entries[:0], marks.classes[:0], marks.holdings[:0]
	marks.left = nil
}

// compareChangedHoldings orders a change's holdings: by number, then cache.
// Most keys differ in their numbers, and an integer compares faster than a
// cache's name.
func compareChangedHoldings(a, b HolderKey) int {
	return cmp.Or(cmp.Compare(a.Number, b.Number), strings.Compare(string(a.Cache), string(b.Cache)))
}

// clearChangesLocked drops every mark, for a cache that has just opened: no
// reader keeps answers from before an open. Requires egraphMu for writing.
func (c *Cache) clearChangesLocked() {
	if c.changes != nil {
		*c.changes = changeMarks{}
	}
}

// changesPendingLocked reports whether any mark waits to be handed back.
// Requires egraphMu.
func (c *Cache) changesPendingLocked() bool {
	return c.changes != nil && len(c.changes.entries)+len(c.changes.classes)+len(c.changes.holdings) > 0
}

// stampEntryLocked records, on a blob-backed cache, that what a stored
// bundle carries of res changed at this write: res takes the next write
// count (sharedResult.changedAt), so every bundle built before it reads res
// as changed (ChangedSince). The writes that stamp are those a bundle reads:
// a class mark (markEntryClassLocked: the entry's value, record, stored
// parts or expiry, or its class, whose parts it can borrow), an entry
// leaving its classes (markLeavingLocked), a kept call added
// (keepCallLocked), and a frame rewritten with a taught content digest
// (teachContentDigest). A holding added or removed, which marks its entry
// alone (markEntryLocked), doesn't: a bundle carries no holding. It is a
// no-op on any other cache, and for a nil res. Requires egraphMu for
// writing.
func (c *Cache) stampEntryLocked(res *sharedResult) {
	if c.changes == nil || res == nil {
		return
	}
	c.writes++
	res.changedAt = c.writes
}

// BundlePosition is the cache's write count (Cache.writes) now: a stored
// bundle built now would have it as its Position, and if it equals a
// bundle's Position, nothing the bundle carries has changed since.
func (c *Cache) BundlePosition() uint64 {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.writes
}

// changedSincePiece is how many entries ChangedSince reads in one hold of
// egraphMu.
const changedSincePiece = 1024

// ChangedSince reports, for each entry number, whether a write since
// position, a stored bundle's (StoredBundleResult.Position), changed what a
// stored bundle carries of the entry (stampEntryLocked), or the entry is
// gone. It holds egraphMu for reading changedSincePiece entries at a time.
func (c *Cache) ChangedSince(numbers []uint64, position uint64) []bool {
	changed := make([]bool, len(numbers))
	for start := 0; start < len(numbers); start += changedSincePiece {
		end := min(start+changedSincePiece, len(numbers))
		c.egraphMu.RLock()
		for i := start; i < end; i++ {
			res := c.resultsByID[sharedResultID(numbers[i])]
			changed[i] = res == nil || res.changedAt > position
		}
		c.egraphMu.RUnlock()
	}
	return changed
}
