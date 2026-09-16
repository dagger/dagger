package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// PartOutputOpener opens a final local descriptor without evaluating a producer
// or changing the output's meaning. The cache has already joined its owner.
type PartOutputOpener interface {
	OpenPart(context.Context, PersistedPartAddress) error
}
type PersistedPartProducerFactory interface {
	PreparePartProducer(context.Context, *PersistDecodeContext, PersistedRecord, PartProducerRoute) (PartProducerInvocation, error)
}
type PartProducerInvocation interface {
	Run(context.Context) error
	Capture(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error)
	Release(context.Context) error
}

func (c *Cache) partDecodeContext(ctx context.Context, row *sharedResult, record PersistedRecord) *PersistDecodeContext {
	server := CurrentDagqlServer(ctx)
	if server == nil {
		server = row.partGate.server.Load()
	}
	return NewPersistDecodeContext(server, uint64(row.id), record.Call).WithSnapshotRoles(record.SnapshotLinks)
}
func (c *Cache) usesPartAcquisition(res AnyResult, row *sharedResult) bool {
	if _, ok := UnwrapAs[HasPartHost](res); !ok {
		return false
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	gate := row.partGate.gate.Load()
	if !row.imported && len(row.partOffers) == 0 && gate == nil {
		return false
	}
	if gate == nil {
		gate = row.partGate.loadOrCreate()
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.managed {
		return true
	}
	if !row.imported && len(row.partOffers) == 0 {
		return false
	}
	for _, group := range gate.groups {
		if group.phase == ProducerRunning {
			return false
		}
	}
	gate.managed = true
	row.partGate.active.Store(true)
	gate.revision++
	return true
}
func (c *Cache) evaluateAcquiredParts(ctx context.Context, res AnyResult, row *sharedResult, parts []PartKey) (rerr error) {
	c.egraphMu.Lock()
	if c.resultsByID[row.id] != row {
		c.egraphMu.Unlock()
		return fmt.Errorf("part demand: unregistered receiver")
	}
	c.incrementIncomingOwnershipLocked(ctx, row)
	c.egraphMu.Unlock()
	defer func() { rerr = errors.Join(rerr, c.releasePartRow(context.WithoutCancel(ctx), row)) }()
	if len(parts) == 0 {
		// Metadata establishes a Container's final target-keyed output set.
		record, _, _, err := c.probePart(ctx, row, PersistedPartAddress{Part: "metadata"})
		if err != nil {
			return err
		}
		outputs, err := mapTransferredOutputs(record)
		if err != nil {
			return err
		}
		hasMetadata := false
		for _, out := range outputs {
			if out.Address.Part == "metadata" {
				hasMetadata = true
			}
		}
		if hasMetadata {
			if err := c.demandPart(ctx, res, PersistedPartAddress{Part: "metadata"}); err != nil {
				return err
			}
			record, _, _, err = c.probePart(ctx, row, PersistedPartAddress{Part: "metadata"})
			if err != nil {
				return err
			}
			outputs, err = mapTransferredOutputs(record)
			if err != nil {
				return err
			}
		}
		for _, out := range outputs {
			parts = append(parts, out.Address.Part)
		}
	}
	eg, ctx := errgroup.WithContext(ctx)
	for _, part := range parts {
		eg.Go(func() error { return c.demandPart(ctx, res, PersistedPartAddress{Part: part}) })
	}
	return eg.Wait()
}
func partTaskKey(prefix string, address PersistedPartAddress) LazyGroupKey {
	if len(address.OutputPath) == 0 {
		return LazyGroupKey(prefix + ":" + string(address.Part))
	}
	key, _ := partAddressKey(address)
	return LazyGroupKey(prefix + ":" + key)
}
func producerTaskKey(address ProducerAddress) LazyGroupKey {
	if len(address.OutputPath) == 0 {
		return LazyGroupKey("producer:" + string(address.Group))
	}
	return LazyGroupKey("producer:" + producerAddressKey(address))
}
func (c *Cache) joinPartInstallation(ctx context.Context, res AnyResult, token *PartTaskToken) error {
	if isPartTaskKey(token.key) {
		return c.RunLazyTask(ctx, res, token.key, LazyTaskSpec{Body: func(context.Context) error {
			if token.settled.Load() {
				return nil
			}
			return fmt.Errorf("part owner continuation missing")
		}})
	}
	parts, _ := UnwrapAs[HasLazyEvaluationParts](res)
	return c.evaluateGroup(ctx, res, token.row, token.key, parts)
}
func (c *Cache) demandPart(ctx context.Context, res AnyResult, address PersistedPartAddress) error {
	demand := &PartDemandState{}
	ctx = context.WithValue(ctx, partDemandContextKey{}, demand)
	for {
		err := c.RunLazyTask(ctx, res, partTaskKey("acquire", address), LazyTaskSpec{Body: func(ctx context.Context) error {
			for {
				if err := context.Cause(ctx); err != nil {
					return err
				}
				row := res.cacheSharedResult()
				gate := row.partGate.loadOrCreate()
				key, _ := partAddressKey(address)
				gate.mu.Lock()
				state := gate.outputs[key]
				gate.mu.Unlock()
				if state.phase == PartOutputInstalled {
					if err := c.joinPartInstallation(ctx, res, state.task); err != nil {
						return err
					}
					continue
				}
				record, _, probe, err := c.probePart(ctx, row, address)
				if errors.Is(err, ErrPersistStateNotReady) {
					return ErrPartReselect
				}
				if err != nil {
					return err
				}
				if probe != nil && probe.LocalComplete && !probe.Busy {
					if opener, ok := UnwrapAs[PartOutputOpener](res); ok {
						if err := opener.OpenPart(ctx, address); err != nil {
							return err
						}
					}
					session, err := partSession(ctx)
					if err != nil {
						return err
					}
					c.egraphMu.RLock()
					allowed := c.sessionSatisfiesResourceRequirementsLocked(session, row)
					c.egraphMu.RUnlock()
					if !allowed {
						return fmt.Errorf("part demand: session no longer satisfies installed requirements")
					}
					return nil
				}
				route, err := routePartRecord(record, address)
				if err != nil {
					return err
				}
				if route.NeedsMetadata {
					if err := c.demandPart(ctx, res, PersistedPartAddress{OutputPath: slices.Clone(address.OutputPath), Part: "metadata"}); err != nil {
						return err
					}
					continue
				}
				source, err := c.AcquireEquivalentPartSource(ctx, res, address)
				if partCanReselect(err) {
					continue
				}
				if err != nil {
					return err
				}
				if source != nil {
					var ownership atomic.Uint32 // 0 caller, 1 body, 2 caller released before body entry
					err = c.RunLazyTask(ctx, res, partTaskKey("obtain", address), LazyTaskSpec{Body: func(ctx context.Context) (rerr error) {
						if !ownership.CompareAndSwap(0, 1) {
							return ErrPartReselect
						}
						defer func() { rerr = errors.Join(rerr, source.Release(context.WithoutCancel(ctx))) }()
						permit, outcome, err := c.TryAcquire(ctx, res, address, PartTaskFromContext(ctx))
						if err != nil {
							return err
						}
						if outcome == GateBusy || outcome == GateExecutionStarted {
							return errPartProducerBusy
						}
						if outcome != GateGranted {
							return ErrPartReselect
						}
						if source.readiness == PartDownloadable {
							return c.installChainPart(ctx, res, source, permit, demand)
						}
						return c.InstallReadyPart(ctx, res, source, permit)
					}})
					if ownership.CompareAndSwap(0, 2) {
						err = errors.Join(err, source.Release(ctx))
					}
					if errors.Is(err, errPartProducerBusy) {
						err = c.joinPartProducer(ctx, res, address)
					}
					if err == nil || partCanReselect(err) {
						continue
					}
					return err
				}
				if !route.HasProducer {
					return errors.Join(fmt.Errorf("%w: %s", ErrUnavailablePart, key), demand.causes())
				}
				err = c.runPartProducerDecision(ctx, res, address, route, demand)
				if partCanReselect(err) {
					continue
				}
				if err != nil {
					return err
				}
			}
		}})
		if partCanReselect(err) {
			continue
		}
		return err
	}
}

func routePartRecord(record PersistedRecord, address PersistedPartAddress) (PartProducerRoute, error) {
	var route PartProducerRoute
	found := false
	err := walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, nil, func(f PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if !slices.Equal(v.Path, address.OutputPath) {
			return v.Payload, nil
		}
		router, ok := f.Transfer.(PersistedPartRouter)
		if !ok {
			return nil, fmt.Errorf("part demand: no producer router")
		}
		var err error
		route, err = router.RouteParts(v, address.Part)
		found = true
		return v.Payload, err
	})
	if err == nil && !found {
		err = fmt.Errorf("part demand: undeclared output path")
	}
	return route, err
}

type PartSourceScan struct {
	Source   *PartSourceLease
	NoSource *SourceCheck
}

func (c *Cache) CheckPartSources(ctx context.Context, res AnyResult, address PersistedPartAddress, drain *DrainTicket, demand *PartDemandState) (PartSourceScan, error) {
	source, candidates, err := c.scanPartSources(ctx, res, address, demand)
	if err != nil {
		return PartSourceScan{}, err
	}
	if source != nil {
		return PartSourceScan{Source: source}, nil
	}
	lookup, err := c.partLookupFor(res.cacheSharedResult())
	if err != nil {
		return PartSourceScan{}, err
	}
	session, err := partSession(ctx)
	if err != nil {
		return PartSourceScan{}, err
	}
	check := &SourceCheck{drain: drain, candidates: candidates, lookup: lookup, sessionID: session, demand: demand, checked: true}
	if demand != nil {
		demand.mu.Lock()
		check.demandRevision = demand.revision
		demand.mu.Unlock()
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if drain == nil || drain.task.row != res.cacheSharedResult() {
		return PartSourceScan{}, fmt.Errorf("source scan: wrong drain")
	}
	if !c.sourceCheckCurrentLocked(check) {
		return PartSourceScan{}, ErrPartReselect
	}
	drain.gate.mu.Lock()
	defer drain.gate.mu.Unlock()
	if !drain.currentLocked() || !drain.drainedLocked() {
		return PartSourceScan{}, ErrPartReselect
	}
	check.gateRevision = drain.gate.revision
	return PartSourceScan{NoSource: check}, nil
}
func (c *Cache) sourceCheckCurrentLocked(check *SourceCheck) bool {
	if !check.checked || check.drain == nil {
		return false
	}
	current := c.collectPartCandidatesLocked(check.drain.task.row, check.lookup, check.sessionID)
	if len(current) != len(check.candidates) {
		return false
	}
	for i, candidate := range current {
		old := check.candidates[i]
		if candidate.row != old.row || candidate.route != old.route || c.partFactsLocked(candidate.row) != old.facts {
			return false
		}
	}
	if check.demand != nil {
		check.demand.mu.Lock()
		defer check.demand.mu.Unlock()
		if check.demandRevision != check.demand.revision {
			return false
		}
	}
	return true
}
func (c *Cache) runPartProducerDecision(ctx context.Context, res AnyResult, address PersistedPartAddress, route PartProducerRoute, demand *PartDemandState) error {
	return c.RunLazyTask(ctx, res, producerTaskKey(route.Group), LazyTaskSpec{Body: func(ctx context.Context) (rerr error) {
		task := PartTaskFromContext(ctx)
		drain, outcome, err := c.PrepareOriginal(ctx, res, route.Group, route.WriteSet, task)
		if err != nil {
			return err
		}
		if outcome == GateExecutionStarted {
			return fmt.Errorf("producer consumed without required output %s", address.Part)
		}
		if outcome != GateGranted {
			return ErrPartReselect
		}
		if err := drain.Wait(ctx); err != nil {
			return err
		}
		row := res.cacheSharedResult()
		var version capturedRowRevision
		record, err := c.capturePartRecord(ctx, row, row.imported, nil, &version)
		if err != nil {
			return err
		}
		family, ok := PersistedObjectFamilyByName(record.Envelope.ObjectCodec)
		if !ok {
			return fmt.Errorf("producer: unsupported output family")
		}
		factory, ok := family.Transfer.(PersistedPartProducerFactory)
		if !ok {
			return fmt.Errorf("producer: family has no saved invoker")
		}
		invocation, err := factory.PreparePartProducer(ctx, c.partDecodeContext(ctx, row, record), record, route)
		if err != nil {
			return err
		}
		cleanup := &partCleanup{fn: invocation.Release}
		defer func() { rerr = errors.Join(rerr, cleanup.release(ctx)) }()
		for scanAttempt := 0; scanAttempt < 2; scanAttempt++ {
			scan, err := c.CheckPartSources(ctx, res, address, drain, demand)
			if err != nil {
				return err
			}
			if scan.Source != nil {
				permit, outcome, err := c.TryAcquireForDecision(ctx, res, address, drain, task)
				if err != nil {
					_ = scan.Source.Release(ctx)
					return err
				}
				if outcome != GateGranted {
					_ = scan.Source.Release(ctx)
					if outcome == GateAlreadyInstalled {
						return nil
					}
					continue
				}
				if scan.Source.readiness == PartDownloadable {
					err = c.installChainPart(ctx, res, scan.Source, permit, demand)
				} else {
					err = c.InstallReadyPart(ctx, res, scan.Source, permit)
				}
				if partCanReselect(err) {
					continue
				}
				return err
			}
			original, outcome, err := c.BeginOriginal(ctx, scan.NoSource)
			if err != nil {
				return err
			}
			if outcome != GateGranted {
				return ErrPartReselect
			}
			if err := invocation.Run(ctx); err != nil {
				return err
			}
			encoding, err := invocation.Capture(ctx, NewPersistEncodeContext(c, uint64(row.id), record.Call))
			if err != nil {
				return err
			}
			produced := record
			produced.Envelope.ObjectJSON = encoding.JSON
			produced.SnapshotLinks = encoding.SnapshotLinks
			return c.publishProducedParts(ctx, res, address, produced, original, cleanup)
		}
		return ErrPartReselect
	}})
}

var errPartProducerBusy = errors.New("part producer owns admission")

func (c *Cache) joinPartProducer(ctx context.Context, res AnyResult, address PersistedPartAddress) error {
	gate := res.cacheSharedResult().partGate.loadOrCreate()
	gate.mu.Lock()
	var token *PartTaskToken
	for _, group := range gate.groups {
		if group.phase != ProducerOpen && containsPart(group.writeSet, address) {
			token = group.task
			break
		}
	}
	gate.mu.Unlock()
	if token == nil {
		return ErrPartReselect
	}
	if !isPartTaskKey(token.key) {
		return c.joinPartInstallation(ctx, res, token)
	}
	err := c.RunLazyTask(ctx, res, token.key, LazyTaskSpec{Body: func(context.Context) error { return ErrPartReselect }})
	if err != nil {
		return err
	}
	return ErrPartReselect
}
