package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
)

// StoredBundleResult is StoredBundle's answer.
type StoredBundleResult struct {
	// Bundle has the records of the roots' closure and outputs for its parts,
	// without addresses or renewal keys.
	Bundle ValueBundle
	// Donors has, for each of Bundle.Outputs, its storing entry: the Cloud
	// entry whose stored parts hold it, the closure entry itself or, for a
	// part borrowed from its class, the donor. The service names that entry
	// in the output's RenewalKey.
	Donors []uint64
	// Gone are the roots the cache has no entry for, and NoValue the roots
	// known only through holdings.
	Gone    []uint64
	NoValue []uint64
	// Held are the numbers of the entries the bundle carries as held rows.
	Held []uint64
	// Position is the cache's write count when the build read it, under the
	// same hold: ChangedSince tells, for each entry it carries, whether a
	// write since changed it.
	Position uint64
	// Partial are, sorted, the numbers of the entries whose record the
	// bundle carries without something a later build could carry once what
	// it waits on is stored or joined, which no write to the entry itself
	// marks: a kept call left out for an input with no stored record, or a
	// part of its class left out for a service the closure has no row of.
	// A reader that keeps the bundle can't tell from ChangedSince alone
	// whether these changed.
	Partial []uint64
	// CallInputs are the numbers of the entries the bundle carries as
	// roots only because a call it sends has them as inputs, and
	// UnsendableCalls counts the terms of its entries' kept calls it sends
	// no call for: none had inputs it could place.
	CallInputs      []uint64
	UnsendableCalls int
	// RootOwners has, for each of Bundle.Roots, the index among the roots it
	// was asked for of the one it travels with: itself, or, for an entry a
	// call's input needs, the first root whose call needs it. StoredSizes
	// prices each root with what travels with it.
	RootOwners []int
	// stored has, by ordinal, the declared parts of each value the bundle
	// carries compressed, as held, which its check reads in place of the
	// value's envelope (checkValueBundle).
	stored map[uint64][]declaredPart
}

// HeldBy names the entries of a blob-backed cache whose values a recipient
// engine cache already holds, for StoredBundle to carry as held rows and
// StoredSizes to price as such. An entry is held when it has a holding of
// Cache that hasn't expired at the read's clock, and isn't in Not, by
// number: the values the recipient answered stale. Only an entry with a call
// frame can be held: its recipe names it to the recipient.
type HeldBy struct {
	Cache CacheID
	Not   map[uint64]bool
}

// heldLocked reports whether the recipient holds res at nowUnix. A nil
// HeldBy holds nothing. Requires egraphMu.
func (h *HeldBy) heldLocked(res *sharedResult, nowUnix int64) bool {
	if h == nil || !res.hasCallFrameLocked() {
		return false
	}
	if h.Not[uint64(res.id)] {
		return false
	}
	return h.holdsLocked(res, nowUnix)
}

// holdsLocked reports whether res has a holding of h.Cache that hasn't
// expired at nowUnix: among its holdings of that cache only
// (cacheHolderRefsLocked), each judged by its own expiry. Requires egraphMu.
func (h *HeldBy) holdsLocked(res *sharedResult, nowUnix int64) bool {
	for _, ref := range res.cacheHolderRefsLocked(h.Cache) {
		if held := ref.h; held.expiresAtUnix == 0 || held.expiresAtUnix > nowUnix {
			return true
		}
	}
	return false
}

