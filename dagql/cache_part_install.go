package dagql

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

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
	// expectedRepresentation is the receiver representation this preparation
	// requires at Commit: its own observation, or with a prepared prefix the
	// one that prefix will have published. seal sets it for every
	// constructor. Only cache orchestration can supply a non-nil base, so
	// this is never caller-supplied wire data.
	expectedRepresentation readyPartRepresentation
	// expectedPredecessors names each preceding address of the same receiver
	// and the owning installation identity reserved for it. Copied scalars
	// only: no task authority, receipt, hold or live carrier is borrowed.
	expectedPredecessors []readyPartPredecessor
	// published is the envelope pointer an encoded Commit installs. seal
	// allocates it for every encoded preparation, so a successor prepared from
	// this representation can name the exact pointer it must find.
	published *PersistedResultEnvelope
	// sealed records that the constructor finished with seal. Commit refuses
	// an unsealed preparation with an error, never with a reselect.
	sealed bool
}

// seal is the step every constructor of a PreparedReadyPart ends with. It
// establishes what Commit relies on, in one place: the receiver
// representation Commit must find, the predecessors whose installations it
// must see, and for an encoded receiver the envelope it installs. A typed
// receiver must carry its prepared store instead. A preparation that breaks
// this is a construction defect and is reported as one: answering it with a
// reselect would send its caller round an unbounded retry.
func (p *PreparedReadyPart) seal(row *sharedResult, base *readyPartPreparationBase) error {
	observed := p.version.payload
	if observed.hasValue != (p.store != nil) {
		return fmt.Errorf("prepare part: typed receiver %t but prepared store %t", observed.hasValue, p.store != nil)
	}
	p.expectedRepresentation = readyPartRepresentation{
		receiver:        row,
		payloadRevision: observed.payloadRevision,
		envelope:        observed.persistedEnvelope,
		hasValue:        observed.hasValue,
	}
	if base != nil {
		p.expectedRepresentation = base.expected
		p.expectedPredecessors = slices.Clone(base.predecessors)
	}
	if !observed.hasValue {
		env := p.next.Envelope
		p.published = &env
	}
	p.sealed = true
	return nil
}

// readyPartRepresentation is the receiver stamp a Commit must find: the
// encoded envelope pointer plus payload revision, with the typed flag that
// distinguishes an encoded row from a decoded one.
type readyPartRepresentation struct {
	receiver        *sharedResult
	payloadRevision uint64
	envelope        *PersistedResultEnvelope
	hasValue        bool
}

// readyPartPredecessor is one earlier address of the same receiver and the
// owning installation identity reserved for it by batch 4's task token.
type readyPartPredecessor struct {
	address    PersistedPartAddress
	receiver   *sharedResult
	key        LazyGroupKey
	generation uint64
}

