package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/engine/snapshots"
)

type partCleanup struct {
	mu   sync.Mutex
	done bool
	fn   func(context.Context) error
}

func (p *partCleanup) release(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return nil
	}
	if err := p.fn(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	p.done = true
	return nil
}
func (c *Cache) publishEvaluatedParts(ctx context.Context, res AnyResult, demanded PersistedPartAddress, produced PersistedRecord, original *OriginalPermit, cleanup *partCleanup, demand *PartDemandState) error {
	watch := partReselectWatch{loop: "publishEvaluatedParts"}
	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		watch.again(ctx, res.cacheSharedResult(), demanded)
		prepared, err := c.prepareEvaluatedParts(ctx, res, demanded, produced, original, cleanup)
		if partCanReselect(err) {
			watch.refused(err)
			if stuck := demand.refused(watch.loop, watch.n, demanded, err); stuck != nil {
				return stuck
			}
			continue
		}
		if err != nil {
			return err
		}
		if prepared == nil {
			return nil
		}
		receipt, outcome, err := c.CommitReadyPart(ctx, prepared)
		if outcome == PartInstalled {
			return errors.Join(err, c.finishReadyPartInline(ctx, receipt))
		}
		if partCanReselect(err) {
			watch.refused(err)
			if stuck := demand.refused(watch.loop, watch.n, demanded, err); stuck != nil {
				return stuck
			}
			continue
		}
		return err
	}
}
func (c *Cache) prepareEvaluatedParts(ctx context.Context, res AnyResult, demanded PersistedPartAddress, produced PersistedRecord, original *OriginalPermit, cleanup *partCleanup) (_ *PreparedReadyPart, rerr error) {
	row := res.cacheSharedResult()
	session, err := partSession(ctx)
	if err != nil {
		return nil, err
	}
	p := &PreparedReadyPart{cache: c, original: original, beforeSyncCleanup: cleanup, source: &PartSourceLease{cache: c, sessionID: session}}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, p.Release(ctx))
		}
	}()
	c.egraphMu.Lock()
	if c.resultsByID[row.id] != row || original.task != PartTaskFromContext(ctx) {
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("operation publication: invalid owner")
	}
	c.incrementIncomingOwnershipLocked(ctx, row)
	p.receiver = row
	original.gate.mu.Lock()
	group := original.gate.groups[lazyGroupAddressKey(original.group)]
	if group == nil || group.phase != LazyEvaluationRunning || group.task != original.task {
		original.gate.mu.Unlock()
		c.egraphMu.Unlock()
		return nil, partRefused("publish: own group not running")
	}
	p.permit = original.gate.newPermit(original.task, demanded, true)
	original.gate.mu.Unlock()
	c.egraphMu.Unlock()
	current, err := c.capturePartRecord(ctx, row, row.imported, nil, &p.version)
	if err != nil {
		return nil, err
	}
	probes, err := describePartRecord(produced)
	if err != nil {
		return nil, err
	}
	producedParts := map[string]PartProbe{}
	for _, probe := range probes {
		key, _ := partAddressKey(probe.Descriptor.Address)
		producedParts[key] = probe
	}
	demandedKey, _ := partAddressKey(demanded)
	if !producedParts[demandedKey].LocalComplete {
		return nil, fmt.Errorf("saved operation left required output %s unset", demanded.Part)
	}
	currentProbes, err := describePartRecord(current)
	if err != nil {
		return nil, err
	}
	complete := map[string]bool{}
	for _, probe := range currentProbes {
		key, _ := partAddressKey(probe.Descriptor.Address)
		complete[key] = probe.LocalComplete
	}
	p.next = current
	descriptors, refs, err := p.addProducedOutputs(ctx, original.writeSet, complete, producedParts, produced)
	if err != nil {
		return nil, err
	}
	if len(descriptors) == 0 {
		return nil, p.Release(ctx)
	}
	c.egraphMu.Lock()
	ids := c.partResourceLeavesLocked(row)
	for _, d := range descriptors {
		ids = append(ids, d.DependencyIDs...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		dep := c.resultsByID[sharedResultID(id)]
		if dep == nil {
			err = fmt.Errorf("operation publication: missing reference %d", id)
			break
		}
		c.incrementIncomingOwnershipLocked(ctx, dep)
		p.deps = append(p.deps, dep)
	}
	if err == nil {
		_, err = c.preparePartDependenciesLocked(row, p.deps)
	}
	c.egraphMu.Unlock()
	if err != nil {
		return nil, err
	}
	if p.version.payload.hasValue {
		if err := p.prepareProducedStores(ctx, res, row, current, demanded, descriptors, refs); err != nil {
			return nil, err
		}
	}
	if err := p.version.check(row); err != nil {
		return nil, err
	}
	if err := p.seal(row, nil); err != nil {
		return nil, err
	}
	if err := c.reachPrepared(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// addProducedOutputs merges the saved operation's outputs for every write-set
// address that is not complete yet into the prepared record and pins their
// snapshots. It returns their descriptors and, for a live value, the opened
// references in the same order.
func (p *PreparedReadyPart) addProducedOutputs(ctx context.Context, writeSet []PersistedPartAddress, complete map[string]bool, producedParts map[string]PartProbe, produced PersistedRecord) ([]PartDescriptor, []snapshots.ImmutableRef, error) {
	var descriptors []PartDescriptor
	var refs []snapshots.ImmutableRef
	for _, address := range writeSet {
		key, _ := partAddressKey(address)
		if complete[key] {
			continue
		}
		probe, ok := producedParts[key]
		if !ok || !probe.LocalComplete {
			return nil, nil, fmt.Errorf("saved operation left write-set output %s unset", address.Part)
		}
		d := probe.Descriptor
		d.Address = clonePartAddress(address)
		next, err := prepareScopedPartRecord(p.next, produced, d, address)
		if err != nil {
			return nil, nil, err
		}
		p.next = next
		descriptors = append(descriptors, d)
		p.addresses = append(p.addresses, clonePartAddress(address))
		var ref snapshots.ImmutableRef
		if d.SnapshotID != "" {
			pin, err := p.cache.snapshotManager.PinSnapshot(ctx, d.SnapshotID)
			if err != nil {
				return nil, nil, err
			}
			p.extraProtections = append(p.extraProtections, &partProtection{ref: pin})
			if p.version.payload.hasValue {
				ref, err = p.cache.snapshotManager.GetBySnapshotID(ctx, d.SnapshotID, snapshots.NoUpdateLastUsed)
				if err != nil {
					return nil, nil, err
				}
				p.extraAccessors = append(p.extraAccessors, ref)
			}
		}
		refs = append(refs, ref)
	}
	return descriptors, refs, nil
}

// prepareProducedStores asks the receiver's live value for the stores that
// publish the produced outputs: one batch store for several descriptors.
func (p *PreparedReadyPart) prepareProducedStores(ctx context.Context, res AnyResult, row *sharedResult, current PersistedRecord, demanded PersistedPartAddress, descriptors []PartDescriptor, refs []snapshots.ImmutableRef) error {
	dec := p.cache.partDecodeContext(ctx, row, p.next).atPath(demanded.OutputPath)
	value, err := inlineValueAt(res, current.Call, demanded.OutputPath)
	if err != nil {
		return err
	}
	local, err := partRecordAt(p.next, demanded.OutputPath)
	if err != nil {
		return err
	}
	for i := range descriptors {
		descriptors[i].Address.OutputPath = nil
	}
	if len(descriptors) > 1 {
		store, ok := UnwrapAs[PartBatchStorePreparer](value)
		if !ok {
			return fmt.Errorf("operation publication: no batch store")
		}
		p.store, err = store.PreparePartStores(ctx, dec, local, descriptors, refs)
		return err
	}
	store, ok := UnwrapAs[PartStorePreparer](value)
	if !ok {
		return fmt.Errorf("operation publication: no store")
	}
	p.store, err = store.PreparePartStore(ctx, dec, local, descriptors[0], refs[0])
	return err
}