// StoredBundle builds a bundle from the entries of a blob-backed cache, the
// Cloud's, for a merge into an engine (design 7.5). In one hold of egraphMu it
// walks the closure of the roots, over their dependencies and the entries
// that the owners and services of their own stored parts name, and copies
// every record: a replacement of a stored record, which takes the lock for
// writing, never mixes into it. Each record goes as stored, naming itself
// and its references by the Cloud's entry numbers, with the entry's number as
// SenderNumber and its replacement count; the rest of the bundle names rows
// by ordinal.
//
// Outputs follow section 5.1 point 3:
//   - an entry's own stored parts travel with its record, expired or not, as
//     they belong to that value, with their owners and services;
//   - a part the entry's record declares and it doesn't store is borrowed
//     from an unexpired entry of its class, the lowest number first, through
//     the placement rule, with its services mapped to rows of this closure of
//     the same class. A part whose services can't be mapped is left out.
//
// An entry held says the recipient holds, a root or not, becomes a held row:
// its recipe and sending facts, with no record, no outputs, and nothing
// below it walked. Each held row's recipe is derived from the entry's call
// frame after egraphMu is released, or read from the stored body that holds
// the frame: a replacement keeps the recipe, and deriving can read the
// cache. A nil held is a bundle with no held rows.
//
// Expiry, of a donor of its class and of a holding of held's cache, is
// judged at nowUnix, the reader's clock, so a bundle agrees with what its
// reader judged of the cache at that moment.
func (c *Cache) StoredBundle(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) (StoredBundleResult, error) {
	var out StoredBundleResult
	if !c.blobBacked {
		return out, fmt.Errorf("stored bundle: the cache has no blob store")
	}
	if err := context.Cause(ctx); err != nil {
		return out, err
	}
	out, frames, err := c.storedBundle(roots, nowUnix, held)
	if err != nil || len(frames) == 0 {
		return out, err
	}
	for i := range out.Bundle.Values {
		value := &out.Bundle.Values[i]
		frame, ok := frames[value.Ordinal]
		if !ok {
			continue
		}
		if value.Held, err = frame.recipeDigest(c); err != nil {
			return StoredBundleResult{}, fmt.Errorf("stored bundle: held entry %d recipe: %w", value.SenderNumber, err)
		}
	}
	if err := validateStoredBundle(out.Bundle, out.stored); err != nil {
		return StoredBundleResult{}, fmt.Errorf("stored bundle: %w", err)
	}
	return out, nil
}

// storedBundle builds StoredBundle's answer in one hold of egraphMu.RLock,
// with held rows' recipes left empty, and returns the call frame of each held
// row by ordinal. A bundle without held rows is validated here.
func (c *Cache) storedBundle(roots []uint64, nowUnix int64, held *HeldBy) (StoredBundleResult, map[TransferOrdinal]entryFrame, error) {
	var out StoredBundleResult
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	var rootEntries []*sharedResult
	seenRoot := map[sharedResultID]bool{}
	for _, number := range roots {
		res := c.resultsByID[sharedResultID(number)]
		switch {
		case res == nil:
			out.Gone = append(out.Gone, number)
		case res.noValueLocked():
			out.NoValue = append(out.NoValue, number)
		case !seenRoot[res.id]:
			seenRoot[res.id] = true
			rootEntries = append(rootEntries, res)
		}
	}
	if len(rootEntries) == 0 {
		return out, nil, nil
	}
	build := &storedBundleBuild{now: nowUnix, ordinals: map[sharedResultID]TransferOrdinal{}, heldBy: held, held: map[sharedResultID]bool{},
		owners: map[sharedResultID]int{}, ordinary: map[sharedResultID]bool{}, rootSeen: seenRoot}
	build.bundle.Version = valueBundleVersion
	visiting := map[sharedResultID]bool{}
	// Root by root, as StoredSizes prices them: its closure, then the calls
	// kept on what it added, with their inputs' closures.
	for i, res := range rootEntries {
		build.current = i
		start := len(build.order)
		if err := c.storedBundleVisitLocked(build, res, visiting); err != nil {
			return StoredBundleResult{}, nil, err
		}
		for _, entry := range build.order[start:] {
			build.ordinary[entry.id] = true
		}
		if err := c.storedBundleCallsLocked(build, start); err != nil {
			return StoredBundleResult{}, nil, fmt.Errorf("stored bundle: %w", err)
		}
	}
	rootEntries = append(rootEntries, build.callRoots...)
	var frames map[TransferOrdinal]entryFrame
	waiting := false
	for _, res := range build.order {
		waiting = waiting || res.waitsForTaughtContentLocked()
		if build.held[res.id] {
			ordinal := build.ordinals[res.id]
			build.bundle.Values = append(build.bundle.Values, TransferredValue{Ordinal: ordinal, SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: PersistedRecord{ResultID: uint64(res.id)}, ExpiresAtUnix: res.expiresAtUnix})
			if frames == nil {
				frames = map[TransferOrdinal]entryFrame{}
			}
			frames[ordinal] = res.entryFrameLocked()
			out.Held = append(out.Held, uint64(res.id))
			continue
		}
		if err := c.storedBundleValueLocked(build, res); err != nil {
			return StoredBundleResult{}, nil, err
		}
	}
	if waiting {
		c.taughtContentBundlesWaiting.Add(1)
	}
	for _, res := range rootEntries {
		expiry := res.expiresAtUnix
		if edge, ok := c.persistedEdgesByResult[res.id]; ok {
			expiry = edge.expiresAtUnix
		}
		build.bundle.Roots = append(build.bundle.Roots, TransferredRoot{Ordinal: build.ordinals[res.id], ExpiresAtUnix: expiry})
	}
	for _, res := range build.order {
		if build.held[res.id] {
			continue
		}
		if err := c.storedBundleOutputsLocked(build, res); err != nil {
			return StoredBundleResult{}, nil, err
		}
	}
	out.Bundle, out.Donors, out.stored = build.bundle, build.donors, build.stored
	out.Position = c.writes
	for id := range build.partial {
		out.Partial = append(out.Partial, uint64(id))
	}
	slices.Sort(out.Partial)
	out.CallInputs, out.UnsendableCalls = build.callInputs, build.unsendableCalls
	for _, res := range rootEntries {
		out.RootOwners = append(out.RootOwners, build.owners[res.id])
	}
	if frames == nil {
		if err := validateStoredBundle(build.bundle, build.stored); err != nil {
			return StoredBundleResult{}, nil, fmt.Errorf("stored bundle: %w", err)
		}
	}
	return out, frames, nil
}

