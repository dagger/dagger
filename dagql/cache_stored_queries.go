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
	// PartUploading: neither stored nor absent, and an upload of the part is
	// in progress for the entry or an entry of its class (MarkPartUploading).
	PartUploading
)

// PartAvailability is AvailablePart's answer. Donor is the number of the
// entry whose stored part or record it is. ValidUntilUnix is the soonest
// moment, after the clock it was read at, that one of the entries the answer
// considered expires (0: none does): before it, only a write can change the
// answer, and the write's change names the entry (RemoteChange.Entries).
//
// It carries no part: its readers read what the cache has, from whom and
// until when, and a stored part's contents reach a reader in a stored bundle
// (StoredBundle).
type PartAvailability struct {
	State          PartAvailabilityState
	Donor          uint64
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
//   - uploading: an upload of the part is in progress for the entry or an
//     entry of its class, whether or not that entry stores a value or has
//     expired (MarkPartUploading), as its reader's claim counts it. The
//     lowest-numbered such entry is the donor. An upload's start and end
//     move no ValidUntilUnix: its reader names the class at each
//     (ClassChange, EndPartUploads);
//   - otherwise unavailable.
//
// Among the class's other entries, the lowest number answers first. Only a
// blob-backed cache answers.
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
	return c.availablePartLocked(number, key, nowUnix)
}

// PartQuery is a part of an entry a reader asks about (AvailableParts).
type PartQuery struct {
	Number  uint64
	Address PersistedPartAddress
}

// PartAnswer is AvailablePart's answer for one PartQuery, or the error it
// answered with.
type PartAnswer struct {
	PartAvailability
	Err error
}

