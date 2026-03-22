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

func (c *cache) Prune(ctx context.Context, policies []CachePrunePolicy) (CachePruneReport, error) {
	report := CachePruneReport{}
	if len(policies) == 0 {
		return report, nil
	}
	now := time.Now()
	for policyIdx, policy := range policies {
		reclaimed := int64(0)
		targetBytes := int64(-1)
		initialUsedBytes := int64(0)
		for reclaimed < math.MaxInt64 {
			c.egraphMu.Lock()
			c.measureAllResultSizesLocked(ctx)
			entries := c.usageEntriesLocked()
			entryByResultID := make(map[sharedResultID]CacheUsageEntry, len(entries))
			usageOwnerByIdentity := make(map[string]sharedResultID, len(c.resultsByID))
			resultIDsByUsageIdentity := make(map[string][]sharedResultID, len(c.resultsByID))
			var usedBytes int64
			for _, ent := range entries {
				usedBytes += ent.SizeBytes
				resultID, ok := cacheUsageEntryResultID(ent.ID)
				if ok {
					entryByResultID[resultID] = ent
				}
			}
			for resultID, res := range c.resultsByID {
				if res == nil {
					continue
				}
				usageIdentity := res.usageIdentity
				if usageIdentity == "" {
					usageIdentity = fmt.Sprintf("dagql.result.%d", resultID)
				}
				resultIDsByUsageIdentity[usageIdentity] = append(resultIDsByUsageIdentity[usageIdentity], resultID)
				if ownerID := usageOwnerByIdentity[usageIdentity]; ownerID == 0 || resultID < ownerID {
					usageOwnerByIdentity[usageIdentity] = resultID
				}
			}

			if targetBytes < 0 {
				targetBytes, _ = pruneTargetBytes(policy, usedBytes)
				initialUsedBytes = usedBytes
			}
			if targetBytes <= 0 {
				c.egraphMu.Unlock()
				slog.Debug("dagql prune skip policy: no reclaim target",
					"policyIndex", policyIdx,
					"usedBytes", initialUsedBytes)
				break
			}
			if reclaimed >= targetBytes {
				c.egraphMu.Unlock()
				break
			}

			cutoffUnixNano := int64(0)
			if policy.KeepDuration > 0 {
				cutoffUnixNano = now.Add(-policy.KeepDuration).UnixNano()
			}

			activeClosure := c.sessionDependencyClosureLocked()
			candidates := make([]pruneCandidate, 0, len(c.persistedEdgesByResult))
			for resultID, edge := range c.persistedEdgesByResult {
				res := c.resultsByID[resultID]
				if res == nil {
					continue
				}
				entry, ok := entryByResultID[resultID]
				if !ok {
					continue
				}
				switch {
				case resultInActiveClosure(activeClosure, resultID):
					continue
				case cutoffUnixNano > 0 && entryRecentlyUsed(entry, cutoffUnixNano) && !persistedEdgeExpired(now, edge):
					continue
				case !cachePrunePolicyMatchesEntry(policy, entry):
					continue
				}
				candidates = append(candidates, pruneCandidate{
					resultID:      resultID,
					entry:         entry,
					expiresAtUnix: edge.expiresAtUnix,
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
				switch {
				case a.entry.ID < b.entry.ID:
					return -1
				case a.entry.ID > b.entry.ID:
					return 1
				default:
					return 0
				}
			})

			var (
				bestCandidate pruneCandidate
				bestReclaim   int64
				haveBest      bool
			)
			for _, candidate := range candidates {
				marginalReclaim := c.simulatePersistedEdgeRemovalLocked(candidate.resultID, usageOwnerByIdentity, resultIDsByUsageIdentity)
				if marginalReclaim == 0 {
					continue
				}
				if !haveBest || marginalReclaim > bestReclaim {
					bestCandidate = candidate
					bestReclaim = marginalReclaim
					haveBest = true
				}
			}
			c.egraphMu.Unlock()

			if !haveBest {
				break
			}

			err := c.removePersistedEdge(ctx, bestCandidate.resultID)
			if err != nil {
				return report, err
			}

			selectedEntry := bestCandidate.entry
			selectedEntry.SizeBytes = bestReclaim
			report.Entries = append(report.Entries, selectedEntry)
			report.ReclaimedBytes += bestReclaim
			reclaimed += bestReclaim

			slog.Debug("dagql prune selected candidate",
				"policyIndex", policyIdx,
				"entryID", bestCandidate.entry.ID,
				"reclaimedBytes", bestReclaim,
				"policyTargetBytes", targetBytes,
				"policyReclaimedBytes", reclaimed)
		}
	}
	c.egraphMu.Lock()
	if len(report.Entries) > 0 {
		if compacted, oldSlots, newSlots := c.compactEqClassesLocked(); compacted {
			slog.Debug("dagql prune compacted eq classes",
				"oldSlots", oldSlots,
				"newSlots", newSlots)
		}
	}
	c.egraphMu.Unlock()
	return report, nil
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

func (c *cache) simulatePersistedEdgeRemovalLocked(
	resultID sharedResultID,
	usageOwnerByIdentity map[string]sharedResultID,
	resultIDsByUsageIdentity map[string][]sharedResultID,
) int64 {
	res := c.resultsByID[resultID]
	if res == nil {
		return 0
	}

	simulatedCounts := map[sharedResultID]int64{
		resultID: res.incomingOwnershipCount - 1,
	}
	queue := make([]sharedResultID, 0, 1)
	if simulatedCounts[resultID] == 0 {
		queue = append(queue, resultID)
	}

	collected := make(map[sharedResultID]struct{})
	for len(queue) > 0 {
		curID := queue[len(queue)-1]
		queue = queue[:len(queue)-1]

		if _, seen := collected[curID]; seen {
			continue
		}
		cur := c.resultsByID[curID]
		if cur == nil {
			continue
		}
		curCount, ok := simulatedCounts[curID]
		if !ok {
			curCount = cur.incomingOwnershipCount
		}
		if curCount != 0 {
			continue
		}
		collected[curID] = struct{}{}

		for depID := range cur.deps {
			dep := c.resultsByID[depID]
			if dep == nil {
				continue
			}
			depCount, ok := simulatedCounts[depID]
			if !ok {
				depCount = dep.incomingOwnershipCount
			}
			depCount--
			simulatedCounts[depID] = depCount
			if depCount == 0 {
				queue = append(queue, depID)
			}
		}
	}

	var reclaimed int64
	for usageIdentity, ownerID := range usageOwnerByIdentity {
		if _, collectedOwner := collected[ownerID]; !collectedOwner {
			continue
		}
		allCollected := true
		for _, id := range resultIDsByUsageIdentity[usageIdentity] {
			if _, collectedID := collected[id]; !collectedID {
				allCollected = false
				break
			}
		}
		if !allCollected {
			continue
		}
		owner := c.resultsByID[ownerID]
		if owner == nil {
			continue
		}
		if owner.sizeEstimateBytes > 0 {
			reclaimed += owner.sizeEstimateBytes
		}
	}

	return reclaimed
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
		// reservedSpace is a hard floor; never drive used bytes below it when
		// pruning against maxUsedSpace.
		addKeepTarget(max(policy.MaxUsedSpace, policy.ReservedSpace))
	}
	if policy.ReservedSpace > 0 && usedBytes > policy.ReservedSpace {
		thresholdTriggered = true
		addKeepTarget(policy.ReservedSpace)
	}
	if policy.MinFreeSpace > 0 && policy.CurrentFreeSpace < policy.MinFreeSpace {
		thresholdTriggered = true
		target = max(target, policy.MinFreeSpace-policy.CurrentFreeSpace)
	}
	if hasKeepTarget && usedBytes > keepTargetBytes {
		target = max(target, usedBytes-keepTargetBytes)
	}
	if thresholdTriggered && policy.TargetSpace > 0 && usedBytes > policy.TargetSpace {
		// targetSpace is a sweep target applied once pruning is triggered. It is
		// still bounded by reservedSpace floor.
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

func cacheUsageEntryResultID(entryID string) (sharedResultID, bool) {
	const prefix = "dagql.result."
	if !strings.HasPrefix(entryID, prefix) {
		return 0, false
	}
	rawID := strings.TrimPrefix(entryID, prefix)
	parsed, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil {
		return 0, false
	}
	if parsed == 0 {
		return 0, false
	}
	return sharedResultID(parsed), true
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
