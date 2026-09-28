package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

//nolint:gocyclo // one check per bundle field and record kind; splitting hides the order of the checks
func validateValueBundle(bundle ValueBundle) ([]uint64, error) {
	if bundle.Version != valueBundleVersion {
		return nil, fmt.Errorf("unsupported value bundle version %d", bundle.Version)
	}
	if len(bundle.Roots) == 0 {
		return nil, fmt.Errorf("bundle has no roots")
	}
	rows := map[uint64]TransferredValue{}
	for _, row := range bundle.Values {
		id := uint64(row.Ordinal)
		if id == 0 || id > uint64(len(bundle.Values)) || row.Record.ResultID != id {
			return nil, fmt.Errorf("invalid ordinal %d", id)
		}
		if _, exists := rows[id]; exists {
			return nil, fmt.Errorf("duplicate ordinal %d", id)
		}
		if err := validateTransferRecord(row.Record, row.DependencyIDs); err != nil {
			return nil, fmt.Errorf("row %d: %w", id, err)
		}
		rows[id] = row
	}
	// Validate complete addresses against the encoded output mapping. A pending
	// slot may replace the descriptor, but cannot change the part's existence.
	addresses := map[uint64]map[string]CapturedCodecOutput{}
	for id, row := range rows {
		outputs, err := mapTransferredOutputs(row.Record)
		if err != nil {
			return nil, err
		}
		addresses[id] = map[string]CapturedCodecOutput{}
		for _, output := range outputs {
			key, err := partAddressKey(output.Address)
			if err != nil {
				return nil, err
			}
			addresses[id][key] = output
		}
	}
	slots := map[string]bool{}
	validateSlot := func(id uint64, address PersistedPartAddress) error {
		key, err := partAddressKey(address)
		if err != nil {
			return err
		}
		part, ok := addresses[id][key]
		if !ok {
			return fmt.Errorf("row %d has no output at %s", id, key)
		}
		if part.State != "pending" {
			return fmt.Errorf("offer at final output %d:%s", id, key)
		}
		key = fmt.Sprintf("%d:%s", id, key)
		if slots[key] {
			return fmt.Errorf("duplicate offered address %s", key)
		}
		slots[key] = true
		return nil
	}
	for id, row := range rows {
		for _, offer := range row.Record.Envelope.PendingOffers {
			if err := validateSlot(id, offer.Address); err != nil {
				return nil, err
			}
			key, _ := partAddressKey(offer.Address)
			if err := validateTransferOffer(offer, addresses[id][key]); err != nil {
				return nil, err
			}
		}
	}
	outputDeps := map[uint64][]uint64{}
	described := map[string]bool{}
	for _, output := range bundle.Outputs {
		id := uint64(output.Ordinal)
		if _, ok := rows[id]; !ok {
			return nil, fmt.Errorf("output names missing ordinal %d", id)
		}
		key, err := partAddressKey(output.Address)
		if err != nil {
			return nil, err
		}
		composite := fmt.Sprintf("%d:%s", id, key)
		if described[composite] {
			return nil, fmt.Errorf("duplicate output %s", composite)
		}
		described[composite] = true
		part, ok := addresses[id][key]
		if !ok {
			return nil, fmt.Errorf("unknown output address %s", composite)
		}
		switch output.State {
		case "pending", "completed", "absent", "metadata":
		default:
			return nil, fmt.Errorf("invalid output state %q", output.State)
		}
		if slots[composite] {
			return nil, fmt.Errorf("duplicate offer description %s", composite)
		}
		if output.State != part.State && (part.State != "pending" || output.State != "completed") {
			return nil, fmt.Errorf("output state differs from recorded part %s", composite)
		}
		if output.Value != nil {
			if err := validateSnapshotValue(*output.Value); err != nil {
				return nil, err
			}
			for _, service := range output.Value.Services {
				if _, ok := rows[service.ServiceResultID]; !ok {
					return nil, fmt.Errorf("output refers to missing service %d", service.ServiceResultID)
				}
			}
		}
		if output.State == "completed" && output.Chain == nil {
			return nil, fmt.Errorf("completed selection requires chain")
		}
		if output.Chain == nil {
			if output.Owner != nil {
				return nil, fmt.Errorf("output owner without chain")
			}
			continue
		}
		if output.Value == nil || output.Owner == nil || output.State != "completed" {
			return nil, fmt.Errorf("chain requires completed descriptor and owner")
		}
		offer := PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: *output.Owner}
		if err := validateTransferOffer(offer, part); err != nil {
			return nil, err
		}
		if err := validateSlot(id, output.Address); err != nil {
			return nil, err
		}
		outputDeps[id] = append(outputDeps[id], output.Owner.DependencyIDs...)
	}
	seen := map[uint64]uint8{}
	var order []uint64
	var walk func(uint64) error
	walk = func(id uint64) error {
		row, ok := rows[id]
		if !ok {
			return fmt.Errorf("missing dependency ordinal %d", id)
		}
		if seen[id] == 1 {
			return fmt.Errorf("ownership cycle through ordinal %d", id)
		}
		if seen[id] == 2 {
			return nil
		}
		seen[id] = 1
		deps := slices.Clone(row.DependencyIDs)
		for _, offer := range row.Record.Envelope.PendingOffers {
			deps = append(deps, offer.Owner.DependencyIDs...)
		}
		deps = append(deps, outputDeps[id]...)
		for _, id := range deps {
			if err := walk(id); err != nil {
				return err
			}
		}
		seen[id] = 2
		order = append(order, id)
		return nil
	}
	roots := map[uint64]bool{}
	for _, root := range bundle.Roots {
		id := uint64(root.Ordinal)
		if roots[id] {
			return nil, fmt.Errorf("duplicate root %d", id)
		}
		roots[id] = true
		if err := walk(id); err != nil {
			return nil, err
		}
	}
	if len(seen) != len(rows) {
		return nil, fmt.Errorf("bundle contains rows outside its root closure")
	}
	return order, nil
}

