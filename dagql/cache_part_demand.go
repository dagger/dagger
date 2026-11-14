package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
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
	return NewPersistDecodeContext(CurrentDagqlServer(ctx), uint64(row.id), record.Call).WithSnapshotRoles(record.SnapshotLinks)
}

// PartTransferShell is a value that can decode from another engine's record
// as a shell: without its recipe, its parts come only through part
// acquisition. A value that decoded with its recipe armed (ArmRecipe) is no
// shell: it evaluates as a value restored from local persistence does.
type PartTransferShell interface{ PartTransferShell() bool }

// transferShellLocked reports whether row's value res came from another
// engine's record as a shell. A value that can't tell is taken for one.
// Requires egraphMu.
func transferShellLocked(res AnyResult, row *sharedResult) bool {
	if !row.imported {
		return false
	}
	shell, ok := UnwrapAs[PartTransferShell](res)
	return !ok || shell.PartTransferShell()
}

func (c *Cache) usesPartAcquisition(ctx context.Context, res AnyResult, row *sharedResult) bool {
	if _, ok := UnwrapAs[HasPartHost](res); !ok {
		return false
	}
	// Only starting to manage a gate writes; every other answer is read under
	// the read lock.
	c.egraphMu.RLock()
	settled, uses := c.partAcquisitionSettledLocked(row)
	c.egraphMu.RUnlock()
	if settled {
		return uses
	}
	wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
	defer c.egraphMu.Unlock()
	shell := transferShellLocked(res, row)
	gate := row.partGate.gate.Load()
	if !shell && !row.partGate.restoredDelegation.Load() && !row.hasPartOffersLocked() && gate == nil {
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
	if !shell && !row.partGate.restoredDelegation.Load() && !row.hasPartOffersLocked() {
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

// partAcquisitionSettledLocked answers usesPartAcquisition without writing
// whenever its answer needs no write, in the same order as the write path:
// true for a managed gate, false for a row with no part state, false while
// a group of the gate is evaluating. settled is false only when the gate
// would start being managed, which needs the write lock. The caller holds
// egraphMu, for reading or writing.
func (c *Cache) partAcquisitionSettledLocked(row *sharedResult) (settled, uses bool) {
	noPartState := !row.imported && !row.partGate.restoredDelegation.Load() && !row.hasPartOffersLocked()
	gate := row.partGate.gate.Load()
	if gate == nil {
		return noPartState, false
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.managed {
		return true, true
	}
	if noPartState {
		return true, false
	}
	for _, group := range gate.groups {
		if group.phase == LazyEvaluationRunning {
			return true, false
		}
	}
	return false, false
}
func (c *Cache) evaluateAcquiredParts(ctx context.Context, res AnyResult, row *sharedResult, parts []PartKey) (rerr error) {
	return c.evaluateAcquiredScope(ctx, res, row, nil, parts)
}
func (c *Cache) evaluateAcquiredScope(ctx context.Context, res AnyResult, row *sharedResult, path PersistedRefPath, parts []PartKey) (rerr error) {
	if c.reuseAcquiredScope(ctx, row, path, parts) {
		return nil
	}
	whole := len(parts) == 0
	var seen partReuse
	if whole {
		seen = c.capturePartReuse(row)
	}
	wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
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
	eg, egCtx := errgroup.WithContext(ctx)
	for _, part := range parts {
		eg.Go(func() error { return c.demandPart(egCtx, res, PersistedPartAddress{OutputPath: path, Part: part}) })
	}
	if err := eg.Wait(); err != nil {
		return err
	}
	if whole {
		c.recordAcquiredScope(ctx, row, path, parts, seen)
	}
	return nil
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
	demand := &PartDemandState{target: clonePartAddress(address), started: time.Now()}
	ctx = context.WithValue(ctx, partDemandContextKey{}, demand)
	outer := partReselectWatch{loop: "demandPart"}
	// ranCall is set once the demand has run the value's call for a part no
	// source supplies, and callErr is why that run failed.
	var (
		ranCall bool
		callErr error
	)
	for {
		outer.again(ctx, res.cacheSharedResult(), address)
		// opened is the row's state the body probed the part at, when it
		// found it complete and opened it.
		var opened *partReuse
		err := c.RunLazyTask(ctx, res, partTaskKey("acquire", address), LazyTaskSpec{Body: func(ctx context.Context) error {
			opened = nil
			watch := partReselectWatch{loop: "demandPart acquire"}
			// retry notes a refusal in the reselect class that is about to be
			// retried. It returns an error when the progress rule forbids the
			// retry.
			retry := func(err error) error {
				watch.refused(err)
				return demand.refused(watch.loop, watch.n, address, err)
			}
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
					joinCtx, endJoin := profDemandStep(ctx, "join")
					err := c.joinPartInstallation(joinCtx, res, state.task)
					endJoin(err)
					if err != nil {
						return err
					}
					continue
				}
				seen := c.capturePartReuse(row)
				probeCtx, endProbe := profDemandStep(ctx, "probe")
				record, _, probe, err := c.probePart(probeCtx, row, address)
				endProbe(err)
				if errors.Is(err, ErrPersistStateNotReady) {
					return partRefusedBy("demand: receiver probe not ready", err)
				}
				if err != nil {
					return err
				}
				if probe != nil && probe.LocalComplete && !probe.Busy {
					openCtx, endOpen := profDemandStep(ctx, "open")
					err := c.openAcquiredPart(openCtx, res, address)
					endOpen(err)
					if err != nil {
						return err
					}
					session, err := partSession(ctx)
					if err != nil {
						return err
					}
					wcprof.ReadLock(ctx, &c.egraphMu, egraphLockIdent)
					allowed := c.sessionSatisfiesResourceRequirementsLocked(session, row)
					c.egraphMu.RUnlock()
					if !allowed {
						return fmt.Errorf("part demand: session no longer satisfies installed requirements")
					}
					opened = &seen
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
				selectCtx, endSelect := profDemandStep(ctx, "select")
				source, pendingParent, err := c.selectDemandPartSource(selectCtx, res, address, route)
				endSelect(err)
				if partCanReselect(err) {
					if stuck := retry(err); stuck != nil {
						return stuck
					}
					continue
				}
				if err != nil {
					return err
				}
				if source != nil {
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
					if err == nil {
						continue
					}
					if partCanReselect(err) {
						if stuck := retry(err); stuck != nil {
							return stuck
						}
						continue
					}
					return err
				}
				if pendingParent != nil {
					if err := c.demandDelegatedParent(ctx, pendingParent); err != nil {
						if partCanReselect(err) {
							if stuck := retry(err); stuck != nil {
								return stuck
							}
							continue
						}
						return errors.Join(demand.causes(), err)
					}
					continue
				}
				if !route.HasLazyOperation {
					// Nothing supplies the part and no operation of the value
					// makes it: its call runs again, once, as a miss runs it,
					// and the value it makes is then a ready source.
					if !ranCall {
						ranCall = true
						callCtx, endCall := profDemandStep(ctx, "call")
						callErr = c.runCallForPart(callCtx, res, key)
						endCall(callErr)
						if callErr == nil {
							continue
						}
					}
					return errors.Join(fmt.Errorf("%w: %s", ErrUnavailablePart, key), demand.causes(), callErr)
				}
				decideCtx, endDecide := profDemandStep(ctx, "decide")
				err = c.runLazyOperationDecision(decideCtx, res, address, route, demand)
				endDecide(err)
				if partCanReselect(err) {
					if stuck := retry(err); stuck != nil {
						return stuck
					}
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
		if err == nil && opened != nil {
			// Only once the task has finished: its finish syncs the row's
			// owner leases, which keep the opened part from pruning.
			c.recordAcquiredPart(ctx, res, address, *opened)
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
	wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
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
		var drainStart time.Time
		if wcprof.Enabled(ctx) {
			drainStart = time.Now()
		}
		err = drain.Wait(ctx)
		if !drainStart.IsZero() {
			wcprof.RecordWaitAt(ctx, "dagql.partDrain", wcprof.WaitReasonLock, drainStart, time.Now())
		}
		if err != nil {
			return err
		}
		row := res.cacheSharedResult()
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
					// The second scan's refusal would otherwise reach the demand
					// as this loop's own, which names no counters.
					if stuck := demand.refused("runLazyOperationDecision", uint64(scanAttempt)+1, address, err); stuck != nil {
						return stuck
					}
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
			// No source could supply the part: it is computed here.
			wcprof.Fact(ctx, 0, "part.no_source", uint64(row.id))
			c.logPartWithoutSource(ctx, row, address, route, record, scan.NoSource)
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
			return c.publishEvaluatedParts(ctx, res, address, produced, original, cleanup, demand)
		}
		return partRefused("decision: both scans refused")
	}})
}

// logPartWithoutSource records a part that no source could supply, which the
// demand now computes: the entry as the remote cache knows it (the cache, the
// entry's number and replacement count, and when a merge installed its
// value), its step and part, and why check's scan found no source. The scan's
// candidates are the entry and the equivalent entries it looked at; an
// address one of them held, which the scan passed over, had expired or was
// tried in this demand and failed. An entry still being written is passed
// over whatever it holds, so its address isn't counted. The reason is the
// first that holds of:
//   - download_failed: every held address was tried in this demand and
//     failed;
//   - offer_unavailable: an address was held, but had expired;
//   - holder_busy: an equivalent entry was still being written, so the scan
//     could not read it or its address, or held the part complete but in use;
//   - no_offer: no candidate held the part or an address for it.
//
// origin is merged for a value a merge installed since the engine started,
// and restored for any other.
//
// The computation is a second execution of the entry's recipe beside what
// the cache holds from the first (cache_part_rebuild_mix.go): kept_parts are
// the entry's other parts of the same operation group that are already
// complete, which stay from the first; merged_dependents are the merged
// entries computed from the first, over a walk of at most
// rebuildMixCountLimit entries, with merged_dependents_more set when the walk
// stopped there.
func (c *Cache) logPartWithoutSource(ctx context.Context, row *sharedResult, address PersistedPartAddress, route LazyOperationRoute, record PersistedRecord, check *SourceCheck) {
	offers, failed, busy := 0, 0, false
	for _, candidate := range check.candidates {
		if candidate.row != row && (candidate.unready || (candidate.probe != nil && candidate.probe.LocalComplete && candidate.probe.Busy)) {
			busy = true
		}
		if candidate.offer == nil || candidate.unready {
			continue
		}
		offers++
		if check.demand != nil && check.demand.exhausted(candidate.row.id, address, candidate.offer, candidate.facts.offers) {
			failed++
		}
	}
	reason := "no_offer"
	switch {
	case offers > 0 && failed == offers:
		reason = "download_failed"
	case offers > 0:
		reason = "offer_unavailable"
	case busy:
		reason = "holder_busy"
	}
	kept := 0
	if probes, err := describePartRecord(record); err == nil {
		key, _ := partAddressKey(address)
		kept = keptParts(probes, route.WriteSet, key)
	}
	wcprof.ReadLock(ctx, &c.egraphMu, egraphLockIdent)
	replacements, mergedAt := row.replacements, row.mergedAtUnixNano
	dependents, more := c.mergedDependentsLocked(row, rebuildMixCountLimit)
	c.egraphMu.RUnlock()
	origin := "restored"
	if mergedAt != 0 {
		origin = "merged"
	}
	part := string(address.Part)
	if len(address.OutputPath) > 0 {
		part = address.OutputPath.String() + "/" + part
	}
	var field string
	if record.Call != nil {
		field = record.Call.Field
	}
	c.partsWithoutSource.count(reason)
	slog.InfoContext(ctx, "remote cache part without a source",
		"cache", c.Identity().ID,
		"entry", uint64(row.id),
		"replacements", replacements,
		"merged_at", mergedAt,
		"origin", origin,
		"field", field,
		"part", part,
		"group", string(route.Group.Group),
		"reason", reason,
		"candidates", len(check.candidates),
		"offers", offers,
		"kept_parts", kept,
		"merged_dependents", dependents,
		"merged_dependents_more", more)
}

// runCallForPart runs again the call that made res, for res's part key that
// no source supplies and no operation of res makes: another engine sent the
// value without the part, and its offer of the part is gone or failed. From
// now until the part is complete, a lookup passes res by (servableLocked), so
// the call computes its value here, as on an engine that never had res, and
// that value, evaluated, is a ready source of the part for res.
func (c *Cache) runCallForPart(ctx context.Context, res AnyResult, key string) error {
	row := res.cacheSharedResult()
	frame := row.loadResultCall()
	if frame == nil {
		return fmt.Errorf("run call for part %s: no call recorded", key)
	}
	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return fmt.Errorf("run call for part %s: no dagql server", key)
	}
	id, err := frame.recipeID(ctx, c)
	if err != nil {
		return fmt.Errorf("run call for part %s: %w", key, err)
	}
	// The call's run is a second execution beside the first's other parts
	// and the merged entries computed from it, as logPartWithoutSource
	// counts them.
	kept := 0
	var version capturedRowRevision
	if record, err := c.capturePartRecord(ctx, row, row.imported, nil, &version); err == nil {
		if probes, err := describePartRecord(record); err == nil {
			kept = keptParts(probes, nil, key)
		}
	}
	wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
	if row.unavailableParts == nil {
		row.unavailableParts = map[string]struct{}{}
	}
	row.unavailableParts[key] = struct{}{}
	dependents, more := c.mergedDependentsLocked(row, rebuildMixCountLimit)
	c.egraphMu.Unlock()
	slog.InfoContext(ctx, "remote cache part unavailable: running its call",
		"cache", c.Identity().ID,
		"entry", uint64(row.id),
		"field", frame.Field,
		"part", key,
		"kept_parts", kept,
		"merged_dependents", dependents,
		"merged_dependents_more", more)
	ran, err := srv.LoadTypeReplayable(ctx, id)
	if err != nil {
		return fmt.Errorf("run call for part %s: %w", key, err)
	}
	if err := c.Evaluate(ctx, ran); err != nil {
		return fmt.Errorf("run call for part %s: %w", key, err)
	}
	return nil
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

// partsWithoutSource counts the parts work read with no source to read them
// from, by the reason logPartWithoutSource gives, over the cache's life.
type partsWithoutSource struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func (p *partsWithoutSource) count(reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counts == nil {
		p.counts = map[string]uint64{}
	}
	p.counts[reason]++
}

// PartsWithoutSource returns how many parts work read with no source over
// the cache's life, by reason (no_offer, download_failed, offer_unavailable,
// holder_busy): each a part the engine had to make again. It is nil when
// there were none.
func (c *Cache) PartsWithoutSource() map[string]uint64 {
	c.partsWithoutSource.mu.Lock()
	defer c.partsWithoutSource.mu.Unlock()
	return maps.Clone(c.partsWithoutSource.counts)
}
