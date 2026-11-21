package dagql

import (
	"errors"
	"fmt"
)

// MarkPartUploading notes, on a blob-backed cache, that an upload of the part
// at address for the entry numbered number started (uploading) or ended (not
// uploading): its reader's own claim on the part, which the cache stores
// nothing of but counts.
//
// The Cloud's passes keep whether a part is needed by AvailablePart and by
// whether an upload of it is in progress for any entry of the class. Such
// an upload changes that answer for every entry of the class, so the change
// names them. An entry with an upload in progress also counts as holding a
// part for its class (holdsAnyPartLocked): a join brings the upload to the
// other side's entries, and the entry's leaving takes it from its class
// mates, so those name them too. AvailablePart doesn't answer uploads: the
// reader keeps its claims and answers them. An entry the cache doesn't have
// changes nothing. The counts live in memory only, as the reader's claims do.
func (c *Cache) MarkPartUploading(number uint64, address PersistedPartAddress, uploading bool) (change RemoteChange, _ error) {
	if !c.blobBacked {
		return change, errors.New("mark part uploading: the cache has no blob store")
	}
	key, err := partAddressKey(address)
	if err != nil {
		return change, fmt.Errorf("mark part uploading: %w", err)
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	defer c.takeChangesLocked(&change)
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return change, nil
	}
	switch {
	case uploading:
		if res.uploading == nil {
			res.uploading = map[string]int{}
		}
		res.uploading[key]++
	case res.uploading[key] > 0:
		if res.uploading[key]--; res.uploading[key] == 0 {
			delete(res.uploading, key)
		}
		if len(res.uploading) == 0 {
			res.uploading = nil
		}
	default:
		return change, nil
	}
	c.markClassMatesLocked(res.id)
	return change, nil
}

// markClassMatesLocked names, for readers, the entry numbered id and every
// entry of its classes, and stamps nothing: what changed is no bundle's.
// Requires egraphMu for writing.
func (c *Cache) markClassMatesLocked(id sharedResultID) {
	if c.changes == nil {
		return
	}
	c.changes.entries = append(c.changes.entries, id)
	for eqID := range c.resultOutputEqClasses[id] {
		root := c.eqClassRootLocked(eqID)
		if root == 0 {
			continue
		}
		for member := range c.outputEqClassResults[root] {
			c.changes.entries = append(c.changes.entries, member)
		}
	}
}

// PartUploading reports whether an upload of the part at address is marked in
// progress for the entry numbered number (MarkPartUploading).
func (c *Cache) PartUploading(number uint64, address PersistedPartAddress) bool {
	key, err := partAddressKey(address)
	if err != nil {
		return false
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	return res != nil && res.uploading[key] > 0
}
