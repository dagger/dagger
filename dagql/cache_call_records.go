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
// The Cloud keeps a call its engines' spans report returning one of its
// entries, as the span encodes it, on that entry, one call of each term
// (keepTermCallLocked), and StoredBundle sends the calls with the entry. The
// receiving engine derives each call's identity itself and teaches it onto
// the entry (teachCallIdentityLocked), as its own cache would have when the
// call completed. A call no other session can ever match
// (ResultCall.onlyItsSessionMatches), such as one keyed to the client that
// made it, isn't kept: no receiving engine could use it.

// keptCall is a call the Cloud keeps on the entry it returned: its encoding
// and the identity derived from it.
type keptCall struct {
	encoded string
	id      callIdentity
}

// newKeptCall derives the call a span reports returning an entry of another
// recipe, or returns nil when it reports none or one the Cloud doesn't keep
// (decodeKeptCall). An error is the call's alone: the span's holding
// attaches without it.
func newKeptCall(desc RemoteHolding) (*keptCall, error) {
	if desc.Call == "" || desc.Request == "" || desc.Request == desc.Recipe {
		return nil, nil
	}
	return decodeKeptCall(desc.Call, desc.Request)
}

// decodeKeptCall derives an encoded call's identity, which must have the
// request digest the call is known by. It returns nil for a call no other
// session can ever match (ResultCall.onlyItsSessionMatches): the Cloud
// keeps none, from a span or from a saved store.
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
	if id.onlyItsSession {
		return nil, nil
	}
	return &keptCall{encoded: encoded, id: id}, nil
}

// keepCallLocked keeps kept, a call that returned entry, on entry, unless
// the entry keeps a call of its term (keepTermCallLocked), and gives the
// entry the expiry the span reports: as a local cache does when a call
// completes returning an existing entry, the entry's expiry is the earliest
// of its calls'. A request digest names a
// computation, not a completion, so a call already kept can still report a
// new one. A completion expires after it, so a span whose expiry has passed
// reports a call that has expired, which no engine may hit: it is read again,
// or read late, and neither its call nor its expiry is kept. It reports
// whether it changed a stored entry, which a save writes. Requires egraphMu
// for writing.
func (c *Cache) keepCallLocked(change *RemoteChange, entry *sharedResult, desc RemoteHolding, kept *keptCall) bool {
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
		c.noteSharedWriteLocked(change, SharedExpiry)
		changed = true
	}
	if kept != nil {
		if _, ok := entry.calls[kept.id.recipe]; !ok && c.keepTermCallLocked(change, entry, kept) {
			changed = true
		}
	}
	return changed && !entry.noValueLocked() && entry.persistedEnvelope != nil
}

// keepTermCallLocked keeps kept on entry as the one call of its term, and
// reports whether it did. The calls of one term are one call to the cache,
// which matches a call by its term, and a bundle sends one call of a term,
// one the recipient can place (placeableCallLocked): a local cache adds no
// second term of a term it has (indexWaitResultInEgraphLocked). So a call of
// a term the entry keeps a call of adds nothing, unless the kept call names
// an input the Cloud stores no record of, which no bundle can place, and
// kept names none such: kept then replaces it. Two calls the entry kept as
// two terms are one term once their inputs' classes are joined, and the entry
// keeps them both until a call is next kept on it, which leaves one of each
// term of its calls (joinedTermCallsLocked). A stored bundle of the entry
// carries its kept calls, so a change to them stamps the entry, with a cause
// that tells a term the bundle can send from one it can't, for diagnostics.
// It reports whether it changed the entry's calls. Requires egraphMu for
// writing.
func (c *Cache) keepTermCallLocked(change *RemoteChange, entry *sharedResult, kept *keptCall) bool {
	stored := c.keptCallStoredLocked(kept)
	key := c.keptCallTermKeyLocked(kept)
	terms, joined := c.joinedTermCallsLocked(entry)
	for _, call := range joined {
		c.dropKeptCallLocked(entry, call)
	}
	if len(joined) > 0 {
		c.stampEntryLocked(entry, StampKeptCallsJoined)
		c.noteSharedWriteLocked(change, SharedKeptCall)
	}
	if other := terms[key]; other != nil {
		if !stored || c.keptCallStoredLocked(other) {
			return len(joined) > 0
		}
		c.dropKeptCallLocked(entry, other)
	}
	cause := StampKeptCallNewTerm
	if !stored {
		cause = StampKeptCallUnsendable
	}
	if entry.calls == nil {
		entry.calls = make(map[digest.Digest]*keptCall)
	}
	entry.calls[kept.id.recipe] = kept
	c.stampEntryLocked(entry, cause)
	c.indexKeptCallLocked(entry, kept)
	c.noteSharedWriteLocked(change, SharedKeptCall)
	return true
}

