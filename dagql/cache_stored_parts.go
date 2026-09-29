package dagql

import (
	"context"
	"errors"
	"fmt"
	"time"
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
// as the same expired value. It reports whether the part was stored. A
// metadata part carries no bytes and is never stored.
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
	if res.storedParts == nil {
		res.storedParts = make(map[string]PersistedPartOffer)
	}
	res.storedParts[key] = copied[0]
	return true, nil
}