// AvailableParts is AvailablePart for each of queries at nowUnix, in their
// order, read under one hold. A reader asking about many parts waits for the
// cache's lock once, not once a part, as EntriesFacts does for entries'
// facts: a writer waiting for the lock stops every new reader, so a reader
// that took it once a part waited for every writer queued between two of its
// parts. A writer then waits for the whole read.
func (c *Cache) AvailableParts(queries []PartQuery, nowUnix int64) []PartAnswer {
	out := make([]PartAnswer, len(queries))
	if !c.blobBacked {
		for i := range out {
			out[i].Err = fmt.Errorf("available part: the cache has no blob store")
		}
		return out
	}
	keys := make([]string, len(queries))
	for i, q := range queries {
		key, err := partAddressKey(q.Address)
		if err != nil {
			out[i].Err = fmt.Errorf("available part: %w", err)
			continue
		}
		keys[i] = key
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for i, q := range queries {
		if out[i].Err == nil {
			out[i].PartAvailability, out[i].Err = c.availablePartLocked(q.Number, keys[i], nowUnix)
		}
	}
	return out
}

// availablePartLocked is AvailablePart's answer for the part key of the
// entry numbered number. Requires egraphMu.
func (c *Cache) availablePartLocked(number uint64, key string, nowUnix int64) (PartAvailability, error) {
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return PartAvailability{}, fmt.Errorf("available part of %d: %w", number, ErrUnknownEntry)
	}
	// Only the part's holders in the class answer for it: the members that
	// store it, or whose stored record marks it absent (holdsPartLocked).
	// Whether a member holds it is asked first: most members of a large class
	// hold nothing, and the checks after it walk a member's holdings.
	candidates := []*sharedResult{res}
	var others []sharedResultID
	for root := range c.outputEqClassRootsLocked(res.id) {
		for id := range c.outputEqClassResults[root] {
			other := c.resultsByID[id]
			if other == nil || other == res || !other.holdsPartLocked(key) || other.noValueLocked() || c.resultExpiredAtLocked(other, nowUnix) {
				continue
			}
			others = append(others, id)
		}
	}
	slices.Sort(others)
	for _, id := range slices.Compact(others) {
		candidates = append(candidates, c.resultsByID[id])
	}
	// Time alone changes the answer only when a holder expires: the entry
	// itself, or another holder of the class, which then stops answering.
	// Expired ones never come back but by a write, and a member that holds
	// nothing never answers, whenever it expires.
	var validUntil int64
	for _, candidate := range candidates {
		if candidate == res && !res.holdsPartLocked(key) {
			continue
		}
		if expires := candidate.expiresAtUnix; expires > nowUnix && (validUntil == 0 || expires < validUntil) {
			validUntil = expires
		}
	}
	for _, candidate := range candidates {
		if candidate == res && c.resultExpiredAtLocked(res, nowUnix) {
			continue
		}
		if _, ok := candidate.storedParts[key]; ok {
			return PartAvailability{State: PartStored, Donor: uint64(candidate.id), ValidUntilUnix: validUntil}, nil
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
	// An upload in progress answers after stored and absent: a reader needs
	// neither, whatever uploads. The count is read a part at a time, so a
	// reader counting an upload never waits for a batch of answers.
	c.uploadsMu.Lock()
	donor, uploading := c.classUploadLocked(res, key)
	c.uploadsMu.Unlock()
	if uploading {
		return PartAvailability{State: PartUploading, Donor: uint64(donor), ValidUntilUnix: validUntil}, nil
	}
	return PartAvailability{ValidUntilUnix: validUntil}, nil
}

// classUploadLocked is the lowest-numbered entry of res's class, res
// included, with an upload of the part whose address key is key in progress
// (MarkPartUploading), whether or not it stores a value or has expired: an
// upload counts for its class as its reader's claim does. A caller that
// decides by the answer, as a start that counts an upload only where none is
// in progress, decides under the same take of uploadsMu. Requires egraphMu
// and uploadsMu.
func (c *Cache) classUploadLocked(res *sharedResult, key string) (sharedResultID, bool) {
	if len(c.uploads) == 0 {
		return 0, false
	}
	var donor sharedResultID
	found := false
	consider := func(id sharedResultID) {
		if c.uploads[id][key] > 0 && (!found || id < donor) {
			donor, found = id, true
		}
	}
	consider(res.id)
	for root := range c.outputEqClassRootsLocked(res.id) {
		for id := range c.outputEqClassResults[root] {
			if c.resultsByID[id] != nil {
				consider(id)
			}
		}
	}
	return donor, found
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

// StoredParts returns the parts the cache stores for the entry numbered
// number, as StoredState names them, sorted by their address keys: none for
// an entry that stores none or that the cache doesn't have. It reads that
// entry alone, so a reader asks for one entry's parts without walking the
// cache, and without keeping a copy of every entry's that has to change with
// it.
func (c *Cache) StoredParts(number uint64) []PersistedPartAddress {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return nil
	}
	var parts []PersistedPartAddress
	for _, part := range res.storedPartsLocked() {
		parts = append(parts, part.address())
	}
	return parts
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
	// stored parts name, each at the largest size they give it, as the disk
	// stage measures them.
	PoolBlobs     int
	PoolBlobBytes int64
	// Entries, Terms, ClassSlots and Holdings are the counts the service's
	// memory estimate weighs.
	Entries    int
	Terms      int
	ClassSlots int
	Holdings   int
}

// BlobCacheUsage reads what the cache holds, as its limits count it, under
// one hold of egraphMu for reading: the pool's measure as the cache keeps it
// (cache_pool_measure.go), and the counts beside it. It walks nothing. Only
// a blob-backed cache answers.
func (c *Cache) BlobCacheUsage() (BlobCacheUsage, error) {
	if !c.blobBacked {
		return BlobCacheUsage{}, fmt.Errorf("blob cache usage: the cache has no blob store")
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	usage := BlobCacheUsage{
		PoolValues:    c.pool.values,
		PoolBytes:     c.pool.bytes,
		PoolBlobs:     len(c.pool.blobs),
		PoolBlobBytes: c.pool.blobBytes,
		Entries:       len(c.resultsByID),
		Terms:         len(c.egraphTerms),
		ClassSlots:    c.eqClassSlotsLocked(),
	}
	for _, state := range c.remoteCaches {
		usage.Holdings += state.holdings
	}
	return usage, nil
}