// storedBundleBuild is a stored bundle under construction: the closure in
// dependency order, each entry's ordinal, and the outputs' storing entries.
type storedBundleBuild struct {
	now      int64
	order    []*sharedResult
	ordinals map[sharedResultID]TransferOrdinal
	bundle   ValueBundle
	donors   []uint64
	// heldBy says which entries the recipient holds, and held are the
	// closure's entries that become held rows.
	heldBy *HeldBy
	held   map[sharedResultID]bool
	// rootSeen are the roots' entries, and callRoots the entries added as
	// roots for the inputs of calls the bundle sends, callInputs their
	// numbers, and unsendableCalls the terms it sends no call for
	// (storedBundleCallsLocked).
	rootSeen        map[sharedResultID]bool
	callRoots       []*sharedResult
	callInputs      []uint64
	unsendableCalls int
	// partial are the entries whose record the build carries without
	// something a later build could carry: a kept call it sends none of
	// (storedBundleCallsLocked), or a part of its class it couldn't place
	// (storedBundleOutputsLocked).
	partial map[sharedResultID]bool
	// owners has, for each entry the build added, the index of the root it
	// was asked for that added it, or whose call's input it is (current, as
	// it's added); ordinary are those the roots' closures added, not their
	// calls' inputs.
	owners   map[sharedResultID]int
	current  int
	ordinary map[sharedResultID]bool
	// stored has, by ordinal, the declared parts of each value the build
	// carries compressed (addStoredValue).
	stored map[uint64][]declaredPart
}

// relocate rewrites a reference to an entry of the closure to its ordinal.
func (build *storedBundleBuild) relocate(ref *PersistedRef) error {
	if ref.RecipeID != nil || ref.ResultID == 0 {
		return nil
	}
	ordinal := build.ordinals[sharedResultID(ref.ResultID)]
	if ordinal == 0 {
		return fmt.Errorf("stored bundle: reference to entry %d outside the closure", ref.ResultID)
	}
	ref.ResultID = uint64(ordinal)
	return nil
}

// storedBundleVisitLocked adds res's closure to the build in dependency
// order: its dependencies, and the entries its own stored parts' owners and
// services name, before it. An entry the recipient holds is added alone, as
// a held row. Requires egraphMu.
func (c *Cache) storedBundleVisitLocked(build *storedBundleBuild, res *sharedResult, visiting map[sharedResultID]bool) error {
	if build.ordinals[res.id] != 0 {
		return nil
	}
	if visiting[res.id] {
		return fmt.Errorf("stored bundle: dependency cycle through entry %d", res.id)
	}
	if build.heldBy.heldLocked(res, build.now) {
		build.held[res.id] = true
		build.order = append(build.order, res)
		build.ordinals[res.id] = TransferOrdinal(len(build.order))
		build.owners[res.id] = build.current
		return nil
	}
	if res.noValueLocked() || res.persistedEnvelope == nil {
		return fmt.Errorf("stored bundle: entry %d in the closure has no stored record", res.id)
	}
	visiting[res.id] = true
	for _, id := range storedClosureChildrenLocked(res) {
		child := c.resultsByID[id]
		if child == nil {
			return fmt.Errorf("stored bundle: entry %d names missing entry %d", res.id, id)
		}
		if err := c.storedBundleVisitLocked(build, child, visiting); err != nil {
			return err
		}
	}
	delete(visiting, res.id)
	build.order = append(build.order, res)
	build.ordinals[res.id] = TransferOrdinal(len(build.order))
	build.owners[res.id] = build.current
	return nil
}

