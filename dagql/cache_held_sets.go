package dagql

import (
	"cmp"
	"slices"
	"sync"
	"sync/atomic"
	"unique"
)

// A holding's sets (its dependencies, unknown dependencies, sessions and
// parts) are sorted slices, nil when empty, and are replaced, never changed
// in place: a write builds a new slice, so a slice a reader was given under
// egraphMu stays as it was after the lock is released (HoldingFacts). Most
// holdings have one dependency, one part and one session, where a Go map
// would cost a header and a group of eight slots each.
//
// The strings many holdings repeat, type names, content digests, sessions
// and part address keys, are held as unique handles: one copy of the bytes,
// and eight bytes a holding.

// internString returns s's handle; the zero handle stands for "".
func internString(s string) unique.Handle[string] {
	if s == "" {
		return unique.Handle[string]{}
	}
	return unique.Make(s)
}

// handleString returns the string h stands for.
func handleString(h unique.Handle[string]) string {
	if h == (unique.Handle[string]{}) {
		return ""
	}
	return h.Value()
}

// withNumber returns set with n: set itself when it has n, a new slice
// otherwise.
func withNumber(set []uint64, n uint64) []uint64 {
	i, found := slices.BinarySearch(set, n)
	if found {
		return set
	}
	out := make([]uint64, len(set)+1)
	copy(out, set[:i])
	out[i] = n
	copy(out[i+1:], set[i:])
	return out
}

// withNumbers returns set with every number of adds, sorted and without
// duplicates: set itself when it has them all, one new slice otherwise.
func withNumbers(set []uint64, adds []uint64) []uint64 {
	if len(adds) == 0 {
		return set
	}
	out := make([]uint64, 0, len(set)+len(adds))
	out = append(out, set...)
	out = append(out, adds...)
	slices.Sort(out)
	out = slices.Compact(out)
	if len(out) == len(set) {
		return set
	}
	return slices.Clip(out)
}

// withoutNumber returns set without n, and whether set had it: set itself
// when it hasn't, a new slice (nil when empty) otherwise.
func withoutNumber(set []uint64, n uint64) ([]uint64, bool) {
	i, found := slices.BinarySearch(set, n)
	if !found {
		return set, false
	}
	if len(set) == 1 {
		return nil, true
	}
	out := make([]uint64, 0, len(set)-1)
	out = append(out, set[:i]...)
	return append(out, set[i+1:]...), true
}

