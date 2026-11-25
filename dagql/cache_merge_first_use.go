package dagql

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

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
	// origin is the merge that installed the record (mergeOrigin), which
	// a first use is counted by and a failure's log names.
	origin *mergeOrigin

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
// (ensurePersistedHitValueLoaded) and a capture of its record
// (capturePartRecord); a save prepares every record nothing used before it
// (prepareMergedPayloadsForSave).
//
// A payload that fails is a fault of the Cloud that wrote it, since the set
// file's digest is checked as it is downloaded. The failure is logged at
// error level with the file and the record's recipe, and the record is
// refused: every later preparation returns the same error,
// errMergedPayloadRefused; a hold finds the entry unknown (HoldEntry), so
// the service learns it is gone when it next names it; a lookup that chose
// the entry misses and takes it back (takeBackRefusedMerged); and a save
// leaves it out (snapshotPersistedRootClosureLocked). It takes only the
// record's own mutex and res.payloadMu, so a caller can hold res's lazy state's mu,
// but no other lock.
//
// A preparation is counted by the path it came by (FirstUseStats.Paths):
// the kind of work ctx carries (withCacheWork), or otherwise when it carries
// none.
func (c *Cache) prepareMergedPayload(ctx context.Context, res *sharedResult, otherwise cacheWork) error {
	return c.prepareMerged(ctx, res, false, otherwise)
}

// prepareMerged is prepareMergedPayload, counted as a save's preparation
// when atSave is set (FirstUseStats).
func (c *Cache) prepareMerged(ctx context.Context, res *sharedResult, atSave bool, otherwise cacheWork) error {
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
	began := time.Now()
	var waited time.Duration
	rec := PersistedRecord{ResultID: uint64(res.id), Envelope: *env, Call: res.loadResultCall(), SnapshotLinks: res.loadSnapshotOwnerLinks()}
	defer func() {
		c.firstUsePrepared(firstUsePreparation{took: time.Since(began), waited: waited, refused: use.err != nil, atSave: atSave, path: cacheWorkOf(ctx, otherwise), origin: use.origin, frame: rec.Call, env: env})
	}()
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
		at := "first use"
		if atSave {
			at = "save"
		}
		slog.ErrorContext(ctx, "remote cache record's payload refused", "at", at, "route", use.origin.route.String(), "merge", use.origin.number, "file", use.origin.file, "recipe", recipe, "entry", uint64(res.id), "error", err)
		return use.err
	}
	if c.testFirstUseInstall != nil {
		c.testFirstUseInstall(res)
	}
	locking := time.Now()
	res.payloadMu.Lock()
	waited = time.Since(locking)
	if res.firstUse == use && res.persistedEnvelope == env {
		res.persistedEnvelope = &prepared.Envelope
		res.firstUse = nil
		res.payloadRevision++
		// The prepared record proves the parts its bundle carried, as
		// describing it finds them: a merge that warmed nothing for the
		// entry while it was unprepared replies with them still.
		res.completeParts.Store(&rowCompleteParts{
			stamp: completePartsStamp{payloadRevision: res.payloadRevision, hasValue: res.hasValue, isObject: res.isObject},
			keys:  use.complete.list(),
		})
	}
	res.payloadMu.Unlock()
	return nil
}

// prepareMergedPayloadsForSave prepares, before a save, the payload of every
// record merged unprepared that nothing has used, so the save writes each
// one naming the cache's numbers; a record refused here is left out of the
// save, with all that owns it (snapshotPersistedRootClosureLocked). The
// preparations run in parallel: at an engine's close nothing else runs. The
// Cloud merges no record unprepared (markUnprepared).
func (c *Cache) prepareMergedPayloadsForSave(ctx context.Context) {
	if c.blobBacked {
		return
	}
	step := c.readLockGraphAs(ctx, "dagql.persist.prepare")
	var pending []*sharedResult
	for _, res := range c.resultsByID {
		if res.holdsUnpreparedPayload() {
			pending = append(pending, res)
		}
	}
	c.runlockGraphAs(step)
	if len(pending) == 0 {
		return
	}
	began := time.Now()
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(pending)) {
		workers.Go(func() {
			for i := next.Add(1) - 1; i < int64(len(pending)); i = next.Add(1) - 1 {
				_ = c.prepareMerged(ctx, pending[i], true, workOther)
			}
		})
	}
	workers.Wait()
	c.firstUseMu.Lock()
	c.firstUseStats.SaveWall += time.Since(began)
	c.firstUseMu.Unlock()
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