// storedClosureChildrenLocked returns what a stored bundle walks below res,
// sorted: its dependencies, and the entries its own stored parts' owners and
// services name. Requires egraphMu.
func storedClosureChildrenLocked(res *sharedResult) []sharedResultID {
	var children []sharedResultID
	for dep := range res.deps {
		children = append(children, dep)
	}
	for _, part := range res.storedParts {
		for _, dep := range part.held.Owner.DependencyIDs {
			children = append(children, sharedResultID(dep))
		}
		for _, service := range part.held.Value.Services {
			children = append(children, sharedResultID(service.ServiceResultID))
		}
	}
	slices.Sort(children)
	return slices.Compact(children)
}

// storedBundleValueLocked copies res's stored record into the build,
// normalized for transfer, as stored. Requires egraphMu.
func (c *Cache) storedBundleValueLocked(build *storedBundleBuild, res *sharedResult) error {
	if body := res.stored; body != nil {
		// A body goes as it is held, compressed, and the bundle's encoding
		// writes it out (TransferredValue.MarshalJSON).
		refs, parts, err := res.storedFactsLocked()
		if err != nil {
			return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
		}
		if build.stored == nil {
			build.stored = map[uint64][]declaredPart{}
		}
		build.stored[uint64(build.ordinals[res.id])] = parts
		if body.frame != nil {
			// So does the frame it holds, which is its transfer form.
			if err := build.addStoredValue(res, PersistedRecord{ResultID: uint64(res.id)}, body.envelope, refs); err != nil {
				return err
			}
			value := &build.bundle.Values[len(build.bundle.Values)-1]
			value.storedCall, value.storedCallBytes = body.frame, body.frameBytes
			return nil
		}
		call, err := normalizeTransferCall(res.loadResultCall())
		if err != nil {
			return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
		}
		return build.addStoredValue(res, PersistedRecord{ResultID: uint64(res.id), Call: call}, body.envelope, refs)
	}
	kept, err := c.storedTransferLocked(res)
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	// The record goes as stored, naming itself and its references by the
	// Cloud's entry numbers, which the receiver maps (transferSenderNumbers):
	// nothing in it is decoded or rewritten. The bundle is its caller's to
	// change, so it carries a plain copy.
	rec := kept.record
	if rec.Envelope, err = clonePersistedEnvelope(rec.Envelope); err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	rec.Call = rec.Call.clone()
	return build.addStoredValue(res, rec, nil, kept.refs)
}

// addStoredValue adds res's record to the build, with its envelope
// compressed in storedEnvelope when it is held so. The bundle is validated by
// shape (validateStoredBundle), so each reference is checked against the
// record's dependencies here, from what normalizing the record found it names
// (refs). Requires egraphMu.
func (build *storedBundleBuild) addStoredValue(res *sharedResult, rec PersistedRecord, storedEnvelope []byte, refs []uint64) error {
	for _, ref := range refs {
		if _, ok := res.deps[sharedResultID(ref)]; !ok {
			return fmt.Errorf("stored bundle: entry %d: reference to %d is not a direct dependency", res.id, ref)
		}
	}
	value := TransferredValue{Ordinal: build.ordinals[res.id], SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: rec, ExpiresAtUnix: res.expiresAtUnix, storedEnvelope: storedEnvelope}
	for dep := range res.deps {
		value.DependencyIDs = append(value.DependencyIDs, uint64(build.ordinals[dep]))
	}
	slices.Sort(value.DependencyIDs)
	build.bundle.Values = append(build.bundle.Values, value)
	return nil
}

// validateStoredBundle checks a stored bundle by its shape
// (validateValueBundleShape): every check of the bundle as a whole, and of
// each record without its payloads, a record it carries compressed by its
// call and declared parts (checkValueBundle's stored). The rest of a record's
// checks are made as the bundle is built: its payloads are validated by
// their codecs and its outputs mapped once for each of its versions, when it
// is normalized (normalizeStoredTransfer) or compressed (newStoredBody), and
// each reference is checked against its dependencies (addStoredValue).
func validateStoredBundle(bundle ValueBundle, stored map[uint64][]declaredPart) error {
	_, err := checkValueBundle(bundle, true, stored)
	return err
}