// readyPartPreparationBase carries the ordered prefix a sharing pass has
// already prepared for one receiver: the real original observation, the
// representation the prefix will have published, the preceding installation
// identities, and the codec-owned immutable record to build the next
// representation from. It holds no ref, source lease, permit, receipt or
// incoming hold, and only cache orchestration creates it.
type readyPartPreparationBase struct {
	receiver     *sharedResult
	original     sharedResultPayloadState
	expected     readyPartRepresentation
	predecessors []readyPartPredecessor
	record       PersistedRecord
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

// holdSourceDependencies takes incoming ownership of every exact reference
// the source depends on and prepares them for the receiver, under the graph
// lock; a reference no longer in the graph refuses the preparation.
func (p *PreparedReadyPart) holdSourceDependencies(ctx context.Context, row *sharedResult) error {
	c := p.cache
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	for _, id := range c.partSourceDependenciesLocked(p.source) {
		dep := c.resultsByID[sharedResultID(id)]
		if dep == nil {
			return fmt.Errorf("prepare part: missing exact reference %d", id)
		}
		c.incrementIncomingOwnershipLocked(ctx, dep)
		p.deps = append(p.deps, dep)
	}
	_, err := c.preparePartDependenciesLocked(row, p.deps)
	return err
}

// PrepareReadyPart is the public single-demand preparation: the nil-base
// wrapper over prepareReadyPartFromBase, which records an empty predecessor
// list and the ordinary observed-representation guard.
func (c *Cache) PrepareReadyPart(ctx context.Context, receiver AnyResult, source *PartSourceLease, permit *PartPermit) (*PreparedReadyPart, error) {
	return c.prepareReadyPartFromBase(ctx, receiver, source, permit, nil)
}

// prepareReadyPartFromBase consumes source and permit on every return and
// produces the same owning PreparedReadyPart as the public entry. A non-nil
// base supplies only expected-version provenance and copied data: the real
// original row is still observed and validated while every store is pending.
func (c *Cache) prepareReadyPartFromBase(ctx context.Context, receiver AnyResult, source *PartSourceLease, permit *PartPermit, base *readyPartPreparationBase) (_ *PreparedReadyPart, rerr error) {
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
	if base != nil {
		if current, err = base.recordFor(row, version); err != nil {
			return nil, err
		}
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

	if err := p.holdSourceDependencies(ctx, row); err != nil {
		return nil, err
	}
	if err := p.pinReadySource(ctx, version.payload.hasValue); err != nil {
		return nil, err
	}
	p.next, err = prepareScopedPartRecord(current, source.record, source.Descriptor(), permit.address)
	if err != nil {
		return nil, err
	}
	if version.payload.hasValue {
		if err := p.prepareValueStore(ctx, row, current); err != nil {
			return nil, err
		}
	}
	if err := version.check(row); err != nil {
		return nil, err
	}
	if err := p.seal(row, base); err != nil {
		return nil, err
	}
	if err := c.reachPrepared(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// recordFor validates that the preparation base still describes row's
// observed representation and returns the record the next representation is
// built from: the validated prefix, not the real record, so the envelope
// contains every earlier role.
func (base *readyPartPreparationBase) recordFor(row *sharedResult, version capturedRowRevision) (PersistedRecord, error) {
	if base.receiver != row || base.expected.receiver != row {
		return PersistedRecord{}, fmt.Errorf("prepare part: preparation base names another receiver")
	}
	// The real row must still hold the observation the prefix was built
	// from: an unexpected representation change aborts this sequence
	// rather than relabelling a stale envelope with a newer revision.
	if version.payload.payloadRevision != base.original.payloadRevision ||
		version.payload.persistedEnvelope != base.original.persistedEnvelope ||
		version.payload.hasValue != base.original.hasValue {
		return PersistedRecord{}, partRefused("prepare: prefix base no longer matches the receiver")
	}
	return base.record, nil
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

// pinReadySource protects the source snapshot for the receiver and, when the
// receiver holds a live value, opens it for that value's store.
func (p *PreparedReadyPart) pinReadySource(ctx context.Context, hasValue bool) error {
	if p.source.descriptor.SnapshotID == "" {
		return nil
	}
	c := p.cache
	if c.snapshotManager == nil {
		return fmt.Errorf("prepare part: no snapshot manager")
	}
	ref, err := c.snapshotManager.PinSnapshot(ctx, p.source.descriptor.SnapshotID)
	if err != nil {
		return err
	}
	p.protection = &partProtection{ref: ref}
	if hasValue {
		p.accessor, err = c.snapshotManager.GetBySnapshotID(ctx, p.source.descriptor.SnapshotID, snapshots.NoUpdateLastUsed)
		if err != nil {
			return err
		}
	}
	return nil
}

// prepareValueStore asks the receiver's live value for the store that will
// publish the part.
func (p *PreparedReadyPart) prepareValueStore(ctx context.Context, row *sharedResult, current PersistedRecord) error {
	value, err := inlineValueAt(Result[Typed]{shared: row}, current.Call, p.permit.address.OutputPath)
	if err != nil {
		return err
	}
	preparer, ok := UnwrapAs[PartStorePreparer](value)
	if !ok {
		return fmt.Errorf("prepare part: typed value has no store")
	}
	dec := p.cache.partDecodeContext(ctx, row, p.next).atPath(p.permit.address.OutputPath)
	d := p.source.Descriptor()
	d.Address = clonePartAddress(p.permit.address)
	nextLocal, err := partRecordAt(p.next, p.permit.address.OutputPath)
	if err != nil {
		return err
	}
	d.Address.OutputPath = nil
	p.store, err = preparer.PreparePartStore(ctx, dec, nextLocal, d, p.accessor)
	return err
}

//nolint:gocyclo // One validate-then-commit sequence under the graph, gate and payload locks; splitting it would split the lock scopes.
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
	// Construction invariants, checked before any lock. A violation is a
	// defect in the constructor, not a changed source: it is an error, so no
	// retry loop can mistake it for a reason to select again.
	if !p.sealed {
		return nil, PartInstallRefused, fmt.Errorf("commit part: preparation was not sealed by its constructor")
	}
	if p.store == nil && p.published == nil {
		return nil, PartInstallRefused, fmt.Errorf("commit part: encoded preparation carries no envelope to install")
	}
	if p.store != nil && p.published != nil {
		return nil, PartInstallRefused, fmt.Errorf("commit part: preparation carries both a typed store and an envelope")
	}
	// Outside the Commit lock interval, on both sides of it.
	if err := c.reachBeforeCommit(ctx, p); err != nil {
		return nil, PartInstallRefused, err
	}
	defer func() {
		if outcome == PartInstalled {
			_ = c.fixtureReach(ctx, p.fixtureEvent(FixtureCommitPublished, ""))
		}
	}()
	// A preparation with a prepared prefix deliberately no longer matches the
	// original observation: its prefix has published since. Its expected
	// representation and every predecessor's installation identity are
	// validated below, which is a stricter check than the original stamp.
	if len(p.expectedPredecessors) == 0 {
		if err := p.version.check(p.receiver); err != nil {
			return nil, PartInstallRefused, p.version.changed("commit: receiver version", p.receiver)
		}
	}
	source := p.source
	if source.delegation != nil {
		if err := source.delegation.childVersion.check(p.receiver); err != nil {
			return nil, PartInstallRefused, source.delegation.childVersion.changed("commit: delegation child version", p.receiver)
		}
	}
	if p.original == nil && source.readiness == PartReady && !source.sessionlessShare {
		// A sessionless share validates its donated address alone, below. The
		// donor's whole-row capture would refuse an unchanged donated part
		// whenever a sibling of the donor published in the same pass.
		if err := source.version.check(source.source); err != nil {
			return nil, PartInstallRefused, source.version.changed("commit: donor version", source.source)
		}
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	row := p.receiver
	if c.resultsByID[row.id] != row || !p.permit.task.active.Load() {
		return nil, PartInstallRefused, partRefused("commit: receiver unregistered or task inactive")
	}
	if source.sessionlessShare {
		// The receiver's original structural admission must still hold. The
		// requirement generation changes only when the stored set actually
		// changes, so an earlier same-pass install that added edges already
		// inside Own(R) does not refuse this slot.
		if row.id != source.receiverID || row.requiredSessionResourcesGen.Load() != source.receiverOwnGen {
			return nil, PartInstallRefused, partRefused("commit: sessionless receiver requirements changed")
		}
		if partRowExpired(row, time.Now().Unix()) {
			return nil, PartInstallRefused, partRefused("commit: sessionless receiver expired")
		}
	}
	if p.original == nil && source.readiness == PartReady {
		if source.source == nil || c.resultsByID[source.source.id] != source.source {
			return nil, PartInstallRefused, partRefused("commit: donor unregistered")
		}
		if source.sessionlessShare {
			key, err := partAddressKey(source.descriptor.Address)
			if err != nil {
				return nil, PartInstallRefused, err
			}
			if c.partDonatedFactsLocked(source.source, key, source.descriptor.Address, source.descriptor.SnapshotID) != source.donated {
				return nil, PartInstallRefused, partRefused("commit: donated facts changed")
			}
		} else if current := c.partFactsLocked(source.source); source.facts != current {
			return nil, PartInstallRefused, partChanged("commit: donor facts changed", source.source, source.facts, current)
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
				return nil, PartInstallRefused, partRefused("commit: dependency not held")
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
	// Every recorded predecessor must have published under exactly the
	// installation identity reserved for it. A matching numeric revision
	// alone never authorizes a different installation.
	for _, pred := range p.expectedPredecessors {
		key, err := partAddressKey(pred.address)
		if err != nil {
			return nil, PartInstallRefused, err
		}
		state := gate.outputs[key]
		if state.phase == PartPending || state.task == nil {
			return nil, PartInstallRefused, partRefused("commit: predecessor not installed")
		}
		if state.task.row != pred.receiver || state.task.key != pred.key || state.task.generation != pred.generation {
			return nil, PartInstallRefused, partRefused("commit: predecessor installed by another task")
		}
	}
	if gate.revision == math.MaxUint64 {
		return nil, PartInstallRefused, fmt.Errorf("commit part: installation revision overflow")
	}
	if p.store != nil {
		if !p.store.TryLock() {
			return nil, PartInstallRefused, partRefused("commit: part store busy")
		}
		defer p.store.Unlock()
	}
	row.payloadMu.Lock()
	defer row.payloadMu.Unlock()
	expected := p.expectedRepresentation
	if row.payloadRevision != expected.payloadRevision || row.hasValue != expected.hasValue || row.persistedEnvelope != expected.envelope {
		return nil, PartInstallRefused, partChanged("commit: receiver representation", row, partSourceFacts{payload: expected.payloadRevision}, partSourceFacts{payload: row.payloadRevision})
	}
	if row.payloadRevision == math.MaxUint64 {
		return nil, PartInstallRefused, fmt.Errorf("commit part: payload revision overflow")
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
		row.persistedEnvelope = p.published
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

// fixtureEvent is the preparation's observation at one of its barrier points.
func (p *PreparedReadyPart) fixtureEvent(point FixtureBarrierPoint, detail string) FixtureBarrierEvent {
	event := FixtureBarrierEvent{Point: point, Detail: detail}
	if p.receiver != nil {
		event.ResultID = uint64(p.receiver.id)
	}
	if p.permit != nil {
		address := p.permit.address
		event.Address = &address
		if p.permit.task != nil {
			event.TaskGeneration = p.permit.task.generation
		}
	}
	return event
}

// reachBeforeCommit is the one site before Commit's locks: the in-package
// test field, which sees the preparation itself, then the gated fixture's
// barrier for the same point.
func (c *Cache) reachBeforeCommit(ctx context.Context, p *PreparedReadyPart) error {
	if c.testBeforePartCommit != nil {
		c.testBeforePartCommit(p)
	}
	return c.fixtureReach(ctx, p.fixtureEvent(FixtureBeforeCommit, ""))
}

// reachPrepared stands after a protected preparation exists, outside every
// lock. If the paused operation is canceled the caller's own error path
// releases the preparation.
func (c *Cache) reachPrepared(ctx context.Context, p *PreparedReadyPart) error {
	return c.fixtureReach(ctx, p.fixtureEvent(FixturePrepareDone, ""))
}
