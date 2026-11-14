package dagql

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"time"

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

// keepCallLocked keeps kept, a call that returned entry, on entry, once per
// request digest, and gives the entry the expiry the span reports: as a
// local cache does when a call completes returning an existing entry, the
// entry's expiry is the earliest of its calls'. A request digest names a
// computation, not a completion, so a call already kept can still report a
// new one. A completion expires after it, so a span whose expiry has passed
// reports a call that has expired, which no engine may hit: it is read again,
// or read late, and neither its call nor its expiry is kept. It reports
// whether it changed a stored entry, which a save writes. Requires egraphMu
// for writing.
func (c *Cache) keepCallLocked(entry *sharedResult, desc RemoteHolding, kept *keptCall) bool {
	if desc.Request == "" || desc.Request == desc.Recipe {
		return false
	}
	if desc.ExpiresAtUnix != 0 && desc.ExpiresAtUnix <= time.Now().Unix() {
		return false
	}
	changed := false
	if expires := mergeSharedResultExpiryUnix(entry.expiresAtUnix, desc.ExpiresAtUnix); expires != entry.expiresAtUnix {
		entry.expiresAtUnix = expires
		c.markEntryClassLocked(entry.id, StampCallExpiry)
		changed = true
	}
	if kept != nil {
		if _, ok := entry.calls[kept.id.recipe]; !ok {
			// A stored bundle of the entry carries its kept calls. The cause
			// tells a new term the bundle can send from one it has (a term
			// the entry's calls already derive) and one it can't (an input
			// with no stored record of its own recipe), for diagnostics.
			cause := c.keptCallCauseLocked(entry, kept)
			if entry.calls == nil {
				entry.calls = make(map[digest.Digest]*keptCall)
			}
			entry.calls[kept.id.recipe] = kept
			c.stampEntryLocked(entry, cause)
			c.indexKeptCallLocked(entry, kept)
			changed = true
		}
	}
	return changed && !entry.noValueLocked() && entry.persistedEnvelope != nil
}

