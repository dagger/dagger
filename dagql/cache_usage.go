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
	cache      *Cache
	op         cacheOperation
	inputs     []cacheUsageMeasurementInput
	released   bool
	callbacks  []OnReleaseFunc
	releaseErr error
}

func (c *Cache) collectUsageMeasurementInputs(ctx context.Context) (*cacheUsageSnapshot, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	snapshot := &cacheUsageSnapshot{cache: c, op: op}
	c.egraphMu.Lock()
	for _, res := range c.resultsByID {
		if res == nil {
			continue
		}
		state := res.loadPayloadState()
		input := cacheUsageMeasurementInput{
			resultID:         res.id,
			row:              res,
			payloadRevision:  state.payloadRevision,
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
		c.incrementIncomingOwnershipLocked(ctx, res)
		snapshot.inputs = append(snapshot.inputs, input)
	}
	c.egraphMu.Unlock()

	for i := range snapshot.inputs {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, snapshot.close(ctx))
		}
		input := &snapshot.inputs[i]
		if input.self != nil {
			input.identities = cacheUsageIdentitiesFromSelf(input.self)
			input.sizeMayChange = cacheUsageSizeMayChangeFromSelf(input.self)
		} else {
			input.identities = cacheUsageIdentitiesFromSnapshotLinks(input.snapshotLinks)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, snapshot.close(ctx))
	}
	return snapshot, nil
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
	for _, input := range snapshot.inputs {
		var err error
		queue, err = snapshot.cache.decrementIncomingOwnershipLocked(ctx, input.row, queue)
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

func (snapshot *cacheUsageSnapshot) closeAndLog(ctx context.Context) {
	if err := snapshot.close(ctx); err != nil {
		slog.Warn("release cache usage snapshot", "err", err)
	}
}
