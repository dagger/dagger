package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"time"
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
}

// HeldBy names the entries of a blob-backed cache whose values a recipient
// engine cache already holds, for StoredBundle to carry as held rows and
// StoredSizes to price as such. An entry is held when it has a holding of
// Cache that hasn't expired at Now (Unix seconds), and isn't in Not, by
// number: the values the recipient answered stale. Only an entry with a call
// frame can be held: its recipe names it to the recipient.
type HeldBy struct {
	Cache CacheID
	Now   int64
	Not   map[uint64]bool
}

// heldLocked reports whether the recipient holds res. A nil HeldBy holds
// nothing. Requires egraphMu.
func (h *HeldBy) heldLocked(res *sharedResult) bool {
	if h == nil || res.loadResultCall() == nil {
		return false
	}
	if h.Not[uint64(res.id)] {
		return false
	}
	for key, held := range res.holders {
		if key.Cache == h.Cache && (held.expiresAtUnix == 0 || held.expiresAtUnix > h.Now) {
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
// writing, never mixes into it. Records are relocated to ordinals, with the
// entry's number as SenderNumber and its replacement count.
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
// frame after egraphMu is released: a replacement keeps the recipe, and
// deriving can read the cache. A nil held is a bundle with no held rows.
func (c *Cache) StoredBundle(ctx context.Context, roots []uint64, held *HeldBy) (StoredBundleResult, error) {
	var out StoredBundleResult
	if !c.blobBacked {
		return out, fmt.Errorf("stored bundle: the cache has no blob store")
	}
	if err := context.Cause(ctx); err != nil {
		return out, err
	}
	out, frames, err := c.storedBundle(roots, held)
	if err != nil || len(frames) == 0 {
		return out, err
	}
	for i := range out.Bundle.Values {
		value := &out.Bundle.Values[i]
		frame := frames[value.Ordinal]
		if frame == nil {
			continue
		}
		if value.Held, err = frame.deriveRecipeDigest(c); err != nil {
			return StoredBundleResult{}, fmt.Errorf("stored bundle: held entry %d recipe: %w", value.SenderNumber, err)
		}
	}
	if err := validateStoredBundle(out.Bundle); err != nil {
		return StoredBundleResult{}, fmt.Errorf("stored bundle: %w", err)
	}
	return out, nil
}

// storedBundle builds StoredBundle's answer in one hold of egraphMu.RLock,
// with held rows' recipes left empty, and returns the call frame of each held
// row by ordinal. A bundle without held rows is validated here.
func (c *Cache) storedBundle(roots []uint64, held *HeldBy) (StoredBundleResult, map[TransferOrdinal]*ResultCall, error) {
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
	build := &storedBundleBuild{now: time.Now().Unix(), ordinals: map[sharedResultID]TransferOrdinal{}, heldBy: held, held: map[sharedResultID]bool{}}
	build.bundle.Version = valueBundleVersion
	visiting := map[sharedResultID]bool{}
	for _, res := range rootEntries {
		if err := c.storedBundleVisitLocked(build, res, visiting); err != nil {
			return StoredBundleResult{}, nil, err
		}
	}
	var frames map[TransferOrdinal]*ResultCall
	for _, res := range build.order {
		if build.held[res.id] {
			ordinal := build.ordinals[res.id]
			build.bundle.Values = append(build.bundle.Values, TransferredValue{Ordinal: ordinal, SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: PersistedRecord{ResultID: uint64(ordinal)}, ExpiresAtUnix: res.expiresAtUnix})
			if frames == nil {
				frames = map[TransferOrdinal]*ResultCall{}
			}
			frames[ordinal] = res.loadResultCall()
			out.Held = append(out.Held, uint64(res.id))
			continue
		}
		if err := c.storedBundleValueLocked(build, res); err != nil {
			return StoredBundleResult{}, nil, err
		}
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
	out.Bundle, out.Donors = build.bundle, build.donors
	if frames == nil {
		if err := validateStoredBundle(build.bundle); err != nil {
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
	if build.heldBy.heldLocked(res) {
		build.held[res.id] = true
		build.order = append(build.order, res)
		build.ordinals[res.id] = TransferOrdinal(len(build.order))
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
		for _, dep := range part.Owner.DependencyIDs {
			children = append(children, sharedResultID(dep))
		}
		for _, service := range part.Value.Services {
			children = append(children, sharedResultID(service.ServiceResultID))
		}
	}
	slices.Sort(children)
	return slices.Compact(children)
}

// storedBundleValueLocked copies res's stored record into the build,
// normalized for transfer and relocated to ordinals. Requires egraphMu.
func (c *Cache) storedBundleValueLocked(build *storedBundleBuild, res *sharedResult) error {
	normalized, err := storedTransferRecordLocked(res)
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	// Relocation leaves what it doesn't rewrite shared with its input, and
	// the bundle is its caller's to change.
	normalized.Envelope, err = clonePersistedEnvelope(normalized.Envelope)
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	value := TransferredValue{Ordinal: build.ordinals[res.id], SenderNumber: uint64(res.id), SenderReplacements: res.replacements, ExpiresAtUnix: res.expiresAtUnix}
	direct := make(map[uint64]bool, len(res.deps))
	for dep := range res.deps {
		ordinal := uint64(build.ordinals[dep])
		value.DependencyIDs = append(value.DependencyIDs, ordinal)
		direct[ordinal] = true
	}
	slices.Sort(value.DependencyIDs)
	// The bundle is validated by shape (validateStoredBundle), so each
	// reference is checked against the record's dependencies here, as it is
	// relocated.
	checkDirect := directTransferReferences(direct)
	value.Record, err = VisitEncodedReferences(normalized, func(ref *PersistedRef) error {
		if err := build.relocate(ref); err != nil {
			return err
		}
		return checkDirect(ref)
	})
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	build.bundle.Values = append(build.bundle.Values, value)
	return nil
}

// validateStoredBundle checks a stored bundle by its shape
// (validateValueBundleShape): every check of the bundle as a whole, and of
// each record without its payloads. The rest of a record's checks are made
// as the bundle is built: its payloads are validated by their codecs and its
// outputs mapped once for each of its versions, when it is normalized
// (storedTransferRecordLocked), relocation rewrites only its references, and
// each reference is checked against its dependencies as it is relocated
// (storedBundleValueLocked).
func validateStoredBundle(bundle ValueBundle) error {
	_, err := validateValueBundleShape(bundle)
	return err
}

// storedTransferRecord is an entry's stored record normalized for transfer,
// without offers, and the envelope and call it was normalized from.
type storedTransferRecord struct {
	envelope *PersistedResultEnvelope
	call     *ResultCall
	record   PersistedRecord
	// size is the entry's value's encoded size in a bundle, once StoredSizes
	// has measured it (storedValueSizeLocked), with what else it read.
	size atomic.Pointer[storedValueSize]
}

// storedValueSize is a measured value size, and the entry's dependency
// revision, replacement count and expiry it was measured at: a value carries
// its dependencies, which a stored part can add to without replacing the
// record, and the other two.
type storedValueSize struct {
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
func storedTransferRecordLocked(res *sharedResult) (PersistedRecord, error) {
	env, call := res.persistedEnvelope, res.loadResultCall()
	if kept := res.storedTransfer.Load(); kept != nil && kept.envelope == env && kept.call == call {
		return kept.record, nil
	}
	rec, err := normalizeTransferRecord(PersistedRecord{ResultID: uint64(res.id), Envelope: *env, Call: call})
	if err != nil {
		return PersistedRecord{}, err
	}
	rec.Envelope.PendingOffers = nil
	// A build validates its bundle by shape, so the record's payloads are
	// checked by their codecs, and its outputs mapped, here.
	if err := validateTransferPayloads(rec); err != nil {
		return PersistedRecord{}, err
	}
	outputs, err := mapTransferredOutputs(rec)
	if err != nil {
		return PersistedRecord{}, err
	}
	for _, output := range outputs {
		if _, err := partAddressKey(output.Address); err != nil {
			return PersistedRecord{}, err
		}
	}
	res.storedTransfer.Store(&storedTransferRecord{envelope: env, call: call, record: rec})
	return rec, nil
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
		copied, err := clonePartOffers([]PersistedPartOffer{res.storedParts[key]})
		if err != nil {
			return err
		}
		if err := build.addOutput(res, copied[0], res.id); err != nil {
			return err
		}
	}
	declared, err := mapTransferredOutputs(PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: res.loadResultCall()})
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	for _, output := range declared {
		key, err := partAddressKey(output.Address)
		if err != nil || output.State != "pending" {
			continue
		}
		if _, own := res.storedParts[key]; own {
			continue
		}
		donor, part := c.storedPartDonorLocked(res, key, build.now)
		if donor == nil {
			continue
		}
		placed, ok := PlacePart(part, func(service uint64) (uint64, bool) {
			row := c.closureRowOfClassLocked(sharedResultID(service), build.ordinals)
			return uint64(row), row != 0
		})
		if !ok {
			continue
		}
		if err := build.addOutput(res, placed, donor.id); err != nil {
			return err
		}
	}
	return nil
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
// than res, with the lowest number that stores the part, and the part.
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
	return best, best.storedParts[key]
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
