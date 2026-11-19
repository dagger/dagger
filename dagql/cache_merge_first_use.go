package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/dagger/dagger/engine/slog"
)

// errMergedPayloadRefused is the error of a record merged from the Cloud
// whose payload failed its check at its first use (prepareMergedPayload): the
// entry is taken back, and a lookup that chose it misses instead, as a fresh
// execution of its call succeeds where the record didn't.
var errMergedPayloadRefused = errors.New("merged record's payload refused at its first use")

// mergedPayloadRefusedError is errMergedPayloadRefused for the entry whose
// payload failed, which a lookup that misses for it takes back.
type mergedPayloadRefusedError struct {
	res   *sharedResult
	cause error
}

func (e *mergedPayloadRefusedError) Error() string {
	return fmt.Sprintf("%s: entry %d: %v", errMergedPayloadRefused, uint64(e.res.id), e.cause)
}

func (e *mergedPayloadRefusedError) Is(target error) bool { return target == errMergedPayloadRefused }

func (e *mergedPayloadRefusedError) Unwrap() error { return e.cause }

// takeBackRefused takes back, for a lookup that chose hit and missed for
// err, the entry whose payload err refused and hit itself, which can't be
// served without it (takeBackRefusedMerged). The caller holds no lock.
func (c *Cache) takeBackRefused(ctx context.Context, hit *sharedResult, err error) {
	var refused *mergedPayloadRefusedError
	if errors.As(err, &refused) && refused.res != hit {
		c.takeBackRefusedMerged(ctx, refused.res)
	}
	c.takeBackRefusedMerged(ctx, hit)
}

// mergedFirstUse is what a record merged from the Cloud keeps, on its entry,
// until its payload's first use prepares it (prepareMergedPayload). A Cloud
// bundle carries each record's complete and declared parts
// (TransferredValue.Parts), so a merge installs such a record from its call
// and offers, relocated to the cache's numbers, without decoding its payload,
// as a restart imports a saved row: the payload stays as the Cloud sent it,
// naming the rows it references by the Cloud's numbers.
type mergedFirstUse struct {
	// numbers are the entries the payload's references name, by the Cloud's
	// number: the record's direct dependencies.
	numbers map[uint64]sharedResultID
	// deps are the entry's direct dependencies, which every reference of the
	// prepared record must be among.
	deps []uint64
	// complete are the parts the record proves complete, as the bundle
	// carried them: the entry's complete parts until it is prepared.
	complete *partKeySet
	// source names the set file the record came in, for the log of a
	// failure (PreparedValueMerge.Source).
	source string

	// mu orders the preparations of the record: the first prepares it, and
	// a later one returns what it found (done, err). refused is set once
	// its payload failed, for readers that don't take mu.
	mu      sync.Mutex
	done    bool
	err     error
	refused atomic.Bool
}

// prepareMergedPayload prepares the payload of res, if res holds a record
// merged from the Cloud and not prepared yet, before anything reads it: it
// relocates the payload's references to the cache's numbers and checks the
// payload by its codecs, as a merge prepares a record it doesn't install
// unprepared (prepareRecordPayload), and installs the prepared envelope.
// Every reader of an entry's payload calls it first: a lookup's decode
// (ensurePersistedHitValueLoaded), a capture of its record
// (capturePartRecord) and a save (persistResultEnvelope).
//
// A payload that fails is a fault of the Cloud that wrote it, since the set
// file's digest is checked as it is downloaded. The failure is logged at
// error level with the file and the record's recipe, and the record is
// refused: every later preparation returns the same error,
// errMergedPayloadRefused; a hold finds the entry unknown (HoldEntry), so
// the service learns it is gone when it next names it; and a lookup that
// chose the entry misses and takes it back (takeBackRefusedMerged). It takes
// only the record's own mutex and res.payloadMu, so a caller can hold res's
// lazyMu, but no other lock.
func (c *Cache) prepareMergedPayload(ctx context.Context, res *sharedResult) error {
	if res == nil {
		return nil
	}
	res.payloadMu.RLock()
	use, env := res.firstUse, res.persistedEnvelope
	res.payloadMu.RUnlock()
	if use == nil {
		return nil
	}
	use.mu.Lock()
	defer use.mu.Unlock()
	if use.done {
		return use.err
	}
	use.done = true
	rec := PersistedRecord{ResultID: uint64(res.id), Envelope: *env, Call: res.loadResultCall(), SnapshotLinks: res.loadSnapshotOwnerLinks()}
	prepared, err := prepareFirstUseRecord(rec, use)
	if err != nil {
		use.err = &mergedPayloadRefusedError{res: res, cause: err}
		use.refused.Store(true)
		var recipe string
		if frame := rec.Call; frame != nil {
			if dig, digErr := frame.deriveRecipeDigest(c); digErr == nil {
				recipe = dig.String()
			}
		}
		slog.ErrorContext(ctx, "remote cache record's payload refused at its first use", "file", use.source, "recipe", recipe, "entry", uint64(res.id), "error", err)
		return use.err
	}
	res.payloadMu.Lock()
	if res.firstUse == use && res.persistedEnvelope == env {
		res.persistedEnvelope = &prepared.Envelope
		res.firstUse = nil
		res.payloadRevision++
	}
	res.payloadMu.Unlock()
	return nil
}

