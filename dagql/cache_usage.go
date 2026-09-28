package dagql

import (
	"context"
	"errors"

	"github.com/dagger/dagger/engine/slog"
)

// cacheUsageSnapshot owns the rows while provider callbacks run outside E.
// A Go pointer alone would not prevent OnRelease from closing their resources.
// Mutable provider identities remain best-effort samples: payloadRevision
// detects replacement of a row payload, not mutations inside its provider.
// The same sampled identities feed both disk-prune accounting passes.
type cacheUsageSnapshot struct {
	cache        *Cache
	op           cacheOperation
	inputs       map[sharedResultID]cacheUsageMeasurementInput
	holds        map[*sharedResult]struct{}
	measurements map[string]cacheUsageIdentityMeasurement
	released     bool
	callbacks    []OnReleaseFunc
	releaseErr   error
}

const cacheUsageMaxSamplingRounds = 3

func (c *Cache) collectUsageMeasurementInputs(ctx context.Context, measure bool) (*cacheUsageSnapshot, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	snapshot := &cacheUsageSnapshot{
		cache: c, op: op,
		inputs:       make(map[sharedResultID]cacheUsageMeasurementInput),
		holds:        make(map[*sharedResult]struct{}),
		measurements: make(map[string]cacheUsageIdentityMeasurement),
	}
	for round := range cacheUsageMaxSamplingRounds {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, snapshot.close(ctx))
		}
		c.egraphMu.Lock()
		pending := snapshot.capturePendingLocked(ctx)
		c.egraphMu.Unlock()
		if len(pending) == 0 {
			break
		}
		if round > 0 {
			slog.Debug("cache usage resampling changed population", "round", round+1, "rows", len(pending))
		}
		for i := range pending {
			if err := ctx.Err(); err != nil {
				return nil, errors.Join(err, snapshot.close(ctx))
			}
			input := &pending[i]
			if input.self != nil {
				input.identities = cacheUsageIdentitiesFromSelf(input.self)
				input.sizeMayChange = cacheUsageSizeMayChangeFromSelf(input.self)
			} else {
				input.identities = cacheUsageIdentitiesFromSnapshotLinks(input.snapshotLinks)
			}
			snapshot.inputs[input.resultID] = *input
		}
		if measure {
			measured := buildCacheUsageMeasurements(ctx, c.snapshotManager, pending)
			for _, byIdentity := range measured {
				for identity, measurement := range byIdentity {
					snapshot.measurements[identity] = measurement
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, snapshot.close(ctx))
	}
	return snapshot, nil
}

func cacheUsageInputLocked(res *sharedResult) cacheUsageMeasurementInput {
	state := res.loadPayloadState()
	input := cacheUsageMeasurementInput{
		resultID: res.id, row: res, payloadRevision: state.payloadRevision,
		existingSizeByID: make(map[string]int64, len(res.cacheUsageSizeByIdentity)),
	}
	if state.hasValue && state.self != nil {
		input.self = state.self
	} else {
		input.snapshotLinks = cloneSnapshotRefLinks(state.snapshotOwnerLinks)
	}
	for identity, size := range res.cacheUsageSizeByIdentity {
		input.existingSizeByID[identity] = size
	}
	return input
}

// Identity-free values need no provider callback. Unmaterialized values instead
// contribute their actual snapshot links, which may share measured identities.
func (input *cacheUsageMeasurementInput) identitiesWithoutCallbacks() bool {
	if input.self == nil {
		input.identities = cacheUsageIdentitiesFromSnapshotLinks(input.snapshotLinks)
		return true
	}
	if _, provider := input.self.(hasCacheUsageIdentity); provider {
		return false
	}
	input.identities = nil
	return true
}

func (snapshot *cacheUsageSnapshot) capturePendingLocked(ctx context.Context) []cacheUsageMeasurementInput {
	var pending []cacheUsageMeasurementInput
	for id, res := range snapshot.cache.resultsByID {
		if input, ok := snapshot.inputs[id]; ok && input.validLocked(snapshot.cache) {
			continue
		}
		if _, held := snapshot.holds[res]; !held {
			snapshot.cache.incrementIncomingOwnershipLocked(ctx, res)
			snapshot.holds[res] = struct{}{}
		}
		input := cacheUsageInputLocked(res)
		if input.identitiesWithoutCallbacks() && len(input.identities) == 0 {
			snapshot.inputs[id] = input
			continue
		}
		pending = append(pending, input)
	}
	return pending
}

// finalizeLocked must run after real hold collection and before graph/count
// copying. Unknown provider membership prevents ALL physical reclaim credit;
// never feed a zero-credit fallback into a planner that can evict every root.
func (snapshot *cacheUsageSnapshot) finalizeLocked() (map[sharedResultID][]string, error) {
	identities := make(map[sharedResultID][]string, len(snapshot.cache.resultsByID))
	unknown := 0
	for id, res := range snapshot.cache.resultsByID {
		input, sampled := snapshot.inputs[id]
		if !sampled || !input.validLocked(snapshot.cache) {
			input = cacheUsageInputLocked(res)
			if !input.identitiesWithoutCallbacks() {
				unknown++
				continue
			}
			snapshot.inputs[id] = input
		}
		identities[id] = input.identities
	}
	if unknown > 0 {
		slog.Debug("cache usage sampling incomplete; preserving previous sizes and deferring prune", "unsampledRows", unknown, "maxRounds", cacheUsageMaxSamplingRounds)
		return nil, errCacheUsageChanged
	}
	return identities, nil
}

func (input cacheUsageMeasurementInput) validLocked(c *Cache) bool {
	return c.resultsByID[input.resultID] == input.row && input.row.loadPayloadState().payloadRevision == input.payloadRevision
}

// releaseLocked removes measurement ownership before pruning copies incoming
// counts. Callbacks remain deferred until after E is released.
func (snapshot *cacheUsageSnapshot) releaseLocked(ctx context.Context) {
	if snapshot.released {
		return
	}
	snapshot.released = true
	var queue []*sharedResult
	for row := range snapshot.holds {
		var err error
		queue, err = snapshot.cache.decrementIncomingOwnershipLocked(ctx, row, queue)
		snapshot.releaseErr = errors.Join(snapshot.releaseErr, err)
	}
	var err error
	snapshot.callbacks, err = snapshot.cache.collectUnownedResultsLocked(ctx, queue)
	snapshot.releaseErr = errors.Join(snapshot.releaseErr, err)
}

func (snapshot *cacheUsageSnapshot) close(ctx context.Context) error {
	defer snapshot.op.finish(false)
	ctx = context.WithoutCancel(ctx)
	if !snapshot.released {
		snapshot.cache.egraphMu.Lock()
		snapshot.releaseLocked(ctx)
		snapshot.cache.egraphMu.Unlock()
	}
	err := errors.Join(snapshot.releaseErr, runOnReleaseFuncs(ctx, snapshot.callbacks))
	if err != nil {
		// These may be callbacks deferred by a concurrent session release.
		snapshot.cache.recordReleaseCleanupError("", true, err)
	}
	return err
}
