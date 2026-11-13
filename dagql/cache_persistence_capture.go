package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/engine/wcprof"
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
	return c.captureRowRecord(ctx, result.cacheSharedResult())
}

// captureRowRecord is CapturePersistedRecord for a row.
func (c *Cache) captureRowRecord(ctx context.Context, shared *sharedResult) (PersistedRecord, error) {
	var record PersistedRecord
	version, err := c.holdRowForCapture(ctx, shared, func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error {
		var err error
		record, err = c.captureHeldPersistedRecord(ctx, shared, imported, offers, version)
		return err
	})
	if version == nil {
		return PersistedRecord{}, err
	}
	return record, err
}

// describeRowParts returns the address keys of the row's locally complete
// parts, as describePartRecord reports them for the record captureRowRecord
// would capture now, and the revision they describe. The row is held, and
// refused, as captureRowRecord holds and refuses it, but objects that
// describe their own parts aren't encoded.
func (c *Cache) describeRowParts(ctx context.Context, shared *sharedResult) ([]string, *capturedRowRevision, error) {
	return c.describeRowPartsHeld(ctx, shared, func(capture captureFunc) (*capturedRowRevision, error) {
		return c.holdRowForCapture(ctx, shared, capture)
	})
}

// describeRowPartsHeld is describeRowParts with the row held by hold.
func (c *Cache) describeRowPartsHeld(ctx context.Context, shared *sharedResult, hold captureHold) ([]string, *capturedRowRevision, error) {
	var keys []string
	version, err := hold(func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error {
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

// captureFunc captures a held row's record, or what it needs of it: it is
// given what the hold read of the row and the revision to record.
type captureFunc func(imported bool, offers []PersistedPartOffer, version *capturedRowRevision) error

// captureHold runs a capture with its row held (holdRowForCapture,
// heldCaptureRow.run), and returns the revision it checked.
type captureHold func(capture captureFunc) (*capturedRowRevision, error)

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
	held, release := c.holdRowsForCapture(ctx, []*sharedResult{shared})
	defer func() {
		rerr = errors.Join(rerr, release())
	}()
	return held[0].run(capture)
}

// capturePieceRows is how many rows holdRowsForCapture takes, or lets go, in
// one hold of egraphMu for writing: a count, not a time, so that a hold is
// the same on every machine. Taking a row checks it and counts one owner,
// microseconds; a piece is under a millisecond.
const capturePieceRows = 256

// heldCaptureRow is a row holdRowsForCapture took for a capture: what its
// hold read of it, or why it refused it.
type heldCaptureRow struct {
	row      *sharedResult
	imported bool
	offers   []PersistedPartOffer
	err      error
}

// run runs capture with the row held, as holdRowForCapture does, and checks
// the revision it recorded.
func (h heldCaptureRow) run(capture captureFunc) (*capturedRowRevision, error) {
	if h.err != nil {
		return nil, h.err
	}
	version := new(capturedRowRevision)
	if err := capture(h.imported, h.offers, version); err != nil {
		return nil, err
	}
	if err := version.check(h.row); err != nil {
		return nil, err
	}
	return version, nil
}

// holdRowsForCapture holds rows as capturing their records requires,
// registered, attached, and owned, each as holdRowForCapture holds one, in
// holds of egraphMu for writing of capturePieceRows rows each: a writer
// waits for egraphMu's readers, and a merge's commit captures thousands of
// targets, which took two holds each. It returns each row's hold, in the
// order given, and release, which lets them go in the same pieces, collects
// what that leaves unowned, and returns any error doing so. The caller holds
// a cache operation until release returns.
func (c *Cache) holdRowsForCapture(ctx context.Context, rows []*sharedResult) ([]heldCaptureRow, func() error) {
	held := make([]heldCaptureRow, len(rows))
	if err := context.Cause(ctx); err != nil {
		for i, row := range rows {
			held[i] = heldCaptureRow{row: row, err: err}
		}
		return held, func() error { return nil }
	}
	piece := capturePieceRows
	if c.capturePieceSize > 0 {
		piece = c.capturePieceSize
	}
	var owned []*sharedResult
	for start := 0; start < len(rows); start += piece {
		if c.testCaptureWriteLock != nil {
			c.testCaptureWriteLock()
		}
		wcprof.WriteLock(ctx, &c.egraphMu, egraphLockIdent)
		for i := start; i < min(start+piece, len(rows)); i++ {
			shared := rows[i]
			held[i].row = shared
			if shared.id == 0 || c.resultsByID[shared.id] != shared {
				held[i].err = fmt.Errorf("capture persisted record: result %d is not registered in this cache", shared.id)
				continue
			}
			switch shared.attachmentState() {
			case resultAttachmentOpen:
				held[i].err = fmt.Errorf("%w: result %d dependency attachment", ErrPersistStateNotReady, shared.id)
				continue
			case resultAttachmentFailed:
				held[i].err = fmt.Errorf("capture persisted record: result %d dependency attachment failed", shared.id)
				continue
			}
			offers, err := shared.pendingOffersLocked()
			if err != nil {
				held[i].err = err
				continue
			}
			held[i].imported, held[i].offers = shared.imported, offers
			c.incrementIncomingOwnershipLocked(ctx, shared)
			owned = append(owned, shared)
		}
		c.egraphMu.Unlock()
	}
	release := func() error {
		cleanupCtx := context.WithoutCancel(ctx)
		var errs []error
		for start := 0; start < len(owned); start += piece {
			if c.testCaptureWriteLock != nil {
				c.testCaptureWriteLock()
			}
			wcprof.WriteLock(cleanupCtx, &c.egraphMu, egraphLockIdent)
			var queue []*sharedResult
			for _, shared := range owned[start:min(start+piece, len(owned))] {
				var decErr error
				queue, decErr = c.decrementIncomingOwnershipLocked(cleanupCtx, shared, queue)
				errs = append(errs, decErr)
			}
			releases, collectErr := c.collectUnownedResultsLocked(cleanupCtx, queue)
			c.egraphMu.Unlock()
			errs = append(errs, collectErr, runOnReleaseFuncs(cleanupCtx, releases))
		}
		return errors.Join(errs...)
	}
	return held, release
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
		env, err := payload.wholeEnvelope(uint64(shared.id))
		if err != nil {
			return PersistedRecord{}, fmt.Errorf("capture persisted record %d: %w", shared.id, err)
		}
		encoding = PersistedResultEncoding{
			Envelope:      *env,
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
