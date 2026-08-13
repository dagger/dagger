package dagql

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/slog"
)

type pruneCandidate struct {
	resultID      sharedResultID
	entry         CacheUsageEntry
	expiresAtUnix int64
}

type pruneUsageIdentityState struct {
	sizeBytes    int64
	aliveMembers int
	ownerID      sharedResultID
}

type pruneSnapshotResult struct {
	resultID                 sharedResultID
	incomingCount            int64
	deps                     []sharedResultID
	directResultBytes        int64
	usageIdentities          []string
	entry                    CacheUsageEntry
	callLabel                string
	callFrame                *ResultCall
	hasPersistedEdge         bool
	persistedEdgeUnpruneable bool
	expiresAtUnix            int64
}

type pruneSnapshot struct {
	results         map[sharedResultID]pruneSnapshotResult
	usageIdentities map[string]pruneUsageIdentityState
	usedBytes       int64
}

type prunePlanEntry struct {
	candidate    pruneCandidate
	reclaimBytes int64
}

type pruneSimulationState struct {
	remainingIncomingCount    map[sharedResultID]int64
	aliveCountByUsageIdentity map[string]int
	sizeBytesByUsageIdentity  map[string]int64
	collected                 map[sharedResultID]struct{}
}

type pruneSnapshotMode uint8

const (
	pruneSnapshotDisk pruneSnapshotMode = iota
	pruneSnapshotMetadata
	pruneCancellationCheckInterval = 256
)

type pruneCancellationChecker struct {
	ctx  context.Context
	work uint64
}

func newPruneCancellationChecker(ctx context.Context) *pruneCancellationChecker {
	if ctx.Done() == nil {
		return nil
	}
	return &pruneCancellationChecker{ctx: ctx}
}

func (checker *pruneCancellationChecker) checkNow() error {
	if checker == nil {
		return nil
	}
	return checker.ctx.Err()
}

func (checker *pruneCancellationChecker) check() error {
	checker.work++
	if checker.work == 1 || checker.work%pruneCancellationCheckInterval == 0 {
		return checker.ctx.Err()
	}
	return nil
}

func metadataDirectResultBytes(estimate CacheMetadataEstimate) int64 {
	if estimate.ResultCount <= 0 {
		return 0
	}
	sharedGraphBytes := cacheMetadataTermEstimatedBytes*int64(estimate.TermCount) +
		cacheMetadataClassSlotEstimatedBytes*int64(estimate.ClassSlotCount)
	return cacheMetadataResultEstimatedBytes +
		(sharedGraphBytes+int64(estimate.ResultCount)-1)/int64(estimate.ResultCount)
}

