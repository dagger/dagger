package dagql

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/opencontainers/go-digest"
)

// A call that returns an existing entry, such as a module function returning
// a container its body built, makes no entry of its own: the cache that runs
// it teaches the entry the call's recipe digest and term
// (indexWaitResultInEgraphLocked). A record carries only its entry's own call,
// so another engine that gets the entry wouldn't know the call returned it.
//
// The Cloud keeps each call its engines' spans report returning one of its
// entries, as the span encodes it, on that entry, and StoredBundle sends the
// calls with the entry. The receiving engine derives each call's identity
// itself and teaches it onto the entry (teachCallIdentityLocked), as its own
// cache would have when the call completed.

// keptCall is a call the Cloud keeps on the entry it returned: its encoding
// and the identity derived from it.
type keptCall struct {
	encoded string
	id      callIdentity
}

// newKeptCall derives the call a span reports returning an entry of another
// recipe, or returns nil when it reports none. An error is the call's alone:
// the span's holding attaches without it.
func newKeptCall(desc RemoteHolding) (*keptCall, error) {
	if desc.Call == "" || desc.Request == "" || desc.Request == desc.Recipe {
		return nil, nil
	}
	return decodeKeptCall(desc.Call, desc.Request)
}

// decodeKeptCall derives an encoded call's identity, which must have the
// request digest the call is known by.
func decodeKeptCall(encoded string, request digest.Digest) (*keptCall, error) {
	var pb callpbv1.Call
	if err := pb.Decode(encoded); err != nil {
		return nil, fmt.Errorf("call %s: %w", request, err)
	}
	id, err := callIdentityFromPB(&pb)
	if err != nil {
		return nil, err
	}
	if id.recipe != request {
		return nil, fmt.Errorf("call %s: encodes call %s", request, id.recipe)
	}
	return &keptCall{encoded: encoded, id: id}, nil
}

// keepCallLocked keeps kept, a call that returned entry, on entry, and gives
// the entry the call's expiry: as a local cache does when a call completes
// returning an existing entry, the entry's expiry is the earliest of its
// calls'. A call already kept isn't a new completion: a span read again, or
// reporting a call of the entry's value before a later export replaced it,
// leaves the entry's expiry as it is. It reports whether it changed a stored
// entry, which a save writes. Requires egraphMu for writing.
func (c *Cache) keepCallLocked(entry *sharedResult, kept *keptCall, expiresAtUnix int64) bool {
	if kept == nil {
		return false
	}
	if _, ok := entry.calls[kept.id.recipe]; ok {
		return false
	}
	if entry.calls == nil {
		entry.calls = make(map[digest.Digest]*keptCall)
	}
	entry.calls[kept.id.recipe] = kept
	entry.expiresAtUnix = mergeSharedResultExpiryUnix(entry.expiresAtUnix, expiresAtUnix)
	return !entry.noValueLocked() && entry.persistedEnvelope != nil
}

// storedBundleCallsLocked adds to the build, for each of its entries, one of
// the calls kept on it for each term they derive on the Cloud: one the
// recipient can place. The recipient can place an input of a call when it
// holds the input's entry, or the bundle carries it: the entry's record names
// the input's recipe, and the recipient derives its equivalence to its own
// inputs as it does for any record. So the first choice is a call whose every
// input the recipient holds or the bundle carries; the next is a call whose
// inputs the Cloud stores, whose entries the bundle then carries too. A term
// with neither is counted, and sends nothing. Requires egraphMu.
func (c *Cache) storedBundleCallsLocked(build *storedBundleBuild) error {
	// Entries the bundle adds for a call's inputs are visited too.
	for i := 0; i < len(build.order); i++ {
		res := build.order[i]
		if len(res.calls) == 0 {
			continue
		}
		for _, group := range c.keptCallTermsLocked(res) {
			chosen, add := c.placeableCallLocked(build, group)
			if chosen == nil {
				build.unsendableCalls++
				continue
			}
			for _, input := range add {
				if err := c.storedBundleVisitLocked(build, input, map[sharedResultID]bool{}); err != nil {
					return fmt.Errorf("call %s input: %w", chosen.id.recipe, err)
				}
				// A merge installs only what its roots need, so an input
				// entry travels as a root.
				if !build.rootSeen[input.id] {
					build.rootSeen[input.id] = true
					build.callRoots = append(build.callRoots, input)
					build.callInputs = append(build.callInputs, uint64(input.id))
				}
			}
			build.bundle.Calls = append(build.bundle.Calls, TransferredCall{Call: chosen.encoded, Value: build.ordinals[res.id]})
		}
	}
	return nil
}

// keptCallTermsLocked groups the calls kept on res by the term each derives
// on the Cloud, the call's self digest over its inputs' classes, in a stable
// order: by term, then by request digest. An input the Cloud has no class for
// stands for itself. Requires egraphMu.
func (c *Cache) keptCallTermsLocked(res *sharedResult) [][]*keptCall {
	byTerm := map[string][]*keptCall{}
	for _, kept := range res.calls {
		key := kept.id.self.String()
		for _, input := range kept.id.inputs {
			if root := c.eqClassRootLocked(c.egraphDigestToClass[input.String()]); root != 0 {
				key += fmt.Sprintf("|%d", root)
			} else {
				key += "|" + input.String()
			}
		}
		byTerm[key] = append(byTerm[key], kept)
	}
	groups := make([][]*keptCall, 0, len(byTerm))
	for _, key := range slices.Sorted(maps.Keys(byTerm)) {
		group := byTerm[key]
		slices.SortFunc(group, func(a, b *keptCall) int { return cmp.Compare(a.id.recipe, b.id.recipe) })
		groups = append(groups, group)
	}
	return groups
}

// placeableCallLocked chooses, of calls that derive one term, the first whose
// every input the recipient holds or the build carries; else the first whose
// every input the Cloud stores, with the input entries the build must add. A
// digest-witnessed string's digest needs neither: the recipient's own call
// reproduces it.
// It returns nil when there is neither. Requires egraphMu.
func (c *Cache) placeableCallLocked(build *storedBundleBuild, group []*keptCall) (*keptCall, []*sharedResult) {
	var fallback *keptCall
	var fallbackAdd []*sharedResult
	for _, kept := range group {
		placed, stored := true, true
		var add []*sharedResult
		for i, input := range kept.id.inputs {
			if kept.id.witnesses[i] {
				// The recipient's own call reproduces a witness.
				continue
			}
			entry := c.entryOfOwnRecipeLocked(input)
			switch {
			case entry == nil:
				placed, stored = false, false
			case build.ordinals[entry.id] != 0 || build.heldBy.heldLocked(entry):
			case entry.noValueLocked() || entry.persistedEnvelope == nil:
				placed, stored = false, false
			default:
				placed = false
				add = append(add, entry)
			}
		}
		if placed {
			return kept, nil
		}
		if stored && fallback == nil {
			fallback, fallbackAdd = kept, add
		}
	}
	return fallback, fallbackAdd
}

// entryOfOwnRecipeLocked returns the entry whose own recipe is dig: the
// recipe index also names entries by the request digests their calls taught
// them, which a record doesn't carry. Requires egraphMu.
func (c *Cache) entryOfOwnRecipeLocked(dig digest.Digest) *sharedResult {
	entry := c.resultsByID[c.entriesByRecipe[dig]]
	if entry == nil || len(entry.recipeKeys) == 0 || entry.recipeKeys[0] != dig {
		return nil
	}
	return entry
}
