package dagql

import (
	"cmp"
	"slices"
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
// holdings on it are one run of them. Changed in place under egraphMu; no
// reader keeps them.
type entryHolders struct {
	refs []holderRef
}

func holderIndex(refs []holderRef, key HolderKey) (int, bool) {
	return slices.BinarySearchFunc(refs, key, func(r holderRef, key HolderKey) int {
		return compareHolderKeys(r.key, key)
	})
}

// holderRefsLocked returns res's holdings, sorted by key. Requires egraphMu.
func (res *sharedResult) holderRefsLocked() []holderRef {
	if res.holders == nil {
		return nil
	}
	return res.holders.refs
}

// holderLocked returns res's holding of key, or nil. Requires egraphMu.
func (res *sharedResult) holderLocked(key HolderKey) *holding {
	refs := res.holderRefsLocked()
	if i, found := holderIndex(refs, key); found {
		return refs[i].h
	}
	return nil
}

// cacheHolderRefsLocked returns res's holdings of one engine cache, sorted
// by number. An engine's Cloud and image holdings are no engine cache's.
// Requires egraphMu.
func (res *sharedResult) cacheHolderRefsLocked(cache CacheID) []holderRef {
	if senderCache(cache) {
		return nil
	}
	refs := res.holderRefsLocked()
	start, _ := holderIndex(refs, HolderKey{Cache: cache})
	end := start
	for end < len(refs) && refs[end].key.Cache == cache {
		end++
	}
	return refs[start:end]
}

// putHolderLocked sets res's holding of key. Requires egraphMu for writing.
func (res *sharedResult) putHolderLocked(key HolderKey, h *holding) {
	if res.holders == nil {
		res.holders = &entryHolders{}
	}
	i, found := holderIndex(res.holders.refs, key)
	if found {
		res.holders.refs[i].h = h
		return
	}
	res.holders.refs = slices.Insert(res.holders.refs, i, holderRef{key: key, h: h})
}

// deleteHolderLocked removes res's holding of key; an entry left with none
// keeps none. Requires egraphMu for writing.
func (res *sharedResult) deleteHolderLocked(key HolderKey) {
	if res.holders == nil {
		return
	}
	i, found := holderIndex(res.holders.refs, key)
	if !found {
		return
	}
	res.holders.refs = slices.Delete(res.holders.refs, i, i+1)
	if len(res.holders.refs) == 0 {
		res.holders = nil
	}
}
