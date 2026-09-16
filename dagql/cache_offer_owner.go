package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
)

type offerOwnerID uint64
type collectionQueue = []*sharedResult

// offerOwner owns exact rows independently of result dependency membership.
// All fields and the registry are guarded by egraphMu. Origin is diagnostic.
type offerOwner struct {
	id      offerOwnerID
	record  PersistedOfferOwner
	holds   int64
	slots   int64
	deps    []*sharedResult
	origin  sharedResultID
	address PersistedPartAddress
}
type partOffer struct {
	record PersistedPartOffer
	owner  *offerOwner
}
type resultOwnershipChild struct {
	result   *sharedResult
	resultID sharedResultID
	owner    *offerOwner
}

func (c *Cache) resultOwnershipChildrenLocked(res *sharedResult) iter.Seq[resultOwnershipChild] {
	return func(yield func(resultOwnershipChild) bool) {
		for id := range res.deps {
			if !yield(resultOwnershipChild{result: c.resultsByID[id], resultID: id}) {
				return
			}
		}
		for _, offer := range res.partOffers {
			if !yield(resultOwnershipChild{owner: offer.owner}) {
				return
			}
		}
	}
}
func offerOwnershipChildrenLocked(owner *offerOwner) iter.Seq[*sharedResult] {
	return slices.Values(owner.deps)
}

func (c *Cache) newOfferOwnerLocked(ctx context.Context, record PersistedOfferOwner) (*offerOwner, error) {
	ids := slices.Clone(record.DependencyIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	deps := make([]*sharedResult, 0, len(ids))
	for _, id := range ids {
		dep := c.resultsByID[sharedResultID(id)]
		if id == 0 || dep == nil {
			return nil, fmt.Errorf("offer owner: missing dependency %d", id)
		}
		deps = append(deps, dep)
	}
	if c.offerOwners == nil {
		c.offerOwners = make(map[offerOwnerID]*offerOwner)
	}
	c.nextOfferOwnerID++
	owner := &offerOwner{id: c.nextOfferOwnerID, record: PersistedOfferOwner{DependencyIDs: ids}, holds: 1, deps: deps}
	c.offerOwners[owner.id] = owner
	for dep := range offerOwnershipChildrenLocked(owner) {
		if dep.offerParents == nil {
			dep.offerParents = make(map[offerOwnerID]struct{})
		}
		dep.offerParents[owner.id] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, dep)
	}
	return owner, nil
}
func (c *Cache) retainOfferOwnerLocked(owner *offerOwner) {
	if owner == nil || c.offerOwners[owner.id] != owner || owner.holds <= 0 {
		panic("retain unregistered offer owner")
	}
	owner.holds++
}
func (c *Cache) releaseOfferOwnerLocked(ctx context.Context, owner *offerOwner) (collectionQueue, error) {
	if owner == nil || c.offerOwners[owner.id] != owner || owner.holds <= owner.slots {
		return nil, fmt.Errorf("release offer owner: no independent hold")
	}
	owner.holds--
	if owner.holds != 0 {
		return nil, nil
	}
	delete(c.offerOwners, owner.id)
	var queue collectionQueue
	var rerr error
	for dep := range offerOwnershipChildrenLocked(owner) {
		var err error
		delete(dep.offerParents, owner.id)
		queue, err = c.decrementIncomingOwnershipLocked(ctx, dep, queue)
		rerr = errors.Join(rerr, err)
	}
	return queue, rerr
}

func partAddressKey(address PersistedPartAddress) (string, error) {
	if address.Part == "" {
		return "", fmt.Errorf("part address: empty part")
	}
	for _, elem := range address.OutputPath {
		if elem.IsIndex {
			if elem.Index < 0 || elem.Field != "" {
				return "", fmt.Errorf("part address: invalid index")
			}
		} else if elem.Field == "" || elem.Index != 0 {
			return "", fmt.Errorf("part address: invalid field")
		}
	}
	data, err := json.Marshal(address)
	return string(data), err
}