// FirstUseStats counts the payloads of records merged from the Cloud that a
// first use prepared (prepareMergedPayload) and those it refused, with the
// time preparing took in all and at the longest: the work a restore defers
// onto the calls that use its records. The longest preparation is named:
// its record's recipe, its payload's size (persistedEnvelopePayloadBytes),
// and its time split into preparing the payload and waiting for the entry's
// payload lock to install it, so a long one shows whether it was the record
// or a wait. Paths splits the first uses by the path they came by, so work a
// call needed can be told from work nothing asked for: lookup (a call's
// decode of its hit), export, offer, part (fetching or installing an
// entry's parts), merge or restore (a merge warming the parts of the
// entries it lands on), and other; a path
// with none is left out. The Save fields count the same for the records
// nothing used, which a save prepared before it wrote them
// (prepareMergedPayloadsForSave), with SaveWall the time the save spent
// preparing them, in parallel. Routes splits the first uses by the route
// that brought their records to the engine (MergeRoute), each beside what
// that route merged (FirstUseRoute); a route that merged nothing is left
// out.
type FirstUseStats struct {
	Prepared, Refused               int64
	Total, Longest                  time.Duration
	LongestRecipe                   string
	LongestBytes                    int64
	LongestPrepare, LongestLockWait time.Duration
	Paths                           map[string]FirstUsePath
	SavePrepared, SaveRefused       int64
	SaveTotal, SaveWall             time.Duration
	Routes                          map[string]FirstUseRoute
}

// FirstUseRoute counts what one route brought to the engine (MergeRoute)
// and what of it the engine's calls first used: the merges committed and
// the records they carried, held rows aside; the records they installed
// with their payloads left for a first use, a record the engine already
// held a value for not being installed; the first uses of those records,
// refused ones included, and the time they took; and the merges at least
// one of whose records was first used. A save's preparation of a record
// nothing used is not a first use (FirstUseStats.SavePrepared).
type FirstUseRoute struct {
	Merges, Sent, Merged int64
	FirstUses            int64
	Total                time.Duration
	Used                 int64
}

// MergeRoute is the route by which a merge brought the Cloud's records to
// an engine (MergeSource).
type MergeRoute uint8

const (
	// MergeRouteUnnamed is a merge that named no route: the image's, and
	// a test's.
	MergeRouteUnnamed MergeRoute = iota
	// MergeRouteSetFile is a part of a set file a restore merged
	// (RestoreValues).
	MergeRouteSetFile
	// MergeRouteDeferred is a merge that answered no single lookup of the
	// engine's: roots the engine's lookups deferred to its queue, which the
	// service sends as the engine's merge slots free, a split's lower half
	// of one, or one sent again.
	MergeRouteDeferred
	// MergeRouteLookup is a merge that answered a lookup of the engine's.
	MergeRouteLookup
	numMergeRoutes
)

var mergeRouteNames = [numMergeRoutes]string{
	MergeRouteUnnamed:  "unnamed",
	MergeRouteSetFile:  "set_file",
	MergeRouteDeferred: "deferred",
	MergeRouteLookup:   "lookup",
}

func (r MergeRoute) String() string { return mergeRouteNames[r] }

// MergeSource names where a merge of the Cloud's records came from: its
// route, and the service's number for the merge or the set file it came
// in.
type MergeSource struct {
	Route MergeRoute
	// Merge is the service's number for a deferred or a lookup merge.
	Merge uint64
	// File names the set file of a set file's part.
	File string
}

// mergeOrigin is, on an engine, the merge from the Cloud that installed an
// entry's value: its source, and when it committed. Every entry the merge
// installed shares it, as does each one's first-use state
// (mergedFirstUse.origin), so it costs an entry a pointer. used is set at
// the first first use of any record it installed, which counts the merge
// as used (FirstUseRoute.Used).
type mergeOrigin struct {
	route  MergeRoute
	number uint64
	file   string
	// committedAt is when the merge committed, in Unix nanoseconds: set
	// under egraphMu by the commit before any entry names the origin, and
	// read under egraphMu.
	committedAt int64
	used        atomic.Bool
}