// storedTransferRecord is an entry's stored record normalized for transfer,
// without offers, and the envelope and call it was normalized from.
type storedTransferRecord struct {
	envelope *PersistedResultEnvelope
	call     *ResultCall
	record   PersistedRecord
	// refs are the entries the record's child and call references name,
	// which a bundle checks against the entry's dependencies, and parts the
	// parts it declares absent or pending.
	refs  []uint64
	parts []declaredPart
	// size is the entry's value's encoded size in a bundle, once StoredSizes
	// has measured it (measureStoredValue), with what else it read.
	size atomic.Pointer[storedValueSize]
}

// storedValueSize is a measured value size, and the entry's dependency
// revision, replacement count and expiry it was measured at: a value carries
// its dependencies, which a stored part can add to without replacing the
// record, and the other two.
type storedValueSize struct {
	// call is the frame measured with a compressed record (storedBody),
	// whose decoded call can be replaced while its body stays; nil for a
	// body that holds its frame, which is replaced with the body.
	call               *ResultCall
	dependencyRevision uint64
	replacements       uint64
	expiresAtUnix      int64
	bytes              int64
}

// storedTransferRecordLocked returns res's stored record normalized for
// transfer, without offers, and checked as validateStoredBundle relies on.
// Normalizing reads only the record's envelope and
// call, which the cache replaces and never changes in place, so the entry
// keeps the result with them and normalizes again once either is replaced.
// The kept record holds both, so neither can be freed and another take its
// address while it is kept. The result is shared: callers copy what they
// change. Requires egraphMu.
func (c *Cache) storedTransferRecordLocked(res *sharedResult) (PersistedRecord, error) {
	kept, err := c.storedTransferLocked(res)
	if err != nil {
		return PersistedRecord{}, err
	}
	return kept.record, nil
}

// storedTransferLocked is storedTransferRecordLocked's kept record whole,
// with the references it names. Requires egraphMu.
func (c *Cache) storedTransferLocked(res *sharedResult) (*storedTransferRecord, error) {
	normalized, err := normalizeStoredTransfer(res, res.persistedEnvelope, res.loadResultCall())
	if err != nil {
		return nil, err
	}
	if c.testStoredTransferNormalized != nil {
		c.testStoredTransferNormalized()
	}
	return keepStoredTransfer(res, normalized), nil
}

// keepStoredTransfer keeps normalized as res's transfer record and returns
// it, unless a record of the same envelope and call is kept already, which it
// returns instead: readers normalize at once under egraphMu.RLock, and a
// record kept first, with its measured size (StoredSizes), stays the one kept.
// The caller has checked, under egraphMu, that the envelope and call are
// res's.
func keepStoredTransfer(res *sharedResult, normalized *storedTransferRecord) *storedTransferRecord {
	for {
		kept := res.storedTransfer.Load()
		if kept != nil && kept.envelope == normalized.envelope && kept.call == normalized.call {
			return kept
		}
		if res.storedTransfer.CompareAndSwap(kept, normalized) {
			return normalized
		}
	}
}

// normalizeStoredTransfer returns res's transfer record of env and call: the
// kept one if it is of them, or a new one, normalized and not kept. It reads
// only env and call, which the cache replaces and never changes in place, so
// it needs no lock: a caller that read them under egraphMu can normalize
// after releasing it, and keeps the record (keepStoredTransfer) under
// egraphMu again only if they are still the entry's (StoredSizes'
// publishStoredSizes).
func normalizeStoredTransfer(res *sharedResult, env *PersistedResultEnvelope, call *ResultCall) (*storedTransferRecord, error) {
	if kept := res.storedTransfer.Load(); kept != nil && kept.envelope == env && kept.call == call {
		return kept, nil
	}
	rec, refs, err := normalizeTransferRecordRefs(PersistedRecord{ResultID: uint64(res.id), Envelope: *env, Call: call})
	if err != nil {
		return nil, err
	}
	rec.Envelope.PendingOffers = nil
	// A build validates its bundle by shape, so the record's payloads are
	// checked by their codecs, and its outputs mapped, here.
	if err := validateTransferPayloads(rec); err != nil {
		return nil, err
	}
	outputs, err := mapTransferredOutputs(rec)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if _, err := partAddressKey(output.Address); err != nil {
			return nil, err
		}
	}
	parts, err := declaredPartsOf(outputs)
	if err != nil {
		return nil, err
	}
	return &storedTransferRecord{envelope: env, call: call, record: rec, refs: refs, parts: parts}, nil
}

