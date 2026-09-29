package dagql

import "iter"

// An engine's Cloud holding is what one of its entries knows about its
// counterpart in the Cloud cache: the Cloud entry the latest message named,
// whether it stores a record and that record's expiry, and the Cloud's
// complete parts, each with the offer that downloads it. An engine keeps at
// most one per entry, in holders under cloudCacheID.
//
// Unlike the holdings the Cloud keeps of engine caches, it owns nothing: it
// adds no ownership unit to its entry, is not in holderEntries, and no
// collection pass looks at it. Sessions, retention edges and dependents own
// engine entries as they always have. It goes with its entry.

// cloudHoldingLocked returns res's Cloud holding and its key, if it has one.
// Requires egraphMu.
func (res *sharedResult) cloudHoldingLocked() (HolderKey, *holding) {
	for key, h := range res.holders {
		if key.Cache == cloudCacheID {
			return key, h
		}
	}
	return HolderKey{}, nil
}

// noteCloudCopyLocked records res's Cloud counterpart as the latest message
// about it names it: the Cloud entry number, whether that entry stores a
// record, and the record's expiry (0 for none). The one Cloud holding takes
// them, re-keyed when an earlier message named another number. Requires
// egraphMu.
func (res *sharedResult) noteCloudCopyLocked(number uint64, stored bool, expiresAtUnix int64) {
	key, h := res.cloudHoldingLocked()
	if h == nil {
		h = &holding{}
	} else if key.Number != number {
		// A later message names another Cloud entry, as after a service
		// restart: the one holding moves to it, with its offers.
		delete(res.holders, key)
	}
	if res.holders == nil {
		res.holders = make(map[HolderKey]*holding)
	}
	res.holders[HolderKey{Cache: cloudCacheID, Number: number}] = h
	h.unstored = !stored
	if !stored {
		expiresAtUnix = 0
	}
	h.expiresAtUnix = expiresAtUnix
}

// partOfferLocked returns the Cloud's offer of res's part at key, or nil.
// Requires egraphMu.
func (res *sharedResult) partOfferLocked(key string) *partOffer {
	_, h := res.cloudHoldingLocked()
	if h == nil {
		return nil
	}
	return h.parts[key].offer
}

// partOffersLocked yields the Cloud's offers of res's parts by part address
// key. A caller may retire the offer it is given. Requires egraphMu.
func (res *sharedResult) partOffersLocked() iter.Seq2[string, *partOffer] {
	return func(yield func(string, *partOffer) bool) {
		_, h := res.cloudHoldingLocked()
		if h == nil {
			return
		}
		for key, part := range h.parts {
			if part.offer != nil && !yield(key, part.offer) {
				return
			}
		}
	}
}

// hasPartOffersLocked reports whether the Cloud offers res any part.
// Requires egraphMu.
func (res *sharedResult) hasPartOffersLocked() bool {
	for range res.partOffersLocked() {
		return true
	}
	return false
}
