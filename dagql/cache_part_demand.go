package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/dagger/dagger/engine"
	"golang.org/x/sync/errgroup"
)

// PartOutputOpener opens a final local descriptor without evaluating an operation
// or changing the output's meaning. The cache has already joined its owner.
type PartOutputOpener interface {
	OpenPart(context.Context, PersistedPartAddress) error
}
type PersistedLazyOperationFactory interface {
	PrepareLazyOperation(context.Context, *PersistDecodeContext, PersistedRecord, LazyOperationRoute) (LazyOperationInvocation, error)
}
type LazyOperationInvocation interface {
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
	if !row.imported && !row.partGate.restoredDelegation.Load() && len(row.partOffers) == 0 && gate == nil {
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
	if !row.imported && !row.partGate.restoredDelegation.Load() && len(row.partOffers) == 0 {
		return false
	}
	for _, group := range gate.groups {
		if group.phase == LazyEvaluationRunning {
			return false
		}
	}
	gate.managed = true
	row.partGate.active.Store(true)
	gate.revision++
	return true
}
func (c *Cache) evaluateAcquiredParts(ctx context.Context, res AnyResult, row *sharedResult, parts []PartKey) (rerr error) {
	return c.evaluateAcquiredScope(ctx, res, row, nil, parts)
}
func (c *Cache) evaluateAcquiredScope(ctx context.Context, res AnyResult, row *sharedResult, path PersistedRefPath, parts []PartKey) (rerr error) {
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
		record, _, _, err := c.probePart(ctx, row, PersistedPartAddress{OutputPath: path, Part: "metadata"})
		if err != nil {
			return err
		}
		outputs, err := mapTransferredOutputs(record)
		if err != nil {
			return err
		}
		hasMetadata := false
		for _, out := range outputs {
			if slices.Equal(out.Address.OutputPath, path) && out.Address.Part == "metadata" {
				hasMetadata = true
			}
		}
		if hasMetadata {
			if err := c.demandPart(ctx, res, PersistedPartAddress{OutputPath: path, Part: "metadata"}); err != nil {
				return err
			}
			record, _, _, err = c.probePart(ctx, row, PersistedPartAddress{OutputPath: path, Part: "metadata"})
			if err != nil {
				return err
			}
			outputs, err = mapTransferredOutputs(record)
			if err != nil {
				return err
			}
		}
		for _, out := range outputs {
			if slices.Equal(out.Address.OutputPath, path) {
				parts = append(parts, out.Address.Part)
			}
		}
	}
	eg, ctx := errgroup.WithContext(ctx)
	for _, part := range parts {
		eg.Go(func() error { return c.demandPart(ctx, res, PersistedPartAddress{OutputPath: path, Part: part}) })
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
func lazyEvaluationTaskKey(address LazyGroupAddress) LazyGroupKey {
	if len(address.OutputPath) == 0 {
		return LazyGroupKey("lazy:" + string(address.Group))
	}
	return LazyGroupKey("lazy:" + lazyGroupAddressKey(address))
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

//nolint:gocyclo // Intrinsically long demand state machine: probe, route, select, obtain, delegate, run.
func (c *Cache) demandPart(ctx context.Context, res AnyResult, address PersistedPartAddress) error {
	if err := engine.CheckSnapshotSharePreparation(ctx, "demand part"); err != nil {
		return err
	}
	var err error
	ctx, err = enterPartDemand(ctx, res.cacheSharedResult(), address)
	if err != nil {
		return err
	}
	demand := &PartDemandState{target: clonePartAddress(address)}
	ctx = context.WithValue(ctx, partDemandContextKey{}, demand)
	outer := partReselectWatch{loop: "demandPart"}
	for {
		outer.again(ctx, res.cacheSharedResult(), address)
		err := c.RunLazyTask(ctx, res, partTaskKey("acquire", address), LazyTaskSpec{Body: func(ctx context.Context) error {
			watch := partReselectWatch{loop: "demandPart acquire"}
			for {
				if err := context.Cause(ctx); err != nil {
					return err
				}
				row := res.cacheSharedResult()
				watch.again(ctx, row, address)
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
					return partRefusedBy("demand: receiver probe not ready", err)
				}
				if err != nil {
					return err
				}
				if probe != nil && probe.LocalComplete && !probe.Busy {
					if err := c.openAcquiredPart(ctx, res, address); err != nil {
						return err
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
				source, pendingParent, err := c.selectDemandPartSource(ctx, res, address, route)
				if partCanReselect(err) {
					watch.refused(err)
					continue
				}
				if err != nil {
					return err
				}
				if source != nil {
					kind := "selected-ready"
					if source.readiness == PartDownloadable {
						kind = "selected-chain"
					}
					if source.delegation != nil {
						c.recordPartFixtureDelegation(row, address, "selected-delegation", source.delegation)
					} else {
						c.recordPartFixture(row, address, kind)
					}
					var ownership atomic.Uint32 // 0 caller, 1 body, 2 caller released before body entry
					err = c.RunLazyTask(ctx, res, partTaskKey("obtain", address), LazyTaskSpec{Body: func(ctx context.Context) (rerr error) {
						if !ownership.CompareAndSwap(0, 1) {
							return partRefused("obtain: source released before the body entered")
						}
						defer func() { rerr = errors.Join(rerr, source.Release(context.WithoutCancel(ctx))) }()
						permit, outcome, err := c.TryAcquire(ctx, res, address, PartTaskFromContext(ctx))
						if err != nil {
							return err
						}
						if outcome == GateBusy || outcome == GateExecutionStarted {
							return errLazyEvaluationBusy
						}
						if outcome != GateGranted {
							return partRefused("obtain: already installed")
						}
						if source.readiness == PartDownloadable {
							return c.installChainPart(ctx, res, source, permit, demand)
						}
						return c.InstallReadyPart(ctx, res, source, permit)
					}})
					if ownership.CompareAndSwap(0, 2) {
						err = errors.Join(err, source.Release(ctx))
					}
					if errors.Is(err, errLazyEvaluationBusy) {
						err = c.joinLazyEvaluation(ctx, res, address)
					}
					if err == nil || partCanReselect(err) {
						watch.refused(err)
						continue
					}
					return err
				}
				if pendingParent != nil {
					if err := c.demandDelegatedParent(ctx, pendingParent); err != nil {
						if partCanReselect(err) {
							watch.refused(err)
							continue
						}
						return errors.Join(demand.causes(), err)
					}
					continue
				}
				if !route.HasLazyOperation {
					return errors.Join(fmt.Errorf("%w: %s", ErrUnavailablePart, key), demand.causes())
				}
				err = c.runLazyOperationDecision(ctx, res, address, route, demand)
				if partCanReselect(err) {
					watch.refused(err)
					continue
				}
				if err != nil {
					return errors.Join(demand.causes(), err)
				}
			}
		}})
		if partCanReselect(err) {
			outer.refused(err)
			continue
		}
		return err
	}
}

func routePartRecord(record PersistedRecord, address PersistedPartAddress) (LazyOperationRoute, error) {
	var route LazyOperationRoute
	found := false
	err := walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, func(f PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		if !slices.Equal(v.Path, address.OutputPath) {
			return v.Payload, nil
		}
		router, ok := f.Transfer.(PersistedPartRouter)
		if !ok {
			return nil, fmt.Errorf("part demand: no operation router")
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
		return PartSourceScan{}, partRefused("source check: candidates or demand changed")
	}
	drain.gate.mu.Lock()
	defer drain.gate.mu.Unlock()
	if !drain.currentLocked() || !drain.drainedLocked() {
		return PartSourceScan{}, partRefused("source check: drain not current or not drained")
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
func (c *Cache) runLazyOperationDecision(ctx context.Context, res AnyResult, address PersistedPartAddress, route LazyOperationRoute, demand *PartDemandState) error {
	if err := engine.CheckSnapshotSharePreparation(ctx, "prepare lazy operation"); err != nil {
		return err
	}
	return c.RunLazyTask(ctx, res, lazyEvaluationTaskKey(route.Group), LazyTaskSpec{Body: func(ctx context.Context) (rerr error) {
		task := PartTaskFromContext(ctx)
		drain, outcome, err := c.PrepareOriginal(ctx, res, route.Group, route.WriteSet, task)
		if err != nil {
			return err
		}
		if outcome == GateExecutionStarted {
			return fmt.Errorf("operation consumed without required output %s", address.Part)
		}
		if outcome != GateGranted {
			return partRefused("decision: another evaluation owns the write set")
		}
		if err := drain.Wait(ctx); err != nil {
			return err
		}
		row := res.cacheSharedResult()
		ctx = c.partFixtureLazyContext(ctx, row, address)
		var version capturedRowRevision
		record, err := c.capturePartRecord(ctx, row, row.imported, nil, &version)
		if err != nil {
			return err
		}
		local, err := partRecordAt(record, address.OutputPath)
		if err != nil {
			return err
		}
		family, ok := PersistedObjectFamilyByName(local.Envelope.ObjectCodec)
		if !ok {
			return fmt.Errorf("lazy: unsupported output family")
		}
		factory, ok := family.Transfer.(PersistedLazyOperationFactory)
		if !ok {
			return fmt.Errorf("lazy: family has no saved invoker")
		}
		if err := engine.CheckSnapshotSharePreparation(ctx, "prepare lazy operation"); err != nil {
			return err
		}
		invocation, err := factory.PrepareLazyOperation(ctx, c.partDecodeContext(ctx, row, record).atPath(address.OutputPath), local, route)
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
					return errors.Join(err, scan.Source.Release(ctx))
				}
				if outcome != GateGranted {
					if err := scan.Source.Release(ctx); err != nil {
						return err
					}
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
				return partRefused("decision: final source check refused")
			}
			if err := engine.CheckSnapshotSharePreparation(ctx, "run lazy operation"); err != nil {
				return err
			}
			c.recordPartFixture(row, address, "lazy-enter")
			if err := invocation.Run(ctx); err != nil {
				return err
			}
			encoding, err := invocation.Capture(ctx, NewPersistEncodeContext(c, uint64(row.id), local.Call))
			if err != nil {
				return err
			}
			local.Envelope.ObjectJSON = encoding.JSON
			local.SnapshotLinks = encoding.SnapshotLinks
			produced, err := replacePartRecord(record, address.OutputPath, local)
			if err != nil {
				return err
			}
			return c.publishEvaluatedParts(ctx, res, address, produced, original, cleanup)
		}
		return partRefused("decision: both scans refused")
	}})
}

var errLazyEvaluationBusy = errors.New("part operation owns admission")

func (c *Cache) joinLazyEvaluation(ctx context.Context, res AnyResult, address PersistedPartAddress) error {
	gate := res.cacheSharedResult().partGate.loadOrCreate()
	gate.mu.Lock()
	var token *PartTaskToken
	for _, group := range gate.groups {
		if group.phase != LazyEvaluationOpen && containsPart(group.writeSet, address) {
			token = group.task
			break
		}
	}
	gate.mu.Unlock()
	if token == nil {
		return partRefused("join: no running evaluation to join")
	}
	if !isPartTaskKey(token.key) {
		return c.joinPartInstallation(ctx, res, token)
	}
	err := c.RunLazyTask(ctx, res, token.key, LazyTaskSpec{Body: func(context.Context) error { return partRefused("join: joined evaluation body") }})
	if err != nil {
		return err
	}
	return partRefused("join: joined evaluation finished")
}