func (c *Cache) validateOfferAttachmentLocked(res *sharedResult, address PersistedPartAddress, offer *partOffer) (string, error) {
	key, err := partAddressKey(address)
	if err != nil {
		return "", err
	}
	if res == nil || res.id == 0 || c.resultsByID[res.id] != res {
		return "", fmt.Errorf("offer attachment: unregistered receiver")
	}
	if offer == nil || offer.owner == nil || c.offerOwners[offer.owner.id] != offer.owner || offer.owner.holds <= offer.owner.slots {
		return "", fmt.Errorf("offer attachment: missing preparation hold")
	}
	describedKey, err := partAddressKey(offer.record.Address)
	if err != nil || describedKey != key {
		return "", fmt.Errorf("offer attachment: descriptor address mismatch")
	}
	if !slices.Equal(offer.record.Owner.DependencyIDs, offer.owner.record.DependencyIDs) {
		return "", fmt.Errorf("offer attachment: owner record mismatch")
	}
	if err := validateOfferReferences(offer.record); err != nil {
		return "", err
	}
	seen := map[sharedResultID]bool{}
	var walk func(*sharedResult) error
	walk = func(row *sharedResult) error {
		if row == nil || c.resultsByID[row.id] != row {
			return fmt.Errorf("offer attachment: unregistered dependency")
		}
		if row == res {
			return fmt.Errorf("offer attachment: ownership cycle through result %d", res.id)
		}
		if seen[row.id] {
			return nil
		}
		seen[row.id] = true
		for child := range c.resultOwnershipChildrenLocked(row) {
			if child.owner != nil {
				for dep := range offerOwnershipChildrenLocked(child.owner) {
					if err := walk(dep); err != nil {
						return err
					}
				}
			} else if err := walk(child.result); err != nil {
				return err
			}
		}
		return nil
	}
	for dep := range offerOwnershipChildrenLocked(offer.owner) {
		if err := walk(dep); err != nil {
			return "", err
		}
	}
	return key, nil
}

// Slot publication transfers the preparation hold. Callers release a refused
// preparation and run any collection callbacks after leaving egraphMu.
func (c *Cache) attachPartOfferLocked(res *sharedResult, address PersistedPartAddress, offer *partOffer) error {
	key, err := c.validateOfferAttachmentLocked(res, address, offer)
	if err != nil {
		return err
	}
	if res.partOffers[key] != nil {
		return fmt.Errorf("offer attachment: occupied address %s", key)
	}
	if offer.owner.slots != 0 {
		return fmt.Errorf("offer attachment: owner already attached")
	}
	if res.partOffers == nil {
		res.partOffers = make(map[string]*partOffer)
	}
	res.partOffers[key] = offer
	offer.owner.slots++
	offer.owner.origin, offer.owner.address = res.id, address
	res.transferRevision++
	res.dependencyOwnershipRevision++
	return nil
}
func (c *Cache) replacePartOfferLocked(ctx context.Context, res *sharedResult, address PersistedPartAddress, offer *partOffer) (collectionQueue, error) {
	key, err := c.validateOfferAttachmentLocked(res, address, offer)
	if err != nil {
		return nil, err
	}
	old := res.partOffers[key]
	if old == nil {
		return nil, c.attachPartOfferLocked(res, address, offer)
	}
	if offer.owner != old.owner && offer.owner.slots != 0 {
		return nil, fmt.Errorf("offer replacement: owner already attached")
	}
	res.partOffers[key] = offer
	offer.owner.slots++
	offer.owner.origin, offer.owner.address = res.id, address
	old.owner.slots--
	res.transferRevision++
	res.dependencyOwnershipRevision++
	return c.releaseOfferOwnerLocked(ctx, old.owner)
}
func (c *Cache) retirePartOfferLocked(ctx context.Context, res *sharedResult, address PersistedPartAddress) (collectionQueue, error) {
	key, err := partAddressKey(address)
	if err != nil {
		return nil, err
	}
	old := res.partOffers[key]
	if old == nil {
		return nil, nil
	}
	delete(res.partOffers, key)
	old.owner.slots--
	res.transferRevision++
	res.dependencyOwnershipRevision++
	return c.releaseOfferOwnerLocked(ctx, old.owner)
}

func visitPersistedPartOffer(offer *PersistedPartOffer, path PersistedRefPath, visit PersistedRefVisitor) error {
	for i := range offer.Owner.DependencyIDs {
		if _, err := VisitPersistedRow(visit, PersistedRefChild, path.Field("owner").Field("dependencyIDs").Index(i), &offer.Owner.DependencyIDs[i]); err != nil {
			return err
		}
	}
	for i := range offer.Value.Services {
		if _, err := VisitPersistedRow(visit, PersistedRefChild, path.Field("value").Field("services").Index(i).Field("serviceResultID"), &offer.Value.Services[i].ServiceResultID); err != nil {
			return err
		}
	}
	return nil
}
func validateOfferReferences(offer PersistedPartOffer) error {
	ids := map[uint64]bool{}
	for _, id := range offer.Owner.DependencyIDs {
		if id == 0 || ids[id] {
			return fmt.Errorf("offer owner: zero or duplicate dependency %d", id)
		}
		ids[id] = true
	}
	for _, service := range offer.Value.Services {
		if service.ServiceResultID == 0 {
			return fmt.Errorf("offer descriptor: zero service reference")
		}
	}
	return visitPersistedPartOffer(&offer, nil, func(ref *PersistedRef) error {
		if !ids[ref.ResultID] {
			return fmt.Errorf("offer descriptor: reference %d outside owner dependencies", ref.ResultID)
		}
		return nil
	})
}
func clonePartOffers(offers []PersistedPartOffer) ([]PersistedPartOffer, error) {
	if offers == nil {
		return nil, nil
	}
	// The JSON round trip deliberately copies every nested map and slice,
	// including layer annotations, before reference relocation can mutate them.
	data, err := json.Marshal(offers)
	if err != nil {
		return nil, fmt.Errorf("%w: copy pending offers: %v", ErrPersistStateNotReady, err)
	}
	var cloned []PersistedPartOffer
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("%w: copy pending offers: %v", ErrPersistStateNotReady, err)
	}
	return cloned, nil
}
func (res *sharedResult) pendingOffersLocked() ([]PersistedPartOffer, error) {
	if len(res.partOffers) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(res.partOffers))
	for key := range res.partOffers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	offers := make([]PersistedPartOffer, 0, len(keys))
	for _, key := range keys {
		offers = append(offers, res.partOffers[key].record)
	}
	return clonePartOffers(offers)
}