// PruneMetadataEstimate removes cold persisted roots when the coarse DAGQL
// structural estimate exceeds maximumBytes. It deliberately skips physical
// usage measurement and returns only aggregate information.
func (c *Cache) PruneMetadataEstimate(ctx context.Context, maximumBytes, targetBytes int64) (report CacheMetadataPruneReport, rerr error) {
	started := time.Now()
	report.MaximumEstimatedBytes = maximumBytes
	report.TargetEstimatedBytes = targetBytes
	defer func() {
		report.Duration = time.Since(started)
		if report.Triggered && c != nil {
			c.traceMetadataPruneFinished(ctx, report, rerr)
			metadataPruneLog(ctx, report, rerr)
		}
	}()

	if c == nil {
		return report, fmt.Errorf("metadata prune: nil cache")
	}
	if maximumBytes <= 0 {
		return report, fmt.Errorf("metadata prune: maximum estimated bytes must be positive")
	}
	if targetBytes <= 0 || targetBytes >= maximumBytes {
		return report, fmt.Errorf("metadata prune: target estimated bytes must be positive and lower than maximum")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	c.egraphMu.Lock()
	report.BeforeCompaction = c.cacheMetadataEstimateLocked()
	report.AfterInitialCompaction = report.BeforeCompaction
	report.AfterPrune = report.BeforeCompaction
	if report.BeforeCompaction.EstimatedBytes <= maximumBytes {
		c.egraphMu.Unlock()
		return report, nil
	}
	report.Triggered = true
	c.traceMetadataPruneStarted(ctx, maximumBytes, targetBytes)
	_, report.InitialCompactionOldClassSlots, report.InitialCompactionNewClassSlots = c.compactEqClassesLocked(true)
	report.AfterInitialCompaction = c.cacheMetadataEstimateLocked()
	report.AfterPrune = report.AfterInitialCompaction
	c.egraphMu.Unlock()

	if report.AfterInitialCompaction.EstimatedBytes <= maximumBytes {
		return report, nil
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	pruneCtx := withMetadataPruneContext(ctx)
	checker := newPruneCancellationChecker(pruneCtx)
	activeRoots, err := c.snapshotSessionResultIDsCancelable(checker)
	if err != nil {
		return report, err
	}
	directResultBytes := metadataDirectResultBytes(report.AfterInitialCompaction)
	snapshot, err := c.snapshotPruneStateCancelable(activeRoots, pruneSnapshotMetadata, directResultBytes, checker)
	if err != nil {
		return report, err
	}
	activeClosure, err := pruneActiveClosureCancelable(snapshot, activeRoots, checker)
	if err != nil {
		return report, err
	}
	candidates, err := c.collectPruneCandidatesCancelable(
		pruneCtx,
		-1,
		snapshot,
		activeClosure,
		CachePrunePolicy{All: true},
		time.Now(),
		checker,
	)
	if err != nil {
		return report, err
	}
	report.CandidateCount = len(candidates)

	reclaimTarget := report.AfterInitialCompaction.EstimatedBytes - targetBytes
	plan, simulatedReclaimed, simulatedCollected, err := buildPrunePlanCancelable(snapshot, candidates, reclaimTarget, checker)
	if err != nil {
		return report, err
	}
	report.PlannedRootCount = len(plan)
	report.SimulatedStructuralBytes = simulatedReclaimed
	report.SimulatedCollectedResultCount = simulatedCollected
	report.CandidatesExhausted = len(plan) == len(candidates) && simulatedReclaimed < reclaimTarget

	for _, planEntry := range plan {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		removed, err := c.removePersistedEdge(pruneCtx, planEntry.candidate.resultID)
		if err != nil {
			return report, err
		}
		if removed {
			report.RemovedPersistedRootCount++
		}
	}

	if len(plan) > 0 {
		c.egraphMu.Lock()
		_, report.FinalCompactionOldClassSlots, report.FinalCompactionNewClassSlots = c.compactEqClassesLocked(true)
		report.AfterPrune = c.cacheMetadataEstimateLocked()
		c.egraphMu.Unlock()
	} else {
		report.AfterPrune = c.MetadataEstimate()
	}

	if report.RemovedPersistedRootCount > 0 && c.snapshotGC != nil {
		report.SnapshotGCAttempted = true
		if err := c.snapshotGC(pruneCtx); err != nil {
			return report, fmt.Errorf("snapshot gc after metadata prune: %w", err)
		}
		report.SnapshotGCSucceeded = true
	}

	return report, nil
}

func metadataPruneLog(ctx context.Context, report CacheMetadataPruneReport, err error) {
	attrs := []any{
		"triggered", report.Triggered,
		"maximumEstimatedBytes", report.MaximumEstimatedBytes,
		"targetEstimatedBytes", report.TargetEstimatedBytes,
		"beforeEstimatedBytes", report.BeforeCompaction.EstimatedBytes,
		"beforeResults", report.BeforeCompaction.ResultCount,
		"beforeTerms", report.BeforeCompaction.TermCount,
		"beforeClassSlots", report.BeforeCompaction.ClassSlotCount,
		"afterInitialCompactionEstimatedBytes", report.AfterInitialCompaction.EstimatedBytes,
		"afterInitialCompactionResults", report.AfterInitialCompaction.ResultCount,
		"afterInitialCompactionTerms", report.AfterInitialCompaction.TermCount,
		"afterInitialCompactionClassSlots", report.AfterInitialCompaction.ClassSlotCount,
		"afterPruneEstimatedBytes", report.AfterPrune.EstimatedBytes,
		"afterPruneResults", report.AfterPrune.ResultCount,
		"afterPruneTerms", report.AfterPrune.TermCount,
		"afterPruneClassSlots", report.AfterPrune.ClassSlotCount,
		"initialCompactionOldClassSlots", report.InitialCompactionOldClassSlots,
		"initialCompactionNewClassSlots", report.InitialCompactionNewClassSlots,
		"finalCompactionOldClassSlots", report.FinalCompactionOldClassSlots,
		"finalCompactionNewClassSlots", report.FinalCompactionNewClassSlots,
		"candidateCount", report.CandidateCount,
		"plannedRootCount", report.PlannedRootCount,
		"simulatedCollectedResultCount", report.SimulatedCollectedResultCount,
		"simulatedStructuralBytes", report.SimulatedStructuralBytes,
		"removedPersistedRootCount", report.RemovedPersistedRootCount,
		"candidatesExhausted", report.CandidatesExhausted,
		"snapshotGCAttempted", report.SnapshotGCAttempted,
		"snapshotGCSucceeded", report.SnapshotGCSucceeded,
		"duration", report.Duration,
	}
	if err != nil {
		attrs = append(attrs, "error", err)
	}
	slog.InfoContext(ctx, "dagql metadata prune finished", attrs...)
}

func (c *Cache) Prune(ctx context.Context, policies []CachePrunePolicy) (CachePruneReport, error) {
	report := CachePruneReport{}
	if len(policies) == 0 {
		return report, nil
	}

	now := time.Now()
	compactedNeeded := false
	for policyIdx, policy := range policies {
		activeRoots := c.snapshotSessionResultIDs()
		c.measureAllResultSizes(ctx)
		snapshot := c.snapshotPruneState(activeRoots, pruneSnapshotDisk, 0)

		targetBytes, _ := pruneTargetBytes(policy, snapshot.usedBytes)
		if targetBytes <= 0 {
			slog.Debug("dagql prune skip policy: no reclaim target",
				"policyIndex", policyIdx,
				"usedBytes", snapshot.usedBytes)
			continue
		}

		activeClosure := pruneActiveClosure(snapshot, activeRoots)
		candidates := c.collectPruneCandidates(ctx, policyIdx, snapshot, activeClosure, policy, now)
		if len(candidates) == 0 {
			continue
		}

		plan, plannedReclaim, _ := buildPrunePlan(snapshot, candidates, targetBytes)
		if len(plan) == 0 {
			continue
		}

		policyReclaimed := int64(0)
		policyApplied := 0
		for _, planEntry := range plan {
			snapRes, ok := snapshot.results[planEntry.candidate.resultID]
			if ok {
				c.tracePruneCandidateSelected(ctx, policyIdx, planEntry.candidate, snapRes, planEntry.reclaimBytes)
			}
			removed, err := c.removePersistedEdge(ctx, planEntry.candidate.resultID)
			if err != nil {
				return report, err
			}
			if !removed {
				continue
			}
			if ok {
				c.tracePrunePersistedEdgeRemoved(ctx, policyIdx, planEntry.candidate, snapRes, planEntry.reclaimBytes)
			}
			compactedNeeded = true
			policyApplied++
			if ok {
				digest := ""
				args := []string(nil)
				if snapRes.callFrame != nil {
					callFrame := snapRes.callFrame.clone()
					if dig, err := callFrame.deriveRecipeDigest(c); err == nil {
						digest = dig.String()
					}
					args = pruneLogCallArgs(callFrame.Args)
				}
				slog.Info("dagql pruned result",
					"resultID", planEntry.candidate.resultID,
					"call", snapRes.callLabel,
					"digest", digest,
					"args", args,
					"description", snapRes.entry.Description,
					"measuredSizeBytes", snapRes.entry.SizeBytes,
					"reclaimedBytes", planEntry.reclaimBytes)
			}
			selectedEntry := planEntry.candidate.entry
			selectedEntry.SizeBytes = planEntry.reclaimBytes
			report.Entries = append(report.Entries, selectedEntry)
			report.ReclaimedBytes += planEntry.reclaimBytes
			policyReclaimed += planEntry.reclaimBytes
		}

		if policyApplied > 0 {
			slog.Debug("dagql prune applied plan",
				"policyIndex", policyIdx,
				"plannedCandidates", len(plan),
				"appliedCandidates", policyApplied,
				"plannedReclaimedBytes", plannedReclaim,
				"appliedReclaimedBytes", policyReclaimed,
				"policyTargetBytes", targetBytes)
		}
	}

	if compactedNeeded {
		c.egraphMu.Lock()
		if compacted, oldSlots, newSlots := c.compactEqClassesLocked(false); compacted {
			slog.Debug("dagql prune compacted eq classes",
				"oldSlots", oldSlots,
				"newSlots", newSlots)
		}
		c.egraphMu.Unlock()
	}

	if len(report.Entries) > 0 && c.snapshotGC != nil {
		if err := c.snapshotGC(ctx); err != nil {
			return report, fmt.Errorf("snapshot gc after prune: %w", err)
		}
	}

	return report, nil
}

func (c *Cache) snapshotPruneState(activeRoots map[sharedResultID]struct{}, mode pruneSnapshotMode, directResultBytes int64) pruneSnapshot {
	snapshot, _ := c.snapshotPruneStateCancelable(activeRoots, mode, directResultBytes, nil)
	return snapshot
}

func (c *Cache) snapshotPruneStateCancelable(
	activeRoots map[sharedResultID]struct{},
	mode pruneSnapshotMode,
	directResultBytes int64,
	checker *pruneCancellationChecker,
) (pruneSnapshot, error) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	snapshot := pruneSnapshot{
		results:         make(map[sharedResultID]pruneSnapshotResult, len(c.resultsByID)),
		usageIdentities: make(map[string]pruneUsageIdentityState),
	}
	if err := checker.checkNow(); err != nil {
		return pruneSnapshot{}, err
	}
	if len(c.resultsByID) == 0 {
		return snapshot, nil
	}

	if mode == pruneSnapshotDisk {
		if err := c.snapshotPruneDiskUsageIdentitiesLocked(&snapshot, checker); err != nil {
			return pruneSnapshot{}, err
		}
	}

	for resID, res := range c.resultsByID {
		if checker != nil {
			if err := checker.check(); err != nil {
				return pruneSnapshot{}, err
			}
		}
		if res == nil {
			continue
		}
		state := res.loadPayloadState()
		createdAt := state.createdAtUnixNano
		lastUsedAt := state.lastUsedAtUnixNano
		if createdAt == 0 {
			createdAt = lastUsedAt
		}
		if lastUsedAt == 0 {
			lastUsedAt = createdAt
		}
		_, activelyUsed := activeRoots[resID]

		deps := make([]sharedResultID, 0, len(res.deps))
		for depID := range res.deps {
			if checker != nil {
				if err := checker.check(); err != nil {
					return pruneSnapshot{}, err
				}
			}
			deps = append(deps, depID)
		}
		if len(deps) > 1 {
			if err := sortPruneResultIDsCancelable(deps, checker); err != nil {
				return pruneSnapshot{}, err
			}
		}

		edge, hasPersistedEdge := c.persistedEdgesByResult[resID]
		snapshotResult := pruneSnapshotResult{
			resultID:      resID,
			incomingCount: res.incomingOwnershipCount,
			deps:          deps,
			entry: CacheUsageEntry{
				CreatedTimeUnixNano:       createdAt,
				MostRecentUseTimeUnixNano: lastUsedAt,
				ActivelyUsed:              activelyUsed,
			},
			hasPersistedEdge:         hasPersistedEdge,
			persistedEdgeUnpruneable: edge.unpruneable,
			expiresAtUnix:            edge.expiresAtUnix,
		}
		if mode == pruneSnapshotMetadata {
			snapshotResult.directResultBytes = directResultBytes
			snapshotResult.entry.SizeBytes = directResultBytes
		} else {
			usageIdentities := cacheUsageIdentities(res)
			sizeBytes := int64(0)
			for _, measured := range res.cacheUsageSizeByIdentity {
				if checker != nil {
					if err := checker.check(); err != nil {
						return pruneSnapshot{}, err
					}
				}
				if measured > 0 {
					sizeBytes += measured
				}
			}
			recordTypes := cacheUsageRecordTypesFromMap(res.cacheUsageRecordTypeByID)
			recordType := cacheUsagePrimaryRecordType(recordTypes, res.recordType)
			description := res.description
			if description == "" {
				description = fmt.Sprintf("dagql cache result %d", resID)
			}
			callFrame := res.loadResultCall()
			callLabel := c.cacheUsageDagqlCallLocked(res)
			snapshotResult.usageIdentities = usageIdentities
			snapshotResult.entry.ID = fmt.Sprintf("dagql.result.%d", resID)
			snapshotResult.entry.Description = description
			snapshotResult.entry.RecordType = recordType
			snapshotResult.entry.RecordTypes = recordTypes
			snapshotResult.entry.DagqlCall = callLabel
			snapshotResult.entry.SizeBytes = sizeBytes
			snapshotResult.callLabel = callLabel
			snapshotResult.callFrame = callFrame
			snapshot.usedBytes += sizeBytes
		}
		snapshot.results[resID] = snapshotResult
	}

	if err := checker.checkNow(); err != nil {
		return pruneSnapshot{}, err
	}
	return snapshot, nil
}

func (c *Cache) snapshotPruneDiskUsageIdentitiesLocked(snapshot *pruneSnapshot, checker *pruneCancellationChecker) error {
	for resID, res := range c.resultsByID {
		if checker != nil {
			if err := checker.check(); err != nil {
				return err
			}
		}
		if res == nil {
			continue
		}
		for _, usageIdentity := range cacheUsageIdentities(res) {
			if checker != nil {
				if err := checker.check(); err != nil {
					return err
				}
			}
			identityState := snapshot.usageIdentities[usageIdentity]
			if identityState.ownerID == 0 || resID < identityState.ownerID {
				identityState.ownerID = resID
			}
			if sizeBytes, ok := res.cacheUsageSizeByIdentity[usageIdentity]; ok && sizeBytes > identityState.sizeBytes {
				identityState.sizeBytes = sizeBytes
			}
			identityState.aliveMembers++
			snapshot.usageIdentities[usageIdentity] = identityState
		}
	}
	return nil
}

func pruneActiveClosure(snapshot pruneSnapshot, activeRoots map[sharedResultID]struct{}) map[sharedResultID]struct{} {
	closure, _ := pruneActiveClosureCancelable(snapshot, activeRoots, nil)
	return closure
}

func pruneActiveClosureCancelable(
	snapshot pruneSnapshot,
	activeRoots map[sharedResultID]struct{},
	checker *pruneCancellationChecker,
) (map[sharedResultID]struct{}, error) {
	if err := checker.checkNow(); err != nil {
		return nil, err
	}
	if len(activeRoots) == 0 {
		return nil, nil
	}
	closure := make(map[sharedResultID]struct{}, len(activeRoots))
	stack := make([]sharedResultID, 0, len(activeRoots))
	for resultID := range activeRoots {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, err
			}
		}
		stack = append(stack, resultID)
	}

	for len(stack) > 0 {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, err
			}
		}
		n := len(stack) - 1
		curID := stack[n]
		stack = stack[:n]
		if _, seen := closure[curID]; seen {
			continue
		}
		closure[curID] = struct{}{}
		cur, ok := snapshot.results[curID]
		if !ok {
			continue
		}
		for _, depID := range cur.deps {
			if checker != nil {
				if err := checker.check(); err != nil {
					return nil, err
				}
			}
			stack = append(stack, depID)
		}
	}

	if err := checker.checkNow(); err != nil {
		return nil, err
	}
	return closure, nil
}

