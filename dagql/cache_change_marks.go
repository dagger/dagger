package dagql

import "slices"

// changeMarks are the holdings and entries a blob-backed cache's writes have
// changed since the last write handed them back (takeChangesLocked), for its
// reader to ask again of only those (RemoteChange.Holdings and Entries).
//
// An entry is marked where anything a reader asks of it changes: its record,
// stored parts or expiry, its recipes, its holdings, its classes' members,
// and its creation. An entry's part answers rest on its classes' other
// entries too (AvailablePart), so each marked entry is widened to every
// entry of its classes when the marks are handed back. Classes only grow
// until an entry is removed, so widening then reaches every entry whose
// class the change touched, but for a removed entry's: its class mates are
// marked as it leaves its classes (markLeavingLocked), while the classes
// still name them.
//
// A holding is marked where any field of its description
// (RemoteHoldingInfo) changes, and where it is created or removed.
//
// Only a blob-backed cache marks: an engine's own cache has no reader to
// tell, and its changes is nil. Guarded by egraphMu.
type changeMarks struct {
	// entries are widened to their classes when handed back, and alone the
	// entries marked as is: removed entries and a leaving entry's class
	// mates.
	entries  map[sharedResultID]struct{}
	alone    map[sharedResultID]struct{}
	holdings map[HolderKey]struct{}
}

// markEntryLocked marks res, and its classes' entries when the marks are
// handed back. Requires egraphMu for writing.
func (c *Cache) markEntryLocked(res *sharedResult) {
	if c.changes == nil || res == nil {
		return
	}
	if c.changes.entries == nil {
		c.changes.entries = map[sharedResultID]struct{}{}
	}
	c.changes.entries[res.id] = struct{}{}
}

// markLeavingLocked marks an entry that is leaving its classes, before it
// leaves them: the entry, and every other entry of its classes, whose part
// answers it may have served. Requires egraphMu for writing.
func (c *Cache) markLeavingLocked(id sharedResultID) {
	if c.changes == nil {
		return
	}
	if c.changes.alone == nil {
		c.changes.alone = map[sharedResultID]struct{}{}
	}
	c.changes.alone[id] = struct{}{}
	for root := range c.outputEqClassRootsLocked(id) {
		for member := range c.outputEqClassResults[root] {
			c.changes.alone[member] = struct{}{}
		}
	}
}

// markHoldingLocked marks the holding key. Requires egraphMu for writing.
func (c *Cache) markHoldingLocked(key HolderKey) {
	if c.changes == nil {
		return
	}
	if c.changes.holdings == nil {
		c.changes.holdings = map[HolderKey]struct{}{}
	}
	c.changes.holdings[key] = struct{}{}
}

// takeChangesLocked hands the marks back in change, sorted, and clears them:
// each marked holding, and each marked entry with every entry of its
// classes. A write takes them at its end, under the hold that made them, so
// each is handed back by the write that made it. Requires egraphMu for
// writing.
func (c *Cache) takeChangesLocked(change *RemoteChange) {
	if c.changes == nil {
		return
	}
	marks := c.changes
	entries := marks.alone
	if entries == nil {
		entries = make(map[sharedResultID]struct{}, len(marks.entries))
	}
	widened := map[eqClassID]struct{}{}
	for id := range marks.entries {
		entries[id] = struct{}{}
		for root := range c.outputEqClassRootsLocked(id) {
			if _, ok := widened[root]; ok {
				continue
			}
			widened[root] = struct{}{}
			for member := range c.outputEqClassResults[root] {
				entries[member] = struct{}{}
			}
		}
	}
	for id := range entries {
		change.Entries = append(change.Entries, uint64(id))
	}
	slices.Sort(change.Entries)
	change.Entries = slices.Compact(change.Entries)
	for key := range marks.holdings {
		change.Holdings = append(change.Holdings, key)
	}
	slices.SortFunc(change.Holdings, compareHolderKeys)
	change.Holdings = slices.CompactFunc(change.Holdings, func(a, b HolderKey) bool { return compareHolderKeys(a, b) == 0 })
	*marks = changeMarks{}
}

// changesPendingLocked reports whether any mark waits to be handed back.
// Requires egraphMu.
func (c *Cache) changesPendingLocked() bool {
	return c.changes != nil && len(c.changes.entries)+len(c.changes.alone)+len(c.changes.holdings) > 0
}
