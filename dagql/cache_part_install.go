package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/engine/snapshots"
	set "github.com/hashicorp/go-set/v3"
)

type PartInstallOutcome uint8

const (
	PartInstallRefused PartInstallOutcome = iota
	PartInstallAlreadyInstalled
	PartInstalled
)

type partProtection struct {
	mu       sync.Mutex
	ref      snapshots.ImmutableRef
	released bool
}

func (p *partProtection) release(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return nil
	}
	if p.ref != nil {
		if err := p.ref.Release(context.WithoutCancel(ctx)); err != nil {
			return err
		}
	}
	p.released = true
	return nil
}

type PreparedReadyPart struct {
	cache             *Cache
	receiver          *sharedResult
	source            *PartSourceLease
	permit            *PartPermit
	version           capturedRowRevision
	next              PersistedRecord
	store             PreparedPartStore
	deps              []*sharedResult
	protection        *partProtection
	accessor          snapshots.ImmutableRef
	consumed          atomic.Bool
	original          *OriginalPermit
	addresses         []PersistedPartAddress
	extraProtections  []*partProtection
	extraAccessors    []snapshots.ImmutableRef
	beforeSyncCleanup *partCleanup
}
type ReadyPartReceipt struct {
	cache      *Cache
	receiver   *sharedResult
	task       *PartTaskToken
	once       sync.Once
	releaseErr error
}

