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
func (c *Cache) CapturePersistedRecord(ctx context.Context, result AnyResult) (PersistedRecord, error) {
	if result == nil || result.cacheSharedResult() == nil {
		return PersistedRecord{}, fmt.Errorf("capture persisted record: detached result")
	}
	record, _, err := c.captureRowRecord(ctx, result.cacheSharedResult())
	return record, err
}

// captureRowRecord is CapturePersistedRecord for a row, also returning the
// revision the record describes.
func (c *Cache) captureRowRecord(ctx context.Context, shared *sharedResult) (PersistedRecord, *capturedRowRevision, error) {
	var record PersistedRecord
	version, err := c.holdRowForCapture(ctx, shared, func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error {
		var err error
		record, err = c.captureHeldPersistedRecord(ctx, shared, imported, offers, version)
		return err
	})
	if version == nil {
		return PersistedRecord{}, nil, err
	}
	return record, version, err
}

// describeRowParts returns the address keys of the row's locally complete
// parts, as describePartRecord reports them for the record captureRowRecord
// would capture now, and the revision they describe. The row is held, and
// refused, as captureRowRecord holds and refuses it, but objects that
// describe their own parts aren't encoded.
func (c *Cache) describeRowParts(ctx context.Context, shared *sharedResult) ([]string, *capturedRowRevision, error) {
	var keys []string
	version, err := c.holdRowForCapture(ctx, shared, func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error {
		shared.lazyMu.Lock()
		defer shared.lazyMu.Unlock()
		if err := lazyEvaluationInFlightLocked(shared); err != nil {
			return err
		}
		var err error
		keys, err = c.describeHeldParts(ctx, shared, imported, offers, version)
		return err
	})
	if version == nil {
		return nil, nil, err
	}
	return keys, version, err
}

// holdRowForCapture runs capture with the row held as capturing its record
// requires: under a cache operation, registered, attached, and owned until
// capture returns. It then checks the revision capture recorded, and returns
// it.
func (c *Cache) holdRowForCapture(ctx context.Context, shared *sharedResult, capture func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error) (_ *capturedRowRevision, rerr error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	defer op.finish(false)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	c.egraphMu.Lock()
	if shared.id == 0 || c.resultsByID[shared.id] != shared {
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("capture persisted record: result %d is not registered in this cache", shared.id)
	}
	switch shared.attachmentState() {
	case resultAttachmentOpen:
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("%w: result %d dependency attachment", ErrPersistStateNotReady, shared.id)
	case resultAttachmentFailed:
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("capture persisted record: result %d dependency attachment failed", shared.id)
	}
	offers, err := shared.pendingOffersLocked()
	if err != nil {
		c.egraphMu.Unlock()
		return nil, err
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
	if err := capture(imported, offers, version); err != nil {
		return nil, err
	}
	if err := version.check(shared); err != nil {
		return nil, err
	}
	return version, nil
}

// The operation and row ownership are already held by the caller. Only this
// row's lazyMu is acquired here, never a dependency's mutex or a new admission.
func (c *Cache) captureHeldPersistedRecord(ctx context.Context, shared *sharedResult, imported bool, offers []PersistedPartOffer, version *capturedRowRevision) (PersistedRecord, error) {
	shared.lazyMu.Lock()
	defer shared.lazyMu.Unlock()
	if err := lazyEvaluationInFlightLocked(shared); err != nil {
		return PersistedRecord{}, err
	}
	return c.capturePartRecord(ctx, shared, imported, offers, version)
}

// lazyEvaluationInFlightLocked refuses a capture while the whole row or any
// of its part groups has an attempt running or a sync pending. It requires
// the row's lazyMu.
func lazyEvaluationInFlightLocked(shared *sharedResult) error {
	if shared.lazyWhole.attempt != nil || shared.lazyWhole.syncPending {
		return fmt.Errorf("%w: result %d whole evaluation", ErrPersistStateNotReady, shared.id)
	}
	for key, group := range shared.lazyPartGroups {
		if group.attempt != nil || group.syncPending {
			return fmt.Errorf("%w: result %d group %q", ErrPersistStateNotReady, shared.id, key)
		}
	}
	return nil
}

// describeHeldParts is capturePartRecord and then describePartRecord, for a
// row held for capture, without encoding objects that describe their own
// parts. An undecoded row, which has only its stored envelope, is captured
// and its record described.
func (c *Cache) describeHeldParts(ctx context.Context, shared *sharedResult, imported bool, offers []PersistedPartOffer, version *capturedRowRevision) ([]string, error) {
	payload := shared.loadPayloadState()
	if payload.persistedEnvelope != nil {
		record, err := c.capturePartRecord(ctx, shared, imported, offers, version)
		if err != nil {
			return nil, err
		}
		probes, err := describePartRecord(record)
		if err != nil {
			return nil, err
		}
		return partAddressKeys(completePartAddresses(probes)), nil
	}
	version.payload = payload
	// Nothing is retained, so the recorded call isn't copied: encoders only
	// read it.
	frame := shared.loadResultCall()
	value := capturedRowValue(shared, payload, frame)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, capturedOutputVersionsKey{}, &version.outputs)
	encodeCtx := ctx
	if frame != nil {
		encodeCtx = ContextWithCall(ctx, frame)
	}
	description := &partDescription{}
	if _, err := encodePersistedResult(encodeCtx, c, value, description); err != nil {
		return nil, fmt.Errorf("describe persisted record %d: %w", shared.id, err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return partAddressKeys(description.complete), nil
}

// capturedRowValue is the row's value as payload holds it, recorded under
// frame, apart from the row itself.
func capturedRowValue(shared *sharedResult, payload sharedResultPayloadState, frame *ResultCall) Result[Typed] {
	captured := &sharedResult{
		id:                    shared.id,
		self:                  payload.self,
		isObject:              payload.isObject,
		hasValue:              payload.hasValue,
		sessionResourceHandle: shared.sessionResourceHandle,
	}
	captured.storeResultCall(frame)
	return Result[Typed]{shared: captured}
}

// capturePartRecord relies on the core nonblocking output guard. Unlike a
// checkpoint capture it may run inside a synthetic coordination task.
func (c *Cache) capturePartRecord(ctx context.Context, shared *sharedResult, imported bool, offers []PersistedPartOffer, version *capturedRowRevision) (PersistedRecord, error) {
	var err error
	payload := shared.loadPayloadState()
	version.payload = payload
	if payload.snapshotLinkIntent != nil {
		payload.snapshotOwnerLinks = cloneSnapshotRefLinks(payload.snapshotLinkIntent.Links)
	}
	frame := shared.loadResultCall().clone()
	value := capturedRowValue(shared, payload, frame)
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
	encoding.Envelope.PersistDeclined = false
	envelope, err := clonePersistedEnvelope(encoding.Envelope)
	if err != nil {
		return PersistedRecord{}, err
	}
	return PersistedRecord{
		ResultID:      uint64(shared.id),
		Envelope:      envelope,
		Call:          frame,
		SnapshotLinks: cloneSnapshotRefLinks(encoding.SnapshotLinks),
	}, nil
}

func clonePersistedEnvelope(env PersistedResultEnvelope) (PersistedResultEnvelope, error) {
	var err error
	env.PendingOffers, err = clonePartOffers(env.PendingOffers)
	if err != nil {
		return PersistedResultEnvelope{}, err
	}
	env.StoredParts, err = clonePartOffers(env.StoredParts)
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
