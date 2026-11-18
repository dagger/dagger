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

// imageHoldingLocked returns res's image holding and its key, if it has one:
// the engine image ships res, and the holding carries the image's offers of
// its parts. Requires egraphMu.
func (res *sharedResult) imageHoldingLocked() (HolderKey, *holding) {
	for key, h := range res.holders {
		if key.Cache == imageCacheID {
			return key, h
		}
	}
	return HolderKey{}, nil
}

// offerHoldingLocked returns the holding whose offers supply res's parts: its
// image holding, whose content is on the engine's own disk, or else its Cloud
// holding. Requires egraphMu.
func (res *sharedResult) offerHoldingLocked() (HolderKey, *holding) {
	if key, h := res.imageHoldingLocked(); h != nil {
		return key, h
	}
	return res.cloudHoldingLocked()
}

// noteImageCopyLocked records that the engine image ships res, as the image's
// bundle numbers it. The image holding stores a record, which doesn't expire.
// Requires egraphMu.
func (res *sharedResult) noteImageCopyLocked(number uint64) {
	key, h := res.imageHoldingLocked()
	if h == nil {
		h = &holding{}
	} else if key.Number != number {
		delete(res.holders, key)
	}
	if res.holders == nil {
		res.holders = make(map[HolderKey]*holding)
	}
	res.holders[HolderKey{Cache: imageCacheID, Number: number}] = h
}

// noteSenderCopyLocked records res's counterpart in the cache a merge is
// from: the Cloud's, or the engine image's. Requires egraphMu.
func (res *sharedResult) noteSenderCopyLocked(from CacheID, number uint64, stored bool, expiresAtUnix int64) {
	if from == imageCacheID {
		res.noteImageCopyLocked(number)
		return
	}
	res.noteCloudCopyLocked(number, stored, expiresAtUnix)
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

// partOfferLocked returns the offer of res's part at key, the image's or the
// Cloud's (offerHoldingLocked), or nil. Requires egraphMu.
func (res *sharedResult) partOfferLocked(key string) *partOffer {
	_, h := res.offerHoldingLocked()
	if h == nil {
		return nil
	}
	return h.parts[key].offer
}

// partOffersLocked yields the offers of res's parts by part address key, the
// image's or the Cloud's (offerHoldingLocked). A caller may retire the offer
// it is given. Requires egraphMu.
func (res *sharedResult) partOffersLocked() iter.Seq2[string, *partOffer] {
	return func(yield func(string, *partOffer) bool) {
		_, h := res.offerHoldingLocked()
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

// hasPartOffersLocked reports whether the image or the Cloud offers res any
// part.
// Requires egraphMu.
func (res *sharedResult) hasPartOffersLocked() bool {
	for range res.partOffersLocked() {
		return true
	}
	return false
}
