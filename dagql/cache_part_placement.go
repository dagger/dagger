package dagql

import (
	"fmt"
	"slices"
	"time"
)

// The placement rule applies wherever a part is placed on an entry other
// than the one that stored it: an offer to an engine's own entry, and a part
// a stored bundle borrows from another entry of the class. A part's value can
// name service bindings by entry number, and an offer's owner must list every
// entry the part names, so each service reference is mapped to a service
// entry the receiver has, of the same recipe or class. The owner lists
// exactly those references and nothing else: the receiving entry's own
// requirements govern every hit on it. A part with a service that can't be
// mapped is not placed.

// PlacePart applies the placement rule with place, which maps a service
// entry's number to the receiver's entry for it. It returns the part with
// its services relocated and an owner of exactly those services, or false
// when a service can't be mapped: the part is not placed.
func PlacePart(part PersistedPartOffer, place func(service uint64) (uint64, bool)) (PersistedPartOffer, bool) {
	copied, err := clonePartOffers([]PersistedPartOffer{part})
	if err != nil {
		return PersistedPartOffer{}, false
	}
	placed := copied[0]
	placed.Owner.DependencyIDs = nil
	for i, binding := range placed.Value.Services {
		mapped, ok := place(binding.ServiceResultID)
		if !ok {
			return PersistedPartOffer{}, false
		}
		placed.Value.Services[i].ServiceResultID = mapped
		placed.Owner.DependencyIDs = append(placed.Owner.DependencyIDs, mapped)
	}
	slices.Sort(placed.Owner.DependencyIDs)
	placed.Owner.DependencyIDs = slices.Compact(placed.Owner.DependencyIDs)
	return placed, true
}

// PlaceOffer places a part stored on the Cloud for an offer to the engine
// cache of receiver. Each service entry is mapped to that cache's entry for
// it: one of the cache's unexpired holdings on an entry of the service
// entry's class, which includes its recipe's, the lowest number first. An
// expired holding is an entry the engine no longer serves. The mapping reads
// the classes under one hold. It reports false when a service can't be
// mapped: the part is not placed.
func (c *Cache) PlaceOffer(receiver HolderKey, part PersistedPartOffer) (PersistedPartOffer, bool, error) {
	if receiver.Cache == "" || receiver.Cache == cloudCacheID {
		return PersistedPartOffer{}, false, fmt.Errorf("place offer: %q is not an engine cache", receiver.Cache)
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	now := time.Now().Unix()
	placed, ok := PlacePart(part, func(service uint64) (uint64, bool) {
		res := c.resultsByID[sharedResultID(service)]
		if res == nil {
			return 0, false
		}
		var best uint64
		for root := range c.outputEqClassRootsLocked(res.id) {
			for id := range c.outputEqClassResults[root] {
				member := c.resultsByID[id]
				if member == nil {
					continue
				}
				for key, h := range member.holders {
					if key.Cache != receiver.Cache || h.expiresAtUnix != 0 && h.expiresAtUnix <= now {
						continue
					}
					if best == 0 || key.Number < best {
						best = key.Number
					}
				}
			}
		}
		return best, best != 0
	})
	return placed, ok, nil
}
