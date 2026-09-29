package dagql

import (
	"context"
	"fmt"
	"slices"
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
func (c *Cache) StoredBundle(ctx context.Context, roots []uint64) (StoredBundleResult, error) {
	var out StoredBundleResult
	if !c.blobBacked {
		return out, fmt.Errorf("stored bundle: the cache has no blob store")
	}
	if err := context.Cause(ctx); err != nil {
		return out, err
	}
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
		return out, nil
	}
	build := &storedBundleBuild{now: time.Now().Unix(), ordinals: map[sharedResultID]TransferOrdinal{}}
	build.bundle.Version = valueBundleVersion
	visiting := map[sharedResultID]bool{}
	for _, res := range rootEntries {
		if err := c.storedBundleVisitLocked(build, res, visiting); err != nil {
			return StoredBundleResult{}, err
		}
	}
	for _, res := range build.order {
		if err := c.storedBundleValueLocked(build, res); err != nil {
			return StoredBundleResult{}, err
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
		if err := c.storedBundleOutputsLocked(build, res); err != nil {
			return StoredBundleResult{}, err
		}
	}
	if _, err := validateValueBundle(build.bundle); err != nil {
		return StoredBundleResult{}, fmt.Errorf("stored bundle: %w", err)
	}
	out.Bundle, out.Donors = build.bundle, build.donors
	return out, nil
}

// storedBundleBuild is a stored bundle under construction: the closure in
// dependency order, each entry's ordinal, and the outputs' storing entries.
type storedBundleBuild struct {
	now      int64
	order    []*sharedResult
	ordinals map[sharedResultID]TransferOrdinal
	bundle   ValueBundle
	donors   []uint64
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
// services name, before it. Requires egraphMu.
func (c *Cache) storedBundleVisitLocked(build *storedBundleBuild, res *sharedResult, visiting map[sharedResultID]bool) error {
	if build.ordinals[res.id] != 0 {
		return nil
	}
	if visiting[res.id] {
		return fmt.Errorf("stored bundle: dependency cycle through entry %d", res.id)
	}
	if res.noValueLocked() || res.persistedEnvelope == nil {
		return fmt.Errorf("stored bundle: entry %d in the closure has no stored record", res.id)
	}
	visiting[res.id] = true
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
	for _, id := range slices.Compact(children) {
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

// storedBundleValueLocked copies res's stored record into the build,
// normalized for transfer and relocated to ordinals. Requires egraphMu.
func (c *Cache) storedBundleValueLocked(build *storedBundleBuild, res *sharedResult) error {
	normalized, err := normalizeTransferRecord(PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: res.loadResultCall()})
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	normalized.Envelope.PendingOffers = nil
	rec, err := VisitEncodedReferences(normalized, build.relocate)
	if err != nil {
		return fmt.Errorf("stored bundle: entry %d: %w", res.id, err)
	}
	value := TransferredValue{Ordinal: build.ordinals[res.id], SenderNumber: uint64(res.id), SenderReplacements: res.replacements, Record: rec, ExpiresAtUnix: res.expiresAtUnix}
	for dep := range res.deps {
		value.DependencyIDs = append(value.DependencyIDs, uint64(build.ordinals[dep]))
	}
	slices.Sort(value.DependencyIDs)
	build.bundle.Values = append(build.bundle.Values, value)
	return nil
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
