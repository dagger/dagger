package dagql

// A part that no source supplies is rebuilt by running its operation or call
// again. That run is a second execution of the entry's recipe, while the
// cache still holds what came with the first: the entry's other parts that
// one execution writes together, and the merged entries computed from it. The
// local cache never holds two executions of one recipe this way: an entry
// keeps everything it depends on, with its content, for as long as it lives.
// The counts below say how much of the first execution a rebuild leaves beside
// the second, so that a run's logs show how often the two are combined.

// rebuildMixCountLimit bounds the walk over an entry's dependents: the count
// reads "at least" past it.
const rebuildMixCountLimit = 4096

// mergedDependentsLocked counts the merged entries that depend on row,
// directly or through other entries, up to limit, and reports whether the
// walk stopped at the limit. A merged entry was computed on another engine
// from row's first execution. Requires egraphMu at least for read.
func (c *Cache) mergedDependentsLocked(row *sharedResult, limit int) (int, bool) {
	if row == nil || row.depParents == nil {
		return 0, false
	}
	seen := map[sharedResultID]struct{}{row.id: {}}
	queue := []*sharedResult{row}
	count := 0
	for len(queue) > 0 {
		res := queue[0]
		queue = queue[1:]
		if res.depParents == nil {
			continue
		}
		for parentID := range res.depParents.Items() {
			if _, ok := seen[parentID]; ok {
				continue
			}
			seen[parentID] = struct{}{}
			if len(seen) > limit {
				return count, true
			}
			parent := c.resultsByID[parentID]
			if parent == nil {
				continue
			}
			if parent.imported {
				count++
			}
			queue = append(queue, parent)
		}
	}
	return count, false
}

// keptParts counts the parts that probes report complete, other than the
// rebuilt one (by part address key), among writeSet, or among all the
// entry's parts when writeSet is nil: a rebuild installs only the parts not
// yet complete, so these stay from the first execution beside the rebuilt
// one.
func keptParts(probes []PartProbe, writeSet []PersistedPartAddress, rebuiltKey string) int {
	var inSet map[string]bool
	if writeSet != nil {
		inSet = map[string]bool{}
		for _, address := range writeSet {
			if key, err := partAddressKey(address); err == nil {
				inSet[key] = true
			}
		}
	}
	counted := map[string]bool{rebuiltKey: true}
	kept := 0
	for _, probe := range probes {
		key, err := partAddressKey(probe.Descriptor.Address)
		if err != nil || counted[key] || !probe.LocalComplete || (inSet != nil && !inSet[key]) {
			continue
		}
		counted[key] = true
		kept++
	}
	return kept
}
