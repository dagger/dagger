package dagql

import (
	"github.com/opencontainers/go-digest"
)

// RemoteReads is a Cloud cache held for reading (HoldRemoteReads), for a run
// of reads answered in one hold of its lock. Each method is the Cache's
// method of the same name and answers what it answers, all at one moment of
// the cache: unlike the Cache's EntriesHeld, the hold never gives way between
// two entries. What the run saves is the waits: a writer waiting for the lock
// keeps new readers out until it has had it, so a run of the Cache's own
// reads waits for every write that comes between two of them, and a run in
// one hold waits for the writes once.
//
// A writer waits for the hold, so the holder reads what it needs and
// releases it. It calls nothing else of the cache until Release: the cache's
// own methods wait for a writer that waits for the hold. Only the Cloud uses
// it, to resolve an engine's lookup of the calls its cache missed.
type RemoteReads struct {
	c *Cache
}

// HoldRemoteReads takes the cache's lock for reading, until the returned
// hold's Release.
func (c *Cache) HoldRemoteReads() *RemoteReads {
	c.egraphMu.RLock()
	return &RemoteReads{c: c}
}

// Release ends the hold. The hold can't be used after.
func (r *RemoteReads) Release() {
	c := r.c
	r.c = nil
	c.egraphMu.RUnlock()
}

// EquivalentEntries is Cache.EquivalentEntries, in the hold.
func (r *RemoteReads) EquivalentEntries(dig string) []uint64 {
	return r.c.equivalentEntriesLocked(dig)
}

// TermEntries is Cache.TermEntries, in the hold.
func (r *RemoteReads) TermEntries(self digest.Digest, inputs []digest.Digest) []uint64 {
	return r.c.termEntriesLocked(self, inputs)
}

// EntriesHeld is Cache.EntriesHeld, in the hold, which it never gives way.
func (r *RemoteReads) EntriesHeld(numbers []uint64, limit int) []EntryHeld {
	return r.c.entriesHeldLocked(numbers, limit, false)
}

// CacheHoldings is Cache.CacheHoldings, in the hold.
func (r *RemoteReads) CacheHoldings(cache CacheID, numbers []uint64) []RemoteHeldEntry {
	return r.c.cacheHoldingsLocked(cache, numbers)
}

// EntryInfo is Cache.EntryInfo, in the hold.
func (r *RemoteReads) EntryInfo(number uint64) (RemoteEntryInfo, bool) {
	return r.c.entryInfoLocked(number)
}