func (c *Cache) collectPruneCandidates(ctx context.Context, policyIndex int, snapshot pruneSnapshot, activeClosure map[sharedResultID]struct{}, policy CachePrunePolicy, now time.Time) []pruneCandidate {
	candidates, _ := c.collectPruneCandidatesCancelable(ctx, policyIndex, snapshot, activeClosure, policy, now, nil)
	return candidates
}

func (c *Cache) collectPruneCandidatesCancelable(
	ctx context.Context,
	policyIndex int,
	snapshot pruneSnapshot,
	activeClosure map[sharedResultID]struct{},
	policy CachePrunePolicy,
	now time.Time,
	checker *pruneCancellationChecker,
) ([]pruneCandidate, error) {
	if err := checker.checkNow(); err != nil {
		return nil, err
	}
	cutoffUnixNano := int64(0)
	if policy.KeepDuration > 0 {
		cutoffUnixNano = now.Add(-policy.KeepDuration).UnixNano()
	}

	candidates := make([]pruneCandidate, 0, len(snapshot.results))
	for resultID, res := range snapshot.results {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, err
			}
		}
		if !res.hasPersistedEdge {
			c.tracePruneCandidateSkipped(ctx, policyIndex, "no_persisted_edge", res)
			continue
		}
		if res.persistedEdgeUnpruneable {
			c.tracePruneCandidateSkipped(ctx, policyIndex, "unpruneable", res)
			continue
		}
		switch {
		case resultInActiveClosure(activeClosure, resultID):
			c.tracePruneCandidateSkipped(ctx, policyIndex, "active_closure", res)
			continue
		case cutoffUnixNano > 0 && entryRecentlyUsed(res.entry, cutoffUnixNano) && !persistedEdgeExpired(now, persistedEdge{expiresAtUnix: res.expiresAtUnix}):
			c.tracePruneCandidateSkipped(ctx, policyIndex, "recently_used_and_not_expired", res)
			continue
		case !cachePrunePolicyMatchesEntry(policy, res.entry):
			c.tracePruneCandidateSkipped(ctx, policyIndex, "policy_filter", res)
			continue
		}
		candidates = append(candidates, pruneCandidate{
			resultID:      resultID,
			entry:         res.entry,
			expiresAtUnix: res.expiresAtUnix,
		})
	}

	if err := sortPruneCandidatesCancelable(candidates, now, checker); err != nil {
		return nil, err
	}

	return candidates, nil
}

