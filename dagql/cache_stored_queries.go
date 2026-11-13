package dagql

import (
	"fmt"
	"maps"
	"slices"

	"github.com/opencontainers/go-digest"
)

// EquivalentEntries returns the numbers of the entries in the class that dig
// names, sorted, read under one hold. It is nil when no class has the digest.
// An entry's number stays its own across the classes' compactions, so the
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

// TermEntries returns the numbers of the entries a call's term matches, as
// the structural lookup matches it: its self digest over its inputs'
// classes. They are the entries of the matching terms' output classes,
// sorted, read under one hold, as EquivalentEntries names a class's. It is
// nil when an input has no class or no term matches.
func (c *Cache) TermEntries(self digest.Digest, inputs []digest.Digest) []uint64 {
	if self == "" {
		return nil
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	ids, missing := c.termInputClassesLocked(inputs)
	if missing >= 0 {
		return nil
	}
	termSet := c.egraphTermsByTermDigest[calcEgraphTermDigest(self, ids)]
	if termSet == nil {
		return nil
	}
	seen := map[eqClassID]struct{}{}
	var numbers []uint64
	for termID := range termSet.Items() {
		term := c.egraphTerms[termID]
		if term == nil {
			continue
		}
		root := c.eqClassRootLocked(term.outputEqID)
		if _, ok := seen[root]; ok || root == 0 {
			continue
		}
		seen[root] = struct{}{}
		for id := range c.outputEqClassResults[root] {
			if c.resultsByID[id] != nil {
				numbers = append(numbers, uint64(id))
			}
		}
	}
	slices.Sort(numbers)
	return slices.Compact(numbers)
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
// PartStored. ValidUntilUnix is the soonest moment, after the clock it was
// read at, that one of the entries the answer considered expires (0: none
// does): before it, only a write can change the answer, and the write's
// change names the entry (RemoteChange.Entries).
type PartAvailability struct {
	State          PartAvailabilityState
	Donor          uint64
	Part           PersistedPartOffer
	ValidUntilUnix int64
}

// AvailablePart reports what the cache has of a part of the entry numbered
// number at nowUnix, the reader's clock, read under one hold. A reader that
// judges other facts of the cache by its own clock asks at that same moment,
// so its answers agree with each other:
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
func (c *Cache) AvailablePart(number uint64, address PersistedPartAddress, nowUnix int64) (PartAvailability, error) {
	if !c.blobBacked {
		return PartAvailability{}, fmt.Errorf("available part: the cache has no blob store")
	}
	key, err := partAddressKey(address)
	if err != nil {
		return PartAvailability{}, fmt.Errorf("available part: %w", err)
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return PartAvailability{}, fmt.Errorf("available part of %d: %w", number, ErrUnknownEntry)
	}
	candidates := []*sharedResult{res}
	var others []sharedResultID
	for root := range c.outputEqClassRootsLocked(res.id) {
		for id := range c.outputEqClassResults[root] {
			other := c.resultsByID[id]
			if other == nil || other == res || other.noValueLocked() || c.resultExpiredAtLocked(other, nowUnix) {
				continue
			}
			others = append(others, id)
		}
	}
	slices.Sort(others)
	for _, id := range slices.Compact(others) {
		candidates = append(candidates, c.resultsByID[id])
	}
	// Time alone changes the answer only when a considered value expires: the
	// entry's own, or another of the class, which then stops answering.
	// Expired ones never come back but by a write.
	var validUntil int64
	for _, candidate := range candidates {
		if expires := candidate.expiresAtUnix; expires > nowUnix && (validUntil == 0 || expires < validUntil) {
			validUntil = expires
		}
	}
	for _, candidate := range candidates {
		if candidate == res && c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		if part, ok := candidate.storedParts[key]; ok {
			return PartAvailability{State: PartStored, Donor: uint64(candidate.id), Part: part.offer(), ValidUntilUnix: validUntil}, nil
		}
	}
	for _, candidate := range candidates {
		if candidate.persistedEnvelope == nil {
			continue
		}
		declared, err := candidate.declaredPartsLocked()
		if err != nil {
			continue
		}
		for _, part := range declared {
			if part.key == key && part.state == "absent" {
				return PartAvailability{State: PartAbsent, Donor: uint64(candidate.id), ValidUntilUnix: validUntil}, nil
			}
		}
	}
	return PartAvailability{ValidUntilUnix: validUntil}, nil
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
			state.Parts[uint64(id)] = append(state.Parts[uint64(id)], part.address())
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

// LivePacks returns every pack a stored part's chain names, in the pool or
// not, sorted: the packs a sweep of the blob store keeps. Only a blob-backed
// cache answers.
func (c *Cache) LivePacks() ([]digest.Digest, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("live packs: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	live := map[digest.Digest]struct{}{}
	for _, res := range c.resultsByID {
		for _, part := range res.storedParts {
			for _, layer := range part.layers {
				if layer.pack != "" {
					live[layer.pack] = struct{}{}
				}
			}
		}
	}
	return slices.Sorted(maps.Keys(live)), nil
}

// LivePackMembers maps every layer a stored part's chain stores in a pack,
// in the pool or not, to that pack: the members the service knows to be
// present through their packs. A layer that stored parts name in more than
// one pack maps to the lowest pack ID. Only a blob-backed cache answers.
func (c *Cache) LivePackMembers() (map[digest.Digest]digest.Digest, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("live pack members: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	members := map[digest.Digest]digest.Digest{}
	for _, res := range c.resultsByID {
		for _, part := range res.storedParts {
			for _, layer := range part.layers {
				blob, pack := layer.layer.Descriptor.Digest, layer.pack
				if pack == "" {
					continue
				}
				if current, ok := members[blob]; !ok || pack < current {
					members[blob] = pack
				}
			}
		}
	}
	return members, nil
}

// BlobCacheUsage is what a blob-backed cache holds, as its limits count it.
type BlobCacheUsage struct {
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
	// memory estimate weighs.
	Entries    int
	Terms      int
	ClassSlots int
	Holdings   int
}

// BlobCacheUsage measures the cache. Only a blob-backed cache answers.
//
// The service reads it for every status request and metrics scrape, outside
// its work lock, so the pool's walk is a read in pieces (readInPieces): it
// lets egraphMu go every readPieceSteps values and takes it again, so a
// writer waiting for the lock waits for one piece, not a walk of the whole
// pool. A value is read whole within one piece, by its number. A write that
// lands between two pieces is seen by the rest of the walk, so the figures
// are of every value as the cache held it when the walk reached it, not of
// the pool at one moment; with no write during it, the walk counts what one
// hold would. The counts beside the pool's are read at the walk's start.
func (c *Cache) BlobCacheUsage() (BlobCacheUsage, error) {
	if !c.blobBacked {
		return BlobCacheUsage{}, fmt.Errorf("blob cache usage: the cache has no blob store")
	}
	hold := c.readInPieces()
	defer hold.done()
	usage := BlobCacheUsage{
		Entries:    len(c.resultsByID),
		Terms:      len(c.egraphTerms),
		ClassSlots: c.eqClassSlotsLocked(),
	}
	for _, state := range c.remoteCaches {
		usage.Holdings += state.holdings
	}
	// The pool as storedPoolLocked walks it, from the stored roots through
	// each value's dependencies; only the pool's values name the blobs it
	// counts, each counted once. Each entry the walk takes off its stack is
	// a step, reached before or not. The stored roots are copied in the first
	// hold, as a map's iteration can't let the lock go partway: about 0.4 ms
	// for 50,000 roots and 1.6 ms for 200,000 on a c8gd machine.
	stack := make([]sharedResultID, 0, len(c.persistedEdgesByResult))
	for id := range c.persistedEdgesByResult {
		stack = append(stack, id)
	}
	visited := map[sharedResultID]struct{}{}
	poolBlobs := map[digest.Digest]struct{}{}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		hold.step()
		if _, seen := visited[id]; seen {
			continue
		}
		visited[id] = struct{}{}
		res := c.resultsByID[id]
		if res == nil || !res.hasOwnValueLocked() {
			continue
		}
		usage.PoolValues++
		usage.PoolBytes += res.storedValueBytes()
		for _, part := range res.storedParts {
			for _, layer := range part.layers {
				blob := layer.layer.Descriptor.Digest
				if _, counted := poolBlobs[blob]; !counted {
					poolBlobs[blob] = struct{}{}
					usage.PoolBlobBytes += layer.layer.Descriptor.Size
				}
			}
		}
		for dep := range res.deps {
			if _, seen := visited[dep]; !seen {
				stack = append(stack, dep)
			}
		}
	}
	usage.PoolBlobs = len(poolBlobs)
	return usage, nil
}