// storedBundleOutputsLocked adds res's outputs to the build: its own stored
// parts, then the declared pending parts it borrows from its class through
// the placement rule. Requires egraphMu.
func (c *Cache) storedBundleOutputsLocked(build *storedBundleBuild, res *sharedResult) error {
	keys := make([]string, 0, len(res.storedParts))
	for key := range res.storedParts {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := build.addOutput(res, res.storedParts[key].offer(), res.id); err != nil {
			return err
		}
	}
	declared, err := res.declaredPartsLocked()
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	for _, part := range declared {
		key := part.key
		if part.state != "pending" {
			continue
		}
		if _, own := res.storedParts[key]; own {
			continue
		}
		donor, part := c.storedPartDonorLocked(res, key, build.now)
		if donor == nil {
			continue
		}
		placed, ok := placeOwnedPart(part, func(service uint64) (uint64, bool) {
			row := c.closureRowOfClassLocked(sharedResultID(service), build.ordinals)
			return uint64(row), row != 0
		})
		if !ok {
			build.markPartial(res)
			continue
		}
		if err := build.addOutput(res, placed, donor.id); err != nil {
			return err
		}
	}
	return nil
}

// markPartial records that the build carries res's record without
// something a later build could carry (StoredBundleResult.Partial).
func (build *storedBundleBuild) markPartial(res *sharedResult) {
	if build.partial == nil {
		build.partial = map[sharedResultID]bool{}
	}
	build.partial[res.id] = true
}

// addOutput adds a part of res to the bundle, relocated to ordinals and
// without addresses, with donor as its storing entry.
func (build *storedBundleBuild) addOutput(res *sharedResult, part PersistedPartOffer, donor sharedResultID) error {
	part.Chain.Addresses, part.Chain.RenewalKey = nil, ""
	if err := visitPersistedPartOffer(&part, nil, build.relocate); err != nil {
		return fmt.Errorf("stored bundle: entry %d part: %w", res.id, err)
	}
	build.bundle.Outputs = append(build.bundle.Outputs, TransferredOutput{Ordinal: build.ordinals[res.id], Address: part.Address, State: "completed", Value: &part.Value, Chain: &part.Chain, Owner: &part.Owner})
	build.donors = append(build.donors, uint64(donor))
	return nil
}

// storedPartDonorLocked returns the unexpired entry of res's class, other
// than res, with the lowest number that stores the part, and a copy of the
// part's offer.
// Requires egraphMu.
func (c *Cache) storedPartDonorLocked(res *sharedResult, key string, now int64) (*sharedResult, PersistedPartOffer) {
	var best *sharedResult
	for root := range c.outputEqClassRootsLocked(res.id) {
		for id := range c.outputEqClassResults[root] {
			member := c.resultsByID[id]
			if member == nil || member == res || c.resultExpiredAtLocked(member, now) {
				continue
			}
			if _, ok := member.storedParts[key]; ok && (best == nil || member.id < best.id) {
				best = member
			}
		}
	}
	if best == nil {
		return nil, PersistedPartOffer{}
	}
	return best, best.storedParts[key].offer()
}

// closureRowOfClassLocked maps a service entry to a row of the closure of
// the same class, the lowest number first: the entry itself, if the closure
// holds it, or another entry of its class. It returns 0 when the closure
// holds none; the entry numbers in the returned part are relocated to
// ordinals afterwards. Requires egraphMu.
func (c *Cache) closureRowOfClassLocked(service sharedResultID, ordinals map[sharedResultID]TransferOrdinal) sharedResultID {
	if ordinals[service] != 0 {
		return service
	}
	if c.resultsByID[service] == nil {
		return 0
	}
	var best sharedResultID
	for root := range c.outputEqClassRootsLocked(service) {
		for id := range c.outputEqClassResults[root] {
			if ordinals[id] != 0 && (best == 0 || id < best) {
				best = id
			}
		}
	}
	return best
}