type pruneSortCancellation struct {
	err error
}

func sortPruneResultIDsCancelable(resultIDs []sharedResultID, checker *pruneCancellationChecker) error {
	if checker == nil {
		slices.Sort(resultIDs)
		return nil
	}
	return sortPruneSliceCancelable(resultIDs, checker, func(a, b sharedResultID) int {
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		default:
			return 0
		}
	})
}

func sortPruneCandidatesCancelable(candidates []pruneCandidate, now time.Time, checker *pruneCancellationChecker) error {
	return sortPruneSliceCancelable(candidates, checker, func(a, b pruneCandidate) int {
		return comparePruneCandidates(a, b, now)
	})
}

func sortPruneSliceCancelable[S ~[]E, E any](items S, checker *pruneCancellationChecker, compare func(E, E) int) (rerr error) {
	if checker == nil {
		slices.SortFunc(items, compare)
		return nil
	}

	// slices.SortFunc cannot return an error from its comparator. Use a private
	// sentinel to stop it immediately on cancellation, while preserving every
	// unrelated panic. The partially ordered slice is discarded by the caller.
	defer func() {
		if recovered := recover(); recovered != nil {
			canceled, ok := recovered.(pruneSortCancellation)
			if !ok {
				panic(recovered)
			}
			rerr = canceled.err
		}
	}()

	slices.SortFunc(items, func(a, b E) int {
		if err := checker.check(); err != nil {
			panic(pruneSortCancellation{err: err})
		}
		return compare(a, b)
	})
	return checker.checkNow()
}