// storedBundleCallsLocked adds to the build, for each of its entries from
// start, those a root's closure added and the inputs added for their calls,
// one of the calls kept on it for each term they derive on the Cloud: one
// the recipient can place. The recipient can place an input of a call when
// it holds the input's entry, or the bundle carries it in the closure of a
// root so far: the entry's record names the input's recipe, and the
// recipient derives its equivalence to its own inputs as it does for any
// record. So the first choice is a call whose every input the recipient
// holds or those closures carry; the next is a call whose inputs the Cloud
// stores, whose entries the bundle then carries too, with the root whose
// entry's call needs them. An input another call added doesn't place a
// call, nor does a later root's closure: the calls chosen are those
// StoredSizes prices, root by root. A term with neither is counted, and
// sends nothing. Requires egraphMu.
func (c *Cache) storedBundleCallsLocked(build *storedBundleBuild, start int) error {
	// Entries the bundle adds for a call's inputs are visited too.
	for i := start; i < len(build.order); i++ {
		res := build.order[i]
		if len(res.calls) == 0 {
			continue
		}
		for _, group := range c.keptCallTermsLocked(res) {
			chosen, add := c.placeableCallLocked(group, func(entry *sharedResult) bool {
				return build.ordinary[entry.id] || build.heldBy.heldLocked(entry, build.now)
			})
			if chosen == nil {
				build.unsendableCalls++
				build.markPartial(res)
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

// keptCallTermKeyLocked is the term a kept call derives on the Cloud, as
// keptCallTermsLocked groups calls by: its self digest over its inputs'
// classes, an input the Cloud has no class for standing for itself.
// Requires egraphMu.
func (c *Cache) keptCallTermKeyLocked(kept *keptCall) string {
	key := kept.id.self.String()
	for _, input := range kept.id.inputs {
		if root := c.eqClassRootLocked(c.egraphDigestToClass[input.String()]); root != 0 {
			key += fmt.Sprintf("|%d", root)
		} else {
			key += "|" + input.String()
		}
	}
	return key
}

// keptCallCauseLocked says what keeping kept on entry, before it is kept,
// changes in a stored bundle of the entry (StampCause): a call of a term the
// entry's calls already derive, one with an input no bundle can place (no
// stored record of its own recipe; a witness needs none), or a new term a
// bundle can send. Requires egraphMu.
func (c *Cache) keptCallCauseLocked(entry *sharedResult, kept *keptCall) StampCause {
	for i, input := range kept.id.inputs {
		if kept.id.witnesses[i] {
			continue
		}
		if in := c.currentEntryForRecipeLocked(input); in == nil || in.noValueLocked() || in.persistedEnvelope == nil {
			return StampKeptCallUnsendable
		}
	}
	key := c.keptCallTermKeyLocked(kept)
	for _, other := range entry.calls {
		if c.keptCallTermKeyLocked(other) == key {
			return StampKeptCallOldTerm
		}
	}
	return StampKeptCallNewTerm
}

// verifyKeptCallTerms, set only by tests, checks every kept grouping of an
// entry's calls against one built fresh, and panics if they differ.
var verifyKeptCallTerms bool

// keptCallTermsLocked groups the calls kept on res by the term each derives
// on the Cloud, the call's self digest over its inputs' classes, in a stable
// order: by term, then by request digest. An input the Cloud has no class for
// stands for itself. The grouping is kept with the entry until its calls, or
// an input's class, change (forgetCallTermsLocked): each pricing and bundle
// visits it, and an entry's calls grow with the runs that taught them.
// Requires egraphMu.
func (c *Cache) keptCallTermsLocked(res *sharedResult) [][]*keptCall {
	if kept := res.callTerms.Load(); kept != nil {
		if verifyKeptCallTerms {
			if fresh := c.callTermsLocked(res); !slices.EqualFunc(*kept, fresh, slices.Equal) {
				panic(fmt.Sprintf("entry %d: kept call terms %v, built fresh %v", res.id, *kept, fresh))
			}
		}
		return *kept
	}
	if c.testCallTermsBuilt != nil {
		c.testCallTermsBuilt()
	}
	groups := c.callTermsLocked(res)
	res.callTerms.Store(&groups)
	return groups
}

// callTermsLocked is keptCallTermsLocked's grouping, built fresh. Requires
// egraphMu.
func (c *Cache) callTermsLocked(res *sharedResult) [][]*keptCall {
	byTerm := map[string][]*keptCall{}
	for _, kept := range res.calls {
		key := c.keptCallTermKeyLocked(kept)
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

// indexKeptCallLocked notes kept, newly kept on entry, under each of its
// inputs, and drops entry's grouping. Requires egraphMu for writing.
func (c *Cache) indexKeptCallLocked(entry *sharedResult, kept *keptCall) {
	for _, input := range kept.id.inputs {
		entries := c.callInputEntries[input.String()]
		if entries == nil {
			if c.callInputEntries == nil {
				c.callInputEntries = make(map[string]map[sharedResultID]struct{})
			}
			entries = make(map[sharedResultID]struct{})
			c.callInputEntries[input.String()] = entries
		}
		entries[entry.id] = struct{}{}
	}
	entry.callTerms.Store(nil)
}

// unindexKeptCallsLocked forgets the inputs of the calls kept on entry, which
// is leaving the cache. Requires egraphMu for writing.
func (c *Cache) unindexKeptCallsLocked(entry *sharedResult) {
	for _, kept := range entry.calls {
		for _, input := range kept.id.inputs {
			entries := c.callInputEntries[input.String()]
			delete(entries, entry.id)
			if len(entries) == 0 {
				delete(c.callInputEntries, input.String())
			}
		}
	}
}

// forgetCallTermsLocked drops the grouping of each entry with a kept call
// whose input is dig, whose class has changed. Requires egraphMu for
// writing.
func (c *Cache) forgetCallTermsLocked(dig string) {
	for id := range c.callInputEntries[dig] {
		if res := c.resultsByID[id]; res != nil {
			res.callTerms.Store(nil)
		}
	}
}

// forgetAllCallTermsLocked drops every entry's grouping. Requires egraphMu
// for writing.
func (c *Cache) forgetAllCallTermsLocked() {
	for _, entries := range c.callInputEntries {
		for id := range entries {
			if res := c.resultsByID[id]; res != nil {
				res.callTerms.Store(nil)
			}
		}
	}
}

// placeableCallLocked chooses, of calls that derive one term, the first whose
// every input the recipient holds or the bundle carries (has); else the first
// whose every input the Cloud stores, with the input entries the bundle must
// add. A digest-witnessed string's digest needs neither: the recipient's own
// call reproduces it.
// It returns nil when there is neither. Requires egraphMu.
func (c *Cache) placeableCallLocked(group []*keptCall, has func(*sharedResult) bool) (*keptCall, []*sharedResult) {
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
			entry := c.currentEntryForRecipeLocked(input)
			switch {
			case entry == nil:
				placed, stored = false, false
			case has(entry):
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
