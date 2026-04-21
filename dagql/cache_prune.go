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
		snapshot := c.snapshotPruneState(activeRoots)

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

		plan, plannedReclaim := buildPrunePlan(snapshot, candidates, targetBytes)
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
		if compacted, oldSlots, newSlots := c.compactEqClassesLocked(); compacted {
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

func (c *Cache) snapshotPruneState(activeRoots map[sharedResultID]struct{}) pruneSnapshot {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	snapshot := pruneSnapshot{
		results:         make(map[sharedResultID]pruneSnapshotResult, len(c.resultsByID)),
		usageIdentities: make(map[string]pruneUsageIdentityState),
	}
	if len(c.resultsByID) == 0 {
		return snapshot
	}

	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		for _, usageIdentity := range cacheUsageIdentities(res) {
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

	for resID, res := range c.resultsByID {
		if res == nil {
			continue
		}
		usageIdentities := cacheUsageIdentities(res)
		sizeBytes := int64(0)
		for _, measured := range res.cacheUsageSizeByIdentity {
			if measured > 0 {
				sizeBytes += measured
			}
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
		recordType := res.recordType
		if recordType == "" {
			recordType = "dagql.unknown"
		}
		description := res.description
		if description == "" {
			description = fmt.Sprintf("dagql cache result %d", resID)
		}
		_, activelyUsed := activeRoots[resID]

		deps := make([]sharedResultID, 0, len(res.deps))
		for depID := range res.deps {
			deps = append(deps, depID)
		}
		slices.Sort(deps)

		callFrame := res.loadResultCall()
		fieldName := ""
		receiverTypeName := ""
		callLabel := ""
		if callFrame != nil {
			frame := callFrame
			if identityField, err := resultCallIdentityField(frame); err == nil {
				fieldName = identityField
			}
			if frame.Receiver != nil {
				if receiverRes := c.resultsByID[sharedResultID(frame.Receiver.ResultID)]; receiverRes != nil {
					receiverFrame := receiverRes.loadResultCall()
					switch {
					case receiverFrame != nil && receiverFrame.Type != nil && receiverFrame.Type.NamedType != "":
						receiverTypeName = receiverFrame.Type.NamedType
					default:
						receiverState := receiverRes.loadPayloadState()
						receiverTypeName = sharedResultObjectTypeName(receiverRes, receiverState)
					}
				}
			}
			switch {
			case receiverTypeName != "" && fieldName != "":
				callLabel = receiverTypeName + "." + fieldName
			case fieldName != "":
				if frame.Kind == ResultCallKindField {
					callLabel = "Query." + fieldName
				} else {
					callLabel = fieldName
				}
			}
		}

		edge, hasPersistedEdge := c.persistedEdgesByResult[resID]
		snapshot.results[resID] = pruneSnapshotResult{
			resultID:        resID,
			incomingCount:   res.incomingOwnershipCount,
			deps:            deps,
			usageIdentities: usageIdentities,
			entry: CacheUsageEntry{
				ID:                        fmt.Sprintf("dagql.result.%d", resID),
				Description:               description,
				RecordType:                recordType,
				SizeBytes:                 sizeBytes,
				CreatedTimeUnixNano:       createdAt,
				MostRecentUseTimeUnixNano: lastUsedAt,
				ActivelyUsed:              activelyUsed,
			},
			callLabel:                callLabel,
			callFrame:                callFrame,
			hasPersistedEdge:         hasPersistedEdge,
			persistedEdgeUnpruneable: edge.unpruneable,
			expiresAtUnix:            edge.expiresAtUnix,
		}
		snapshot.usedBytes += sizeBytes
	}

	return snapshot
}

func pruneActiveClosure(snapshot pruneSnapshot, activeRoots map[sharedResultID]struct{}) map[sharedResultID]struct{} {
	if len(activeRoots) == 0 {
		return nil
	}
	closure := make(map[sharedResultID]struct{}, len(activeRoots))
	stack := make([]sharedResultID, 0, len(activeRoots))
	for resultID := range activeRoots {
		stack = append(stack, resultID)
	}

	for len(stack) > 0 {
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
		stack = append(stack, cur.deps...)
	}

	return closure
}

func (c *Cache) collectPruneCandidates(ctx context.Context, policyIndex int, snapshot pruneSnapshot, activeClosure map[sharedResultID]struct{}, policy CachePrunePolicy, now time.Time) []pruneCandidate {
	cutoffUnixNano := int64(0)
	if policy.KeepDuration > 0 {
		cutoffUnixNano = now.Add(-policy.KeepDuration).UnixNano()
	}

	candidates := make([]pruneCandidate, 0, len(snapshot.results))
	for resultID, res := range snapshot.results {
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

	slices.SortFunc(candidates, func(a, b pruneCandidate) int {
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
		default:
			return 0
		}
	})

	return candidates
}

func buildPrunePlan(snapshot pruneSnapshot, candidates []pruneCandidate, targetBytes int64) ([]prunePlanEntry, int64) {
	if len(candidates) == 0 {
		return nil, 0
	}
	sim := newPruneSimulationState(snapshot)
	plan := make([]prunePlanEntry, 0, len(candidates))
	var reclaimed int64
	for _, candidate := range candidates {
		immediateReclaim := sim.applyCandidate(snapshot, candidate.resultID)
		plan = append(plan, prunePlanEntry{
			candidate:    candidate,
			reclaimBytes: immediateReclaim,
		})
		reclaimed += immediateReclaim
		if reclaimed >= targetBytes {
			break
		}
	}
	return plan, reclaimed
}

func newPruneSimulationState(snapshot pruneSnapshot) pruneSimulationState {
	state := pruneSimulationState{
		remainingIncomingCount:    make(map[sharedResultID]int64, len(snapshot.results)),
		aliveCountByUsageIdentity: make(map[string]int, len(snapshot.usageIdentities)),
		sizeBytesByUsageIdentity:  make(map[string]int64, len(snapshot.usageIdentities)),
		collected:                 make(map[sharedResultID]struct{}),
	}
	for resultID, res := range snapshot.results {
		state.remainingIncomingCount[resultID] = res.incomingCount
	}
	for identity, identityState := range snapshot.usageIdentities {
		state.aliveCountByUsageIdentity[identity] = identityState.aliveMembers
		state.sizeBytesByUsageIdentity[identity] = identityState.sizeBytes
	}
	return state
}

func (s *pruneSimulationState) applyCandidate(snapshot pruneSnapshot, resultID sharedResultID) int64 {
	curCount, ok := s.remainingIncomingCount[resultID]
	if !ok {
		return 0
	}
	s.remainingIncomingCount[resultID] = curCount - 1

	queue := make([]sharedResultID, 0, 1)
	if curCount-1 == 0 {
		queue = append(queue, resultID)
	}

	var reclaimed int64
	for len(queue) > 0 {
		curID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if _, seen := s.collected[curID]; seen {
			continue
		}
		if s.remainingIncomingCount[curID] != 0 {
			continue
		}
		s.collected[curID] = struct{}{}

		cur, ok := snapshot.results[curID]
		if !ok {
			continue
		}
		for _, identity := range cur.usageIdentities {
			alive := s.aliveCountByUsageIdentity[identity] - 1
			s.aliveCountByUsageIdentity[identity] = alive
			if alive == 0 {
				reclaimed += s.sizeBytesByUsageIdentity[identity]
			}
		}

		for _, depID := range cur.deps {
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

	return reclaimed
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
		key, value, ok := strings.Cut(filter, "==")
		if !ok {
			return false
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)
		switch key {
		case "id":
			if entry.ID != value {
				return false
			}
		case "type", "recordtype":
			if entry.RecordType != value {
				return false
			}
		case "description":
			if entry.Description != value {
				return false
			}
		case "inuse":
			want, err := strconv.ParseBool(value)
			if err != nil || entry.ActivelyUsed != want {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