func comparePruneCandidates(a, b pruneCandidate, now time.Time) int {
	if aExpired, bExpired := persistedEdgeExpired(now, persistedEdge{expiresAtUnix: a.expiresAtUnix}), persistedEdgeExpired(now, persistedEdge{expiresAtUnix: b.expiresAtUnix}); aExpired != bExpired {
		if aExpired {
			return -1
		}
		return 1
	}
	if a.entry.MostRecentUseTimeUnixNano != b.entry.MostRecentUseTimeUnixNano {
		if a.entry.MostRecentUseTimeUnixNano < b.entry.MostRecentUseTimeUnixNano {
			return -1
		}
		return 1
	}
	if a.entry.CreatedTimeUnixNano != b.entry.CreatedTimeUnixNano {
		if a.entry.CreatedTimeUnixNano < b.entry.CreatedTimeUnixNano {
			return -1
		}
		return 1
	}
	if a.entry.SizeBytes != b.entry.SizeBytes {
		if a.entry.SizeBytes > b.entry.SizeBytes {
			return -1
		}
		return 1
	}
	switch {
	case a.entry.ID < b.entry.ID:
		return -1
	case a.entry.ID > b.entry.ID:
		return 1
	case a.resultID < b.resultID:
		return -1
	case a.resultID > b.resultID:
		return 1
	default:
		return 0
	}
}

