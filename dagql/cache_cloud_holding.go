package dagql

import "iter"

// An engine's Cloud holding is what one of its entries knows about its
// counterpart in the Cloud cache: the Cloud entry the latest message named,
// whether it stores a record and that record's expiry, and the Cloud's
// complete parts, each with the offer that downloads it. An engine keeps at
// most one per entry, in holders under cloudCacheID.
//
// Unlike the holdings the Cloud keeps of engine caches, it owns nothing: it
// adds no ownership unit to its entry, is in no engine cache's index, and no
// collection pass looks at it. Sessions, retention edges and dependents own
// engine entries as they always have. It goes with its entry.

// cloudHoldingLocked returns res's Cloud holding and its key, if it has one.
// Requires egraphMu.
func (res *sharedResult) cloudHoldingLocked() (HolderKey, *holding) {
	for _, ref := range res.holderRefsLocked() {
		if ref.key.Cache == cloudCacheID {
			return ref.key, ref.h
		}
	}
	return HolderKey{}, nil
}

// imageHoldingLocked returns res's image holding and its key, if it has one:
// the engine image ships res, and the holding carries the image's offers of
// its parts. Requires egraphMu.
func (res *sharedResult) imageHoldingLocked() (HolderKey, *holding) {
	for _, ref := range res.holderRefsLocked() {
		if ref.key.Cache == imageCacheID {
			return ref.key, ref.h
		}
	}
	return HolderKey{}, nil
}

// imageSuppliesLocked reports whether the engine image supplies res's parts:
// its image holding carries the image's offer of one. The image ships parts
// for the results it was built from, and the records of their closures
// without any. Such a record has no content of its own on the engine, and
// takes its parts as any other entry does. Requires egraphMu.
func (res *sharedResult) imageSuppliesLocked() bool {
	_, h := res.imageHoldingLocked()
	if h == nil {
		return false
	}
	for _, part := range h.parts {
		if part.offer != nil {
			return true
		}
	}
	return false
}

// offerHoldingLocked returns the holding whose offers supply res's parts: its
// image holding when the image supplies them (imageSuppliesLocked), whose
// content is on the engine's own disk, or else its Cloud holding. Requires
// egraphMu.
func (res *sharedResult) offerHoldingLocked() (HolderKey, *holding) {
	if res.imageSuppliesLocked() {
		return res.imageHoldingLocked()
	}
	return res.cloudHoldingLocked()
}

// senderHoldingLocked returns res's holding of the cache from, the image or
// the Cloud, which an offer from that cache goes into. Requires egraphMu.
func (res *sharedResult) senderHoldingLocked(from CacheID) (HolderKey, *holding) {
	if from == imageCacheID {
		return res.imageHoldingLocked()
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
		res.deleteHolderLocked(key)
	}
	res.putHolderLocked(HolderKey{Cache: imageCacheID, Number: number}, h)
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
		// restart: the one holding moves to it, with its offers. What the
		// engine merged was the other entry's.
		res.deleteHolderLocked(key)
		h.cloudMerge = nil
	}
	res.putHolderLocked(HolderKey{Cache: cloudCacheID, Number: number}, h)
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
	return h.partLocked(key).offer
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
		// A caller may retire the offer, which replaces h.parts: this walks
		// the slice it started with.
		for _, part := range h.parts {
			if part.offer != nil && !yield(handleString(part.key), part.offer) {
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

// HasPartOffer reports whether the image or the Cloud offers res's part: an
// address the part can be fetched from instead of made.
func (c *Cache) HasPartOffer(res AnyResult, part PartKey) bool {
	row := res.cacheSharedResult()
	if row == nil {
		return false
	}
	key, err := partAddressKey(PersistedPartAddress{Part: part})
	if err != nil {
		return false
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return row.partOfferLocked(key) != nil
}

// cloudMergeNote is what an engine last merged of its Cloud holding's entry:
// the Cloud cache's start count and the entry's change stamp the merged
// value carried (ValueBundle.SenderGeneration, TransferredValue.SenderStamp),
// and the engine entry's own transfer revision once the merge left it. A
// later value of the same Cloud entry at the same start count and stamp
// carries the same record and expiry, and while the engine entry's revision
// is unchanged, its identity and the Cloud's copy are what the merge taught
// it then (unchangedCloudRowLocked).
type cloudMergeNote struct {
	generation, stamp, revision uint64
}
