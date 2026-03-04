package dagql

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/engine/slog"
)

type pruneCandidate struct {
	resultID sharedResultID
	entry    CacheUsageEntry
}

func (c *cache) Prune(ctx context.Context, policies []CachePrunePolicy) (CachePruneReport, error) {
	report := CachePruneReport{}
	if len(policies) == 0 {
		return report, nil
	}

	onReleases := []OnReleaseFunc{}

	c.egraphMu.Lock()
	now := time.Now()
	for policyIdx, policy := range policies {
		c.measureAllResultSizesLocked(ctx)
		entries := c.usageEntriesLocked()
		entryByResultID := make(map[sharedResultID]CacheUsageEntry, len(entries))
		var usedBytes int64
		for _, ent := range entries {
			usedBytes += ent.SizeBytes
			resultID, ok := cacheUsageEntryResultID(ent.ID)
			if !ok {
				continue
			}
			entryByResultID[resultID] = ent
		}

		targetBytes, _ := pruneTargetBytes(policy, usedBytes)
		if targetBytes <= 0 {
			slog.Debug("dagql prune skip policy: no reclaim target",
				"policyIndex", policyIdx,
				"usedBytes", usedBytes)
			continue
		}

		cutoffUnixNano := int64(0)
		if policy.KeepDuration > 0 {
			cutoffUnixNano = now.Add(-policy.KeepDuration).UnixNano()
		}

		activeClosure := c.activeDependencyClosureLocked()
		candidates := make([]pruneCandidate, 0, len(entryByResultID))
		for resultID, res := range c.resultsByID {
			if res == nil {
				continue
			}
			entry, ok := entryByResultID[resultID]
			if !ok {
				continue
			}
			switch {
			case atomic.LoadInt64(&res.refCount) > 0:
				slog.Debug("dagql prune skip candidate",
					"policyIndex", policyIdx,
					"entryID", entry.ID,
					"reason", "actively-used")
				continue
			case resultInActiveClosure(activeClosure, resultID):
				slog.Debug("dagql prune skip candidate",
					"policyIndex", policyIdx,
					"entryID", entry.ID,
					"reason", "dependency-of-active-result")
				continue
			case cutoffUnixNano > 0 && entryRecentlyUsed(entry, cutoffUnixNano):
				slog.Debug("dagql prune skip candidate",
					"policyIndex", policyIdx,
					"entryID", entry.ID,
					"reason", "keepDuration")
				continue
			case !cachePrunePolicyMatchesEntry(policy, entry):
				slog.Debug("dagql prune skip candidate",
					"policyIndex", policyIdx,
					"entryID", entry.ID,
					"reason", "filter")
				continue
			}
			candidates = append(candidates, pruneCandidate{
				resultID: resultID,
				entry:    entry,
			})
		}

		slices.SortFunc(candidates, func(a, b pruneCandidate) int {
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

		reclaimed := int64(0)
		for _, candidate := range candidates {
			if reclaimed >= targetBytes {
				break
			}
			res := c.resultsByID[candidate.resultID]
			if res == nil {
				continue
			}
			if atomic.LoadInt64(&res.refCount) > 0 {
				continue
			}

			onRelease := c.pruneResultLocked(candidate.resultID)
			if onRelease != nil {
				onReleases = append(onReleases, onRelease)
			}

			report.Entries = append(report.Entries, candidate.entry)
			report.ReclaimedBytes += candidate.entry.SizeBytes
			reclaimed += candidate.entry.SizeBytes

			slog.Debug("dagql prune selected candidate",
				"policyIndex", policyIdx,
				"entryID", candidate.entry.ID,
				"reclaimedBytes", candidate.entry.SizeBytes,
				"policyTargetBytes", targetBytes,
				"policyReclaimedBytes", reclaimed)
		}
	}
	c.egraphMu.Unlock()

	var releaseErr error
	for _, onRelease := range onReleases {
		if onRelease == nil {
			continue
		}
		releaseErr = errors.Join(releaseErr, onRelease(ctx))
	}
	return report, releaseErr
}

func (c *cache) pruneResultLocked(resultID sharedResultID) OnReleaseFunc {
	res := c.resultsByID[resultID]
	if res == nil {
		return nil
	}
	c.removeResultFromEgraphLocked(res)
	for _, remaining := range c.resultsByID {
		if remaining == nil || len(remaining.deps) == 0 {
			continue
		}
		delete(remaining.deps, resultID)
	}
	return res.onRelease
}

func resultInActiveClosure(activeClosure map[sharedResultID]struct{}, resultID sharedResultID) bool {
	if len(activeClosure) == 0 {
		return false
	}
	_, blocked := activeClosure[resultID]
	return blocked
}

func entryRecentlyUsed(entry CacheUsageEntry, cutoffUnixNano int64) bool {
	mostRecentUse := entry.MostRecentUseTimeUnixNano
	if mostRecentUse == 0 {
		mostRecentUse = entry.CreatedTimeUnixNano
	}
	return mostRecentUse >= cutoffUnixNano
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