func buildPrunePlan(snapshot pruneSnapshot, candidates []pruneCandidate, targetBytes int64) ([]prunePlanEntry, int64, int) {
	plan, reclaimed, collected, _ := buildPrunePlanCancelable(snapshot, candidates, targetBytes, nil)
	return plan, reclaimed, collected
}

func buildPrunePlanCancelable(
	snapshot pruneSnapshot,
	candidates []pruneCandidate,
	targetBytes int64,
	checker *pruneCancellationChecker,
) ([]prunePlanEntry, int64, int, error) {
	if err := checker.checkNow(); err != nil {
		return nil, 0, 0, err
	}
	if len(candidates) == 0 {
		return nil, 0, 0, nil
	}
	sim, err := newPruneSimulationStateCancelable(snapshot, checker)
	if err != nil {
		return nil, 0, 0, err
	}
	plan := make([]prunePlanEntry, 0, len(candidates))
	var reclaimed int64
	var collected int
	for _, candidate := range candidates {
		if checker != nil {
			if err := checker.check(); err != nil {
				return nil, 0, 0, err
			}
		}
		immediateReclaim, immediateCollected, err := sim.applyCandidateCancelable(snapshot, candidate.resultID, checker)
		if err != nil {
			return nil, 0, 0, err
		}
		plan = append(plan, prunePlanEntry{
			candidate:    candidate,
			reclaimBytes: immediateReclaim,
		})
		reclaimed += immediateReclaim
		collected += immediateCollected
		if reclaimed >= targetBytes {
			break
		}
	}
	if err := checker.checkNow(); err != nil {
		return nil, 0, 0, err
	}
	return plan, reclaimed, collected, nil
}

func newPruneSimulationStateCancelable(snapshot pruneSnapshot, checker *pruneCancellationChecker) (pruneSimulationState, error) {
	state := pruneSimulationState{
		remainingIncomingCount:    make(map[sharedResultID]int64, len(snapshot.results)),
		aliveCountByUsageIdentity: make(map[string]int, len(snapshot.usageIdentities)),
		sizeBytesByUsageIdentity:  make(map[string]int64, len(snapshot.usageIdentities)),
		collected:                 make(map[sharedResultID]struct{}),
	}
	for resultID, res := range snapshot.results {
		if checker != nil {
			if err := checker.check(); err != nil {
				return pruneSimulationState{}, err
			}
		}
		state.remainingIncomingCount[resultID] = res.incomingCount
	}
	for identity, identityState := range snapshot.usageIdentities {
		if checker != nil {
			if err := checker.check(); err != nil {
				return pruneSimulationState{}, err
			}
		}
		state.aliveCountByUsageIdentity[identity] = identityState.aliveMembers
		state.sizeBytesByUsageIdentity[identity] = identityState.sizeBytes
	}
	return state, nil
}