// mergedAt is when the merge that installed an entry's value committed, or
// 0 for an entry with no origin: a value computed here, or one restored
// after a restart. Requires egraphMu.
func (o *mergeOrigin) mergedAt() int64 {
	if o == nil {
		return 0
	}
	return o.committedAt
}

// mergeCommitted counts a committed merge of origin's route: the records
// its bundle carried, held rows aside, and those it installed for a first
// use.
func (c *Cache) mergeCommitted(origin *mergeOrigin, sent, merged int) {
	c.firstUseMu.Lock()
	defer c.firstUseMu.Unlock()
	route := &c.firstUseRoutes[origin.route]
	route.Merges++
	route.Sent += int64(sent)
	route.Merged += int64(merged)
}

// FirstUsePath counts the first uses that came by one path: the payloads
// prepared, refused ones included, and the time that took in all and at the
// longest.
type FirstUsePath struct {
	Prepared       int64
	Total, Longest time.Duration
}

func (p *FirstUsePath) add(took time.Duration) {
	p.Prepared++
	p.Total += took
	if took > p.Longest {
		p.Longest = took
	}
}

// firstUsePreparation is one preparation of a merged record's payload, as
// firstUsePrepared counts it: how long it took, how much of that waited for
// the entry's payload lock, whether it was refused, whether a save made it,
// and the record's frame and envelope as it found them.
type firstUsePreparation struct {
	took, waited    time.Duration
	refused, atSave bool
	path            cacheWork
	origin          *mergeOrigin
	frame           *ResultCall
	env             *PersistedResultEnvelope
}

// firstUsePrepared counts one preparation. One longer than every first use
// before it is named, its recipe derived outside firstUseMu.
func (c *Cache) firstUsePrepared(p firstUsePreparation) {
	c.firstUseMu.Lock()
	if p.atSave {
		if p.refused {
			c.firstUseStats.SaveRefused++
		} else {
			c.firstUseStats.SavePrepared++
		}
		c.firstUseStats.SaveTotal += p.took
		c.firstUseMu.Unlock()
		return
	}
	if p.refused {
		c.firstUseStats.Refused++
	} else {
		c.firstUseStats.Prepared++
	}
	c.firstUseStats.Total += p.took
	c.firstUsePaths[p.path].add(p.took)
	route := &c.firstUseRoutes[p.origin.route]
	route.FirstUses++
	route.Total += p.took
	if p.origin.used.CompareAndSwap(false, true) {
		route.Used++
	}
	longest := p.took > c.firstUseStats.Longest
	c.firstUseMu.Unlock()
	if !longest {
		return
	}
	var recipe string
	if p.frame != nil {
		if dig, err := p.frame.deriveRecipeDigest(c); err == nil {
			recipe = dig.String()
		}
	}
	c.firstUseMu.Lock()
	defer c.firstUseMu.Unlock()
	if p.took > c.firstUseStats.Longest {
		c.firstUseStats.Longest = p.took
		c.firstUseStats.LongestRecipe = recipe
		c.firstUseStats.LongestBytes = persistedEnvelopePayloadBytes(p.env)
		c.firstUseStats.LongestPrepare = p.took - p.waited
		c.firstUseStats.LongestLockWait = p.waited
	}
}

// FirstUse returns the cache's FirstUseStats so far.
func (c *Cache) FirstUse() FirstUseStats {
	c.firstUseMu.Lock()
	defer c.firstUseMu.Unlock()
	stats := c.firstUseStats
	for path, counted := range c.firstUsePaths {
		if counted.Prepared == 0 {
			continue
		}
		if stats.Paths == nil {
			stats.Paths = map[string]FirstUsePath{}
		}
		stats.Paths[cacheWork(path).String()] = counted
	}
	for route, counted := range c.firstUseRoutes {
		if counted.Merges == 0 {
			continue
		}
		if stats.Routes == nil {
			stats.Routes = map[string]FirstUseRoute{}
		}
		stats.Routes[MergeRoute(route).String()] = counted
	}
	return stats
}
