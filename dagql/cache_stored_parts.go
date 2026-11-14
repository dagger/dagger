package dagql

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

// A blob-backed cache, the Cloud's, keeps its entries' parts as layer chains
// in its own blob store, not as snapshots. A stored part is that cache's local
// completion of the part, as a settled part is an engine's: it belongs to the
// entry's current value, and is available while that value has not expired.

// WithBlobStore marks the cache blob-backed: it accepts SetStoredPart. An
// engine's cache is not.
func WithBlobStore() CacheOption {
	return func(c *Cache) {
		c.blobBacked = true
		c.storedLayers = &storedLayers{}
		c.changes = &changeMarks{}
	}
}

// SetStoredPart records that part's layer chain is in the cache's blob store,
// for the entry with the number. The part was uploaded from a copy of the
// entry's recipe that expires at copyExpiresAtUnix (0: never). It is admitted
// against the value the entry holds now, by the rule for attaching parts to
// values: a copy that has not expired is interchangeable with it, and an
// expired copy's part attaches only while the entry's value has expired too,
// as the same expired value. Storing a part adds the entries its owner and
// services name as the entry's dependencies, as installing a part does on an
// engine, so they live as long as the value; a part that names an entry the
// cache doesn't have, an entry with no value, or one that reaches the entry,
// is not stored. A named entry can have lost its value to a prune between the
// export's merge, which kept it through its sender's holding only, and this
// call: storing the part would leave a value whose bundle can never be built.
// It reports whether the part was stored, and what it changed. A metadata
// part carries no bytes and is never stored.
func (c *Cache) SetStoredPart(ctx context.Context, number uint64, part PersistedPartOffer, copyExpiresAtUnix int64) (_ bool, change RemoteChange, _ error) {
	got, change, err := c.SetStoredParts(ctx, []StoredPartWrite{{Number: number, Part: part, CopyExpiresAtUnix: copyExpiresAtUnix}})
	if err != nil {
		return false, change, err
	}
	return got[0].Stored, change, got[0].Err
}

// StoredPartWrite is one part SetStoredParts stores: the entry's number,
// the part, and the expiry of the copy it was uploaded from (0: never), as
// SetStoredPart takes them.
type StoredPartWrite struct {
	Number            uint64
	Part              PersistedPartOffer
	CopyExpiresAtUnix int64
}

// StoredPartResult is what SetStoredParts answers of one part: whether it
// was stored, and the error SetStoredPart would have returned for it alone.
type StoredPartResult struct {
	Stored bool
	Err    error
}