func (s *pruneSimulationState) applyCandidateCancelable(
	snapshot pruneSnapshot,
	resultID sharedResultID,
	checker *pruneCancellationChecker,
) (int64, int, error) {
	if checker != nil {
		if err := checker.check(); err != nil {
			return 0, 0, err
		}
	}
	curCount, ok := s.remainingIncomingCount[resultID]
	if !ok {
		return 0, 0, nil
	}
	s.remainingIncomingCount[resultID] = curCount - 1

	queue := make([]sharedResultID, 0, 1)
	if curCount-1 == 0 {
		queue = append(queue, resultID)
	}

	var reclaimed int64
	var collected int
	for len(queue) > 0 {
		if checker != nil {
			if err := checker.check(); err != nil {
				return 0, 0, err
			}
		}
		curID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if _, seen := s.collected[curID]; seen {
			continue
		}
		if s.remainingIncomingCount[curID] != 0 {
			continue
		}
		s.collected[curID] = struct{}{}
		collected++

		cur, ok := snapshot.results[curID]
		if !ok {
			continue
		}
		reclaimed += cur.directResultBytes
		for _, identity := range cur.usageIdentities {
			if checker != nil {
				if err := checker.check(); err != nil {
					return 0, 0, err
				}
			}
			alive := s.aliveCountByUsageIdentity[identity] - 1
			s.aliveCountByUsageIdentity[identity] = alive
			if alive == 0 {
				reclaimed += s.sizeBytesByUsageIdentity[identity]
			}
		}

		for _, depID := range cur.deps {
			if checker != nil {
				if err := checker.check(); err != nil {
					return 0, 0, err
				}
			}
			depCount, ok := s.remainingIncomingCount[depID]
			if !ok {
				continue
			}
			depCount--
			s.remainingIncomingCount[depID] = depCount
			if depCount == 0 {
				queue = append(queue, depID)
			}
		}
	}

	return reclaimed, collected, nil
}

func resultInActiveClosure(activeClosure map[sharedResultID]struct{}, resultID sharedResultID) bool {
	if len(activeClosure) == 0 {
		return false
	}
	_, blocked := activeClosure[resultID]
	return blocked
}

func persistedEdgeExpired(now time.Time, edge persistedEdge) bool {
	return edge.expiresAtUnix > 0 && now.Unix() >= edge.expiresAtUnix
}

func entryRecentlyUsed(entry CacheUsageEntry, cutoffUnixNano int64) bool {
	mostRecentUse := entry.MostRecentUseTimeUnixNano
	if mostRecentUse == 0 {
		mostRecentUse = entry.CreatedTimeUnixNano
	}
	return mostRecentUse >= cutoffUnixNano
}

func pruneLogCallArgs(args []*ResultCallArg) []string {
	if len(args) == 0 {
		return nil
	}
	formatted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == nil {
			continue
		}
		formatted = append(formatted, arg.Name+"="+pruneLogLiteralValue(arg, arg.Value, 3))
	}
	if len(formatted) == 0 {
		return nil
	}
	return formatted
}