// prepareFirstUseRecord returns rec, a record merged unprepared, with its
// payload's references relocated to the cache's numbers and checked by its
// codecs. rec's identity, call and offers are already the cache's; only its
// payload names rows by the Cloud's numbers, each of which must be one of
// its direct dependencies.
func prepareFirstUseRecord(rec PersistedRecord, use *mergedFirstUse) (PersistedRecord, error) {
	bare := rec
	bare.Envelope.PendingOffers = nil
	env, links, err := visitEncodedPayloadReferences(bare, func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		n, ok := use.numbers[ref.ResultID]
		if !ok {
			return fmt.Errorf("reference to %d is not a direct dependency", ref.ResultID)
		}
		ref.ResultID = uint64(n)
		return nil
	})
	if err != nil {
		return PersistedRecord{}, err
	}
	env.PendingOffers = rec.Envelope.PendingOffers
	prepared := PersistedRecord{ResultID: rec.ResultID, Envelope: env, Call: rec.Call, SnapshotLinks: links}
	if _, err := validateMemoTransferRecordContent(prepared, use.deps, payloadMemos{}); err != nil {
		return PersistedRecord{}, err
	}
	return prepared, nil
}

// takeBackRefusedMerged takes back res, whose merged payload was refused at
// its first use, as a merge takes back an entry whose value met an
// invariant violation (failMergedEntryLocked): it leaves the recipe index,
// so no later lookup finds it, loses its retention edge, and is collected
// once nothing owns it. A lookup that chose it calls it as it misses, with
// no lock held.
func (c *Cache) takeBackRefusedMerged(ctx context.Context, res *sharedResult) {
	ctx = context.WithoutCancel(ctx)
	c.egraphMu.Lock()
	if res.payloadRefused || c.resultsByID[res.id] != res {
		c.egraphMu.Unlock()
		return
	}
	res.payloadRefused = true
	queue := c.failMergedEntryLocked(ctx, res, nil)
	releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
	c.stampPendingLocked()
	c.egraphMu.Unlock()
	if err := errors.Join(collectErr, runOnReleaseFuncs(ctx, releases)); err != nil {
		slog.WarnContext(ctx, "remote cache record taken back: collecting what it left unowned failed", "entry", uint64(res.id), "error", err)
	}
}

// refusedPayloadLocked reports whether res's merged payload was refused at
// its first use, taken back or not. Requires egraphMu.
func (res *sharedResult) refusedPayloadLocked() bool {
	if res.payloadRefused {
		return true
	}
	res.payloadMu.RLock()
	use := res.firstUse
	res.payloadMu.RUnlock()
	return use != nil && use.refused.Load()
}

// firstUseCompleteParts returns the complete parts of res while it holds a
// record merged unprepared, as the bundle carried them, and whether it does.
// Requires res.payloadMu, or egraphMu for a read that a merge doesn't race.
func (res *sharedResult) firstUseCompletePartsLocked() ([]string, bool) {
	if res.firstUse == nil {
		return nil, false
	}
	return res.firstUse.complete.list(), true
}

// holdsUnpreparedPayload reports whether res holds a record merged
// unprepared whose first use hasn't prepared it yet.
func (res *sharedResult) holdsUnpreparedPayload() bool {
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	return res.firstUse != nil
}