// SetStoredParts stores parts as SetStoredPart stores each, in order, under
// one take of the graph lock: an upload's parts wait once for the readers
// holding it, not once a part, while a long read, such as a stored closure's
// walk, holds it. Each part is admitted against the cache as the parts
// before it left it, at the clock read once the lock is held, and answered
// as SetStoredPart would have answered it alone. It returns an error, and
// stores nothing, only when the cache has no blob store or ctx has ended.
func (c *Cache) SetStoredParts(ctx context.Context, parts []StoredPartWrite) (_ []StoredPartResult, change RemoteChange, _ error) {
	if !c.blobBacked {
		return nil, change, errors.New("set stored part: the cache has no blob store")
	}
	results := make([]StoredPartResult, len(parts))
	type prepared struct {
		key    string
		stored storedPart
	}
	ready := make([]*prepared, len(parts))
	for i, w := range parts {
		key, err := partAddressKey(w.Part.Address)
		if err != nil {
			results[i].Err = fmt.Errorf("set stored part: %w", err)
			continue
		}
		if w.Part.Address.Part == "metadata" {
			results[i].Err = errors.New("set stored part: a metadata part carries no bytes")
			continue
		}
		copied, err := clonePartOffers([]PersistedPartOffer{w.Part})
		if err != nil {
			results[i].Err = fmt.Errorf("set stored part: %w", err)
			continue
		}
		// The part takes the cache's layers before the graph lock: a part
		// that isn't stored leaves them to the collector.
		ready[i] = &prepared{key: key, stored: c.storedLayers.part(copied[0])}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, change, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	defer c.takeChangesLocked(&change)
	// Admission is against the value the entry holds now, so the clock is
	// read once the graph lock is held, not while waiting for it.
	now := time.Now().Unix()
	for i, w := range parts {
		if ready[i] == nil {
			continue
		}
		results[i].Stored, results[i].Err = c.setStoredPartLocked(ctx, w.Number, ready[i].key, ready[i].stored, w.CopyExpiresAtUnix, now)
	}
	return results, change, nil
}

// setStoredPartLocked admits one part as SetStoredPart does, at now.
// Requires egraphMu for writing.
func (c *Cache) setStoredPartLocked(ctx context.Context, number uint64, key string, stored storedPart, copyExpiresAtUnix, now int64) (bool, error) {
	res := c.resultsByID[sharedResultID(number)]
	if res == nil {
		return false, fmt.Errorf("set stored part: no entry %d", number)
	}
	if res.noValueLocked() {
		return false, fmt.Errorf("set stored part: entry %d has no value", number)
	}
	copyExpired := copyExpiresAtUnix != 0 && now >= copyExpiresAtUnix
	if copyExpired && !c.resultExpiredAtLocked(res, now) {
		return false, nil
	}
	named, ok := c.storedPartDependenciesLocked(res, stored.held)
	if !ok {
		return false, nil
	}
	if res.storedParts == nil {
		res.storedParts = make(map[string]storedPart)
	}
	res.storedParts[key] = stored
	c.markEntryClassLocked(res.id, StampPartStored)
	// The Cloud serves no sessions, so there are no requirements to carry.
	c.applyPartDependenciesLocked(ctx, res, named, nil)
	return true, nil
}

// storedPartDependenciesLocked returns the entries part's owner and services
// name, or false if one is missing, has no value, or reaches res, which would
// close a cycle. The check is an engine's part commit's; the session
// requirements it also computes don't apply to the Cloud. Requires egraphMu.
func (c *Cache) storedPartDependenciesLocked(res *sharedResult, part PersistedPartOffer) ([]*sharedResult, bool) {
	ids := slices.Clone(part.Owner.DependencyIDs)
	for _, service := range part.Value.Services {
		ids = append(ids, service.ServiceResultID)
	}
	slices.Sort(ids)
	named := make([]*sharedResult, 0, len(ids))
	for _, id := range slices.Compact(ids) {
		dep := c.resultsByID[sharedResultID(id)]
		if dep == nil || !dep.hasOwnValueLocked() {
			return nil, false
		}
		named = append(named, dep)
	}
	if _, err := c.preparePartDependenciesLocked(res, named); err != nil {
		return nil, false
	}
	return named, true
}

// storedPartsLocked returns res's stored parts, sorted by part address key,
// for its saved envelope. Requires egraphMu.
func (res *sharedResult) storedPartsLocked() []storedPart {
	if len(res.storedParts) == 0 {
		return nil
	}
	parts := make([]storedPart, 0, len(res.storedParts))
	for _, key := range slices.Sorted(maps.Keys(res.storedParts)) {
		parts = append(parts, res.storedParts[key])
	}
	return parts
}

// restoreStoredPartsLocked takes a blob-backed cache's restored value's
// stored parts out of its saved envelope, onto the entry, and sets the size
// of its record as merge measured it: the call frame's JSON and the envelope's
// without them. callFrameBytes and payloadBytes are the sizes of the row's
// saved call frame and envelope. Requires egraphMu for writing.
func (c *Cache) restoreStoredPartsLocked(res *sharedResult, env *PersistedResultEnvelope, callFrameBytes, payloadBytes int) error {
	res.storedRecordBytes = int64(callFrameBytes + payloadBytes)
	if len(env.StoredParts) == 0 {
		return nil
	}
	parts := env.StoredParts
	env.StoredParts = nil
	res.storedParts = make(map[string]storedPart, len(parts))
	for _, part := range parts {
		key, err := partAddressKey(part.Address)
		if err != nil {
			return err
		}
		res.storedParts[key] = c.storedLayers.part(part)
	}
	stripped, err := json.Marshal(env)
	if err != nil {
		return err
	}
	res.storedRecordBytes = int64(callFrameBytes + len(stripped))
	return nil
}

// StoredPart names one stored part: the number of its entry, and its address.
type StoredPart struct {
	Number  uint64
	Address PersistedPartAddress
}

// DropStoredParts removes every stored part whose layer chain names the blob,
// on every entry, and returns them, sorted. The service calls it when the
// blob store no longer has the blob, so that no later bundle or offer
// carries those parts. It changes nothing else: the values, and the entries
// their parts named, stay.
func (c *Cache) DropStoredParts(blob digest.Digest) ([]StoredPart, RemoteChange, error) {
	if !c.blobBacked {
		return nil, RemoteChange{}, errors.New("drop stored parts: the cache has no blob store")
	}
	dropped, change := c.dropStoredParts(func(part storedPart) bool {
		return slices.ContainsFunc(part.layers, func(layer *storedLayer) bool {
			return layer.layer.Descriptor.Digest == blob
		})
	})
	return dropped, change, nil
}

// DropStoredPack removes every stored part whose layer chain names the pack,
// on every entry, and returns them, sorted: what DropStoredParts does for a
// blob, for a pack the blob store no longer has.
func (c *Cache) DropStoredPack(pack digest.Digest) ([]StoredPart, RemoteChange, error) {
	if !c.blobBacked {
		return nil, RemoteChange{}, errors.New("drop stored pack: the cache has no blob store")
	}
	dropped, change := c.dropStoredParts(func(part storedPart) bool {
		return slices.ContainsFunc(part.layers, func(layer *storedLayer) bool {
			return layer.pack != "" && layer.pack == pack
		})
	})
	return dropped, change, nil
}

// dropStoredParts removes every stored part that drop selects, on every
// entry, and returns them, sorted, and what it changed.
func (c *Cache) dropStoredParts(drop func(storedPart) bool) (dropped []StoredPart, change RemoteChange) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	defer c.takeChangesLocked(&change)
	for id, res := range c.resultsByID {
		for key, part := range res.storedParts {
			if !drop(part) {
				continue
			}
			delete(res.storedParts, key)
			c.markEntryClassLocked(id, StampPartDropped)
			dropped = append(dropped, StoredPart{Number: uint64(id), Address: part.address()})
		}
		if len(res.storedParts) == 0 {
			res.storedParts = nil
		}
	}
	slices.SortFunc(dropped, func(a, b StoredPart) int {
		return cmp.Or(cmp.Compare(a.Number, b.Number), comparePartAddresses(a.Address, b.Address))
	})
	return dropped, change
}
