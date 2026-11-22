package dagql

import (
	"errors"
	"fmt"
	"slices"
)

// MarkPartUploading notes, on a blob-backed cache, that an upload of the part
// at address for the entry numbered number started: its reader's own claim on
// the part, which the cache stores nothing of but counts until the reader
// ends it (EndPartUploads).
//
// The Cloud's passes keep whether a part is needed by AvailablePart and by
// whether an upload of it is in progress for any entry of the class, which
// depends on which entries the class has. An entry with an upload in
// progress counts as holding a part for its class (holdsAnyPartLocked): a
// join brings the upload to the other side's entries, and the entry's
// leaving takes it from its class mates, so those name them. AvailablePart
// doesn't answer uploads: the reader keeps its claims and answers them.
//
// A start changes no answer the graph gives, so it takes no hold of
// egraphMu, and names nothing: the reader names the entry's class mates
// itself, after the start, with ClassChange. A join or a leaving that lands
// between the two sees the count, or is in the class ClassChange reads. The
// counts live in memory only, as the reader's claims do, under uploadsMu: the
// reader may start an upload under a lock of its own.
func (c *Cache) MarkPartUploading(number uint64, address PersistedPartAddress) error {
	if !c.blobBacked {
		return errors.New("mark part uploading: the cache has no blob store")
	}
	key, err := partAddressKey(address)
	if err != nil {
		return fmt.Errorf("mark part uploading: %w", err)
	}
	id := sharedResultID(number)
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	keys := c.uploads[id]
	if keys == nil {
		if c.uploads == nil {
			c.uploads = map[sharedResultID]map[string]int{}
		}
		keys = map[string]int{}
		c.uploads[id] = keys
	}
	keys[key]++
	return nil
}

// PartUpload is an upload of the part at Address for the entry numbered
// Number, as a blob-backed cache's reader starts and ends it
// (MarkPartUploading, EndPartUploads).
type PartUpload struct {
	Number  uint64
	Address PersistedPartAddress
}

// EndPartUploads notes, on a blob-backed cache, that uploads its reader
// started (MarkPartUploading) ended, and names, for the reader, each entry
// and every entry of its classes, whose answers counted the upload. It names
// and ends them in one hold of egraphMu for reading, so no join or leaving
// lands between the two: one before sees the count, and names what the
// upload changed for it, or is in the classes the end names; one after needs
// to name nothing for the upload. The reader ends its own claims first, so
// what the end names answers without them. An entry the cache doesn't have
// is named by no one, its leaving having named its class mates while it
// counted the upload, and an end with no start changes no count.
func (c *Cache) EndPartUploads(ends []PartUpload) (RemoteChange, error) {
	var change RemoteChange
	if len(ends) == 0 {
		return change, nil
	}
	if !c.blobBacked {
		return change, errors.New("end part uploads: the cache has no blob store")
	}
	keys := make([]string, len(ends))
	numbers := make([]uint64, len(ends))
	var errs error
	for i, end := range ends {
		key, err := partAddressKey(end.Address)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("end part upload of entry %d: %w", end.Number, err))
		}
		keys[i], numbers[i] = key, end.Number
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	change = c.classChangeLocked(numbers)
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	for i, end := range ends {
		id, key := sharedResultID(end.Number), keys[i]
		if key == "" || c.uploads[id][key] <= 0 {
			continue
		}
		if c.uploads[id][key]--; c.uploads[id][key] == 0 {
			delete(c.uploads[id], key)
		}
		if len(c.uploads[id]) == 0 {
			delete(c.uploads, id)
		}
	}
	return change, errs
}

// uploadingAny reports whether an upload of any part is marked in progress
// for the entry id (MarkPartUploading). It takes uploadsMu.
func (c *Cache) uploadingAny(id sharedResultID) bool {
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	return len(c.uploads[id]) > 0
}

// PartUploading reports whether an upload of the part at address is marked in
// progress for the entry numbered number (MarkPartUploading).
func (c *Cache) PartUploading(number uint64, address PersistedPartAddress) bool {
	key, err := partAddressKey(address)
	if err != nil {
		return false
	}
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	return c.uploads[sharedResultID(number)][key] > 0
}

// ClassChange names, for a reader, each entry numbered numbers and every entry
// of its classes, read under one hold of egraphMu for reading: what a change
// the reader keeps outside the graph changes for an entry's class mates when
// their answers count the class's entries, as an upload's start does
// (MarkPartUploading). An entry the cache doesn't have is skipped.
func (c *Cache) ClassChange(numbers []uint64) RemoteChange {
	if len(numbers) == 0 {
		return RemoteChange{}
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.classChangeLocked(numbers)
}

// classChangeLocked is ClassChange's answer, sorted. Requires egraphMu.
func (c *Cache) classChangeLocked(numbers []uint64) RemoteChange {
	var change RemoteChange
	for _, number := range numbers {
		id := sharedResultID(number)
		if c.resultsByID[id] == nil {
			continue
		}
		change.Entries = append(change.Entries, number)
		for root := range c.outputEqClassRootsLocked(id) {
			for member := range c.outputEqClassResults[root] {
				if c.resultsByID[member] != nil {
					change.Entries = append(change.Entries, uint64(member))
				}
			}
		}
	}
	slices.Sort(change.Entries)
	change.Entries = slices.Compact(change.Entries)
	return change
}