// joinedTermCallsLocked returns the call entry keeps of each term its calls
// derive now, by term key, and the calls beyond those: calls kept as two terms
// whose inputs' classes have since been joined. Of the calls of one term it
// keeps the first, by request digest, of those the Cloud stores the inputs
// of, or the first. Requires egraphMu.
func (c *Cache) joinedTermCallsLocked(entry *sharedResult) (map[string]*keptCall, []*keptCall) {
	terms := make(map[string]*keptCall, len(entry.calls))
	var joined []*keptCall
	for _, recipe := range slices.Sorted(maps.Keys(entry.calls)) {
		call := entry.calls[recipe]
		key := c.keptCallTermKeyLocked(call)
		first, ok := terms[key]
		switch {
		case !ok:
			terms[key] = call
		case !c.keptCallStoredLocked(first) && c.keptCallStoredLocked(call):
			terms[key] = call
			joined = append(joined, first)
		default:
			joined = append(joined, call)
		}
	}
	return terms, joined
}

// dropKeptCallLocked forgets kept, a call entry keeps, and its index under
// each of its inputs that no other call entry keeps names. Requires egraphMu
// for writing.
func (c *Cache) dropKeptCallLocked(entry *sharedResult, kept *keptCall) {
	delete(entry.calls, kept.id.recipe)
	for _, input := range kept.id.inputs {
		named := false
		for _, other := range entry.calls {
			if slices.Contains(other.id.inputs, input) {
				named = true
				break
			}
		}
		if named {
			continue
		}
		entries := c.callInputEntries[input.String()]
		delete(entries, entry.id)
		if len(entries) == 0 {
			delete(c.callInputEntries, input.String())
		}
	}
	entry.callTerms.Store(nil)
}

// storedBundleCallsLocked adds to the build, for each of its entries from
// start, those a root's closure added and the inputs added for their calls,
// one of the calls kept on it for each term they derive on the Cloud: one
// the recipient can place (placeableCallLocked). Each of the call's inputs
// goes with the row of the entry that places it, an entry of the input's
// class (TransferredCall.Inputs): one the bundle carries in the closure of a
// root so far; else one the recipient holds, as a held row the call alone
// names; else one the Cloud stores, which the bundle carries as a root of its
// own, with the root whose entry's call needs it. An input another call added doesn't place a call,
// nor does a later root's closure: the calls chosen are those StoredSizes
// prices, root by root. A term none of whose calls can be placed is counted,
// and sends nothing. Only the terms the recipient's check made are candidates
// (HeldBy.Calls), each with all its calls: a term with none of the check's
// calls sends nothing and isn't counted.
// Requires egraphMu.
func (c *Cache) storedBundleCallsLocked(build *storedBundleBuild, start int) error {
	// Entries the bundle adds for a call's inputs are visited too.
	for i := start; i < len(build.order); i++ {
		res := build.order[i]
		if len(res.calls) == 0 {
			continue
		}
		for _, group := range c.keptCallTermsLocked(res) {
			if group = c.sendableCallsLocked(build.heldBy, build.terms, group); len(group) == 0 {
				continue
			}
			chosen, places := c.placeableCallLocked(group, build.now, func(entry *sharedResult) bool {
				return build.ordinary[entry.id] || build.holdsLocked(entry)
			})
			if chosen == nil {
				build.unsendableCalls++
				build.markPartial(res)
				continue
			}
			inputs := make([]TransferOrdinal, len(places))
			for j, place := range places {
				if place == nil {
					continue
				}
				if err := c.storedBundleVisitLocked(build, place, map[sharedResultID]bool{}); err != nil {
					return fmt.Errorf("call %s input: %w", chosen.id.recipe, err)
				}
				// A merge installs only what its roots need, so a stored
				// input entry the roots' closures don't carry travels as a
				// root of its own: carried below another call's input, it
				// would go if that one expired. One the recipient holds
				// goes as a held row the call alone names, no root: a merge
				// that doesn't find it then drops the call, not the bundle.
				if !build.ordinary[place.id] && !build.held[place.id] && !build.rootSeen[place.id] {
					build.rootSeen[place.id] = true
					build.callRoots = append(build.callRoots, place)
					build.callInputs = append(build.callInputs, uint64(place.id))
				}
				inputs[j] = build.ordinals[place.id]
			}
			build.bundle.Calls = append(build.bundle.Calls, TransferredCall{Call: chosen.encoded, Value: build.ordinals[res.id], Inputs: inputs})
		}
	}
	return nil
}

