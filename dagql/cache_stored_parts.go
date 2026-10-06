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

	"github.com/dagger/dagger/engine/snapshots"
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
// It reports whether the part was stored. A metadata part carries no bytes
// and is never stored.
func (c *Cache) SetStoredPart(ctx context.Context, number uint64, part PersistedPartOffer, copyExpiresAtUnix int64) (bool, error) {
	if !c.blobBacked {
		return false, errors.New("set stored part: the cache has no blob store")
	}
	key, err := partAddressKey(part.Address)
	if err != nil {
		return false, fmt.Errorf("set stored part: %w", err)
	}
	if part.Address.Part == "metadata" {
		return false, errors.New("set stored part: a metadata part carries no bytes")
	}
	copied, err := clonePartOffers([]PersistedPartOffer{part})
	if err != nil {
		return false, fmt.Errorf("set stored part: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	// Admission is against the value the entry holds now, so the clock is
	// read once the graph lock is held, not while waiting for it.
	now := time.Now().Unix()
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
	named, ok := c.storedPartDependenciesLocked(res, copied[0])
	if !ok {
		return false, nil
	}
	if res.storedParts == nil {
		res.storedParts = make(map[string]PersistedPartOffer)
	}
	res.storedParts[key] = copied[0]
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
func (res *sharedResult) storedPartsLocked() []PersistedPartOffer {
	if len(res.storedParts) == 0 {
		return nil
	}
	parts := make([]PersistedPartOffer, 0, len(res.storedParts))
	for _, key := range slices.Sorted(maps.Keys(res.storedParts)) {
		parts = append(parts, res.storedParts[key])
	}
	return parts
}

// restoreStoredPartsLocked takes a blob-backed cache's restored value's
// stored parts out of its saved envelope, onto the entry, and sets the size
// of its record as merge measured it: the call frame's JSON and the envelope's
// without them. Requires egraphMu for writing.
func restoreStoredPartsLocked(res *sharedResult, env *PersistedResultEnvelope, callFrameJSON string, payload []byte) error {
	res.storedRecordBytes = int64(len(callFrameJSON) + len(payload))
	if len(env.StoredParts) == 0 {
		return nil
	}
	parts := env.StoredParts
	env.StoredParts = nil
	res.storedParts = make(map[string]PersistedPartOffer, len(parts))
	for _, part := range parts {
		key, err := partAddressKey(part.Address)
		if err != nil {
			return err
		}
		res.storedParts[key] = part
	}
	stripped, err := json.Marshal(env)
	if err != nil {
		return err
	}
	res.storedRecordBytes = int64(len(callFrameJSON) + len(stripped))
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
func (c *Cache) DropStoredParts(blob digest.Digest) ([]StoredPart, error) {
	if !c.blobBacked {
		return nil, errors.New("drop stored parts: the cache has no blob store")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	var dropped []StoredPart
	for id, res := range c.resultsByID {
		for key, part := range res.storedParts {
			if !slices.ContainsFunc(part.Chain.Layers, func(layer snapshots.ExportLayer) bool {
				return layer.Descriptor.Digest == blob
			}) {
				continue
			}
			delete(res.storedParts, key)
			dropped = append(dropped, StoredPart{Number: uint64(id), Address: part.Address})
		}
		if len(res.storedParts) == 0 {
			res.storedParts = nil
		}
	}
	slices.SortFunc(dropped, func(a, b StoredPart) int {
		return cmp.Or(cmp.Compare(a.Number, b.Number), comparePartAddresses(a.Address, b.Address))
	})
	return dropped, nil
}
