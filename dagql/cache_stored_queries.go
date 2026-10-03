package dagql

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

// EquivalentEntries returns the numbers of the entries in the class that dig
// names, sorted, read under one hold. It is nil when no class has the digest.
// An entry's number stays its own across the classes' renumbering, so the
// numbers are safe to keep past the call.
func (c *Cache) EquivalentEntries(dig string) []uint64 {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	classID, ok := c.egraphDigestToClass[dig]
	if !ok {
		return nil
	}
	var numbers []uint64
	for id := range c.outputEqClassResults[c.eqClassRootLocked(classID)] {
		if c.resultsByID[id] != nil {
			numbers = append(numbers, uint64(id))
		}
	}
	slices.Sort(numbers)
	return numbers
}

// PartAvailabilityState is what a blob-backed cache has of an entry's part.
type PartAvailabilityState uint8

const (
	// PartUnavailable: the cache has neither the part's bytes nor a record
	// that shows the part carries none.
	PartUnavailable PartAvailabilityState = iota
	// PartStored: the part's layer chain is in the cache's blob store.
	PartStored
	// PartAbsent: a stored record maps the part as absent, a part with no
	// bytes, so it is never stored.
	PartAbsent
)

// PartAvailability is AvailablePart's answer. Donor is the number of the
// entry whose stored part or record it is, and Part the stored part, for
// PartStored.
type PartAvailability struct {
	State PartAvailabilityState
	Donor uint64
	Part  PersistedPartOffer
}

// AvailablePart reports what the cache has of a part of the entry numbered
// number, read under one hold with the clock:
//   - stored: the entry, while its value has not expired, or else an
//     unexpired entry of its class, has the part in its blob store: a stored
//     part is available only while the value it belongs to is;
//   - absent: the stored record of the entry, expired or not, or of an
//     unexpired entry of its class, maps the part as absent through the
//     transfer codec, read from the stored envelope, never a decoded value.
//     Absence is a property of the value's structure, not of its freshness;
//   - otherwise unavailable.
//
// Among the class's other entries, the lowest number answers first. Part is
// a copy of the stored part. Only a blob-backed cache answers.
func (c *Cache) AvailablePart(number uint64, address PersistedPartAddress) (PartAvailability, error) {
	if !c.blobBacked {
		return PartAvailability{}, fmt.Errorf("available part: the cache has no blob store")
	}
	key, err := partAddressKey(address)
	if err != nil {
		return PartAvailability{}, fmt.Errorf("available part: %w", err)
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	now := time.Now().Unix()
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return PartAvailability{}, fmt.Errorf("available part of %d: %w", number, ErrUnknownEntry)
	}
	candidates := []*sharedResult{res}
	var others []sharedResultID
	for root := range c.outputEqClassRootsLocked(res.id) {
		for id := range c.outputEqClassResults[root] {
			other := c.resultsByID[id]
			if other == nil || other == res || other.noValueLocked() || c.resultExpiredAtLocked(other, now) {
				continue
			}
			others = append(others, id)
		}
	}
	slices.Sort(others)
	for _, id := range slices.Compact(others) {
		candidates = append(candidates, c.resultsByID[id])
	}
	for _, candidate := range candidates {
		if candidate == res && c.resultExpiredAtLocked(res, now) {
			continue
		}
		if part, ok := candidate.storedParts[key]; ok {
			copied, err := clonePartOffers([]PersistedPartOffer{part})
			if err != nil {
				return PartAvailability{}, fmt.Errorf("available part: %w", err)
			}
			return PartAvailability{State: PartStored, Donor: uint64(candidate.id), Part: copied[0]}, nil
		}
	}
	for _, candidate := range candidates {
		if candidate.persistedEnvelope == nil {
			continue
		}
		outputs, err := mapTransferredOutputs(PersistedRecord{ResultID: uint64(candidate.id), Envelope: *candidate.persistedEnvelope, Call: candidate.loadResultCall()})
		if err != nil {
			continue
		}
		for _, output := range outputs {
			if outputKey, _ := partAddressKey(output.Address); outputKey == key && output.State == "absent" {
				return PartAvailability{State: PartAbsent, Donor: uint64(candidate.id)}, nil
			}
		}
	}
	return PartAvailability{}, nil
}

// StoredState is what a blob-backed cache stores: its stored roots, and each
// entry's stored parts, by entry number. The service rebuilds its own maps of
// them from it, after a restore or a prune.
type StoredState struct {
	Roots []uint64
	Parts map[uint64][]PersistedPartAddress
}

// StoredState returns what the cache stores, read under one hold. Only a
// blob-backed cache answers.
func (c *Cache) StoredState() (StoredState, error) {
	if !c.blobBacked {
		return StoredState{}, fmt.Errorf("stored state: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	state := StoredState{Parts: map[uint64][]PersistedPartAddress{}}
	for id := range c.persistedEdgesByResult {
		if res := c.resultsByID[id]; res != nil && res.hasOwnValueLocked() {
			state.Roots = append(state.Roots, uint64(id))
		}
	}
	slices.Sort(state.Roots)
	for id, res := range c.resultsByID {
		for _, part := range res.storedPartsLocked() {
			state.Parts[uint64(id)] = append(state.Parts[uint64(id)], part.Address)
		}
	}
	return state, nil
}

// LiveBlobs returns every layer blob a stored part names, in the pool or
// not, sorted: the blobs a sweep of the blob store keeps. Only a blob-backed
// cache answers.
func (c *Cache) LiveBlobs() ([]digest.Digest, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("live blobs: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	live := map[digest.Digest]struct{}{}
	for _, res := range c.resultsByID {
		for blob := range res.storedBlobsLocked() {
			live[blob] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(live)), nil
}

// CloudUsage is what a blob-backed cache holds, as its limits count it.
type CloudUsage struct {
	// PoolValues and PoolBytes are the pool as the memory stage measures
	// it: the values in the closure of the stored roots, each counted as its
	// record's bytes plus an entry's estimate.
	PoolValues int
	PoolBytes  int64
	// PoolBlobs and PoolBlobBytes are the distinct layer blobs the pool's
	// stored parts name, as the disk stage measures them.
	PoolBlobs     int
	PoolBlobBytes int64
	// Entries, Terms, ClassSlots and Holdings are the counts the service's
	// memory estimate weighs, and RecordBytes the bytes of every stored
	// record, in the pool or not.
	Entries     int
	Terms       int
	ClassSlots  int
	Holdings    int
	RecordBytes int64
}

// CloudUsage measures the cache, read under one hold. Only a blob-backed
// cache answers.
func (c *Cache) CloudUsage() (CloudUsage, error) {
	if !c.blobBacked {
		return CloudUsage{}, fmt.Errorf("cloud usage: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	pool := c.poolEstimateLocked()
	blobs, err := c.storedBlobUsageLocked(nil)
	if err != nil {
		return CloudUsage{}, err
	}
	usage := CloudUsage{
		PoolValues:    pool.ResultCount,
		PoolBytes:     pool.EstimatedBytes,
		PoolBlobBytes: blobs.poolBytes,
		Entries:       len(c.resultsByID),
		Terms:         len(c.egraphTerms),
		ClassSlots:    c.eqClassSlotsLocked(),
		Holdings:      len(c.holderEntries),
	}
	for _, res := range c.resultsByID {
		usage.RecordBytes += res.storedRecordBytes
	}
	usage.PoolBlobs = blobs.poolBlobs
	return usage, nil
}