// keptCallTermKeyLocked is the term a kept call derives on the Cloud, as
// keptCallTermsLocked groups calls by: its self digest over its inputs'
// classes, an input the Cloud has no class for standing for itself.
// Requires egraphMu.
func (c *Cache) keptCallTermKeyLocked(kept *keptCall) string {
	if c.testCallTermKeyed != nil {
		c.testCallTermKeyed()
	}
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

// keptCallStoredLocked reports whether the Cloud stores a record of every
// input kept names by its own recipe, so that a bundle can place the call
// with what the Cloud stores (placeableCallLocked); a witness needs none.
// Requires egraphMu.
func (c *Cache) keptCallStoredLocked(kept *keptCall) bool {
	for i, input := range kept.id.inputs {
		if kept.id.witnesses[i] {
			continue
		}
		if in := c.currentEntryForRecipeLocked(input); in == nil || in.noValueLocked() || in.persistedEnvelope == nil {
			return false
		}
	}
	return true
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

// placeableCallLocked chooses, of calls that derive one term, one the
// recipient can place, and returns it with the entry that places each of its
// inputs (callPlacementLocked), or nil when none can be placed. It prefers
// the call whose inputs are all on their own entries, which the recipient
// holds or the bundle carries (has); then on their own entries, some of
// which the bundle adds; then on entries of their classes, held or carried;
// then with some of those added: the first call of the group at the best of
// these. Requires egraphMu.
func (c *Cache) placeableCallLocked(group []*keptCall, now int64, has func(*sharedResult) bool) (*keptCall, []*sharedResult) {
	var best *keptCall
	var bestPlaces []*sharedResult
	bestRank := placementNone
	for _, kept := range group {
		rank, places := c.callPlacementLocked(kept, now, has)
		if rank < bestRank {
			best, bestPlaces, bestRank = kept, places, rank
		}
		if bestRank == placementOwnHeld {
			break
		}
	}
	return best, bestPlaces
}

// callPlacement ranks how a recipient can place a call's inputs, the best
// first (callPlacementLocked).
type callPlacement int

const (
	// placementOwnHeld: every input on its own entry, held or carried.
	placementOwnHeld callPlacement = iota
	// placementOwnStored: every input on its own entry, some stored only.
	placementOwnStored
	// placementClassHeld: some input on another entry of its class, every
	// input's entry held or carried.
	placementClassHeld
	// placementClassStored: some input on another entry of its class, some
	// input's entry stored only.
	placementClassStored
	// placementNone: some input has no entry that can place it.
	placementNone
)

// callPlacementLocked returns how the recipient can place kept's inputs and,
// by input, the entry that places it, nil for a digest-witnessed string's
// digest, which the recipient's own call reproduces. An input is placed by
// an entry of its class (inputPlaceLocked). Requires egraphMu.
func (c *Cache) callPlacementLocked(kept *keptCall, now int64, has func(*sharedResult) bool) (callPlacement, []*sharedResult) {
	places := make([]*sharedResult, len(kept.id.inputs))
	byClass, added := false, false
	for i, input := range kept.id.inputs {
		if kept.id.witnesses[i] {
			continue
		}
		entry, own, held := c.inputPlaceLocked(input, now, has)
		if entry == nil {
			return placementNone, nil
		}
		places[i] = entry
		byClass = byClass || !own
		added = added || !held
	}
	switch {
	case !byClass && !added:
		return placementOwnHeld, places
	case !byClass:
		return placementOwnStored, places
	case !added:
		return placementClassHeld, places
	default:
		return placementClassStored, places
	}
}

// inputPlaceLocked returns the entry that places input, a call's input, for
// a recipient: the entry under input's own recipe, when the recipient holds
// it or the bundle carries it (has) or the Cloud stores it; else another
// unexpired entry of input's class, the lowest-numbered one held or carried,
// else the lowest-numbered one stored. The Cloud holds input in that class
// from an engine's word, a recipe or content digest a span or record
// reported: members of a class are interchangeable, and the recipient puts
// input in the entry's class (TransferredCall.Inputs). own reports whether
// the entry is input's own, and held whether the recipient holds it or the
// bundle carries it. It returns nil when no entry can place input. Requires
// egraphMu.
func (c *Cache) inputPlaceLocked(input digest.Digest, now int64, has func(*sharedResult) bool) (entry *sharedResult, own, held bool) {
	stored := func(e *sharedResult) bool { return !e.noValueLocked() && e.persistedEnvelope != nil }
	if e := c.currentEntryForRecipeLocked(input); e != nil {
		if has(e) {
			return e, true, true
		}
		if stored(e) {
			return e, true, false
		}
	}
	root := c.eqClassRootLocked(c.egraphDigestToClass[input.String()])
	if root == 0 {
		return nil, false, false
	}
	var heldBest, storedBest *sharedResult
	for id := range c.outputEqClassResults[root] {
		member := c.resultsByID[id]
		if member == nil || c.resultExpiredAtLocked(member, now) {
			continue
		}
		switch {
		case has(member):
			if heldBest == nil || member.id < heldBest.id {
				heldBest = member
			}
		case stored(member):
			if storedBest == nil || member.id < storedBest.id {
				storedBest = member
			}
		}
	}
	if heldBest != nil {
		return heldBest, false, true
	}
	if storedBest != nil {
		return storedBest, false, false
	}
	return nil, false, false
}