// IsImportedResult reports immutable row origin, including after typed decode.
func IsImportedResult(result AnyResult) bool {
	return result != nil && result.cacheSharedResult() != nil && result.cacheSharedResult().imported
}

// ownedResultIDsLocked projects ownership paths for closure-only walks. It
// deliberately preserves duplicates; it is never a direct dependency set.
func (c *Cache) ownedResultIDsLocked(res *sharedResult) iter.Seq[sharedResultID] {
	return func(yield func(sharedResultID) bool) {
		for child := range c.resultOwnershipChildrenLocked(res) {
			if child.owner != nil {
				for dep := range offerOwnershipChildrenLocked(child.owner) {
					if !yield(dep.id) {
						return
					}
				}
			} else {
				if !yield(child.resultID) {
					return
				}
			}
		}
	}
}
func offerMetadataBytes(owner *offerOwner) int64 {
	return 128 + int64(len(owner.record.DependencyIDs))*16
}

func (c *Cache) restoreOfferOwnersLocked(ctx context.Context) error {
	for _, res := range c.resultsByID {
		env := res.persistedEnvelope
		if env == nil {
			continue
		}
		for _, record := range env.PendingOffers {
			owner, err := c.newOfferOwnerLocked(ctx, record.Owner)
			if err != nil {
				return err
			}
			record.Owner = owner.record
			if err := c.attachPartOfferLocked(res, record.Address, &partOffer{record: record, owner: owner}); err != nil {
				return err
			}
		}
	}
	return nil
}

type CacheDebugOfferOwner struct {
	OwnerID        uint64               `json:"owner_id"`
	OriginResultID uint64               `json:"origin_result_id,omitempty"`
	Address        PersistedPartAddress `json:"address"`
	HoldCount      int64                `json:"hold_count"`
	SlotCount      int64                `json:"slot_count"`
	DependencyIDs  []uint64             `json:"dependency_ids,omitempty"`
}

func (c *Cache) debugOfferOwnersLocked() []CacheDebugOfferOwner {
	owners := make([]CacheDebugOfferOwner, 0, len(c.offerOwners))
	for _, owner := range c.offerOwners {
		owners = append(owners, CacheDebugOfferOwner{OwnerID: uint64(owner.id), OriginResultID: uint64(owner.origin), Address: owner.address, HoldCount: owner.holds, SlotCount: owner.slots, DependencyIDs: slices.Clone(owner.record.DependencyIDs)})
	}
	slices.SortFunc(owners, func(a, b CacheDebugOfferOwner) int {
		if a.OwnerID < b.OwnerID {
			return -1
		}
		if a.OwnerID > b.OwnerID {
			return 1
		}
		return 0
	})
	return owners
}

// validateStoredOwnershipLocked scopes offer references to their own owners.
// The ordinary envelope walker receives a copy without those separate edges.
func (c *Cache) validateStoredOwnershipLocked() error {
	for _, res := range c.resultsByID {
		if res.persistedEnvelope == nil {
			continue
		}
		env := *res.persistedEnvelope
		for _, offer := range env.PendingOffers {
			if err := validateOfferReferences(offer); err != nil {
				return fmt.Errorf("result %d: %w", res.id, err)
			}
		}
		env.PendingOffers = nil
		_, err := VisitEncodedReferences(PersistedRecord{ResultID: uint64(res.id), Envelope: env, Call: res.loadResultCall(), SnapshotLinks: res.loadSnapshotOwnerLinks()}, func(ref *PersistedRef) error {
			if ref.RecipeID != nil {
				return nil
			}
			switch ref.Kind {
			case PersistedRefChild, PersistedRefCall:
				if _, ok := res.deps[sharedResultID(ref.ResultID)]; !ok {
					return fmt.Errorf("reference %s to %d is not a direct dependency", ref.Path, ref.ResultID)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("result %d: %w", res.id, err)
		}
	}
	return nil
}