func pruneLogLiteralValue(arg *ResultCallArg, lit *ResultCallLiteral, depth int) string {
	if arg != nil && arg.IsSensitive {
		return "<sensitive>"
	}
	if depth <= 0 {
		return "<max-depth>"
	}
	if lit == nil {
		return "null"
	}
	switch lit.Kind {
	case ResultCallLiteralKindNull:
		return "null"
	case ResultCallLiteralKindBool:
		if lit.BoolValue {
			return "true"
		}
		return "false"
	case ResultCallLiteralKindInt:
		return strconv.FormatInt(lit.IntValue, 10)
	case ResultCallLiteralKindFloat:
		return strconv.FormatFloat(lit.FloatValue, 'g', -1, 64)
	case ResultCallLiteralKindString:
		return strconv.Quote(pruneLogTruncateString(lit.StringValue, 120))
	case ResultCallLiteralKindEnum:
		return lit.EnumValue
	case ResultCallLiteralKindDigestedString:
		if lit.DigestedStringDigest == "" {
			return "digest:<missing>"
		}
		return "digest:" + lit.DigestedStringDigest.String()
	case ResultCallLiteralKindResultRef:
		switch {
		case lit.ResultRef == nil:
			return "result:<missing>"
		case lit.ResultRef.ResultID != 0:
			return "result:" + strconv.FormatUint(lit.ResultRef.ResultID, 10)
		case lit.ResultRef.Call != nil:
			if field, err := resultCallIdentityField(lit.ResultRef.Call); err == nil && field != "" {
				return "inline:" + field
			}
			return "inline_call"
		default:
			return "result:<missing>"
		}
	case ResultCallLiteralKindList:
		limit := len(lit.ListItems)
		if limit > 5 {
			limit = 5
		}
		items := make([]string, 0, limit+1)
		for i := 0; i < limit; i++ {
			items = append(items, pruneLogLiteralValue(nil, lit.ListItems[i], depth-1))
		}
		if len(lit.ListItems) > limit {
			items = append(items, fmt.Sprintf("...(+%d)", len(lit.ListItems)-limit))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case ResultCallLiteralKindObject:
		limit := len(lit.ObjectFields)
		if limit > 5 {
			limit = 5
		}
		fields := make([]string, 0, limit+1)
		for i := 0; i < limit; i++ {
			field := lit.ObjectFields[i]
			if field == nil {
				continue
			}
			fields = append(fields, field.Name+":"+pruneLogLiteralValue(field, field.Value, depth-1))
		}
		if len(lit.ObjectFields) > limit {
			fields = append(fields, fmt.Sprintf("...(+%d)", len(lit.ObjectFields)-limit))
		}
		return "{" + strings.Join(fields, ", ") + "}"
	default:
		return "<unknown>"
	}
}

func pruneLogTruncateString(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func pruneTargetBytes(policy CachePrunePolicy, usedBytes int64) (int64, bool) {
	target := int64(0)
	thresholdTriggered := false
	thresholdConfigured := policy.MaxUsedSpace > 0 ||
		policy.ReservedSpace > 0 ||
		policy.MinFreeSpace > 0 ||
		policy.TargetSpace > 0

	keepTargetBytes := int64(0)
	hasKeepTarget := false
	addKeepTarget := func(keepBytes int64) {
		if keepBytes < 0 {
			keepBytes = 0
		}
		if !hasKeepTarget || keepBytes > keepTargetBytes {
			keepTargetBytes = keepBytes
		}
		hasKeepTarget = true
	}
	if policy.MaxUsedSpace > 0 && usedBytes > policy.MaxUsedSpace {
		thresholdTriggered = true
		addKeepTarget(max(policy.MaxUsedSpace, policy.ReservedSpace))
	}
	if policy.MinFreeSpace > 0 && policy.CurrentFreeSpace < policy.MinFreeSpace {
		thresholdTriggered = true
		target = max(target, policy.MinFreeSpace-policy.CurrentFreeSpace)
	}
	if hasKeepTarget && usedBytes > keepTargetBytes {
		target = max(target, usedBytes-keepTargetBytes)
	}
	if thresholdTriggered && policy.TargetSpace > 0 && usedBytes > policy.TargetSpace {
		target = max(target, usedBytes-max(policy.TargetSpace, policy.ReservedSpace))
	}
	if !thresholdTriggered && !thresholdConfigured && (policy.All || len(policy.Filters) > 0) {
		return math.MaxInt64, false
	}

	return target, thresholdTriggered
}

func cachePrunePolicyMatchesEntry(policy CachePrunePolicy, entry CacheUsageEntry) bool {
	if policy.All {
		return true
	}
	if len(policy.Filters) == 0 {
		return true
	}
	for _, filter := range policy.Filters {
		if !cachePruneFilterMatchesEntry(filter, entry) {
			return false
		}
	}
	return true
}

func cachePruneFilterMatchesEntry(filter string, entry CacheUsageEntry) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return false
	}

	if strings.Contains(filter, ",") {
		clauses := strings.Split(filter, ",")
		recordTypeFilter := true
		recordTypeMatch := false
		for _, clause := range clauses {
			key, value, ok := strings.Cut(clause, "==")
			if !ok {
				recordTypeFilter = false
				break
			}
			key = strings.TrimSpace(strings.ToLower(key))
			if key != "type" && key != "recordtype" {
				recordTypeFilter = false
				break
			}
			if cacheUsageEntryRecordTypeMatches(entry, strings.TrimSpace(value)) {
				recordTypeMatch = true
			}
		}
		if recordTypeFilter {
			return recordTypeMatch
		}
	}

	key, value, ok := strings.Cut(filter, "==")
	if !ok {
		return false
	}
	key = strings.TrimSpace(strings.ToLower(key))
	value = strings.TrimSpace(value)
	switch key {
	case "id":
		return entry.ID == value
	case "type", "recordtype":
		return cacheUsageEntryRecordTypeMatches(entry, value)
	case "description":
		return entry.Description == value
	case "inuse":
		want, err := strconv.ParseBool(value)
		return err == nil && entry.ActivelyUsed == want
	default:
		return false
	}
}

func cacheUsageEntryRecordTypeMatches(entry CacheUsageEntry, value string) bool {
	if value == "" {
		return false
	}
	for _, recordType := range entry.RecordTypes {
		if recordType == value {
			return true
		}
	}
	return entry.RecordType == value
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