type transferIdentityPlan struct {
	row          *sharedResult
	recipe, self digest.Digest
	inputs       []digest.Digest
	provenance   []egraphInputProvenanceKind
}

//nolint:gocyclo // one step per imported record kind under one transaction; splitting hides the order of the steps
func (c *Cache) ImportValues(ctx context.Context, input ValueBundle) ([]ImportedValue, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish(false)
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var bundle ValueBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, err
	}
	order, err := validateValueBundle(bundle)
	if err != nil {
		return nil, err
	}
	index := map[uint64]*TransferredValue{}
	ownerCount := uint64(0)
	for i := range bundle.Values {
		row := &bundle.Values[i]
		index[uint64(row.Ordinal)] = row
		ownerCount += uint64(len(row.Record.Envelope.PendingOffers))
	}
	checkRoots := func() error {
		now := time.Now().Unix()
		for _, root := range bundle.Roots {
			expiry := index[uint64(root.Ordinal)].ExpiresAtUnix
			if root.ExpiresAtUnix != 0 && root.ExpiresAtUnix <= now || expiry != 0 && expiry <= now {
				return fmt.Errorf("expired transfer root %d", root.Ordinal)
			}
		}
		return context.Cause(ctx)
	}
	if err := checkRoots(); err != nil {
		return nil, err
	}
	for _, output := range bundle.Outputs {
		if output.Chain == nil {
			continue
		}
		row := index[uint64(output.Ordinal)]
		row.Record.Envelope.PendingOffers = append(row.Record.Envelope.PendingOffers, PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: *output.Owner})
		ownerCount++
	}
	bundle.Outputs = nil
	if _, err := validateValueBundle(bundle); err != nil {
		return nil, err
	}
	c.egraphMu.Lock()
	if c.nextSharedResultID == 0 {
		c.nextSharedResultID = 1
	}
	firstID := uint64(c.nextSharedResultID)
	c.nextSharedResultID += sharedResultID(len(bundle.Values))
	firstOwnerID := c.nextOfferOwnerID
	c.nextOfferOwnerID += offerOwnerID(ownerCount)
	c.egraphMu.Unlock()
	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		if index[ref.ResultID] == nil {
			return fmt.Errorf("missing transfer reference %d", ref.ResultID)
		}
		ref.ResultID = firstID + ref.ResultID - 1
		return nil
	}
	private := &Cache{resultsByID: map[sharedResultID]*sharedResult{}, nextOfferOwnerID: firstOwnerID}
	for _, id := range order {
		row := index[id]
		rec, err := VisitEncodedReferences(row.Record, relocate)
		if err != nil {
			return nil, err
		}
		rec.Envelope.Imported = true
		deps := make(map[sharedResultID]struct{}, len(row.DependencyIDs))
		for _, dep := range row.DependencyIDs {
			deps[sharedResultID(firstID+dep-1)] = struct{}{}
		}
		relocatedDeps := make([]uint64, 0, len(deps))
		for dep := range deps {
			relocatedDeps = append(relocatedDeps, uint64(dep))
		}
		if err := validateTransferRecord(rec, relocatedDeps); err != nil {
			return nil, err
		}
		res := &sharedResult{id: sharedResultID(rec.ResultID), imported: true, isObject: rec.Envelope.Kind == persistedResultKindObject, persistedEnvelope: &rec.Envelope, deps: deps, expiresAtUnix: row.ExpiresAtUnix, sessionResourceHandle: rec.Envelope.SessionResourceHandle, createdAtUnixNano: time.Now().UnixNano()}
		res.storeResultCall(rec.Call)
		private.resultsByID[res.id] = res
	}
	if err := private.validateStoredOwnershipLocked(); err != nil {
		return nil, err
	}
	for _, res := range private.resultsByID {
		if err := walkTransferCalls(res.loadResultCall(), func(*ResultCall) error { return nil }, func(ref *ResultCallRef) error {
			ref.shared = private.resultsByID[sharedResultID(ref.ResultID)]
			return nil
		}); err != nil {
			return nil, err
		}
		for depID := range res.deps {
			dep := private.resultsByID[depID]
			private.rememberDependencyEdgeLocked(res, dep)
			private.incrementIncomingOwnershipLocked(ctx, dep)
		}
	}
	if err := private.restoreOfferOwnersLocked(ctx); err != nil {
		return nil, err
	}
	plans := make([]transferIdentityPlan, 0, len(order))
	for _, id := range order {
		res := private.resultsByID[sharedResultID(firstID+id-1)]
		if _, err := private.recomputeRequiredSessionResourcesLocked(res); err != nil {
			return nil, err
		}
		frame := res.loadResultCall()
		recipe, err := frame.deriveRecipeDigest(private)
		if err != nil {
			return nil, err
		}
		self, refs, err := frame.selfDigestAndInputRefs(private)
		if err != nil {
			return nil, err
		}
		plan := transferIdentityPlan{row: res, recipe: recipe, self: self}
		plan.provenance, err = private.inputProvenanceForRefs(refs)
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			dig, err := ref.inputDigest(private)
			if err != nil {
				return nil, err
			}
			plan.inputs = append(plan.inputs, dig)
		}
		plans = append(plans, plan)
		if c.testTransferPlanPrepared != nil {
			if err := c.testTransferPlanPrepared(len(plans)); err != nil {
				return nil, err
			}
		}
	}
	if c.testBeforeTransferCommit != nil {
		c.testBeforeTransferCommit()
	}
	c.egraphMu.Lock()
	notify, notifyOwner := c.beginShareNotificationsLocked()
	if err := checkRoots(); err != nil {
		// A failed root validation publishes nothing, so no private plan can
		// enqueue.
		c.discardShareNotificationsLocked(notify, notifyOwner)
		c.egraphMu.Unlock()
		return nil, err
	}
	c.initEgraphLocked()
	if c.offerOwners == nil {
		c.offerOwners = map[offerOwnerID]*offerOwner{}
	}
	for id, res := range private.resultsByID {
		c.resultsByID[id] = res
		res.onRelease = c.resultSnapshotLeaseCleanup(res)
	}
	for id, owner := range private.offerOwners {
		c.offerOwners[id] = owner
	}
	for _, root := range bundle.Roots {
		c.upsertPersistedEdgeLocked(ctx, private.resultsByID[sharedResultID(firstID+uint64(root.Ordinal)-1)], root.ExpiresAtUnix, false)
	}
	for _, plan := range plans {
		c.applyPreparedResultIdentityLocked(ctx, plan.row, plan.row.loadResultCall(), plan.recipe, plan.self, plan.inputs, plan.provenance, plan.recipe)
	}
	// After every identity application: notify each final class the imported
	// rows now belong to, together with the interval's union and membership
	// records. Coalescing absorbs the duplicates.
	for _, plan := range plans {
		c.queueSnapshotShareRowLocked(ctx, plan.row)
	}
	c.flushShareNotificationsLocked(ctx, notify, notifyOwner)
	duplicates := c.takeShareDuplicateHoldsLocked()
	c.egraphMu.Unlock()
	c.releaseShareDuplicateHolds(ctx, duplicates)
	if c.testAfterTransferCommit != nil {
		c.testAfterTransferCommit()
	}
	imported := make([]ImportedValue, 0, len(bundle.Roots))
	for _, root := range bundle.Roots {
		imported = append(imported, ImportedValue{Ordinal: root.Ordinal, ResultID: firstID + uint64(root.Ordinal) - 1})
	}
	return imported, nil
}
