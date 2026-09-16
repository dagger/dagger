package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// CapturePersistedRecord copies one registered row without evaluating it or
// opening its snapshots. The returned record owns its bytes and still uses
// local result IDs. It is the encoding step of live metadata export, not a
// portable graph: callers must separately capture and relocate its references.
// A row being evaluated reports ErrPersistStateNotReady; unstarted work is
// encoded with its original inputs.
func (c *Cache) CapturePersistedRecord(ctx context.Context, result AnyResult) (_ PersistedRecord, rerr error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return PersistedRecord{}, err
	}
	defer op.finish(false)
	if err := context.Cause(ctx); err != nil {
		return PersistedRecord{}, err
	}
	if result == nil || result.cacheSharedResult() == nil {
		return PersistedRecord{}, fmt.Errorf("capture persisted record: detached result")
	}
	shared := result.cacheSharedResult()
	c.egraphMu.Lock()
	if shared.id == 0 || c.resultsByID[shared.id] != shared {
		c.egraphMu.Unlock()
		return PersistedRecord{}, fmt.Errorf("capture persisted record: result %d is not registered in this cache", shared.id)
	}
	switch shared.attachmentState() {
	case resultAttachmentOpen:
		c.egraphMu.Unlock()
		return PersistedRecord{}, fmt.Errorf("%w: result %d dependency attachment", ErrPersistStateNotReady, shared.id)
	case resultAttachmentFailed:
		c.egraphMu.Unlock()
		return PersistedRecord{}, fmt.Errorf("capture persisted record: result %d dependency attachment failed", shared.id)
	}
	offers, err := shared.pendingOffersLocked()
	if err != nil {
		c.egraphMu.Unlock()
		return PersistedRecord{}, err
	}
	imported := shared.imported
	c.incrementIncomingOwnershipLocked(ctx, shared)
	c.egraphMu.Unlock()
	defer func() {
		cleanupCtx := context.WithoutCancel(ctx)
		c.egraphMu.Lock()
		queue, decErr := c.decrementIncomingOwnershipLocked(cleanupCtx, shared, nil)
		releases, collectErr := c.collectUnownedResultsLocked(cleanupCtx, queue)
		c.egraphMu.Unlock()
		rerr = errors.Join(rerr, decErr, collectErr, runOnReleaseFuncs(cleanupCtx, releases))
	}()

	version := new(capturedRowRevision)
	record, err := c.captureHeldPersistedRecord(ctx, shared, imported, offers, version)
	if err != nil {
		return PersistedRecord{}, err
	}
	if err := version.check(shared); err != nil {
		return PersistedRecord{}, err
	}
	return record, nil
}

// The operation and row ownership are already held by the caller. Only this
// row's lazyMu is acquired here, never a dependency's mutex or a new admission.
func (c *Cache) captureHeldPersistedRecord(ctx context.Context, shared *sharedResult, imported bool, offers []PersistedPartOffer, version *capturedRowRevision) (PersistedRecord, error) {
	var err error
	shared.lazyMu.Lock()
	defer shared.lazyMu.Unlock()

	if shared.lazyWhole.attempt != nil || shared.lazyWhole.syncPending {
		return PersistedRecord{}, fmt.Errorf("%w: result %d whole evaluation", ErrPersistStateNotReady, shared.id)
	}
	for key, group := range shared.lazyPartGroups {
		if group.attempt != nil || group.syncPending {
			return PersistedRecord{}, fmt.Errorf("%w: result %d group %q", ErrPersistStateNotReady, shared.id, key)
		}
	}

	payload := shared.loadPayloadState()
	version.payload = payload
	if payload.snapshotLinkIntent != nil {
		payload.snapshotOwnerLinks = slices.Clone(payload.snapshotLinkIntent.Links)
	}
	frame := shared.loadResultCall().clone()
	captured := &sharedResult{
		id:                    shared.id,
		self:                  payload.self,
		isObject:              payload.isObject,
		hasValue:              payload.hasValue,
		sessionResourceHandle: shared.sessionResourceHandle,
	}
	captured.storeResultCall(frame)
	value := Result[Typed]{shared: captured}
	if err := context.Cause(ctx); err != nil {
		return PersistedRecord{}, err
	}

	versions := &version.outputs
	ctx = context.WithValue(ctx, capturedOutputVersionsKey{}, versions)
	var encoding PersistedResultEncoding
	if payload.persistedEnvelope != nil {
		encoding = PersistedResultEncoding{
			Envelope:      *payload.persistedEnvelope,
			SnapshotLinks: payload.snapshotOwnerLinks,
		}
	} else {
		encodeCtx := ctx
		if frame != nil {
			encodeCtx = ContextWithCall(ctx, frame)
		}
		encoding, err = DefaultPersistedSelfCodec.EncodeResult(encodeCtx, c, value)
		if err != nil {
			return PersistedRecord{}, fmt.Errorf("capture persisted record %d: %w", shared.id, err)
		}
	}
	if err := context.Cause(ctx); err != nil {
		return PersistedRecord{}, err
	}
	encoding.Envelope.Imported, encoding.Envelope.PendingOffers = imported, offers
	envelope, err := clonePersistedEnvelope(encoding.Envelope)
	if err != nil {
		return PersistedRecord{}, err
	}
	return PersistedRecord{
		ResultID:      uint64(shared.id),
		Envelope:      envelope,
		Call:          frame,
		SnapshotLinks: slices.Clone(encoding.SnapshotLinks),
	}, nil
}

func clonePersistedEnvelope(env PersistedResultEnvelope) (PersistedResultEnvelope, error) {
	var err error
	env.PendingOffers, err = clonePartOffers(env.PendingOffers)
	if err != nil {
		return PersistedResultEnvelope{}, err
	}
	env.ObjectJSON = slices.Clone(env.ObjectJSON)
	env.ScalarJSON = slices.Clone(env.ScalarJSON)
	env.Items = slices.Clone(env.Items)
	for i := range env.Items {
		env.Items[i], err = clonePersistedEnvelope(env.Items[i])
		if err != nil {
			return PersistedResultEnvelope{}, err
		}
	}
	return env, nil
}
