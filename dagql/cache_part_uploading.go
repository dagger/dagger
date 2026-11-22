package dagql

import (
	"errors"
	"fmt"
	"slices"
)

// PartStart is StartPartUploads' answer for one part: the part's answer as
// the start read it (AvailableParts), and whether the start counted an upload
// of it. UploadedBy is, for a part the start refused because its class has
// an upload of it in progress, the lowest-numbered entry with that upload.
type PartStart struct {
	PartAnswer
	Started    bool
	UploadedBy uint64
}

// StartPartUploads starts, on a blob-backed cache, an upload of each part of
// starts for its entry, where the part is neither stored in the entry's class
// nor being uploaded for an entry of it, the entry itself included: its
// reader's claim on the part, which the cache counts until the reader ends it
// (EndPartUploads). One upload serves a class, so a second for any of its
// entries is refused, the answer naming the entry that uploads it
// (PartStart.UploadedBy). A part the cache can't answer, as one of an entry
// it doesn't have, isn't started.
//
// Each part is answered as AvailableParts answers it at nowUnix, and its
// start decided from that answer and its class's uploads under one take of
// uploadsMu, in one hold of egraphMu for reading (startPartLocked): two
// starts of class mates' parts, from any holds, refuse each other, and a
// join or a leaving, which writes, can't land between a part's answer and
// its start. A start names its entry's classes for readers in that hold
// (classChangeLocked), so a reader that keeps answers asks them again, as
// the end does. Between two parts, the batch gives way to a writer that
// waits (giveWayLocked), as AvailableParts does: the writer waits for the
// part in progress, not the batch, and a later part's answer counts what
// landed while the batch let go, other readers' starts included.
//
// An entry with an upload in progress counts as holding a part for its class
// (holdsAnyPartLocked): a join brings the upload to the other side's
// entries, and the entry's leaving takes it from its class mates, so those
// name them. The counts live in memory only, as the reader's claims do.
func (c *Cache) StartPartUploads(starts []PartQuery, nowUnix int64) ([]PartStart, RemoteChange) {
	out := make([]PartStart, len(starts))
	if len(starts) == 0 {
		return out, RemoteChange{}
	}
	if !c.blobBacked {
		for i := range out {
			out[i].Err = errors.New("start part uploads: the cache has no blob store")
		}
		return out, RemoteChange{}
	}
	keys := make([]string, len(starts))
	for i, q := range starts {
		key, err := partAddressKey(q.Address)
		if err != nil {
			out[i].Err = fmt.Errorf("start part upload: %w", err)
			continue
		}
		keys[i] = key
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var change RemoteChange
	for i, q := range starts {
		if out[i].Err == nil {
			change.Entries = append(change.Entries, c.startPartLocked(q, keys[i], nowUnix, &out[i])...)
		}
		if c.testBatchAnswered != nil {
			c.testBatchAnswered(i)
		}
		if i+1 < len(starts) {
			c.giveWayLocked()
		}
	}
	slices.Sort(change.Entries)
	change.Entries = slices.Compact(change.Entries)
	return out, change
}

// startPartLocked answers q's part, of key, into out as StartPartUploads
// does, and counts an upload of it where its class has it neither stored nor
// uploading: its answer, check and count are one step, read in the caller's
// one hold of egraphMu and decided under one take of uploadsMu. It returns
// the entries a start names (classChangeLocked), none when it starts
// nothing. Requires egraphMu.
func (c *Cache) startPartLocked(q PartQuery, key string, nowUnix int64, out *PartStart) []uint64 {
	res := c.resultsByID[sharedResultID(q.Number)]
	if res == nil {
		out.Err = fmt.Errorf("start part upload of %d: %w", q.Number, ErrUnknownEntry)
		return nil
	}
	av, held := c.heldPartLocked(res, key, nowUnix)
	out.PartAvailability = av
	if held && av.State == PartStored {
		return nil
	}
	c.uploadsMu.Lock()
	if donor, uploading := c.classUploadLocked(res, key); uploading {
		out.UploadedBy = uint64(donor)
		if !held {
			out.State, out.Donor = PartUploading, uint64(donor)
		}
	} else {
		c.countUploadLocked(res.id, key)
		out.Started = true
	}
	c.uploadsMu.Unlock()
	if !out.Started {
		return nil
	}
	return c.classChangeLocked([]uint64{q.Number}).Entries
}

// countUploadLocked counts an upload of the part key for the entry id.
// Requires uploadsMu.
func (c *Cache) countUploadLocked(id sharedResultID, key string) {
	keys := c.uploads[id]
	if keys == nil {
		if c.uploads == nil {
			c.uploads = map[sharedResultID]map[string]int{}
		}
		keys = map[string]int{}
		c.uploads[id] = keys
	}
	keys[key]++
}

// UploadCounts returns, on a blob-backed cache, how many uploads its reader
// started and hasn't ended for each entry (StartPartUploads,
// EndPartUploads), for the reader to compare with the uploads it knows of:
// one it doesn't know of is a count left behind, which answers uploading
// until the cache is opened again.
func (c *Cache) UploadCounts() map[uint64]int {
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	counts := make(map[uint64]int, len(c.uploads))
	for id, keys := range c.uploads {
		for _, n := range keys {
			counts[uint64(id)] += n
		}
	}
	return counts
}

// PartUpload is an upload of the part at Address for the entry numbered
// Number, as a blob-backed cache's reader starts and ends it
// (StartPartUploads, EndPartUploads).
type PartUpload struct {
	Number  uint64
	Address PersistedPartAddress
}

// EndPartUploads notes, on a blob-backed cache, that uploads its reader
// started (StartPartUploads) ended, and names, for the reader, each entry
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
// for the entry id (StartPartUploads). It takes uploadsMu.
func (c *Cache) uploadingAny(id sharedResultID) bool {
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	return len(c.uploads[id]) > 0
}

// PartUploading reports whether an upload of the part at address is marked in
// progress for the entry numbered number (StartPartUploads).
func (c *Cache) PartUploading(number uint64, address PersistedPartAddress) bool {
	key, err := partAddressKey(address)
	if err != nil {
		return false
	}
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()
	return c.uploads[sharedResultID(number)][key] > 0
}

// classChangeLocked names, for a reader, each entry numbered numbers and
// every entry of its classes, sorted: what an upload's start or end changes
// for an entry's class mates, whose answers count the class's uploads. An
// entry the cache doesn't have is skipped. Requires egraphMu.
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