func (r *ReadyPartReceipt) release(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.once.Do(func() { r.releaseErr = r.cache.releasePartRow(context.WithoutCancel(ctx), r.receiver) })
	return r.releaseErr
}
func (p *PreparedReadyPart) Release(ctx context.Context) error {
	if p == nil || !p.consumed.CompareAndSwap(false, true) {
		return nil
	}
	return p.release(ctx, false)
}
func (p *PreparedReadyPart) release(ctx context.Context, published bool) error {
	ctx = context.WithoutCancel(ctx)
	var err error
	if !published {
		for _, protection := range p.extraProtections {
			err = errors.Join(err, protection.release(ctx))
		}
		for _, ref := range p.extraAccessors {
			err = errors.Join(err, ref.Release(ctx))
		}
		err = errors.Join(err, p.protection.release(ctx))
		if p.accessor != nil {
			err = errors.Join(err, p.accessor.Release(ctx))
		}
	}
	// Drop independent graph holds before owner sync, including offer owners
	// with backreferences to this receiver.
	for _, dep := range p.deps {
		err = errors.Join(err, p.cache.releasePartRow(ctx, dep))
	}
	err = errors.Join(err, p.source.Release(ctx))
	p.permit.Release()
	if !published && p.receiver != nil {
		err = errors.Join(err, p.cache.releasePartRow(ctx, p.receiver))
	}
	return err
}
func (c *Cache) PrepareReadyPart(ctx context.Context, receiver AnyResult, source *PartSourceLease, permit *PartPermit) (_ *PreparedReadyPart, rerr error) {
	p := &PreparedReadyPart{cache: c, source: source, permit: permit}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, p.Release(ctx))
		}
	}()
	if source == nil || permit == nil || source.cache != c {
		return nil, fmt.Errorf("prepare part: missing source or permit")
	}
	if source.readiness != PartReady && source.descriptor.SnapshotID == "" {
		return nil, fmt.Errorf("prepare part: source is not materialized")
	}
	c.egraphMu.Lock()
	row, err := c.validatePartTaskLocked(receiver, permit.task)
	if err == nil && source.delegation != nil && !source.delegation.currentLocked(c, source, row) {
		err = partRefused("prepare: delegation proof not current")
	}
	if err == nil {
		c.incrementIncomingOwnershipLocked(ctx, row)
		p.receiver = row
	}
	c.egraphMu.Unlock()
	if err != nil {
		return nil, err
	}
	current, version, probe, err := c.probePart(ctx, row, permit.address)
	if err != nil {
		return nil, err
	}
	p.version = version
	if probe == nil {
		return nil, fmt.Errorf("prepare part: undeclared output")
	}
	if probe.LocalComplete {
		return nil, partRefused("prepare: receiver part already complete")
	}
	local, err := partRecordAt(current, permit.address.OutputPath)
	if err != nil {
		return nil, err
	}
	family, ok := PersistedObjectFamilyByName(local.Envelope.ObjectCodec)
	if !ok {
		return nil, fmt.Errorf("prepare part: unknown family")
	}
	if source.descriptor.Family != "" && source.descriptor.Family != family.Name {
		return nil, fmt.Errorf("prepare part: incompatible output family")
	}

	c.egraphMu.Lock()
	for _, id := range c.partSourceDependenciesLocked(source) {
		dep := c.resultsByID[sharedResultID(id)]
		if dep == nil {
			err = fmt.Errorf("prepare part: missing exact reference %d", id)
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
	if source.descriptor.SnapshotID != "" {
		if c.snapshotManager == nil {
			return nil, fmt.Errorf("prepare part: no snapshot manager")
		}
		ref, err := c.snapshotManager.PinSnapshot(ctx, source.descriptor.SnapshotID)
		if err != nil {
			return nil, err
		}
		p.protection = &partProtection{ref: ref}
		if version.payload.hasValue {
			p.accessor, err = c.snapshotManager.GetBySnapshotID(ctx, source.descriptor.SnapshotID, snapshots.NoUpdateLastUsed)
			if err != nil {
				return nil, err
			}
		}
	}
	p.next, err = prepareScopedPartRecord(current, source.record, source.Descriptor(), permit.address)
	if err != nil {
		return nil, err
	}
	if version.payload.hasValue {
		value, err := inlineValueAt(Result[Typed]{shared: row}, current.Call, permit.address.OutputPath)
		if err != nil {
			return nil, err
		}
		preparer, ok := UnwrapAs[PartStorePreparer](value)
		if !ok {
			return nil, fmt.Errorf("prepare part: typed value has no store")
		}
		dec := c.partDecodeContext(ctx, row, p.next).atPath(permit.address.OutputPath)
		d := source.Descriptor()
		d.Address = clonePartAddress(permit.address)
		nextLocal, err := partRecordAt(p.next, permit.address.OutputPath)
		if err != nil {
			return nil, err
		}
		d.Address.OutputPath = nil
		p.store, err = preparer.PreparePartStore(ctx, dec, nextLocal, d, p.accessor)
		if err != nil {
			return nil, err
		}
	}
	if err := version.check(row); err != nil {
		return nil, err
	}
	return p, nil
}

// preparePartDependenciesLocked computes every fallible graph/requirement
// operation before publication. Offer edges participate in cycle detection but
// never in the propagated own-resource requirements.
func (c *Cache) preparePartDependenciesLocked(receiver *sharedResult, deps []*sharedResult) (map[*sharedResult]*set.TreeSet[SessionResourceHandle], error) {
	seen := map[sharedResultID]bool{}
	var walk func(*sharedResult) error
	walk = func(row *sharedResult) error {
		if row == nil || c.resultsByID[row.id] != row {
			return fmt.Errorf("part graph: missing dependency")
		}
		if row == receiver {
			return fmt.Errorf("part graph: ownership cycle through %d", row.id)
		}
		if seen[row.id] {
			return nil
		}
		seen[row.id] = true
		for child := range c.resultOwnershipChildrenLocked(row) {
			if child.owner != nil {
				for _, dep := range child.owner.deps {
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
	for _, dep := range deps {
		if err := walk(dep); err != nil {
			return nil, err
		}
	}
	requirements := map[*sharedResult]*set.TreeSet[SessionResourceHandle]{}
	queue := []*sharedResult{receiver}
	seen = map[sharedResultID]bool{}
	for len(queue) > 0 {
		row := queue[0]
		queue = queue[1:]
		if seen[row.id] {
			continue
		}
		seen[row.id] = true
		for id := range row.deps {
			if c.resultsByID[id] == nil {
				return nil, fmt.Errorf("part graph: missing ancestor dependency %d", id)
			}
		}
		req := set.NewTreeSet(compareSessionResourceHandles)
		if row.requiredSessionResources != nil {
			req = req.Union(row.requiredSessionResources).(*set.TreeSet[SessionResourceHandle])
		}
		for _, dep := range deps {
			if dep.requiredSessionResources != nil {
				req = req.Union(dep.requiredSessionResources).(*set.TreeSet[SessionResourceHandle])
			}
		}
		requirements[row] = req
		if row.depParents != nil {
			for id := range row.depParents.Items() {
				parent := c.resultsByID[id]
				if parent == nil {
					return nil, fmt.Errorf("part graph: missing ancestor %d", id)
				}
				queue = append(queue, parent)
			}
		}
	}
	return requirements, nil
}
func (c *Cache) applyPartDependenciesLocked(ctx context.Context, receiver *sharedResult, deps []*sharedResult, requirements map[*sharedResult]*set.TreeSet[SessionResourceHandle]) {
	if receiver.deps == nil {
		receiver.deps = map[sharedResultID]struct{}{}
	}
	for _, dep := range deps {
		if _, ok := receiver.deps[dep.id]; ok {
			continue
		}
		receiver.deps[dep.id] = struct{}{}
		receiver.dependencyOwnershipRevision++
		c.rememberDependencyEdgeLocked(receiver, dep)
		c.incrementIncomingOwnershipLocked(ctx, dep)
	}
	for row, req := range requirements {
		if !sessionResourceSetsEqual(row.requiredSessionResources, req) {
			row.requiredSessionResources = req
			row.requiredSessionResourcesGen.Add(1)
		}
	}
}
func (c *Cache) CommitReadyPart(ctx context.Context, p *PreparedReadyPart) (_ *ReadyPartReceipt, outcome PartInstallOutcome, rerr error) {
	if p == nil || p.cache != c || !p.consumed.CompareAndSwap(false, true) {
		return nil, PartInstallRefused, fmt.Errorf("commit part: consumed preparation")
	}
	defer func() {
		rerr = errors.Join(rerr, p.release(ctx, outcome == PartInstalled))
		if outcome == PartInstalled {
			kind := "installed-ready"
			if p.original != nil {
				kind = "installed-lazy"
			} else if p.source.readiness == PartDownloadable {
				kind = "installed-chain"
			}
			if p.source.delegation != nil {
				c.recordPartFixtureDelegation(p.receiver, p.permit.address, "installed-delegation", p.source.delegation)
			} else {
				c.recordPartFixture(p.receiver, p.permit.address, kind)
			}
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return nil, PartInstallRefused, err
	}
	if err := p.version.check(p.receiver); err != nil {
		return nil, PartInstallRefused, partRefused("commit: receiver version")
	}
	source := p.source
	if source.delegation != nil {
		if err := source.delegation.childVersion.check(p.receiver); err != nil {
			return nil, PartInstallRefused, partRefused("commit: delegation child version")
		}
	}
	if p.original == nil && source.readiness == PartReady {
		if err := source.version.check(source.source); err != nil {
			return nil, PartInstallRefused, partRefused("commit: donor version")
		}
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	row := p.receiver
	if c.resultsByID[row.id] != row || !p.permit.task.active.Load() {
		return nil, PartInstallRefused, partRefused("commit: receiver unregistered or task inactive")
	}
	if p.original == nil && source.readiness == PartReady {
		if source.source == nil || c.resultsByID[source.source.id] != source.source || source.facts != c.partFactsLocked(source.source) {
			return nil, PartInstallRefused, partRefused("commit: donor unregistered or facts changed")
		}
		found := false
		if source.delegation != nil {
			found = source.delegation.currentLocked(c, source, row)
		} else if source.sessionlessShare {
			_, found = c.sessionlessPartEquivalentLocked(row, source.source, source.lookup)
			found = found && row.imported
		} else {
			for _, candidate := range c.collectPartCandidatesLocked(row, source.lookup, source.sessionID) {
				if candidate.row == source.source {
					found = true
					break
				}
			}
		}
		if !found {
			return nil, PartInstallRefused, partRefused("commit: donor no longer eligible")
		}
	} else if p.original == nil && !c.offerAllowedLocked(source.sessionID, source.offerOwner) {
		return nil, PartInstallRefused, partRefused("commit: offer owner not allowed")
	}
	for _, dep := range p.deps {
		if !c.partSourceReferenceAllowedLocked(row, source, dep) {
			return nil, PartInstallRefused, partRefused("commit: dependency not allowed")
		}
	}
	if p.original == nil {
		for _, id := range c.partSourceDependenciesLocked(source) {
			found := false
			for _, dep := range p.deps {
				if uint64(dep.id) == id {
					found = true
					break
				}
			}
			if !found {
				return nil, PartInstallRefused, partRefused("commit: donated facts changed")
			}
		}
	}
	requirements, err := c.preparePartDependenciesLocked(row, p.deps)
	if err != nil {
		return nil, PartInstallRefused, err
	}
	gate := p.permit.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	addresses := p.addresses
	if len(addresses) == 0 {
		addresses = []PersistedPartAddress{p.permit.address}
	}
	for _, address := range addresses {
		key, _ := partAddressKey(address)
		if gate.outputs[key].phase != PartPending {
			return nil, PartInstallAlreadyInstalled, nil
		}
	}
	if p.original != nil {
		group := gate.groups[lazyGroupAddressKey(p.original.group)]
		if group == nil || group.phase != LazyEvaluationRunning || group.task != p.permit.task {
			return nil, PartInstallRefused, partRefused("commit: own group not running")
		}
	}
	if gate.writers[p.permit.ticket] != p.permit {
		return nil, PartInstallRefused, partRefused("commit: own permit gone")
	}
	if p.store != nil {
		if !p.store.TryLock() {
			return nil, PartInstallRefused, partRefused("commit: part store busy")
		}
		defer p.store.Unlock()
	}
	row.payloadMu.Lock()
	defer row.payloadMu.Unlock()
	if row.payloadRevision != p.version.payload.payloadRevision || row.hasValue != p.version.payload.hasValue || row.persistedEnvelope != p.version.payload.persistedEnvelope {
		return nil, PartInstallRefused, partRefused("commit: receiver representation")
	}
	// No fallible work after this point. Protection cleanup belongs to the row,
	// independently of the receipt and the continuation's temporary row hold.
	c.applyPartDependenciesLocked(ctx, row, p.deps, requirements)
	protections := slices.Clone(p.extraProtections)
	if p.protection != nil {
		protections = append(protections, p.protection)
	}
	for _, protection := range protections {
		row.onRelease = joinOnRelease(row.onRelease, protection.release)
	}
	if p.beforeSyncCleanup != nil {
		row.onRelease = joinOnRelease(row.onRelease, p.beforeSyncCleanup.release)
	}
	row.snapshotLinkIntent = &snapshotLinkIntent{Links: cloneSnapshotRefLinks(p.next.SnapshotLinks)}
	if p.store == nil {
		env := p.next.Envelope
		row.persistedEnvelope = &env
	} else {
		p.store.Publish()
	}
	row.payloadRevision++
	gate.managed = true
	row.partGate.active.Store(true)
	gate.revision++
	token := p.permit.task
	installation := gate.revision
	for _, address := range addresses {
		key, _ := partAddressKey(address)
		gate.outputs[key] = partOutputState{phase: PartOutputInstalled, task: token, installation: installation}
	}
	previous := token.installed.Load()
	installed := &InstalledOutputs{}
	if previous != nil {
		installed.outputs = slices.Clone(previous.outputs)
		installed.protections = slices.Clone(previous.protections)
		installed.beforeSync = slices.Clone(previous.beforeSync)
	}
	for _, address := range addresses {
		installed.outputs = append(installed.outputs, installedPartOutput{address: clonePartAddress(address), installation: installation})
	}
	installed.protections = append(installed.protections, protections...)
	if p.beforeSyncCleanup != nil {
		installed.beforeSync = append(installed.beforeSync, p.beforeSyncCleanup)
	}

	token.installed.Store(installed)
	return &ReadyPartReceipt{cache: c, receiver: row, task: token}, PartInstalled, nil
}
func (c *Cache) FinishReadyPart(ctx context.Context, receipt *ReadyPartReceipt) (rerr error) {
	if receipt == nil || receipt.cache != c {
		return fmt.Errorf("finish part: invalid receipt")
	}
	defer func() { rerr = errors.Join(rerr, receipt.release(ctx)) }()
	if PartTaskFromContext(ctx) == receipt.task {
		return fmt.Errorf("finish part: owning Body must use inline handoff")
	}
	receipt.task.openOwnerSync()
	return c.RunLazyTask(ctx, Result[Typed]{shared: receipt.receiver}, receipt.task.key, LazyTaskSpec{Body: func(context.Context) error {
		if receipt.task.settled.Load() {
			return nil
		}
		return fmt.Errorf("part receipt lost its owning continuation")
	}})
}
func (c *Cache) finishReadyPartInline(ctx context.Context, receipt *ReadyPartReceipt) error {
	if receipt == nil || PartTaskFromContext(ctx) != receipt.task {
		return fmt.Errorf("inline finish: wrong task")
	}
	receipt.task.openOwnerSync()
	return receipt.release(ctx)
}
func (c *Cache) InstallReadyPart(ctx context.Context, receiver AnyResult, source *PartSourceLease, permit *PartPermit) error {
	p, err := c.PrepareReadyPart(ctx, receiver, source, permit)
	if err != nil {
		return err
	}
	receipt, outcome, err := c.CommitReadyPart(ctx, p)
	if outcome == PartInstalled {
		if PartTaskFromContext(ctx) == receipt.task {
			return errors.Join(err, c.finishReadyPartInline(ctx, receipt))
		}
		return errors.Join(err, c.FinishReadyPart(ctx, receipt))
	}
	if err != nil {
		return err
	}
	if outcome == PartInstallRefused {
		return partRefused("install: commit refused")
	}
	return nil
}
