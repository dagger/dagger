package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"slices"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type offerOwnerID uint64
type collectionQueue = []*sharedResult

// offerOwner owns exact rows independently of result dependency membership.
// All fields and the registry are guarded by egraphMu. While the owner holds
// a slot, origin and address name the entry and part whose offer it is: a
// merge's cycle check walks up through them (offerHolderLocked).
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
		for _, offer := range res.partOffersLocked() {
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

// partAddressKey is a part address's key: its JSON encoding. Every call row
// keys its parts, so the common address, a part of the value itself under a
// name of plain characters, is written out directly, as json.Marshal writes
// it: the encoder and the path's check cost a call row more than the rest of
// its parts' handling.
func partAddressKey(address PersistedPartAddress) (string, error) {
	if address.Part == "" {
		return "", fmt.Errorf("part address: empty part")
	}
	if len(address.OutputPath) == 0 && plainJSONString(string(address.Part)) {
		return `{"part":"` + string(address.Part) + `"}`, nil
	}
	if _, err := canonicalPath(address.OutputPath); err != nil {
		return "", err
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

// Slot publication transfers the preparation hold. The offer goes into the
// holding of the cache it came from, res's image or Cloud holding
// (offerHoldingLocked), which the caller has noted. Callers release a refused
// preparation and run any collection callbacks after leaving egraphMu.
func (c *Cache) attachPartOfferLocked(res *sharedResult, address PersistedPartAddress, offer *partOffer) error {
	key, err := c.validateOfferAttachmentLocked(res, address, offer)
	if err != nil {
		return err
	}
	_, cloud := res.offerHoldingLocked()
	if cloud == nil {
		return fmt.Errorf("offer attachment: result %d has no Cloud or image holding", res.id)
	}
	if cloud.partLocked(key).offer != nil {
		return fmt.Errorf("offer attachment: occupied address %s", key)
	}
	if offer.owner.slots != 0 {
		return fmt.Errorf("offer attachment: owner already attached")
	}
	cloud.setPartLocked(key, heldPart{state: heldPartComplete, offer: offer})
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
	old := res.partOfferLocked(key)
	if old == nil {
		return nil, c.attachPartOfferLocked(res, address, offer)
	}
	if offer.owner != old.owner && offer.owner.slots != 0 {
		return nil, fmt.Errorf("offer replacement: owner already attached")
	}
	_, cloud := res.offerHoldingLocked()
	cloud.setPartLocked(key, heldPart{state: heldPartComplete, offer: offer})
	c.partOfferReplaced(old, offer)
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
	old := res.partOfferLocked(key)
	if old == nil {
		return nil, nil
	}
	_, cloud := res.offerHoldingLocked()
	cloud.deletePartLocked(key)
	c.partOfferGone(old)
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
	cloned := make([]PersistedPartOffer, len(offers))
	for i, offer := range offers {
		var err error
		cloned[i], err = clonePartOffer(offer)
		if err != nil {
			return nil, fmt.Errorf("%w: copy pending offers: %w", ErrPersistStateNotReady, err)
		}
	}
	return cloned, nil
}

// clonePartOffer copies offer for a caller that may change the copy: every
// nested map, slice and pointer of its address, value, owner and chain, so
// that reference relocation and the removal of addresses can't reach the
// original. The copy shares the chain's layers. An offered layer is never
// written after it's built: not the layer, its descriptor's annotations,
// URLs, data or platform, nor its creation time. The layers are most of an
// offer, and every copy of every offer repeated them. It refuses an offer
// that can't be encoded as its record is, which only a layer's creation time
// can make it.
func clonePartOffer(offer PersistedPartOffer) (PersistedPartOffer, error) {
	offer.Address.OutputPath = slices.Clone(offer.Address.OutputPath)
	offer.Value.Platform = clonePlatform(offer.Value.Platform)
	offer.Value.Services = slices.Clone(offer.Value.Services)
	for i := range offer.Value.Services {
		offer.Value.Services[i].Aliases = slices.Clone(offer.Value.Services[i].Aliases)
	}
	for _, layer := range offer.Chain.Layers {
		if layer.CreatedAt != nil {
			if _, err := layer.CreatedAt.MarshalJSON(); err != nil {
				return PersistedPartOffer{}, err
			}
		}
	}
	// Clipped, so that an append to the copy's layers can't write over the
	// original's.
	offer.Chain.Layers = slices.Clip(offer.Chain.Layers)
	offer.Chain.Addresses = maps.Clone(offer.Chain.Addresses)
	offer.Chain.Packs = maps.Clone(offer.Chain.Packs)
	offer.Owner.DependencyIDs = slices.Clone(offer.Owner.DependencyIDs)
	return offer, nil
}

func clonePlatform(platform *ocispecs.Platform) *ocispecs.Platform {
	if platform == nil {
		return nil
	}
	cloned := *platform
	cloned.OSFeatures = slices.Clone(platform.OSFeatures)
	return &cloned
}
func (res *sharedResult) pendingOffersLocked() ([]PersistedPartOffer, error) {
	if _, image := res.imageHoldingLocked(); image != nil {
		// The image's offers aren't saved: the merge of the image's bundle at
		// the next start attaches them again, and an image that no longer
		// ships the entry leaves it to run its recipe.
		return nil, nil
	}
	return res.attachedOffersLocked()
}

// attachedOffersLocked is every offer attached to res, by part key, the
// image's included.
func (res *sharedResult) attachedOffersLocked() ([]PersistedPartOffer, error) {
	var keys []string
	for key := range res.partOffersLocked() {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	slices.Sort(keys)
	offers := make([]PersistedPartOffer, 0, len(keys))
	for _, key := range keys {
		offers = append(offers, res.partOfferLocked(key).record)
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
			} else if !yield(child.resultID) {
				return
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
// Rows are checked on all cores: a check reads only its own row, and nothing
// writes rows while the caller holds egraphMu.
func (c *Cache) validateStoredOwnershipLocked() error {
	results := make([]*sharedResult, 0, len(c.resultsByID))
	for _, res := range c.resultsByID {
		if res.persistedEnvelope != nil {
			results = append(results, res)
		}
	}
	return forEachIndexParallel(len(results), func(i int) error {
		return validateStoredOwnership(results[i])
	})
}

func validateStoredOwnership(res *sharedResult) error {
	env := *res.persistedEnvelope
	for _, offer := range env.PendingOffers {
		if err := validateOfferReferences(offer); err != nil {
			return fmt.Errorf("result %d: %w", res.id, err)
		}
	}
	env.PendingOffers = nil
	if res.stored != nil {
		// A record held compressed names what its body's facts say.
		refs, _, err := res.storedFactsLocked()
		if err != nil {
			return fmt.Errorf("result %d: %w", res.id, err)
		}
		for _, ref := range refs {
			if _, ok := res.deps[sharedResultID(ref)]; !ok {
				return fmt.Errorf("result %d: reference to %d is not a direct dependency", res.id, ref)
			}
		}
		return nil
	}
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
	return nil
}

// plainJSONString reports whether json.Marshal writes s between its quotes as
// it is: printable ASCII with nothing it escapes, neither a quote nor a
// backslash, nor <, > and &, which it escapes for HTML.
func plainJSONString(s string) bool {
	for i := 0; i < len(s); i++ {
		switch b := s[i]; {
		case b < 0x20 || b >= 0x7f:
			return false
		case b == '"' || b == '\\' || b == '<' || b == '>' || b == '&':
			return false
		}
	}
	return true
}