// keepNumbers returns the numbers of set that keep reports true for: set
// itself when it keeps them all, a new slice (nil when empty) otherwise.
func keepNumbers(set []uint64, keep func(uint64) bool) []uint64 {
	var out []uint64
	for i, n := range set {
		if keep(n) {
			if out != nil {
				out = append(out, n)
			}
			continue
		}
		if out == nil {
			out = make([]uint64, i, len(set)-1)
			copy(out, set[:i])
		}
	}
	if out == nil {
		return set
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// hasNumber reports whether the sorted set has n.
func hasNumber(set []uint64, n uint64) bool {
	_, found := slices.BinarySearch(set, n)
	return found
}

// insertNumber adds n to the sorted set in place, for the cache's own
// indexes, which no reader is given.
func insertNumber(set []uint64, n uint64) []uint64 {
	i, found := slices.BinarySearch(set, n)
	if found {
		return set
	}
	return slices.Insert(set, i, n)
}

// removeNumber removes n from the sorted set in place (nil when empty), for
// the cache's own indexes.
func removeNumber(set []uint64, n uint64) []uint64 {
	i, found := slices.BinarySearch(set, n)
	if !found {
		return set
	}
	set = slices.Delete(set, i, i+1)
	if len(set) == 0 {
		return nil
	}
	return set
}

func compareHandles(a, b unique.Handle[string]) int {
	return cmp.Compare(handleString(a), handleString(b))
}

// withSession returns sessions with session, and whether it is new.
func withSession(sessions []unique.Handle[string], session string) ([]unique.Handle[string], bool) {
	h := internString(session)
	i, found := slices.BinarySearchFunc(sessions, h, compareHandles)
	if found {
		return sessions, false
	}
	out := make([]unique.Handle[string], len(sessions)+1)
	copy(out, sessions[:i])
	out[i] = h
	copy(out[i+1:], sessions[i:])
	return out, true
}

// withoutSession returns sessions without session, and whether it had it.
func withoutSession(sessions []unique.Handle[string], session string) ([]unique.Handle[string], bool) {
	i, found := slices.BinarySearchFunc(sessions, internString(session), compareHandles)
	if !found {
		return sessions, false
	}
	if len(sessions) == 1 {
		return nil, true
	}
	out := make([]unique.Handle[string], 0, len(sessions)-1)
	out = append(out, sessions[:i]...)
	return append(out, sessions[i+1:]...), true
}

// partIndex finds the part at address key in the sorted parts.
func partIndex(parts []heldPart, key string) (int, bool) {
	return slices.BinarySearchFunc(parts, key, func(p heldPart, key string) int {
		return cmp.Compare(handleString(p.key), key)
	})
}

// partLocked returns h's part at address key, the zero part when h has none.
func (h *holding) partLocked(key string) heldPart {
	if i, found := partIndex(h.parts, key); found {
		return h.parts[i]
	}
	return heldPart{}
}

// setPartLocked sets h's part at address key, in a new slice.
func (h *holding) setPartLocked(key string, part heldPart) {
	part.key = internString(key)
	i, found := partIndex(h.parts, key)
	if found {
		out := slices.Clone(h.parts)
		out[i] = part
		h.parts = out
		return
	}
	out := make([]heldPart, len(h.parts)+1)
	copy(out, h.parts[:i])
	out[i] = part
	copy(out[i+1:], h.parts[i:])
	h.parts = out
}

// deletePartLocked removes h's part at address key, in a new slice.
func (h *holding) deletePartLocked(key string) {
	i, found := partIndex(h.parts, key)
	if !found {
		return
	}
	if len(h.parts) == 1 {
		h.parts = nil
		return
	}
	out := make([]heldPart, 0, len(h.parts)-1)
	out = append(out, h.parts[:i]...)
	h.parts = append(out, h.parts[i+1:]...)
}

// holderRef is one holding on an entry, with its key.
type holderRef struct {
	key HolderKey
	h   *holding
}

// entryHolders are an entry's holdings sorted by key: an engine cache's
// holdings on it are one run of them. The list is changed in place under mu:
// a reader takes mu to read it, and copies what it keeps past mu
// (holderRefsLocked, cacheHolderRefsLocked) or reads it under mu with a
// function that takes no lock (eachHolderLocked, eachCacheHolderLocked).
// engines counts the holdings of engine caches among them, an engine's Cloud
// and image holdings not, so that whether any engine holds the entry is one
// load. A writer of one engine cache's holdings changes only its own place
// in the list, so writers of different engine caches' holdings can take
// turns on mu alone (sharedResult.holders). mu is a leaf: taken alone, under
// egraphMu in either mode, and nothing is taken while it is held.
type entryHolders struct {
	mu      sync.Mutex
	refs    []holderRef
	engines atomic.Int32
}

func holderIndex(refs []holderRef, key HolderKey) (int, bool) {
	return slices.BinarySearchFunc(refs, key, func(r holderRef, key HolderKey) int {
		return compareHolderKeys(r.key, key)
	})
}

// holderRefsLocked returns a copy of res's holdings, sorted by key, for a
// reader that takes another lock as it reads them. Requires egraphMu.
func (res *sharedResult) holderRefsLocked() []holderRef {
	holders := res.holders.Load()
	if holders == nil {
		return nil
	}
	holders.mu.Lock()
	defer holders.mu.Unlock()
	return slices.Clone(holders.refs)
}

// eachHolderLocked calls f with each of res's holdings, sorted by key,
// until it returns false, under the list's lock: f takes no lock and
// changes no holder list. Requires egraphMu.
func (res *sharedResult) eachHolderLocked(f func(holderRef) bool) {
	holders := res.holders.Load()
	if holders == nil {
		return
	}
	holders.mu.Lock()
	defer holders.mu.Unlock()
	for _, ref := range holders.refs {
		if !f(ref) {
			return
		}
	}
}

// engineHoldersLocked counts res's holdings of engine caches. Requires
// egraphMu.
func (res *sharedResult) engineHoldersLocked() int32 {
	holders := res.holders.Load()
	if holders == nil {
		return 0
	}
	return holders.engines.Load()
}

// cacheRunLocked returns the bounds of one engine cache's run of holdings in
// refs, sorted by key. An engine's Cloud and image holdings are no engine
// cache's: their run is empty.
func cacheRunLocked(refs []holderRef, cache CacheID) (int, int) {
	if senderCache(cache) {
		return 0, 0
	}
	start, _ := holderIndex(refs, HolderKey{Cache: cache})
	end := start
	for end < len(refs) && refs[end].key.Cache == cache {
		end++
	}
	return start, end
}

// cacheHolderRefsLocked returns a copy of res's holdings of one engine
// cache, sorted by number. Requires egraphMu.
func (res *sharedResult) cacheHolderRefsLocked(cache CacheID) []holderRef {
	holders := res.holders.Load()
	if holders == nil {
		return nil
	}
	holders.mu.Lock()
	defer holders.mu.Unlock()
	start, end := cacheRunLocked(holders.refs, cache)
	return slices.Clone(holders.refs[start:end])
}

// eachCacheHolderLocked calls f with each of res's holdings of one engine
// cache, sorted by number, until it returns false, under the list's lock, as
// eachHolderLocked does. Requires egraphMu.
func (res *sharedResult) eachCacheHolderLocked(cache CacheID, f func(holderRef) bool) {
	holders := res.holders.Load()
	if holders == nil {
		return
	}
	holders.mu.Lock()
	defer holders.mu.Unlock()
	start, end := cacheRunLocked(holders.refs, cache)
	for _, ref := range holders.refs[start:end] {
		if !f(ref) {
			return
		}
	}
}

// lockHoldersLocked returns res's holder list locked, creating it when res
// has none. A list emptied and dropped (deleteHolderLocked) between its load
// and its lock is no longer res's, and another is taken. Requires egraphMu.
func (res *sharedResult) lockHoldersLocked() *entryHolders {
	for {
		holders := res.holders.Load()
		if holders == nil {
			holders = &entryHolders{}
			if !res.holders.CompareAndSwap(nil, holders) {
				continue
			}
		}
		holders.mu.Lock()
		if res.holders.Load() == holders {
			return holders
		}
		holders.mu.Unlock()
	}
}

// putHolderLocked sets res's holding of key. Requires egraphMu for writing,
// or for reading with the lock of key's engine cache.
func (res *sharedResult) putHolderLocked(key HolderKey, h *holding) {
	holders := res.lockHoldersLocked()
	defer holders.mu.Unlock()
	i, found := holderIndex(holders.refs, key)
	if found {
		holders.refs[i].h = h
		return
	}
	holders.refs = slices.Insert(holders.refs, i, holderRef{key: key, h: h})
	if !senderCache(key.Cache) {
		holders.engines.Add(1)
	}
}

// deleteHolderLocked removes res's holding of key; an entry left with none
// keeps none. Requires egraphMu for writing, or for reading with the lock of
// key's engine cache.
func (res *sharedResult) deleteHolderLocked(key HolderKey) {
	holders := res.holders.Load()
	if holders == nil {
		return
	}
	holders.mu.Lock()
	defer holders.mu.Unlock()
	if res.holders.Load() != holders {
		return
	}
	i, found := holderIndex(holders.refs, key)
	if !found {
		return
	}
	if !senderCache(key.Cache) {
		holders.engines.Add(-1)
	}
	holders.refs = slices.Delete(holders.refs, i, i+1)
	if len(holders.refs) == 0 {
		holders.refs = nil
		res.holders.Store(nil)
	}
}
