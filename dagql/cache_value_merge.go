package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/opencontainers/go-digest"
)

// MergeValues merges a bundle of transferred values into the cache, and
// replaces a fresh import: every record lands on its recipe's current entry
// when the cache has one. The Cloud calls it to store an engine's export, from
// that engine's cache; an engine calls it for the Cloud's merge, from
// cloudCacheID, and at start for its image's entries, from imageCacheID.
//
// It derives each record's recipe outside the graph lock, in a private cache
// with provisional numbers, as an import did. An engine prepares a record its
// cache already holds a value for from its call and offers only, since merge
// keeps that value. Then, in one hold of egraphMu, it picks every record's
// target and decides it, relocates the records to the final numbers and runs
// every check that can fail, and only then changes the cache. The decision
// starts again in two cases: an entry whose dependency attachment is still
// open is waited on first, and a decision that installs a record prepared from
// its call and offers only prepares the bundle in full first.
//
// For each record, by its recipe's current entry:
//   - none: a new entry stores the record;
//   - an entry with no value, known only through holdings: it stores the
//     record, and its replacement count stays;
//   - an unexpired value: the entry keeps it, since the same recipe means an
//     interchangeable value;
//   - an expired value and an unexpired record: the Cloud replaces the value
//     in place. An engine does the same when nothing uses the entry, and
//     otherwise retires it and stores the record in a new entry;
//   - both expired: the entry keeps its value and adds only what it lacks.
//
// Every target learns the record's identity, and the sender's holding: on an
// engine its Cloud holding, on the Cloud the sending engine's holding at the
// record's replacement count. Unexpired roots are retained. An engine admits
// the bundle's parts as offers by what the target kept; the Cloud admits none
// until it has the bytes (SetStoredPart). An expired root is answered expired,
// and only what the live roots need is merged. An engine merges its image's
// entries without the expiries the cache that built the image gave them, so
// none is left out however old the image.
//
// A held row names by recipe a value the recipient holds: the Cloud's, in a
// bundle the Cloud sends an engine, or the engine's own, in an export an
// engine sends the Cloud. It lands on the recipe's current entry, which keeps
// its value, and the rows that depend on it relocate onto that entry. It
// holds when merge would keep the entry's value for a record of the recipe
// with the row's expiry, judged against the cache before the merge, and the
// entry has every part the bundle puts on the row (staleHeldPartsLocked). On
// the Cloud, a held row teaches the sending engine's holding of the row when
// the Cloud has none yet.
// If any held row doesn't hold, the merge commits nothing and its reply names
// every stale held row. A bundle that carries a held row and a record of the
// same recipe is malformed.
//
// The reply reports every target as the commit left it, read under the same
// lock, with the cache's generation and clock. Merge runs in no session and
// emits no span.
//
// MergeValues is PrepareValueMerge then CommitValueMerge. A caller that holds
// a lock of its own across a merge can call them apart, and prepare outside
// that lock.
func (c *Cache) MergeValues(ctx context.Context, from CacheID, input ValueBundle) (MergeReply, error) {
	ctx = withCacheWork(ctx, workMerge)
	p, err := c.PrepareValueMerge(ctx, from, input)
	if err != nil {
		return MergeReply{}, err
	}
	return c.CommitValueMerge(ctx, p)
}

// MergeServiceValues merges a bundle the remote cache service sent an
// engine, as MergeValues merges one from the Cloud, with the same reply,
// errors and locks; source names the route that brought it and the
// service's number for it (PreparedValueMerge.Source).
func (c *Cache) MergeServiceValues(ctx context.Context, input ValueBundle, source MergeSource) (MergeReply, error) {
	ctx = withCacheWork(ctx, workMerge)
	p, err := c.PrepareValueMerge(ctx, cloudCacheID, input)
	if err != nil {
		return MergeReply{}, err
	}
	p.Source(source)
	return c.CommitValueMerge(ctx, p)
}

// RestoreValues merges a part of a file of the Cloud's records that an engine
// restores, as MergeValues merges a bundle from the Cloud, with the same
// reply, errors and locks. Only prefetching tells them apart: the offers a
// restore attaches wait in the prefetcher's backlog, in the order the commit
// attaches them, which for a file is the order its records were first used,
// and never push out the offers of other merges (partOfferRestored).
//
// It is CheckRestoreValues then RestoreCheckedValues, and its reply's Prepare
// is both parts of the preparation.
//
// The commit also holds every entry the part lands on, until the reply's
// Release: a set file's later parts name its earlier parts' records as held
// rows, and a part's roots in unretained, the closure of a root that a later
// part carries, are merged with no retention edge (Unretained), so that only
// the hold keeps them until the root lands. source names the set file, for
// the log of a record whose payload fails at its first use, and its
// records' route is a set file's (Source).
func (c *Cache) RestoreValues(ctx context.Context, bundle ValueBundle, unretained []TransferOrdinal, source string) (MergeReply, error) {
	checked, err := c.CheckRestoreValues(bundle)
	if err != nil {
		return MergeReply{}, err
	}
	reply, err := c.RestoreCheckedValues(ctx, checked, unretained, source)
	reply.Prepare += checked.Took()
	return reply, err
}

// CheckedValues is a part of a set file checked for its restore ahead of it
// (CheckRestoreValues): everything RestoreValues does before it reads the
// cache's state, which nothing the cache does meanwhile changes. A restore
// checks a file's next part while the part before it merges, and merges it
// with RestoreCheckedValues, once.
type CheckedValues struct {
	cache *Cache
	// input is the bundle as sent, for a commit that prepares in full again
	// (PreparedValueMerge.input).
	input ValueBundle
	prep  *valueMergePrep
	took  time.Duration
	used  atomic.Bool
}

// Took returns how long the check took.
func (v *CheckedValues) Took() time.Duration {
	return v.took
}

// CheckRestoreValues checks a part of a set file of the Cloud's records for
// an engine's restore, as RestoreValues begins: its addresses, and the start
// of its merge's preparation, everything before that reads the cache's
// state (checkValueMerge). Its caller leaves bundle unchanged until the part
// is merged.
func (c *Cache) CheckRestoreValues(bundle ValueBundle) (*CheckedValues, error) {
	began := time.Now()
	if c.blobBacked {
		return nil, fmt.Errorf("merge values: the Cloud merges from engine caches")
	}
	if err := checkSenderAddresses(cloudCacheID, bundle); err != nil {
		return nil, err
	}
	prep, err := c.checkValueMerge(cloudCacheID, bundle)
	if err != nil {
		return nil, err
	}
	return &CheckedValues{cache: c, input: bundle, prep: prep, took: time.Since(began)}, nil
}

// RestoreCheckedValues merges a checked part (CheckRestoreValues) as
// RestoreValues merges a part: it prepares the rest of its merge as the
// cache stands and commits it, with the same reply, errors, locks and hold.
// The reply's Prepare is the rest of the preparation, without the check.
func (c *Cache) RestoreCheckedValues(ctx context.Context, checked *CheckedValues, unretained []TransferOrdinal, source string) (MergeReply, error) {
	switch {
	case checked == nil:
		return MergeReply{}, errors.New("restore values: no checked part")
	case checked.cache != c:
		return MergeReply{}, errors.New("restore values: the part was checked for another cache")
	case !checked.used.CompareAndSwap(false, true):
		return MergeReply{}, errors.New("restore values: the checked part was merged already")
	}
	ctx = withCacheWork(ctx, workRestore)
	began := time.Now()
	p, err := c.prepareCheckedMerge(ctx, cloudCacheID, checked.input, checked.prep)
	checked.prep = nil
	if err != nil {
		return MergeReply{}, err
	}
	prepared := time.Now()
	p.restore, p.hold = true, true
	p.Unretained(unretained...)
	p.Source(MergeSource{Route: MergeRouteSetFile, File: source})
	reply, err := c.CommitValueMerge(ctx, p)
	reply.Prepare, reply.Commit = prepared.Sub(began), time.Since(prepared)
	return reply, err
}

// PreparedValueMerge is a merge prepared for one cache and sending cache,
// waiting for its one terminal action: CommitValueMerge, or Release when it
// won't be committed. Until then it holds a cache operation, so the cache
// doesn't close under it, and the prepared bundle, whose memory grows with
// the input's. The cache bounds neither how many prepared merges are open nor
// how long one waits, as it bounds no caller's concurrent MergeValues: the
// caller that prepares merges ahead of committing them bounds them.
type PreparedValueMerge struct {
	cache *Cache
	from  CacheID
	// input is the bundle as sent, for the commit to prepare in full again
	// if it installs a light record after all. Only an engine prepares
	// light records, and only then is input kept.
	input ValueBundle
	prep  *valueMergePrep
	op    cacheOperation
	// unretained are the bundle's roots, by ordinal, the commit gives no
	// retention edge (Unretained).
	unretained map[TransferOrdinal]bool
	// restore is set for a restore's merge (RestoreValues): its commit
	// queues the offers it attaches in the prefetcher's backlog, and hold
	// has it hold every entry it lands on until the reply's Release.
	restore bool
	hold    bool
	// source names where the bundle came from (Source).
	source MergeSource
	// ended is set once, by the terminal action.
	ended atomic.Bool
}

// Source names where the bundle came from, on an engine: the route that
// brought it and the service's number for it or its set file
// (MergeSource). The entries the merge installs keep it, and the first uses
// of their records are counted by it (FirstUseStats.Routes). Call it
// before CommitValueMerge.
func (p *PreparedValueMerge) Source(source MergeSource) {
	p.source = source
}

// Unretained has the commit give the bundle's roots of these ordinals no
// retention edge: on an engine, an engine's retention edge; on the Cloud,
// its stored root. Such a root's value lives, as a value nothing retains
// does on an engine, only through what holds it: the entries that depend
// on it, and holdings, its sender's among them. A remote cache service
// merges so the records an export carries for a root it doesn't send, as
// the lower half of a root's closure or the members of a root still
// running, so that they keep no retention of their own beyond the root
// that carries them. It is the merging caller's choice, never a bundle's:
// nothing an engine sends says it. Call it before CommitValueMerge.
func (p *PreparedValueMerge) Unretained(ordinals ...TransferOrdinal) {
	if p.unretained == nil {
		p.unretained = map[TransferOrdinal]bool{}
	}
	for _, ordinal := range ordinals {
		p.unretained[ordinal] = true
	}
}

// errValueMergeEnded is CommitValueMerge's answer for a prepared merge whose
// terminal action has already run.
var errValueMergeEnded = errors.New("commit value merge: the prepared merge has ended")

// PrepareValueMerge prepares a merge of input, from the cache from, as
// MergeValues does before it decides: it clones and validates the bundle,
// reserves the records' numbers, derives each record's identity, and decodes
// and validates every payload the merge will install. It reserves the
// numbers without egraphMu (takeResultIDs), and takes egraphMu to read the
// cache and, warming its targets' parts, to capture them. It reads input
// only while it runs, and keeps it only on an engine
// whose preparation prepared light records, for the commit to prepare in
// full again (PreparedValueMerge.input): that engine's caller leaves input
// unchanged until the prepared merge's terminal action. The Cloud never keeps
// it, and neither does an engine with no light record: their callers may
// change or drop input once PrepareValueMerge returns.
//
// A preparation that fails, or whose context is cancelled, returns no
// prepared merge.
func (c *Cache) PrepareValueMerge(ctx context.Context, from CacheID, input ValueBundle) (*PreparedValueMerge, error) {
	switch {
	case from == "":
		return nil, fmt.Errorf("merge values: no sending cache")
	case c.blobBacked && from == cloudCacheID:
		return nil, fmt.Errorf("merge values: the Cloud merges from engine caches")
	case !c.blobBacked && !senderCache(from):
		return nil, fmt.Errorf("merge values: an engine merges only from the Cloud or its image, not %q", from)
	}
	if !c.blobBacked {
		if err := checkSenderAddresses(from, input); err != nil {
			return nil, err
		}
	}
	prep, err := c.checkValueMerge(from, input)
	if err != nil {
		return nil, err
	}
	return c.prepareCheckedMerge(ctx, from, input, prep)
}

// prepareCheckedMerge is PrepareValueMerge's rest for a bundle
// checkValueMerge checked: the rest of the preparation as the cache stands
// (prepareCheckedValueMerge), the warming of its targets' parts, and the
// renumbering of its records.
func (c *Cache) prepareCheckedMerge(ctx context.Context, from CacheID, input ValueBundle, prep *valueMergePrep) (*PreparedValueMerge, error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return nil, err
	}
	prep, err = c.prepareCheckedValueMerge(ctx, prep, true)
	if err != nil {
		op.finish(false)
		return nil, err
	}
	p := &PreparedValueMerge{cache: c, from: from, prep: prep, op: op}
	if len(prep.light) > 0 {
		p.input = input
	}
	// The commit's warming captures only targets that changed since: its
	// caller commits under its own lock, the remote cache service's work
	// lock, and the captures take egraphMu for writing, which waits for
	// egraphMu's readers.
	// It renumbers its records too, as the commit will decide them: the
	// commit takes each one whose numbers still hold, and relocates any
	// other under the graph lock (relocateMergeRowsLocked).
	if len(prep.stale) == 0 {
		// The warming's and renumbering's waits for egraphMu are the
		// preparation's.
		warmCtx := withGraphLockWaits(ctx, prep.lockWaits)
		c.warmMergeTargetParts(warmCtx, prep)
		c.prerelocateMergeRows(warmCtx, prep)
	}
	// A prepare cancelled while it warmed fails, as one cancelled before
	// it did: its caller has no merge to commit or release.
	if err := context.Cause(ctx); err != nil {
		p.Release()
		return nil, err
	}
	return p, nil
}

// CommitValueMerge commits a prepared merge as MergeValues does: it selects
// warming targets under egraphMu.RLock, releases it before capturing their
// complete parts, waits for attachments, decides under egraphMu by the cache
// as it stands, prepares in full again outside egraphMu when the decision
// installs a light record, commits, and runs the commit's finish, with
// MergeValues' committed-with-error result. It is the prepared merge's
// terminal action whatever it returns, but for a prepared merge of another
// cache, which it refuses and leaves to its owner.
func (c *Cache) CommitValueMerge(ctx context.Context, p *PreparedValueMerge) (MergeReply, error) {
	switch {
	case p == nil:
		return MergeReply{}, errors.New("commit value merge: no prepared merge")
	case p.cache != c:
		return MergeReply{}, errors.New("commit value merge: the merge was prepared for another cache")
	case !p.ended.CompareAndSwap(false, true):
		return MergeReply{}, errValueMergeEnded
	}
	defer p.op.finish(false)
	from, input, prep, restore, hold := p.from, p.input, p.prep, p.restore, p.hold
	// The commit, and the first uses its warming makes, are the merge's, or
	// the restore's.
	work := workMerge
	if restore {
		work = workRestore
	}
	ctx = withCacheWork(ctx, work)
	p.input, p.prep = ValueBundle{}, nil
	prep.unretained = p.unretained
	var origin *mergeOrigin
	if !c.blobBacked {
		origin = &mergeOrigin{route: p.source.Route, number: p.source.Merge, file: p.source.File}
	}
	prep.origin = origin
	if len(prep.stale) > 0 {
		return c.staleMergeReply(prep.stale, prep.staleReasons()), nil
	}
	// Reading an entry's complete parts captures its record, which needs the
	// graph lock free. The reply reads them back only while that capture
	// still describes the entry's value.
	warming := time.Now()
	if c.testWarmStarted != nil {
		c.testWarmStarted()
	}
	c.warmMergeTargetParts(ctx, prep)
	warmed := time.Since(warming)
	if c.testBeforeTransferCommit != nil {
		c.testBeforeTransferCommit()
	}
	for {
		asked := time.Now()
		c.lockGraphFor(ctx, work)
		locked := time.Now()
		if c.testMergeCommitLocked != nil {
			c.testMergeCommitLocked()
		}
		// Each row's current entry is looked up once in a hold, for every
		// step of the commit that reads it: nothing else writes meanwhile.
		targets := c.mergeTargetsLocked(prep)
		if target, wait := c.mergeAttachmentWaitLocked(prep, targets); wait != nil {
			c.egraphMu.Unlock()
			if c.testMergeWaitsOnAttachment != nil {
				c.testMergeWaitsOnAttachment(target)
			}
			select {
			case <-wait:
			case <-ctx.Done():
				return MergeReply{}, context.Cause(ctx)
			}
			continue
		}
		commit, err := c.planValueMergeLocked(ctx, from, prep, targets)
		var stale *mergeStaleError
		if errors.As(err, &stale) {
			c.egraphMu.Unlock()
			return c.staleMergeReply(stale.ordinals, stale.why), nil
		}
		if errors.Is(err, errMergeNeedsFullPrep) {
			c.egraphMu.Unlock()
			waited := prep.lockWaits
			if prep, err = c.prepareValueMerge(ctx, from, input, false); err != nil {
				return MergeReply{}, err
			}
			prep.lockWaits.addAll(waited)
			prep.unretained = p.unretained
			prep.origin = origin
			if len(prep.stale) > 0 {
				return c.staleMergeReply(prep.stale, prep.staleReasons()), nil
			}
			continue
		}
		if err != nil {
			c.egraphMu.Unlock()
			return MergeReply{}, err
		}
		commit.restore, commit.hold = restore, hold
		reply, parts, finish := c.commitValueMergeLocked(ctx, commit)
		reply.Committed, reply.CommitHold, reply.Warm = true, time.Since(locked), warmed
		reply.LockWait = locked.Sub(asked)
		reply.PrepareLockWait, reply.PrepareLockWaits = prep.lockWaits.total(), prep.lockWaits.byHolder()
		var stored []*sharedResult
		merged := 0
		for _, row := range commit.rows {
			switch {
			case !row.action.installs():
			case c.blobBacked:
				stored = append(stored, row.res)
			case row.firstUse != nil:
				merged++
			}
		}
		c.egraphMu.Unlock()
		c.finishMergeReply(&reply, parts)
		c.queueStoredBodies(stored)
		if origin != nil {
			sent := 0
			for _, value := range prep.bundle.Values {
				if value.Held == "" {
					sent++
				}
			}
			c.mergeCommitted(origin, sent, merged)
		}
		// A recording engine records the commit's hold of the graph lock as
		// an op of the merge's.
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.merge.commit", wcprof.OpOpts{}, locked, locked.Add(reply.CommitHold), wcprof.OutcomeOK)
		err = finish(ctx)
		if c.testAfterTransferCommit != nil {
			c.testAfterTransferCommit()
		}
		return reply, err
	}
}

// Release is a prepared merge's terminal action when it won't be committed:
// it drops the prepared bundle and ends the cache operation. The numbers the
// preparation reserved stay unused, as gaps: the counter isn't rewound. After
// the terminal action, and on a nil prepared merge, Release does nothing, so
// a caller can defer it.
func (p *PreparedValueMerge) Release() {
	if p == nil || !p.ended.CompareAndSwap(false, true) {
		return
	}
	p.input, p.prep = ValueBundle{}, nil
	p.op.finish(false)
}

// MergeReply is what a merge committed, read under the lock that committed it.
type MergeReply struct {
	// Values has one entry per merged record, in dependency order.
	Values []MergedValue
	// Roots has one entry per root of the bundle.
	Roots []MergedRoot
	// Generation and EngineTimeUnixNano are the cache's start count and clock
	// at the commit. They order the reply's retention among the cache's other
	// observations.
	Generation         uint64
	EngineTimeUnixNano int64
	// Change is, on the Cloud, what the sender's holdings changed, for the
	// service's next CollectRemoteHoldings.
	Change RemoteChange
	// Committed reports that the merge changed the cache. A committed merge
	// can still return an error, from releasing a replaced value after the
	// lock; the reply then describes what the cache holds all the same.
	Committed bool
	// Stale are the held rows the cache didn't hold when the merge decided.
	// A merge with stale rows commits nothing: the reply has only them, and
	// the generation and clock.
	Stale []TransferOrdinal
	// StaleWhy says, for each of Stale in order, which check found it stale,
	// for the Cloud's log. It isn't sent to the engine.
	StaleWhy []string
	// CommitHold is how long the commit held egraphMu, from taking it to
	// releasing it, and LockWait how long the commit waited to take it.
	CommitHold time.Duration
	LockWait   time.Duration
	// Release, on a restore's reply (RestoreValues), releases the commit's
	// hold of the entries it landed on; it does something once. Nil on any
	// other merge's.
	Release func(context.Context) error
	// Rows counts the bundle's records by what the commit did with each.
	Rows MergeRowCounts
	// Warm is how long the commit took to capture its targets' complete
	// parts before it decided (warmMergeTargetParts). Each capture takes
	// egraphMu for writing, briefly, so it waits for egraphMu's readers.
	Warm time.Duration
	// Prepare and Commit are, on a restore's reply (RestoreValues), how long
	// the merge took to prepare and to commit, from end to end.
	Prepare, Commit time.Duration
	// PrepareLockWait is how long the merge's preparation waited for
	// egraphMu, in all, and PrepareLockWaits that time by the kind of writer
	// whose release let each wait in, as LookupLockWaits names them.
	PrepareLockWait  time.Duration
	PrepareLockWaits map[string]time.Duration
}

// MergeRowCounts counts a merge's records by what its commit did with each:
// a held row, which stands for the recipient's own value; a record that
// lands on an entry that keeps its value (kept): one the cache already had,
// or one another record of the bundle with the same recipe stores; one a new
// entry stores (created), one an entry with no value takes (stored), one
// that replaces an expired value in place (replaced), and one a new entry
// stores while the expired value it supersedes stays in use (retired).
//
// Of the kept records, on a blob-backed cache, KeptWithHolding are those
// whose sender's holding the cache had before the commit, and KeptTaught
// those of them whose identity taught the entry a class, a term or a digest
// all the same: what a held row in their place wouldn't have taught it. On
// an engine, Unchanged are the kept records of the Cloud's whose entry
// merged them already, unchanged since: the commit didn't teach them again
// (unchangedCloudRowLocked).
type MergeRowCounts struct {
	Held, Kept, Created, Stored, Replaced, Retired int
	KeptWithHolding, KeptTaught                    int
	Unchanged                                      int
}

// count counts one decided row.
func (n *MergeRowCounts) count(row *mergeRow) {
	switch {
	case row.value.Held != "":
		n.Held++
	case row.action == mergeKeep:
		n.Kept++
		if row.keptWithHolding {
			n.KeptWithHolding++
		}
		if row.keptTaught {
			n.KeptTaught++
		}
		if row.unchanged {
			n.Unchanged++
		}
	case row.action == mergeCreate:
		n.Created++
	case row.action == mergeStore:
		n.Stored++
	case row.action == mergeReplace:
		n.Replaced++
	case row.action == mergeRetire:
		n.Retired++
	}
}

// MergedValue is one merged record's target as the commit left it.
type MergedValue struct {
	Ordinal      TransferOrdinal
	Number       uint64
	Replacements uint64
	// Revision is the target's revisions as the commit left them, for
	// KnownRows on an engine.
	Revision RowRevision
	// ExpiresAtUnix is the target's own expiry (0: none).
	ExpiresAtUnix int64
	// Deps are the target's actual direct dependencies by number: a kept
	// value's own, which can differ from the record's.
	Deps []uint64
	// Parts are the target's complete parts, and OfferedParts those it has an
	// offer of and not the bytes.
	Parts        []PersistedPartAddress
	OfferedParts []PersistedPartAddress
	// Stored reports that the commit stored the record's value on the
	// target: a new entry, one with no value, or one in place of an expired
	// value (MergeRowCounts' created, stored, replaced and retired).
	Stored bool
}

// MergedRoot is one root of the bundle: its target and retention, or expired.
type MergedRoot struct {
	Ordinal TransferOrdinal
	// Expired marks a root that had expired when the merge decided: it was
	// skipped, with whatever only it needed.
	Expired                bool
	Number                 uint64
	Retained               bool
	RetentionExpiresAtUnix int64
}

// Imported returns the unexpired roots' numbers.
func (r MergeReply) Imported() []ImportedValue {
	out := make([]ImportedValue, 0, len(r.Roots))
	for _, root := range r.Roots {
		if !root.Expired {
			out = append(out, ImportedValue{Ordinal: root.Ordinal, ResultID: root.Number})
		}
	}
	return out
}

// valueMergePrep is what merge prepares outside the graph lock: the validated
// bundle, its records in dependency order, and each record's identity, derived
// in a private cache under provisional numbers. A record that becomes a new
// entry keeps its provisional number, firstID + ordinal - 1.
type valueMergePrep struct {
	// senderImported are, on the Cloud, the rows whose record its sender
	// imported, by ordinal: the sender's envelope's mark, read before the
	// prepared record imported (mergeRunLocked).
	senderImported map[uint64]bool
	// payloadPreparing is the cache's testRecordPayloadPreparing.
	payloadPreparing func(uint64) error
	// lockWaits gathers the preparation's waits for egraphMu, by the kind of
	// writer that let each in (MergeReply.PrepareLockWaits): its own
	// sections', and its warming's.
	lockWaits *graphLockWaits
	// prerelocated are the records relocated before the commit took
	// egraphMu, by ordinal (prerelocateMergeRows); the commit takes each
	// whose numbers still hold.
	prerelocated map[uint64]*prerelocatedRecord
	// from is the cache the bundle is from.
	from   CacheID
	bundle ValueBundle
	// unretained are the bundle's roots the commit gives no retention edge
	// (PreparedValueMerge.Unretained).
	unretained map[TransferOrdinal]bool
	order      []uint64
	index      map[uint64]*TransferredValue
	// rows are the bundle's ordinals by sender number, which its records'
	// own references name rows by, and senders the reverse
	// (transferSenderNumbers).
	rows    map[uint64]uint64
	senders transferSenderNumbers
	firstID uint64
	plans   map[uint64]transferIdentityPlan
	// records are the bundle's records at their provisional numbers, by
	// ordinal, and refs the provisional numbers each one references, itself
	// included. A light record is relocated in its call and offers only, and
	// has no refs.
	records map[uint64]PersistedRecord
	refs    map[uint64][]uint64
	// recordBytes are, on a blob-backed cache only, each record's size as
	// persistence writes it, by ordinal (encodedRecordBytes), measured once
	// the record is prepared with its payload: relocation changes only
	// reference numbers.
	recordBytes map[uint64]int64
	// completeParts are the part keys each record but a light one proves
	// complete, by ordinal. A part key is an address and its completeness,
	// which the record's payload decides; relocation rewrites only reference
	// numbers, never to or from zero, so the keys read at provisional numbers
	// are the ones at the final numbers.
	completeParts map[uint64][]string
	// partFacts are, on a blob-backed cache, each record's parts as the
	// Cloud keeps them with it (partFacts), by ordinal, for a record
	// prepared with its payload: those it declares absent or pending, as
	// its outputs map them, and its complete ones, each read from addresses
	// and states, which relocation leaves as sent. An engine keeps none.
	partFacts map[uint64]*partFacts
	// light are the records prepared from their call and offers only: the
	// cache held a value for their recipe that merge keeps.
	light map[uint64]bool
	// unprepared are, on an engine, the records whose values carry their
	// parts (TransferredValue.Parts), the Cloud's: each is prepared from
	// its call and offers only, its complete parts are the ones it carries,
	// and an install leaves its payload for its first use
	// (mergedFirstUse). origin is, on an engine, the merge that installs
	// them (mergeOrigin), nil on the Cloud.
	unprepared map[uint64]bool
	origin     *mergeOrigin
	// held are the held rows, by ordinal. Each is prepared from its
	// recipe's current entry's call frame, with no record or payload, and
	// its plan has only its recipe. stale are the held rows the cache didn't
	// hold at preparation: a merge with any commits nothing, and its
	// preparation stops once they are known.
	held  map[uint64]bool
	stale []TransferOrdinal
	// callOnly are the held rows outside the roots' closure, which a call
	// alone names as the row placing an input (TransferredCall.Inputs): no
	// row of the merge, never stale, each read when its call is taught.
	callOnly map[uint64]bool
	// staleWhy says why each of stale is stale (markStale).
	staleWhy map[TransferOrdinal]string
	// heldParts are, on the Cloud, the outputs an engine's bundle puts on
	// its held rows, by ordinal, which the commit checks against the
	// Cloud's own copies of the rows (staleHeldPartsLocked).
	heldParts map[uint64][]TransferredOutput
	// early is the decision of the rows made as the records were prepared,
	// on an engine (earlyMergeRows), or nil: each record that installs is
	// relocated to its final numbers as it is prepared, where it can be
	// (earlyNumbers), and only the others are relocated after.
	early *valueMergeCommit
	// calls are the bundle's calls, each with its identity derived from
	// its encoding, for the rows of the entries they returned.
	calls []mergedCall
}

// mergedCall is a bundle's call to teach onto the entry of a row: its
// identity, and by input the row placing it, 0 for a witness
// (TransferredCall.Inputs).
type mergedCall struct {
	value  uint64
	id     callIdentity
	inputs []uint64
}

// mergeStaleError is planValueMergeLocked's answer when a held row doesn't
// hold: the merge commits nothing, and answers with the stale rows.
type mergeStaleError struct {
	ordinals []TransferOrdinal
	// why says, for each of ordinals, which check found it stale.
	why []string
}

func (e *mergeStaleError) Error() string {
	return fmt.Sprintf("merge values: %d held rows are stale", len(e.ordinals))
}

// staleMergeReply is the reply of a merge refused for stale held rows.
func (c *Cache) staleMergeReply(stale []TransferOrdinal, why []string) MergeReply {
	c.egraphMu.RLock()
	generation := c.identity.Generation
	c.egraphMu.RUnlock()
	return MergeReply{Generation: generation, EngineTimeUnixNano: time.Now().UnixNano(), Stale: stale, StaleWhy: why}
}

// errMergeNeedsFullPrep is planValueMergeLocked's answer when it installs a
// light record: the merge prepares the bundle in full and decides again.
var errMergeNeedsFullPrep = errors.New("merge values: a light record is installed")

// prepareValueMerge validates the bundle and derives each record's identity.
// On an engine, the bundle's outputs with chains become offers embedded in
// their records; the Cloud drops the bundle's parts, since the upload decides
// what it stores.
//
// A record's identity comes from its call. With skipKept, on an engine, a
// record whose recipe's current entry holds a value merge would keep, as the
// cache stands, is prepared light: from its call and offers only, since merge
// doesn't install it. Its payload is neither validated by its codec nor
// relocated; its shape, its call's references and its offers' are checked
// (validateTransferRecordShape).
//
// It returns ctx's cause when ctx is cancelled: it checks before each payload
// and once more at the end.
func (c *Cache) prepareValueMerge(ctx context.Context, from CacheID, input ValueBundle, skipKept bool) (*valueMergePrep, error) {
	prep, err := c.checkValueMerge(from, input)
	if err != nil {
		return nil, err
	}
	return c.prepareCheckedValueMerge(ctx, prep, skipKept)
}

// checkValueMerge is the start of a merge's preparation (prepareValueMerge),
// everything before it reads the cache's state: it clones the bundle,
// resolves its chains' prefixes, validates it, embeds its parts as offers on
// an engine, and derives its calls' identities. It reads only what the cache
// is, an engine's or the Cloud's, so nothing the cache does meanwhile changes
// what it returns, and the rest of the preparation (prepareCheckedValueMerge)
// can follow it at any later time.
func (c *Cache) checkValueMerge(from CacheID, input ValueBundle) (*valueMergePrep, error) {
	bundle, err := cloneValueBundle(input)
	if err != nil {
		return nil, err
	}
	if from == imageCacheID {
		// An expiry the image's bundle carries is the one the cache that
		// built the image gave an entry, such as the lifetime of the module
		// function that returned it. The image outlives it, so the merge
		// drops it.
		for i := range bundle.Roots {
			bundle.Roots[i].ExpiresAtUnix = 0
		}
		for i := range bundle.Values {
			bundle.Values[i].ExpiresAtUnix = 0
		}
	}
	// A prefix output becomes the offer of the chain layers it names.
	if err := ResolveChainPrefixes(&bundle); err != nil {
		return nil, err
	}
	// The bundle is validated once, before its outputs become offers: each
	// output with a chain is checked as the offer it becomes, against the
	// same slots, and the ownership walk takes its owner's dependencies in
	// the same order. Each payload merge uses is validated below.
	order, err := checkValueBundle(bundle, true, nil, carriedDeclaredParts(c, bundle))
	if err != nil {
		return nil, err
	}
	// The Cloud drops the parts below, but checks those on held rows against
	// its own copies of the rows when it commits (staleHeldPartsLocked).
	var heldParts map[uint64][]TransferredOutput
	if c.blobBacked {
		heldParts = heldRowOutputs(bundle)
	}
	c.embedMergedParts(&bundle)
	var recordBytes map[uint64]int64
	var facts map[uint64]*partFacts
	if c.blobBacked {
		// Dropping the parts drops their owners' edges, so the roots'
		// closure is walked again without them. Its rows were checked above,
		// and dropping their parts changed nothing else of them.
		rows := make(bundleRows, len(bundle.Values)+1)
		for i := range bundle.Values {
			rows[bundle.Values[i].Ordinal] = &bundle.Values[i]
		}
		if order, err = bundleClosure(bundle, rows, nil); err != nil {
			return nil, err
		}
		recordBytes = map[uint64]int64{}
		facts = map[uint64]*partFacts{}
	}
	prep := &valueMergePrep{lockWaits: &graphLockWaits{}, payloadPreparing: c.testRecordPayloadPreparing, from: from, bundle: bundle, order: order, index: map[uint64]*TransferredValue{}, plans: map[uint64]transferIdentityPlan{}, records: map[uint64]PersistedRecord{}, refs: map[uint64][]uint64{}, recordBytes: recordBytes, completeParts: map[uint64][]string{}, partFacts: facts, held: map[uint64]bool{}, heldParts: heldParts}
	prep.rows = make(map[uint64]uint64, len(prep.bundle.Values))
	for i := range prep.bundle.Values {
		row := &prep.bundle.Values[i]
		prep.index[uint64(row.Ordinal)] = row
		if c.blobBacked && row.Record.Envelope.Imported {
			if prep.senderImported == nil {
				prep.senderImported = map[uint64]bool{}
			}
			prep.senderImported[uint64(row.Ordinal)] = true
		}
		prep.rows[row.SenderNumber] = uint64(row.Ordinal)
	}
	prep.senders = transferSenders(prep.bundle)
	prep.markUnprepared(c)
	if prep.calls, err = c.prepareMergedCalls(prep.bundle.Calls); err != nil {
		return nil, err
	}
	// The rows outside the order, the roots' closure, are the held rows a
	// call alone names (checkValueBundle).
	if len(order) < len(prep.bundle.Values) {
		inOrder := make(map[uint64]bool, len(order))
		for _, id := range order {
			inOrder[id] = true
		}
		prep.callOnly = map[uint64]bool{}
		for _, row := range prep.bundle.Values {
			if id := uint64(row.Ordinal); !inOrder[id] {
				prep.callOnly[id] = true
			}
		}
	}
	return prep, nil
}

// prepareCheckedValueMerge is the rest of a merge's preparation
// (prepareValueMerge) for a bundle checkValueMerge checked: it resolves the
// held rows and derives each record's identity as the cache stands, decides
// which records are light with skipKept, and prepares the others' payloads.
func (c *Cache) prepareCheckedValueMerge(ctx context.Context, prep *valueMergePrep, skipKept bool) (*valueMergePrep, error) {
	ctx = withGraphLockWaits(ctx, prep.lockWaits)
	heldFrames := c.resolveHeldRows(ctx, prep)
	if len(prep.stale) > 0 {
		return prep, nil
	}

	// The records' numbers are the merge's own from here, taken without the
	// graph lock (takeResultIDs): no entry takes one until the commit, under
	// egraphMu, and those it doesn't take stay gaps.
	prep.firstID = uint64(c.takeResultIDs(uint64(len(prep.bundle.Values))))

	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		if prep.index[ref.ResultID] == nil {
			return fmt.Errorf("missing transfer reference %d", ref.ResultID)
		}
		ref.ResultID = prep.firstID + ref.ResultID - 1
		return nil
	}
	// A record names itself and the rows it references by the sender's
	// numbers, as the sender stores it; the bundle's own offers name rows
	// by ordinal (relocate).
	relocateRecord := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		ordinal, ok := prep.rows[ref.ResultID]
		if !ok {
			return fmt.Errorf("transfer reference %d names no row", ref.ResultID)
		}
		ref.ResultID = ordinal
		return relocate(ref)
	}
	// Every record is first relocated in its call and offers, which is all
	// its identity and the bundle's ownership graph read. Relocation maps
	// ordinals one to one onto nonzero numbers, so what the validation
	// checked at ordinals holds at provisional numbers: neither the records
	// nor the private cache's ownership are checked again.
	private, err := c.privateMergeCache(ctx, prep, heldFrames, relocateRecord, relocate)
	if err != nil {
		return nil, err
	}
	if err := c.planMergeIdentities(prep, private); err != nil {
		return nil, err
	}
	if err := prep.checkHeldRecipes(); err != nil {
		return nil, err
	}

	if skipKept && !c.blobBacked {
		prep.light = c.keptMergeRecords(ctx, prep)
	}
	// The rows are decided now, as the cache stands, so that a record is
	// relocated to its final numbers as it is prepared, with one decode of
	// its payload (earlyMergeRows), rather than decoded again to be
	// relocated after (prerelocateMergeRows). On the Cloud, a record's size
	// is measured so, at the numbers it is installed at (recordBytes).
	if !c.testNoPrerelocation {
		prep.early = c.earlyMergeRows(ctx, prep)
	}
	// Every other record is prepared with its payload.
	if err := prep.prepareRecordPayloads(ctx, relocateRecord, relocate); err != nil {
		return nil, err
	}
	// A preparation cancelled after its last payload, or with every record
	// light, returns its cause all the same.
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if !c.testKeepRecordCopies {
		prep.dropRecordCopies(private)
	}
	return prep, nil
}

// dropRecordCopies lets go of the copies of the bundle's records that only
// the preparation read, once every record is prepared: each row's record
// but its offers, which the commit's closure reads (liveClosure), and each
// private entry's envelope, which shares that record's payload. The commit
// reads each record as prepared (records), each row's own fields, and of
// each private entry its frame, description and record type, so it commits
// the same merge; a prepared merge waiting for its commit holds only that.
// After preparation, read a row's record, its call and payload, from records,
// never from the bundle's row.
func (prep *valueMergePrep) dropRecordCopies(private *Cache) {
	for i := range prep.bundle.Values {
		row := &prep.bundle.Values[i]
		row.Record = PersistedRecord{Envelope: PersistedResultEnvelope{PendingOffers: row.Record.Envelope.PendingOffers}}
	}
	for _, res := range private.resultsByID {
		res.persistedEnvelope = nil
	}
}

// prepareMergedCalls derives the identity of each of a bundle's calls from its
// encoding. Only an engine merges calls: the Cloud learns calls from spans.
func (c *Cache) prepareMergedCalls(calls []TransferredCall) ([]mergedCall, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if c.blobBacked {
		return nil, fmt.Errorf("merge values: the bundle carries calls, which only an engine merges")
	}
	merged := make([]mergedCall, 0, len(calls))
	for i, transferred := range calls {
		var pb callpbv1.Call
		if err := pb.Decode(transferred.Call); err != nil {
			return nil, fmt.Errorf("merge values: call %d: %w", i, err)
		}
		id, err := callIdentityFromPB(&pb)
		if err != nil {
			return nil, fmt.Errorf("merge values: call %d: %w", i, err)
		}
		// Every input but a witness names the row that places it: the
		// call is taught onto its entry over its inputs' classes, and an
		// input the bundle places nowhere would leave its term unknown.
		if len(transferred.Inputs) != len(id.inputs) {
			return nil, fmt.Errorf("merge values: call %d: %d input rows for %d inputs", i, len(transferred.Inputs), len(id.inputs))
		}
		inputs := make([]uint64, len(id.inputs))
		for j, row := range transferred.Inputs {
			switch {
			case id.witnesses[j] && row != 0:
				return nil, fmt.Errorf("merge values: call %d: input %d is a witness, placed by row %d", i, j, row)
			case !id.witnesses[j] && row == 0:
				return nil, fmt.Errorf("merge values: call %d: input %d is placed by no row", i, j)
			}
			inputs[j] = uint64(row)
		}
		merged = append(merged, mergedCall{value: uint64(transferred.Value), id: id, inputs: inputs})
	}
	return merged, nil
}

// privateMergeCache builds the private cache a preparation derives the
// records' identities in: each record at its provisional number, relocated
// in its call and offers, a held row standing in with its entry's own call
// frame (heldFrames), and the bundle's ownership graph, with its offers'
// owners. It holds no lock: the private cache is the preparation's own.
func (c *Cache) privateMergeCache(ctx context.Context, prep *valueMergePrep, heldFrames map[uint64]entryFrame, relocateRecord, relocate PersistedRefVisitor) (*Cache, error) {
	private := &Cache{resultsByID: map[sharedResultID]*sharedResult{}, testRecipeFromBody: c.testRecipeFromBody}
	for _, id := range prep.order {
		row := prep.index[id]
		if prep.held[id] {
			// The held row stands in with its entry's own call frame, whose
			// recipe the rows that depend on it derive theirs from: decoded,
			// or held in the entry's stored body with its recipe. The frame
			// is the cache's: nothing below relocates or rewires it.
			rec := PersistedRecord{ResultID: prep.firstID + id - 1}
			prep.records[id] = rec
			res := &sharedResult{id: sharedResultID(rec.ResultID), imported: true, expiresAtUnix: row.ExpiresAtUnix}
			if frame := heldFrames[id]; frame.call != nil {
				res.storeResultCall(frame.call)
			} else {
				res.stored = frame.body
			}
			private.putResultLocked(res)
			continue
		}
		rec := row.Record
		rec.ResultID = prep.firstID + id - 1
		rec.Envelope.ResultID = rec.ResultID
		rec.Envelope.Imported = true
		rec.Call = rec.Call.clone()
		if err := visitResultCallReferences(rec.Call, relocateRecord); err != nil {
			return nil, err
		}
		offers, err := clonePartOffers(rec.Envelope.PendingOffers)
		if err != nil {
			return nil, err
		}
		for i := range offers {
			if err := visitPersistedPartOffer(&offers[i], nil, relocate); err != nil {
				return nil, err
			}
		}
		rec.Envelope.PendingOffers = offers
		deps := make(map[sharedResultID]struct{}, len(row.DependencyIDs))
		for _, dep := range row.DependencyIDs {
			deps[sharedResultID(prep.firstID+dep-1)] = struct{}{}
		}
		prep.records[id] = rec
		res := &sharedResult{id: sharedResultID(rec.ResultID), imported: true, isObject: rec.Envelope.Kind == persistedResultKindObject, persistedEnvelope: &rec.Envelope, deps: deps, expiresAtUnix: row.ExpiresAtUnix, sessionResourceHandle: rec.Envelope.SessionResourceHandle, boundSession: rec.Envelope.BoundSession}
		res.storeResultCall(rec.Call)
		if !c.blobBacked {
			// The private offers attach to the sender's holding, as they will
			// in the cache.
			res.noteSenderCopyLocked(prep.from, row.SenderNumber, true, row.ExpiresAtUnix)
		}
		private.putResultLocked(res)
	}
	for _, res := range private.resultsByID {
		if prep.held[uint64(res.id)-prep.firstID+1] {
			continue
		}
		if err := walkTransferCalls(res.loadResultCall(), func(*ResultCall) error { return nil }, func(ref *ResultCallRef) error {
			ref.shared = private.resultsByID[sharedResultID(ref.ResultID)]
			return nil
		}); err != nil {
			return nil, err
		}
		for depID := range res.deps {
			dep := private.resultsByID[depID]
			private.rememberDependencyEdgeLocked(res, dep)
			private.incrementIncomingOwnershipLocked(ctx, dep)
		}
	}
	// The bundle's offers are checked in its own ownership graph here, and
	// against the cache's when the merge decides.
	if err := private.restoreOfferOwnersLocked(ctx); err != nil {
		return nil, err
	}
	return private, nil
}

// planMergeIdentities derives each record's identity in the private cache,
// in dependency order; a held row's is its recipe alone.
func (c *Cache) planMergeIdentities(prep *valueMergePrep, private *Cache) error {
	for i, id := range prep.order {
		res := private.resultsByID[sharedResultID(prep.firstID+id-1)]
		plan := transferIdentityPlan{row: res, recipe: prep.index[id].Held}
		if !prep.held[id] {
			var err error
			if plan, err = private.transferIdentityPlanOf(res); err != nil {
				return err
			}
		}
		prep.plans[id] = plan
		if c.testTransferPlanPrepared != nil {
			if err := c.testTransferPlanPrepared(i + 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// preparesPayload reports whether the merge prepares record id with its
// payload: one that isn't light, held, or left for its first use
// (valueMergePrep.unprepared).
func (prep *valueMergePrep) preparesPayload(id uint64) bool {
	return !prep.light[id] && !prep.held[id] && !prep.unprepared[id]
}

// preparesPayloads reports whether the merge prepares any record with its
// payload (preparesPayload). A restore of a set file, whose every record
// carries its parts, prepares none.
func (prep *valueMergePrep) preparesPayloads() bool {
	return slices.ContainsFunc(prep.order, prep.preparesPayload)
}

// prepareRecordPayloads prepares every record but a light one with its
// payload (prepareRecordPayload), on up to GOMAXPROCS goroutines
// (forEachIndexParallel), and applies the prepared records in dependency
// order. A record's preparation reads only what the merge fixed before it and
// writes only its own result, so each is prepared as it would be alone. It
// returns what preparing them one by one would: ctx's cause when ctx was
// cancelled before the first record in dependency order that failed was
// started, or once a record before it was prepared; otherwise that record's
// failure.
func (prep *valueMergePrep) prepareRecordPayloads(ctx context.Context, relocateRecord, relocate PersistedRefVisitor) error {
	ids := make([]uint64, 0, len(prep.order))
	for _, id := range prep.order {
		if prep.preparesPayload(id) {
			ids = append(ids, id)
		}
	}
	prepared := make([]preparedRecordPayload, len(ids))
	// cancelled[i] is whether ctx was cancelled once record i was prepared,
	// when preparing the records one by one wouldn't have started the next.
	cancelled := make([]bool, len(ids))
	failures := make([]bool, len(ids))
	if err := forEachIndexParallel(len(ids), func(i int) (err error) {
		defer func() { failures[i] = err != nil }()
		if err := context.Cause(ctx); err != nil {
			return err
		}
		if prep.payloadPreparing != nil {
			if err := prep.payloadPreparing(ids[i]); err != nil {
				return err
			}
		}
		if prepared[i], err = prep.prepareRecordPayload(ids[i], relocateRecord, relocate, prep.earlyNumbers(ids[i])); err != nil {
			return err
		}
		cancelled[i] = context.Cause(ctx) != nil
		return nil
	}); err != nil {
		if slices.Contains(cancelled[:slices.Index(failures, true)], true) {
			return context.Cause(ctx)
		}
		return err
	}
	for i, id := range ids {
		p := prepared[i]
		if p.numbers != nil {
			// Relocated to its final numbers as it was prepared: the
			// merge keeps the record at those alone, and its call and
			// offers at its provisional numbers too, for a commit that
			// keeps the entry's value instead.
			if prep.prerelocated == nil {
				prep.prerelocated = map[uint64]*prerelocatedRecord{}
			}
			// One-to-one by earlyNumbers.
			prep.prerelocated[id] = newPreparedRelocation(p.record, p.numbers)
			call := prep.records[id].Call
			prep.records[id] = PersistedRecord{ResultID: prep.firstID + id - 1, Call: call, Envelope: PersistedResultEnvelope{PendingOffers: p.offers}}
		} else {
			prep.records[id] = p.record
		}
		prep.refs[id] = p.refs
		prep.completeParts[id] = p.completeParts
		if prep.partFacts != nil {
			prep.partFacts[id] = &partFacts{declared: p.declared, complete: sharedPartKeys(p.completeParts)}
		}
		if prep.recordBytes != nil {
			prep.recordBytes[id] = p.recordBytes
		}
	}
	return nil
}

// preparedRecordPayload is a record prepared with its payload
// (prepareRecordPayload), which prepareRecordPayloads applies to the merge's
// preparation. A record relocated to its final numbers as it was prepared
// has the final number of each provisional number it references, in
// numbers, and its offers at their provisional numbers too, in offers.
type preparedRecordPayload struct {
	record        PersistedRecord
	refs          []uint64
	completeParts []string
	declared      []declaredPart
	recordBytes   int64
	numbers       map[uint64]sharedResultID
	offers        []PersistedPartOffer
}

// earlyMergeRows decides the bundle's rows under the read lock as the commit
// would decide them as the cache stands (decideMergeRowsLocked), for each
// record's preparation to relocate it to its final numbers (earlyNumbers).
// It decides nothing, and returns nil, when the decision installs a light
// record: the commit prepares the bundle in full again first. Nor does it
// decide anything for a merge that prepares no record with its payload
// (preparesPayloads), as a restore of a set file, whose every record
// carries its parts: there is nothing to relocate so, and the merge decides
// its rows once, in its commit.
func (c *Cache) earlyMergeRows(ctx context.Context, prep *valueMergePrep) *valueMergeCommit {
	if !prep.preparesPayloads() {
		return nil
	}
	commit := &valueMergeCommit{prep: prep, byOrdinal: map[uint64]*mergeRow{}, now: time.Now().Unix()}
	c.readLockGraphGathered(ctx)
	c.decideMergeRowsLocked(commit)
	c.egraphMu.RUnlock()
	if commit.installsLight() {
		return nil
	}
	return commit
}

// earlyNumbers returns the final number the early decision (earlyMergeRows)
// gives each provisional number, for the record of ordinal id, or nil when
// the record isn't to be relocated early: nothing was decided early, its row
// doesn't install its record, a row it can name was left out of the merge,
// or two rows it can name land on one entry. A record names only itself, its
// direct dependencies and, in its offers, their owners' dependencies and
// services, so with the function it returns its preparation relocates every
// reference, and to numbers that each name one provisional number: a commit
// that decides other numbers relocates the record from those
// (relocateMergeRowsLocked).
func (prep *valueMergePrep) earlyNumbers(id uint64) func(uint64) (sharedResultID, bool) {
	if prep.early == nil {
		return nil
	}
	row := prep.early.byOrdinal[id]
	if row == nil || !row.action.installs() {
		return nil
	}
	final := func(provisional uint64) (sharedResultID, bool) {
		decided := prep.early.byOrdinal[provisional-prep.firstID+1]
		if decided == nil {
			return 0, false
		}
		return decided.res.id, true
	}
	named := append([]uint64{id}, prep.index[id].DependencyIDs...)
	for _, offer := range prep.index[id].Record.Envelope.PendingOffers {
		named = append(named, offer.Owner.DependencyIDs...)
		for _, service := range offer.Value.Services {
			named = append(named, service.ServiceResultID)
		}
	}
	landed := make(map[sharedResultID]uint64, len(named))
	for _, ordinal := range named {
		n, ok := final(prep.firstID + ordinal - 1)
		if !ok {
			return nil
		}
		if other, ok := landed[n]; ok && other != ordinal {
			return nil
		}
		landed[n] = ordinal
	}
	return final
}

// checkHeldRecipes refuses a bundle that carries a held row and a record of
// the same recipe, as malformed: the held row is checked against the
// recipe's current entry, which the record could replace. It runs once every
// row's recipe is derived; a preparation that finds a held row stale has
// answered before then, committing nothing either way.
func (prep *valueMergePrep) checkHeldRecipes() error {
	if len(prep.held) == 0 {
		return nil
	}
	held := map[string]uint64{}
	for _, id := range prep.order {
		if prep.held[id] {
			held[prep.plans[id].recipe.String()] = id
		}
	}
	for _, id := range prep.order {
		if prep.held[id] {
			continue
		}
		if other, ok := held[prep.plans[id].recipe.String()]; ok {
			return fmt.Errorf("merge values: held row %d and row %d have the same recipe", other, id)
		}
	}
	return nil
}

// resolveHeldRows marks the bundle's held rows and returns, by ordinal, the
// call frame of each one's recipe's current entry, whose recipe it checks is
// the row's. A held row is stale (prep.stale, sorted) when the commit would
// find it so as the cache stands (planValueMergeLocked), or its entry's frame
// derives another recipe, so the list is complete for this moment. It
// derives the recipes after egraphMu is released, since deriving can read the
// cache.
func (c *Cache) resolveHeldRows(ctx context.Context, prep *valueMergePrep) map[uint64]entryFrame {
	frames := map[uint64]entryFrame{}
	c.readLockGraphGathered(ctx)
	now := time.Now().Unix()
	for _, row := range prep.bundle.Values {
		if row.Held == "" || prep.callOnly[uint64(row.Ordinal)] {
			continue
		}
		id := uint64(row.Ordinal)
		prep.held[id] = true
		cur := c.currentEntryForRecipeLocked(row.Held)
		why := c.heldRowStaleWhyLocked(cur, row.ExpiresAtUnix, now)
		var frame entryFrame
		if why == "" {
			if frame = cur.entryFrameLocked(); !frame.ok() {
				why = "no call frame"
			}
		}
		if why != "" {
			prep.markStale(row.Ordinal, why)
			continue
		}
		frames[id] = frame
	}
	c.egraphMu.RUnlock()
	for id, frame := range frames {
		if recipe, err := frame.recipeDigest(c); err != nil {
			prep.markStale(TransferOrdinal(id), fmt.Sprintf("recipe: %v", err))
		} else if recipe != prep.index[id].Held {
			prep.markStale(TransferOrdinal(id), "the entry's recipe is another")
		}
	}
	slices.Sort(prep.stale)
	return frames
}

// markStale records the held row ordinal as stale at preparation, and why.
func (prep *valueMergePrep) markStale(ordinal TransferOrdinal, why string) {
	prep.stale = append(prep.stale, ordinal)
	if prep.staleWhy == nil {
		prep.staleWhy = map[TransferOrdinal]string{}
	}
	prep.staleWhy[ordinal] = why
}

// staleReasons are why each of prep's stale rows is stale, in their order.
func (prep *valueMergePrep) staleReasons() []string {
	why := make([]string, len(prep.stale))
	for i, ordinal := range prep.stale {
		why[i] = prep.staleWhy[ordinal]
	}
	return why
}

// heldRowStaleLocked reports whether a held row with the expiry doesn't hold
// on cur, its recipe's current entry: merge would keep cur's value for a
// record of the recipe with that expiry (decideMergeRowLocked) only when cur
// has a value, and the value hasn't expired or the row has. Requires egraphMu.
func (c *Cache) heldRowStaleLocked(cur *sharedResult, expiresAtUnix, now int64) bool {
	return c.heldRowStaleWhyLocked(cur, expiresAtUnix, now) != ""
}

// heldRowStaleWhyLocked says why a held row on cur doesn't hold, as
// heldRowStaleLocked judges it, and "" when it holds.
func (c *Cache) heldRowStaleWhyLocked(cur *sharedResult, expiresAtUnix, now int64) string {
	switch {
	case cur == nil:
		return "no entry"
	case cur.noValueLocked():
		return "no value"
	case c.resultExpiredAtLocked(cur, now) && (expiresAtUnix == 0 || expiresAtUnix > now):
		return "expired"
	}
	return ""
}

// prepareRecordPayload validates the record of ordinal id with its payload,
// as validateTransferRecord does, and relocates it in full to its provisional
// numbers with relocate. One visit of the record's references both checks
// and relocates them, since visiting decodes its payload; its offers, whose
// references validateTransferRecordContent checks, are relocated apart. With
// final, the same visit relocates every reference on to the final number
// final gives its provisional number, which must give one, so the record is
// decoded and encoded once to reach the numbers the commit installs it at;
// the result's numbers are those final numbers, by provisional number, when
// any differs, and its offers the record's offers at provisional numbers. It
// returns the prepared record and changes nothing in prep.
func (prep *valueMergePrep) prepareRecordPayload(id uint64, relocateRecord, relocate PersistedRefVisitor, final func(uint64) (sharedResultID, bool)) (preparedRecordPayload, error) {
	row := prep.index[id]
	// The record's payloads are decoded once for its validation, its part
	// mapping and its part description, which all read them as sent; the
	// visit below checks and relocates its references.
	memos := payloadMemos{}
	direct, err := validateMemoTransferRecordContent(row.Record, prep.senders.dependencies(row.DependencyIDs), memos)
	if err != nil {
		return preparedRecordPayload{}, fmt.Errorf("row %d: %w", id, err)
	}
	outputs, err := mapMemoTransferredOutputs(row.Record, memos)
	if err != nil {
		return preparedRecordPayload{}, err
	}
	// The complete parts and the declared ones are read from the parts'
	// addresses and states, which relocation leaves as sent, so they're
	// described as sent. A record with an output at an address that has no
	// key declares none, as its answers read it when they decoded it: a
	// bundle of it fails as it normalizes it.
	completeParts := memoRecordCompletePartKeys(row.Record, memos)
	var declared []declaredPart
	if prep.partFacts != nil {
		declared, _ = declaredPartsOf(outputs)
	}
	var refs []uint64
	numbers := map[uint64]sharedResultID{}
	moved := false
	collect := func(relocate PersistedRefVisitor) PersistedRefVisitor {
		return func(ref *PersistedRef) error {
			if err := relocate(ref); err != nil {
				return err
			}
			if ref.RecipeID == nil && ref.ResultID != 0 {
				refs = append(refs, ref.ResultID)
				if final != nil {
					n, ok := final(ref.ResultID)
					if !ok {
						return fmt.Errorf("row %d: reference %d is outside the merged closure", id, ref.ResultID)
					}
					numbers[ref.ResultID] = n
					moved = moved || uint64(n) != ref.ResultID
					ref.ResultID = uint64(n)
				}
			}
			return nil
		}
	}
	// The record's own references name rows by sender number, as do its
	// direct dependencies here; its offers, the bundle's, by ordinal.
	checkDirect := directTransferReferences(direct)
	ownRef := collect(relocateRecord)
	bare := row.Record
	bare.Envelope.PendingOffers = nil
	// The visit reads each payload's decode from memos, and is their last
	// reader.
	rec, err := visitMemoEncodedReferences(bare, memos, func(ref *PersistedRef) error {
		if err := checkDirect(ref); err != nil {
			return fmt.Errorf("row %d: %w", id, err)
		}
		return ownRef(ref)
	})
	if err != nil {
		return preparedRecordPayload{}, err
	}
	if rec.Envelope.PendingOffers, err = clonePartOffers(row.Record.Envelope.PendingOffers); err != nil {
		return preparedRecordPayload{}, err
	}
	offerRef := collect(relocate)
	for i := range rec.Envelope.PendingOffers {
		if err := visitPersistedPartOffer(&rec.Envelope.PendingOffers[i], nil, offerRef); err != nil {
			return preparedRecordPayload{}, err
		}
	}
	slices.Sort(refs)
	rec.Envelope.Imported = true
	out := preparedRecordPayload{record: rec, refs: slices.Compact(refs), completeParts: completeParts, declared: declared}
	if moved {
		// Relocated to its final numbers, call and all: the record's call
		// and offers at provisional numbers are the merge's too.
		out.numbers = numbers
		if out.offers, err = clonePartOffers(row.Record.Envelope.PendingOffers); err != nil {
			return preparedRecordPayload{}, err
		}
		for i := range out.offers {
			if err := visitPersistedPartOffer(&out.offers[i], nil, relocate); err != nil {
				return preparedRecordPayload{}, err
			}
		}
	} else {
		// The record shares its call with the private cache's entry, whose
		// identity merge applies.
		out.record.Call = prep.records[id].Call
	}
	// A blob-backed cache prepares every record so: skipKept applies to an
	// engine only.
	if prep.recordBytes != nil {
		out.recordBytes = encodedRecordBytes(rec)
	}
	return out, nil
}

// keptMergeRecords returns the records merge would keep, deciding now
// (decideMergeRowLocked): their recipe's current entry has a value, and
// either it hasn't expired or the record has. A wrong guess costs no
// correctness: planValueMergeLocked refuses to install a light record.
func (c *Cache) keptMergeRecords(ctx context.Context, prep *valueMergePrep) map[uint64]bool {
	kept := map[uint64]bool{}
	now := time.Now().Unix()
	c.readLockGraphGathered(ctx)
	defer c.egraphMu.RUnlock()
	for _, id := range prep.order {
		if prep.held[id] {
			continue
		}
		cur := c.currentEntryForRecipeLocked(prep.plans[id].recipe)
		if cur == nil || cur.noValueLocked() {
			continue
		}
		expiry := prep.index[id].ExpiresAtUnix
		if !c.resultExpiredAtLocked(cur, now) || expiry != 0 && expiry <= now {
			kept[id] = true
		}
	}
	return kept
}

// embedMergedParts prepares the bundle's parts for the cache. On an engine,
// the outputs with chains become offers embedded in their records. The Cloud
// drops every part, since the upload decides what it stores.
func (c *Cache) embedMergedParts(bundle *ValueBundle) {
	if c.blobBacked {
		for i := range bundle.Values {
			bundle.Values[i].Record.Envelope.PendingOffers = nil
		}
	} else {
		index := map[uint64]*TransferredValue{}
		for i := range bundle.Values {
			index[uint64(bundle.Values[i].Ordinal)] = &bundle.Values[i]
		}
		for _, output := range bundle.Outputs {
			if output.Chain == nil {
				continue
			}
			row := index[uint64(output.Ordinal)]
			row.Record.Envelope.PendingOffers = append(row.Record.Envelope.PendingOffers, PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: *output.Owner})
		}
	}
	bundle.Outputs = nil
}

// cloneValueBundle copies a bundle for merge to change, leaving the input
// untouched, without encoding its payloads: each record and each output
// field by field. Layers, the bundle's and its chains', are copied no deeper
// than their slices: an offered layer is never written after it's built
// (clonePartOffer).
func cloneValueBundle(in ValueBundle) (ValueBundle, error) {
	out := ValueBundle{Version: in.Version, Roots: slices.Clone(in.Roots), Calls: slices.Clone(in.Calls), Layers: slices.Clone(in.Layers), Addresses: maps.Clone(in.Addresses), SenderCache: in.SenderCache}
	if in.Values != nil {
		out.Values = make([]TransferredValue, len(in.Values))
	}
	for i, value := range in.Values {
		if value.storedEnvelope != nil {
			// A stored envelope is decoded as the bundle's encoding would
			// have carried it.
			rec, err := (&storedBody{envelope: value.storedEnvelope, call: value.Record.Call}).record(value.Record.ResultID)
			if err != nil {
				return ValueBundle{}, err
			}
			value.Record.Envelope, value.storedEnvelope = rec.Envelope, nil
		}
		if value.storedCall != nil {
			// So is a stored call frame.
			frame, err := (&storedBody{frame: value.storedCall, frameBytes: value.storedCallBytes}).decodeFrame()
			if err != nil {
				return ValueBundle{}, err
			}
			value.Record.Call, value.storedCall, value.storedCallBytes = frame, nil, 0
		}
		env, err := clonePersistedEnvelope(value.Record.Envelope)
		if err != nil {
			return ValueBundle{}, err
		}
		value.Record.Envelope = env
		value.Record.Call = value.Record.Call.clone()
		value.Record.SnapshotLinks = cloneSnapshotRefLinks(value.Record.SnapshotLinks)
		value.DependencyIDs = slices.Clone(value.DependencyIDs)
		out.Values[i] = value
	}
	if in.Outputs != nil {
		out.Outputs = make([]TransferredOutput, len(in.Outputs))
		for i, output := range in.Outputs {
			cloned, err := cloneTransferredOutput(output)
			if err != nil {
				return ValueBundle{}, err
			}
			out.Outputs[i] = cloned
		}
	}
	return out, nil
}

// cloneTransferredOutput copies output for merge to change, as
// clonePartOffer copies an offer: every nested map, slice and pointer of its
// address, value, chain, prefix and owner, sharing the chain's layers. It
// refuses an output whose chain can't be encoded as the bundle is.
func cloneTransferredOutput(output TransferredOutput) (TransferredOutput, error) {
	output.Address = clonePartAddress(output.Address)
	if output.Value != nil {
		value := cloneSnapshotValue(*output.Value)
		output.Value = &value
	}
	if output.Chain != nil {
		chain, err := cloneOfferedChain(*output.Chain)
		if err != nil {
			return TransferredOutput{}, err
		}
		output.Chain = &chain
	}
	if output.Prefix != nil {
		prefix := *output.Prefix
		prefix.Address = clonePartAddress(prefix.Address)
		output.Prefix = &prefix
	}
	if output.Owner != nil {
		owner := PersistedOfferOwner{DependencyIDs: slices.Clone(output.Owner.DependencyIDs)}
		output.Owner = &owner
	}
	output.Layers = slices.Clone(output.Layers)
	return output, nil
}

// transferIdentityPlanOf derives the identity of res, a record in the private
// cache c: its recipe, and its term's self digest, inputs and provenance.
func (c *Cache) transferIdentityPlanOf(res *sharedResult) (transferIdentityPlan, error) {
	frame := res.loadResultCall()
	recipe, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	self, refs, err := frame.selfDigestAndInputRefs(c)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	plan := transferIdentityPlan{row: res, recipe: recipe, self: self}
	plan.provenance, err = c.inputProvenanceForRefs(refs)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	for _, ref := range refs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return transferIdentityPlan{}, err
		}
		plan.inputs = append(plan.inputs, dig)
	}
	return plan, nil
}

// warmMergeTargetParts reads the complete parts of each recipe's current
// entry, so the reply can report them without capturing under the lock. It
// holds the targets whose parts it must read again for their captures
// together (holdRowsForCapture), in a few holds of egraphMu for writing
// rather than two for each target: each hold waits for egraphMu's readers.
func (c *Cache) warmMergeTargetParts(ctx context.Context, prep *valueMergePrep) {
	// A blob-backed cache, the Cloud's, has no local completions to find,
	// and its merge's caller reads no parts from the reply (mergeReplyLocked).
	if c.blobBacked {
		return
	}
	reader := c.completePartSizeReader()
	c.readLockGraphGathered(ctx)
	targets := make([]*sharedResult, 0, len(prep.plans))
	seen := map[*sharedResult]bool{}
	for _, plan := range prep.plans {
		if res := c.currentEntryForRecipeLocked(plan.recipe); res != nil && !seen[res] && !res.noValueLocked() && !res.holdsUnpreparedPayload() && !completePartsCurrent(res, reader) {
			seen[res] = true
			targets = append(targets, res)
		}
	}
	c.egraphMu.RUnlock()
	if len(targets) == 0 {
		return
	}
	op, err := c.beginCacheOperation()
	if err != nil {
		return
	}
	defer op.finish(false)
	held, release := c.holdRowsForCapture(ctx, targets)
	if c.testWarmHeld != nil {
		c.testWarmHeld()
	}
	for _, h := range held {
		c.completePartsHeld(ctx, h.row, reader, h.run)
	}
	if c.testWarmCaptured != nil {
		c.testWarmCaptured()
	}
	// Warming is a cache the reply reads only while it still describes an
	// entry's value: a failure to let a target go costs nothing here, as a
	// capture's own failure doesn't.
	_ = release()
}

// mergeTargets are each row's current entry, its recipe's, by ordinal, as
// one hold of egraphMu finds them (mergeTargetsLocked): the commit's steps in
// that hold read them there, not from the recipe index once each.
type mergeTargets []*sharedResult

// mergeTargetsLocked returns each of prep's rows' current entry, by
// ordinal. Requires egraphMu, and holds only while it is held.
func (c *Cache) mergeTargetsLocked(prep *valueMergePrep) mergeTargets {
	targets := make(mergeTargets, len(prep.bundle.Values)+1)
	for _, id := range prep.order {
		if id < uint64(len(targets)) {
			targets[id] = c.currentEntryForRecipeLocked(prep.plans[id].recipe)
		}
	}
	return targets
}

// of returns the current entry of the row of ordinal id: from the hold's
// targets when it has them, and from the recipe index otherwise, as an
// engine's early decision reads it (earlyMergeRows). Requires egraphMu.
func (targets mergeTargets) of(c *Cache, prep *valueMergePrep, id uint64) *sharedResult {
	if id < uint64(len(targets)) {
		return targets[id]
	}
	return c.currentEntryForRecipeLocked(prep.plans[id].recipe)
}

// mergeAttachmentWaitLocked returns the attachment to wait on when a record's
// target is still attaching: merge changes nothing until every target is
// settled. Requires egraphMu.
func (c *Cache) mergeAttachmentWaitLocked(prep *valueMergePrep, targets mergeTargets) (*sharedResult, <-chan struct{}) {
	for _, id := range prep.order {
		res := targets.of(c, prep, id)
		if res == nil || res.attachmentState() != resultAttachmentOpen {
			continue
		}
		res.attachDepsMu.Lock()
		wait := res.attachDepsWaitCh
		res.attachDepsMu.Unlock()
		return res, wait
	}
	return nil, nil
}

// mergeAction is what merge does with one record.
type mergeAction uint8

const (
	// mergeCreate: the recipe has no current entry; a new entry stores the
	// record.
	mergeCreate mergeAction = iota
	// mergeStore: the current entry has no value; it stores the record.
	mergeStore
	// mergeKeep: the current entry keeps its value.
	mergeKeep
	// mergeReplace: the current entry's value has expired, and the record
	// replaces it in place.
	mergeReplace
	// mergeRetire: the current entry's expired value is in use; the entry is
	// retired and a new entry stores the record.
	mergeRetire
)

// installs reports whether the action stores the record as a value.
func (a mergeAction) installs() bool {
	return a != mergeKeep
}

// mergeRow is one record's decision.
type mergeRow struct {
	ordinal uint64
	value   *TransferredValue
	// run is, on the Cloud, the run the row's record comes from, and runRan
	// whether, and how, the Cloud knows that run ran its step, as the record
	// the row installs keeps them (mergeRunLocked).
	run    RunName
	runRan runRanBy
	// parts are, on the Cloud, the parts of the record the row installs
	// (partFacts), from its preparation.
	parts  *partFacts
	plan   transferIdentityPlan
	action mergeAction
	// target is the recipe's entry before the merge, if any.
	target *sharedResult
	// res is the entry that holds the record's value after the merge: the
	// target, or a new entry.
	res *sharedResult
	// expired marks an incoming record that had expired when merge decided.
	expired bool
	// keptExpired marks a kept value that had expired.
	keptExpired bool
	// keptWithHolding marks, on a blob-backed cache, a kept record whose
	// sender's holding the cache had before the commit, and keptTaught one
	// of those whose identity taught its entry a class, a term or a digest
	// that the holding hadn't (applyMergedIdentitiesLocked).
	keptWithHolding, keptTaught bool
	// unchanged marks, on an engine, a kept record of the Cloud's that its
	// entry merged already at the same change stamp, unchanged here since:
	// its identity and the Cloud's copy aren't taught again
	// (unchangedCloudRowLocked).
	unchanged bool
	// rec and deps are the record relocated to the final numbers, for an
	// action that installs it. firstUse is set for a record installed with
	// its payload unprepared (relocateUnpreparedRow).
	rec      PersistedRecord
	deps     []sharedResultID
	firstUse *mergedFirstUse
	// recordBytes is rec's size, on a blob-backed cache.
	recordBytes int64
	// offers are the record's incoming offers, and admitted those this row's
	// selection admitted, in commit order: a later row can still supersede
	// one for the same part. The final set per entry and part key is
	// commit.finalOffers (selectMergedOffersLocked).
	offers   []PersistedPartOffer
	admitted []PersistedPartOffer
}

// valueMergeCommit is a merge decided and checked, ready to change the cache.
type valueMergeCommit struct {
	prep      *valueMergePrep
	from      CacheID
	rows      []*mergeRow
	byOrdinal map[uint64]*mergeRow
	// byNumber maps each row's entry number to the entry it lands on, and
	// installed each entry that takes a record to the row it takes.
	byNumber  map[sharedResultID]*sharedResult
	installed map[*sharedResult]*mergeRow
	// finalOffers holds, for each entry a row admits offers to, its offer
	// per part key as the commit leaves it, and newOffers the keys whose
	// final offer is an admitted one (selectMergedOffersLocked).
	finalOffers map[*sharedResult]map[string]PersistedPartOffer
	newOffers   map[*sharedResult]map[string]bool
	// attached are the offers applyMergedOffersLocked attached, which the
	// commit's finish queues for prefetch once egraphMu is released.
	attached []mergeAttachedOffer
	// restore is set for a restore's merge: its finish queues the attached
	// offers in the prefetcher's backlog, not its lanes. hold has the commit
	// hold every entry it lands on (RestoreValues).
	restore bool
	hold    bool
	roots   []MergedRoot
	now     int64
	// targets are the rows' current entries as the commit's hold found
	// them, nil for a decision made apart from a commit (earlyMergeRows).
	targets mergeTargets
}

// mergeAttachedOffer is an offer a merge attached: the entry, its part key
// and the offer.
type mergeAttachedOffer struct {
	res   *sharedResult
	key   string
	offer *partOffer
}

// planValueMergeLocked decides every record and runs every check that can
// fail, without changing the cache, by the rows' current entries as the
// hold finds them (targets). Requires egraphMu for writing.
func (c *Cache) planValueMergeLocked(ctx context.Context, from CacheID, prep *valueMergePrep, targets mergeTargets) (*valueMergeCommit, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// Every expiry is judged by the clock read under the lock that decides.
	now := time.Now().Unix()
	commit := &valueMergeCommit{prep: prep, from: from, byOrdinal: map[uint64]*mergeRow{}, now: now, targets: targets}

	// A held row holds when merge would keep its recipe's current entry's
	// value for a record with the row's expiry, as the cache stands before
	// the merge.
	if stale, why := c.staleHeldRowsLocked(prep, targets, now); len(stale) > 0 {
		return nil, &mergeStaleError{ordinals: stale, why: why}
	}
	if stale, why := c.staleHeldPartsLocked(prep, targets); len(stale) > 0 {
		return nil, &mergeStaleError{ordinals: stale, why: why}
	}

	c.decideMergeRowsLocked(commit)
	// Everything after the decisions reads the payloads and parts of the
	// records merge installs, which a light record doesn't have: the cache
	// changed since the preparation.
	if commit.installsLight() {
		return nil, errMergeNeedsFullPrep
	}
	commit.byNumber = make(map[sharedResultID]*sharedResult, len(commit.rows))
	commit.installed = map[*sharedResult]*mergeRow{}
	for _, row := range commit.rows {
		commit.byNumber[row.res.id] = row.res
		if row.action.installs() {
			commit.installed[row.res] = row
		}
	}

	if err := c.relocateMergeRowsLocked(commit); err != nil {
		return nil, err
	}
	if err := c.selectMergedOffersLocked(commit); err != nil {
		return nil, err
	}
	if err := c.checkMergeGraphLocked(ctx, commit); err != nil {
		return nil, err
	}
	return commit, nil
}

// decideMergeRowsLocked decides what commit's merge does with each record of
// its prepared bundle as the cache stands, by commit's clock: the rows of the
// unexpired roots' closure, each with its action and the entry it lands on.
// It reads the cache and changes nothing: egraphMu is held for reading at
// least.
func (c *Cache) decideMergeRowsLocked(commit *valueMergeCommit) {
	prep, now := commit.prep, commit.now
	// An expired root is skipped, with whatever only it needs.
	var merged map[uint64]bool
	merged, commit.roots = prep.liveClosure(now)

	// A recipe is decided once, by its best record: the first unexpired one
	// in dependency order, or the first if all have expired. The recipe's
	// other records land on the same entry.
	best := map[string]*mergeRow{}
	for _, id := range prep.order {
		if !merged[id] {
			continue
		}
		value := prep.index[id]
		row := &mergeRow{ordinal: id, value: value, plan: prep.plans[id]}
		row.expired = value.ExpiresAtUnix != 0 && value.ExpiresAtUnix <= now
		commit.rows = append(commit.rows, row)
		commit.byOrdinal[id] = row
		recipe := row.plan.recipe.String()
		if b := best[recipe]; b == nil || b.expired && !row.expired {
			best[recipe] = row
		}
	}
	for _, row := range commit.rows {
		if b := best[row.plan.recipe.String()]; b == row {
			c.decideMergeRowLocked(row, commit.targets.of(c, prep, row.ordinal), now, sharedResultID(prep.firstID+row.ordinal-1))
		}
	}
	for _, row := range commit.rows {
		b := best[row.plan.recipe.String()]
		if b == row {
			continue
		}
		// Its parts are admitted against the value the entry holds after
		// the merge: the best record's, or the one the entry keeps.
		row.action, row.target, row.res = mergeKeep, b.res, b.res
		row.keptExpired = b.keptExpired || b.action.installs() && b.expired
	}
}

// liveClosure returns the rows the bundle's unexpired roots need, judged at
// now, and each root, in order, marked expired or not: an expired root is
// skipped, with whatever only it needs. It reads the prepared bundle alone.
func (prep *valueMergePrep) liveClosure(now int64) (map[uint64]bool, []MergedRoot) {
	merged := map[uint64]bool{}
	var roots []MergedRoot
	var need func(uint64)
	need = func(id uint64) {
		if merged[id] {
			return
		}
		merged[id] = true
		row := prep.index[id]
		for _, dep := range row.DependencyIDs {
			need(dep)
		}
		for _, offer := range row.Record.Envelope.PendingOffers {
			for _, dep := range offer.Owner.DependencyIDs {
				need(dep)
			}
		}
	}
	for _, root := range prep.bundle.Roots {
		expiry := prep.index[uint64(root.Ordinal)].ExpiresAtUnix
		expired := root.ExpiresAtUnix != 0 && root.ExpiresAtUnix <= now || expiry != 0 && expiry <= now
		roots = append(roots, MergedRoot{Ordinal: root.Ordinal, Expired: expired})
		if !expired {
			need(uint64(root.Ordinal))
		}
	}
	return merged, roots
}

// staleHeldRowsLocked returns, sorted, the held rows that don't hold as the
// cache stands (heldRowStaleLocked), with expiries judged at now. On the
// Cloud, an engine's held row holds whether or not the Cloud holds that
// engine's holding of the row yet: the commit teaches a missing one
// (applyMergedIdentitiesLocked). Requires egraphMu.
func (c *Cache) staleHeldRowsLocked(prep *valueMergePrep, targets mergeTargets, now int64) ([]TransferOrdinal, []string) {
	why := map[TransferOrdinal]string{}
	for _, id := range prep.order {
		if !prep.held[id] {
			continue
		}
		row := prep.index[id]
		if reason := c.heldRowStaleWhyLocked(targets.of(c, prep, id), row.ExpiresAtUnix, now); reason != "" {
			why[TransferOrdinal(id)] = reason
		}
	}
	return sortedStale(why)
}

// sortedStale returns the stale rows of why, sorted, and each one's reason
// in the same order.
func sortedStale(why map[TransferOrdinal]string) ([]TransferOrdinal, []string) {
	if len(why) == 0 {
		return nil, nil
	}
	stale := slices.Sorted(maps.Keys(why))
	reasons := make([]string, len(stale))
	for i, ordinal := range stale {
		reasons[i] = why[ordinal]
	}
	return stale, reasons
}

// heldRowOutputs returns the bundle's outputs on its held rows, by ordinal.
func heldRowOutputs(bundle ValueBundle) map[uint64][]TransferredOutput {
	held := map[uint64]bool{}
	for _, row := range bundle.Values {
		if row.Held != "" {
			held[uint64(row.Ordinal)] = true
		}
	}
	var outputs map[uint64][]TransferredOutput
	for _, output := range bundle.Outputs {
		if id := uint64(output.Ordinal); held[id] {
			if outputs == nil {
				outputs = map[uint64][]TransferredOutput{}
			}
			outputs[id] = append(outputs[id], output)
		}
	}
	return outputs
}

// staleHeldPartsLocked returns, sorted, the held rows whose parts the Cloud's
// own copy of the row doesn't have: the current entry of the row's recipe,
// which the row lands on, must have each part at its address, at least as
// complete as the row says it is (checkHeldPart). A held
// row whose recipe has no current entry is stale already
// (staleHeldRowsLocked). Requires egraphMu.
func (c *Cache) staleHeldPartsLocked(prep *valueMergePrep, targets mergeTargets) ([]TransferOrdinal, []string) {
	why := map[TransferOrdinal]string{}
	for id, outputs := range prep.heldParts {
		cur := targets.of(c, prep, id)
		if cur == nil {
			why[TransferOrdinal(id)] = "no entry"
		} else if reason := c.entryPartsWhyLocked(cur, outputs); reason != "" {
			why[TransferOrdinal(id)] = reason
		}
	}
	return sortedStale(why)
}

// entryHasPartsLocked reports whether res's stored record has each of
// outputs' parts (entryPartsWhyLocked). Requires egraphMu.
func (c *Cache) entryHasPartsLocked(res *sharedResult, outputs []TransferredOutput) bool {
	return c.entryPartsWhyLocked(res, outputs) == ""
}

// entryPartsWhyLocked says why res's stored record doesn't have each of
// outputs' parts, as checkHeldPart checks them (recordPartsLocked), and ""
// when it has them. Requires egraphMu.
func (c *Cache) entryPartsWhyLocked(res *sharedResult, outputs []TransferredOutput) string {
	if res.noValueLocked() {
		return "no value"
	}
	parts, ok := c.recordPartsLocked(res)
	if !ok {
		return "no record of its parts"
	}
	for _, output := range outputs {
		key, err := partAddressKey(output.Address)
		if err != nil {
			return fmt.Sprintf("part address: %v", err)
		}
		part, ok := parts.at(key, output.Address)
		if !ok {
			return fmt.Sprintf("part %s missing", key)
		}
		if err := checkHeldPart(output, part); err != nil {
			return fmt.Sprintf("part %s: %v", key, err)
		}
	}
	return ""
}

// recordParts are a stored record's parts as its mapping has them, for a
// check of outputs against them (checkOutputPart), which reads a part's
// state and a pending part's kind. A Cloud record's are the parts its entry
// keeps of it (partFacts), whole or compressed, as a stored row's outputs
// are checked (checkValueBundle): the parts it declares absent or pending,
// with their kinds, and those it proves complete, a state of completed,
// absent or metadata. Together they are every part the record maps to, so
// the record isn't decoded. An engine's record's are its mapping's.
type recordParts struct {
	declared []declaredPart
	complete *partKeySet
	mapped   map[string]CapturedCodecOutput
}

// recordPartsLocked returns res's stored record's parts, or false if the
// record can't be read. Requires egraphMu.
func (c *Cache) recordPartsLocked(res *sharedResult) (recordParts, bool) {
	if c.blobBacked {
		return recordParts{declared: res.partFacts.declaredParts(), complete: res.partFacts.completeParts()}, true
	}
	rec, err := c.storedRecordLocked(res)
	if err != nil {
		return recordParts{}, false
	}
	outputs, err := mapTransferredOutputs(rec)
	if err != nil {
		return recordParts{}, false
	}
	mapped := make(map[string]CapturedCodecOutput, len(outputs))
	for _, part := range outputs {
		if key, err := partAddressKey(part.Address); err == nil {
			mapped[key] = part
		}
	}
	return recordParts{mapped: mapped}, true
}

// at returns the record's part at the address, whose key is key: a mapped
// one; a declared one, the last declared at the key, as checkValueBundle
// reads a stored row's; or one only proven complete, which is completed,
// or metadata for the metadata part, the one part whose complete state is
// metadata.
func (p recordParts) at(key string, address PersistedPartAddress) (CapturedCodecOutput, bool) {
	if p.mapped != nil {
		part, ok := p.mapped[key]
		return part, ok
	}
	for i := len(p.declared) - 1; i >= 0; i-- {
		if p.declared[i].key == key {
			return p.declared[i].output(), true
		}
	}
	if !p.complete.has(key) {
		return CapturedCodecOutput{}, false
	}
	state := "completed"
	if address.Part == "metadata" {
		state = "metadata"
	}
	return CapturedCodecOutput{Address: address, State: state}, true
}

// installsLight reports whether the commit installs a light record.
func (commit *valueMergeCommit) installsLight() bool {
	for _, row := range commit.rows {
		if row.action.installs() && commit.prep.light[row.ordinal] {
			return true
		}
	}
	return false
}

// decideMergeRowLocked decides a record whose recipe no earlier record of the
// bundle has, by the recipe's current entry, cur; fresh is the number a new
// entry takes. Requires egraphMu.
func (c *Cache) decideMergeRowLocked(row *mergeRow, cur *sharedResult, now int64, fresh sharedResultID) {
	row.target = cur
	switch {
	case cur == nil:
		row.action, row.res = mergeCreate, &sharedResult{id: fresh}
	case cur.noValueLocked():
		row.action, row.res = mergeStore, cur
	case !c.resultExpiredAtLocked(cur, now):
		row.action, row.res = mergeKeep, cur
	case row.expired:
		row.action, row.res, row.keptExpired = mergeKeep, cur, true
	case c.blobBacked || !c.resultInUseLocked(cur):
		// The Cloud always replaces: nothing of it uses an entry across an
		// unlock.
		row.action, row.res = mergeReplace, cur
	default:
		row.action, row.res = mergeRetire, &sharedResult{id: fresh}
	}
}

// prerelocatedRecord is a record a merge relocated before its commit took
// egraphMu (prerelocateMergeRows), with the final number it gave each
// provisional number it renumbered.
//
// A record relocated as it was prepared (earlyNumbers) is the only form of
// the record the merge keeps, so from gives back the provisional number of
// each final number it was given, for a commit whose numbers differ to
// relocate it from those; it is nil for any other.
type prerelocatedRecord struct {
	rec     PersistedRecord
	numbers map[uint64]sharedResultID
	from    map[uint64]uint64
	// firstUse and deps are, for a record left unprepared
	// (relocateUnpreparedRow), what its row takes with it: what preparing
	// the record at its first use takes, and its direct dependencies.
	firstUse *mergedFirstUse
	deps     []sharedResultID
}

// holds reports whether final, the commit's numbering, gives every number the
// relocation renumbered the number the relocation gave it. Relocating a
// record reads nothing but its own encoding and those numbers, so then the
// relocated record is the one the commit would relocate.
func (p *prerelocatedRecord) holds(final func(uint64) (sharedResultID, error)) bool {
	for provisional, n := range p.numbers {
		if got, err := final(provisional); err != nil || got != n {
			return false
		}
	}
	return true
}

// prerelocateMergeRows relocates, before the commit takes egraphMu for
// writing, each record the commit would relocate as the cache stands now:
// under the read lock it decides every row as the commit does
// (decideMergeRowsLocked), and with no lock held it renumbers each moved
// record's references, and relocates each record left unprepared
// (relocateUnpreparedRow), the records in parallel. The commit uses a
// relocated record only when every number it gave still holds
// (relocateMergeRowsLocked); any other is relocated under the lock, as
// before. Decoding and encoding a record's payload is most of a commit's
// hold of the lock on an engine taking a stream of merges, whose records
// mostly reference entries earlier merges made; a restore's set, whose
// records are all left unprepared, has every record it installs relocated.
// A merge that prepares no record with its payload and leaves none
// unprepared relocates nothing here, and doesn't decide its rows before its
// commit.
//
// The Cloud doesn't decide its rows again once it decided them as it prepared
// the records (earlyMergeRows): its commit comes later, behind the commits of
// its sender's earlier exports, so a second decision here is no nearer the
// commit's than the first. It relocates here, by that decision, only the
// records the preparation didn't relocate, and the commit relocates under the
// lock each record whose numbers no longer hold, as it relocates any.
func (c *Cache) prerelocateMergeRows(ctx context.Context, prep *valueMergePrep) {
	if c.testNoPrerelocation || len(prep.stale) > 0 || !prep.preparesPayloads() && len(prep.unprepared) == 0 {
		return
	}
	commit := c.prerelocationDecision(ctx, prep)
	if commit == nil {
		return
	}
	final := func(provisional uint64) (sharedResultID, bool) {
		row := commit.byOrdinal[provisional-prep.firstID+1]
		if row == nil {
			return 0, false
		}
		return row.res.id, true
	}
	// A record relocated as it was prepared is relocated again here, from
	// its numbers, when this decision gives it others: another merge
	// committed since the early decision, as a whole set merging on a lane
	// beside this one's does.
	finalErr := func(provisional uint64) (sharedResultID, error) {
		n, ok := final(provisional)
		if !ok {
			return 0, errMergeRowOutsideClosure
		}
		return n, nil
	}
	var moved, again []uint64
	var unprepared []*mergeRow
	for _, row := range commit.rows {
		if !row.action.installs() {
			continue
		}
		if prep.unprepared[row.ordinal] {
			unprepared = append(unprepared, row)
			continue
		}
		if pre := prep.prerelocated[row.ordinal]; pre != nil {
			if pre.from != nil && !pre.holds(finalErr) {
				again = append(again, row.ordinal)
			}
			continue
		}
		for _, provisional := range prep.refs[row.ordinal] {
			if n, ok := final(provisional); !ok || uint64(n) != provisional {
				moved = append(moved, row.ordinal)
				break
			}
		}
	}
	relocated := make([]*prerelocatedRecord, len(moved))
	_ = forEachIndexParallel(len(moved), func(i int) error {
		numbers := map[uint64]sharedResultID{}
		rec, err := VisitEncodedReferences(prep.records[moved[i]], func(ref *PersistedRef) error {
			if ref.RecipeID != nil || ref.ResultID == 0 {
				return nil
			}
			n, ok := final(ref.ResultID)
			if !ok {
				return errMergeRowOutsideClosure
			}
			numbers[ref.ResultID] = n
			ref.ResultID = uint64(n)
			return nil
		})
		// A record that doesn't relocate here is left to the commit, which
		// relocates it under the lock and answers its error.
		if err == nil {
			relocated[i] = &prerelocatedRecord{rec: rec, numbers: numbers}
		}
		return nil
	})
	// A record relocated again stays the merge's only form of it, so it is
	// taken only at numbers that each name one provisional number; any other
	// is left to the commit, which relocates it from the numbers it has.
	relocatedAgain := make([]*prerelocatedRecord, len(again))
	_ = forEachIndexParallel(len(again), func(i int) error {
		pre := prep.prerelocated[again[i]]
		rec, numbers, err := pre.relocateFrom(again[i], finalErr)
		if err == nil {
			relocatedAgain[i] = newPreparedRelocation(rec, numbers)
		}
		return nil
	})
	relocatedUnprepared := make([]*prerelocatedRecord, len(unprepared))
	_ = forEachIndexParallel(len(unprepared), func(i int) error {
		relocatedUnprepared[i] = prerelocateUnpreparedRow(prep, unprepared[i], final)
		return nil
	})
	if prep.prerelocated == nil {
		prep.prerelocated = make(map[uint64]*prerelocatedRecord, len(moved)+len(unprepared))
	}
	for i, ordinal := range moved {
		if relocated[i] != nil {
			prep.prerelocated[ordinal] = relocated[i]
		}
	}
	for i, row := range unprepared {
		if relocatedUnprepared[i] != nil {
			prep.prerelocated[row.ordinal] = relocatedUnprepared[i]
		}
	}
	for i, ordinal := range again {
		if relocatedAgain[i] != nil {
			prep.prerelocated[ordinal] = relocatedAgain[i]
		}
	}
	c.notePrerelocated(prep)
}

// prerelocationDecision is the decision prerelocateMergeRows relocates by: on
// the Cloud, the one its preparation made (earlyMergeRows), if it made one;
// otherwise one made now under the read lock, as the commit would decide, or
// nil when that installs a light record, which the commit prepares in full
// again first.
func (c *Cache) prerelocationDecision(ctx context.Context, prep *valueMergePrep) *valueMergeCommit {
	if c.blobBacked && prep.early != nil {
		return prep.early
	}
	commit := &valueMergeCommit{prep: prep, byOrdinal: map[uint64]*mergeRow{}, now: time.Now().Unix()}
	c.readLockGraphGathered(ctx)
	c.decideMergeRowsLocked(commit)
	c.egraphMu.RUnlock()
	if commit.installsLight() {
		return nil
	}
	return commit
}

// prerelocateUnpreparedRow relocates a record left unprepared as the commit
// would relocate it (relocateUnpreparedRow), at the numbers final gives, and
// notes each number it gives, the record's own among them, for the commit to
// check (prerelocatedRecord.holds). A record that doesn't relocate here is
// left to the commit, which relocates it under the lock and answers its
// error: it returns nil.
func prerelocateUnpreparedRow(prep *valueMergePrep, row *mergeRow, final func(uint64) (sharedResultID, bool)) *prerelocatedRecord {
	numbers := map[uint64]sharedResultID{prep.firstID + row.ordinal - 1: row.res.id}
	number := func(ordinal uint64) (sharedResultID, error) {
		n, ok := final(prep.firstID + ordinal - 1)
		if !ok {
			return 0, errMergeRowOutsideClosure
		}
		numbers[prep.firstID+ordinal-1] = n
		return n, nil
	}
	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		n, ok := final(ref.ResultID)
		if !ok {
			return errMergeRowOutsideClosure
		}
		numbers[ref.ResultID] = n
		ref.ResultID = uint64(n)
		return nil
	}
	relocated := &mergeRow{ordinal: row.ordinal, value: row.value, res: row.res}
	if err := relocateUnpreparedRow(prep, relocated, prep.records[row.ordinal], number, relocate); err != nil {
		return nil
	}
	return &prerelocatedRecord{rec: relocated.rec, numbers: numbers, firstUse: relocated.firstUse, deps: relocated.deps}
}

// notePrerelocated tells a test the numbers each record relocated before the
// commit was given (testPrerelocated).
func (c *Cache) notePrerelocated(prep *valueMergePrep) {
	if c.testPrerelocated != nil {
		used := make(map[uint64]map[uint64]sharedResultID, len(prep.prerelocated))
		for ordinal, pre := range prep.prerelocated {
			used[ordinal] = pre.numbers
		}
		c.testPrerelocated(used)
	}
}

// relocateUnpreparedRow relocates an installed record that a merge leaves
// unprepared (valueMergePrep.unprepared): its identity, call and offers go
// to the final numbers, which number and relocate give, as
// relocateMergeRowsLocked relocates any record, and its payload stays as the
// Cloud sent it. The row keeps what preparing it at its first use takes
// (mergedFirstUse): the entry of each Cloud number its payload can name, one
// of its direct dependencies, which are the row's dependencies too, and its
// complete parts as the bundle carried them, and the merge's origin as the
// preparation has it, which the commit gives a record relocated before it.
// It reads only the preparation, the record and the numbers, so it runs
// before the commit's lock (prerelocateMergeRows) as under it.
func relocateUnpreparedRow(prep *valueMergePrep, row *mergeRow, source PersistedRecord, number func(uint64) (sharedResultID, error), relocate PersistedRefVisitor) error {
	rec := source
	rec.ResultID = uint64(row.res.id)
	rec.Envelope.ResultID = rec.ResultID
	rec.Call = source.Call.clone()
	if err := visitResultCallReferences(rec.Call, relocate); err != nil {
		return err
	}
	offers, err := clonePartOffers(source.Envelope.PendingOffers)
	if err != nil {
		return err
	}
	for i := range offers {
		if err := visitPersistedPartOffer(&offers[i], nil, relocate); err != nil {
			return err
		}
	}
	compactOfferOwners(offers)
	rec.Envelope.PendingOffers = offers
	use := &mergedFirstUse{numbers: make(map[uint64]sharedResultID, len(row.value.DependencyIDs)), complete: sharedPartKeys(prep.completeParts[row.ordinal]), origin: prep.origin}
	seen := map[sharedResultID]bool{}
	for _, dep := range row.value.DependencyIDs {
		n, err := number(dep)
		if err != nil {
			return err
		}
		use.numbers[prep.index[dep].SenderNumber] = n
		if !seen[n] {
			seen[n] = true
			row.deps = append(row.deps, n)
			use.deps = append(use.deps, uint64(n))
		}
	}
	row.rec = rec
	row.recordBytes = prep.recordBytes[row.ordinal]
	row.offers = rec.Envelope.PendingOffers
	row.firstUse = use
	return nil
}

// errMergeRowOutsideClosure stops a relocation before the lock at a
// reference outside the rows the merge decides.
var errMergeRowOutsideClosure = errors.New("merge values: reference outside the merged closure")

// relocateMergeRowsLocked relocates each decided record from its provisional
// numbers to the final ones; each row's incoming offers are relocated too. A
// new entry keeps its provisional number, so a record whose own number and
// references all keep their provisional numbers is already at its final ones
// and is installed as it is. An installed record whose own number or any
// reference changes is walked; references can move to existing entries or
// when records of one recipe coalesce. A row that keeps its value relocates
// only its offers. Requires egraphMu.
func (c *Cache) relocateMergeRowsLocked(commit *valueMergeCommit) error {
	prep := commit.prep
	number := func(ordinal uint64) (sharedResultID, error) {
		row := commit.byOrdinal[ordinal]
		if row == nil {
			return 0, fmt.Errorf("merge values: reference to ordinal %d outside the merged closure", ordinal)
		}
		return row.res.id, nil
	}
	final := func(provisional uint64) (sharedResultID, error) {
		return number(provisional - prep.firstID + 1)
	}
	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		n, err := final(ref.ResultID)
		ref.ResultID = uint64(n)
		return err
	}
	for _, row := range commit.rows {
		source := prep.records[row.ordinal]
		if !row.action.installs() {
			if !c.blobBacked {
				offers, err := clonePartOffers(source.Envelope.PendingOffers)
				if err != nil {
					return err
				}
				for i := range offers {
					if err := visitPersistedPartOffer(&offers[i], nil, relocate); err != nil {
						return err
					}
				}
				compactOfferOwners(offers)
				row.offers = offers
			}
			continue
		}
		if prep.unprepared[row.ordinal] {
			if err := c.relocateUnpreparedRowLocked(prep, row, source, final, number, relocate); err != nil {
				return err
			}
			continue
		}
		moved := false
		for _, provisional := range prep.refs[row.ordinal] {
			n, err := final(provisional)
			if err != nil {
				return err
			}
			moved = moved || uint64(n) != provisional
		}
		rec := source
		if pre := prep.prerelocated[row.ordinal]; pre != nil && pre.from != nil {
			delete(prep.prerelocated, row.ordinal)
			var err error
			if rec, err = c.relocatePreparedRecordLocked(row.ordinal, pre, final); err != nil {
				return err
			}
		} else if moved {
			if pre, ok := prep.prerelocated[row.ordinal]; ok && pre.holds(final) {
				// Relocated before the lock with the numbers the commit
				// decides: the same record relocation here would give.
				rec = pre.rec
				delete(prep.prerelocated, row.ordinal)
			} else {
				if c.testRelocatedUnderLock != nil {
					c.testRelocatedUnderLock(row.ordinal)
				}
				var err error
				if rec, err = VisitEncodedReferences(source, relocate); err != nil {
					return err
				}
			}
		} else {
			// The record is shared with the preparation: copy what the
			// commit changes.
			rec.Call = source.Call.clone()
			offers, err := clonePartOffers(source.Envelope.PendingOffers)
			if err != nil {
				return err
			}
			rec.Envelope.PendingOffers = offers
		}
		compactOfferOwners(rec.Envelope.PendingOffers)
		seen := map[sharedResultID]bool{}
		for _, dep := range row.value.DependencyIDs {
			n, err := number(dep)
			if err != nil {
				return err
			}
			if !seen[n] {
				seen[n] = true
				row.deps = append(row.deps, n)
			}
		}
		// The preparation validated the record at its ordinals
		// (validateTransferRecord). Relocation maps each ordinal to one
		// nonzero number and applies that map to references and
		// dependencies alike, and it lists each dependency once as it maps
		// them (seen, above), though two records of one recipe map to one
		// entry. So every child or call reference is still a direct
		// dependency, none is zero and none is listed twice: the payload's
		// checks aren't repeated here. The offers, every row's, are checked
		// at the final numbers with the graph (checkMergeGraphLocked).
		row.rec = rec
		row.recordBytes = prep.recordBytes[row.ordinal]
		if !c.blobBacked {
			row.offers = rec.Envelope.PendingOffers
		}
	}
	return nil
}

// relocateUnpreparedRowLocked relocates, for the commit, an installed record
// left unprepared: it takes the record relocated before the lock when every
// number that relocation gave still holds (prerelocateUnpreparedRow), with
// the merge's origin, and relocates it here otherwise. Requires egraphMu.
func (c *Cache) relocateUnpreparedRowLocked(prep *valueMergePrep, row *mergeRow, source PersistedRecord, final, number func(uint64) (sharedResultID, error), relocate PersistedRefVisitor) error {
	pre := prep.prerelocated[row.ordinal]
	delete(prep.prerelocated, row.ordinal)
	if pre != nil && pre.holds(final) {
		row.rec, row.recordBytes, row.offers, row.firstUse, row.deps = pre.rec, prep.recordBytes[row.ordinal], pre.rec.Envelope.PendingOffers, pre.firstUse, pre.deps
		row.firstUse.origin = prep.origin
		return nil
	}
	if c.testRelocatedUnderLock != nil {
		c.testRelocatedUnderLock(row.ordinal)
	}
	return relocateUnpreparedRow(prep, row, source, number, relocate)
}

// relocatePreparedRecordLocked returns a record relocated as it was prepared
// (earlyNumbers) at the commit's numbers, final: as it is when those are the
// numbers it was given, and otherwise relocated from those, each of which
// names one provisional number (prerelocatedRecord.from). Requires egraphMu.
func (c *Cache) relocatePreparedRecordLocked(ordinal uint64, pre *prerelocatedRecord, final func(uint64) (sharedResultID, error)) (PersistedRecord, error) {
	if pre.holds(final) {
		return pre.rec, nil
	}
	if c.testRelocatedUnderLock != nil {
		c.testRelocatedUnderLock(ordinal)
	}
	rec, _, err := pre.relocateFrom(ordinal, final)
	return rec, err
}

// relocateFrom returns a record relocated as it was prepared, of ordinal, at
// the number final gives the provisional number each of its numbers names
// (from), with those numbers by provisional number.
func (p *prerelocatedRecord) relocateFrom(ordinal uint64, final func(uint64) (sharedResultID, error)) (PersistedRecord, map[uint64]sharedResultID, error) {
	numbers := map[uint64]sharedResultID{}
	rec, err := VisitEncodedReferences(p.rec, func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		provisional, ok := p.from[ref.ResultID]
		if !ok {
			return fmt.Errorf("merge values: row %d's reference %d names no row", ordinal, ref.ResultID)
		}
		n, err := final(provisional)
		numbers[provisional] = n
		ref.ResultID = uint64(n)
		return err
	})
	return rec, numbers, err
}

// newPreparedRelocation is a record relocated as it was prepared at numbers,
// by provisional number, with their inverse (prerelocatedRecord.from), or
// nil when two provisional numbers have one number.
func newPreparedRelocation(rec PersistedRecord, numbers map[uint64]sharedResultID) *prerelocatedRecord {
	from := make(map[uint64]uint64, len(numbers))
	for provisional, n := range numbers {
		if _, ok := from[uint64(n)]; ok {
			return nil
		}
		from[uint64(n)] = provisional
	}
	return &prerelocatedRecord{rec: rec, numbers: numbers, from: from}
}

// compactOfferOwners lists each offer owner's dependencies once, sorted. Two
// records of one recipe land on one entry, so an owner that lists both names
// that entry once, as offerPart lists the owners of the offers it receives.
func compactOfferOwners(offers []PersistedPartOffer) {
	for i := range offers {
		owner := &offers[i].Owner
		slices.Sort(owner.DependencyIDs)
		owner.DependencyIDs = slices.Compact(owner.DependencyIDs)
	}
}

// checkMergeGraphLocked checks, before anything changes, the ownership graph
// the merge would leave: every offer's owner names entries the merge leaves,
// and no cycle closes through an entry that takes a record or an offer. The
// graph is each entry's dependencies, the installed record's new ones for an
// entry that takes one, and the owners of each entry's offers as the commit
// leaves them (selectMergedOffersLocked): an offer admission discards, or one
// an admitted offer replaces, adds no edge. Each bundle's own graph is
// acyclic, but a kept entry's edges and a new record's can close a cycle when
// two caches' values of the same recipes depend on each other differently
// through explicit dependencies. A bundle that would close one is refused,
// like any failed check. Requires egraphMu.
func (c *Cache) checkMergeGraphLocked(ctx context.Context, commit *valueMergeCommit) error {
	installed := commit.installed
	// An entry's children are its dependencies, the installed record's for
	// an entry that takes one, and the owners of its offers as the commit
	// leaves them: an entry that takes a record starts with none, and one a
	// row admits offers to has selectMergedOffersLocked's final set.
	children := func(res *sharedResult) []*sharedResult {
		var out []*sharedResult
		if row := installed[res]; row != nil {
			for _, dep := range row.deps {
				out = append(out, c.mergeEntryLocked(commit, dep))
			}
		} else {
			for id := range res.deps {
				out = append(out, c.resultsByID[id])
			}
		}
		if offers, ok := commit.finalOffers[res]; ok {
			for _, offer := range offers {
				for _, dep := range offer.Owner.DependencyIDs {
					out = append(out, c.mergeEntryLocked(commit, sharedResultID(dep)))
				}
			}
		} else if installed[res] == nil {
			for _, offer := range res.partOffersLocked() {
				out = append(out, slices.Collect(offerOwnershipChildrenLocked(offer.owner))...)
			}
		}
		return out
	}
	// Every changed entry is checked for an owner that names a missing
	// entry, and the graph is searched for a cycle from the changed entries
	// only: the cache's graph and the bundle's were each acyclic, so a new
	// cycle runs through one of them.
	var changed []*sharedResult
	for _, row := range commit.rows {
		for _, offer := range row.offers {
			if _, err := partAddressKey(offer.Address); err != nil {
				return err
			}
			if err := validateOfferReferences(offer); err != nil {
				return fmt.Errorf("merge values: row %d: %w", row.ordinal, err)
			}
			for _, dep := range offer.Owner.DependencyIDs {
				if c.mergeEntryLocked(commit, sharedResultID(dep)) == nil {
					return fmt.Errorf("merge values: row %d: offer owner names missing entry %d", row.ordinal, dep)
				}
			}
		}
		if len(row.admitted) > 0 || installed[row.res] == row {
			changed = append(changed, row.res)
		}
	}
	judge := mergeGraphJudge.Load()
	if judge == nil {
		_, err := c.searchMergeGraphLocked(changed, children, true)
		return err
	}
	start := time.Now()
	narrow, err := c.searchMergeGraphLocked(changed, children, true)
	narrowTime := time.Since(start)
	start = time.Now()
	whole, wholeErr := c.searchMergeGraphLocked(changed, children, false)
	(*judge)(ctx, MergeGraphJudgement{
		From:   commit.from,
		Narrow: err, Whole: wholeErr,
		NarrowVisits: narrow.visits, NarrowReach: narrow.reach, WholeVisits: whole.visits,
		NarrowTime: narrowTime, WholeTime: time.Since(start),
	})
	return err
}

// mergeGraphSearch is what one search of a merge's graph visited: the
// entries it followed, and the stored ancestors it walked up to first.
type mergeGraphSearch struct {
	visits, reach int
}

// searchMergeGraphLocked searches depth-first for a cycle from the changed
// entries, over the graph children gives. Narrowed, it follows a child only
// if the merge changes it, or it already existed and the stored graph has a
// path from it to an existing changed entry (mergeReachLocked); whole, it
// follows every child, as the check did before it was narrowed, and judges
// the narrowed one in tests and judging builds (JudgeMergeGraphs).
//
// The two refuse the same merges while the stored graph is acyclic, as every
// merge's check keeps it. An unchanged entry's children are its stored
// ones, so every edge the merge adds starts at a changed entry and every
// cycle runs through one. From one changed entry to the next, a cycle runs
// only through unchanged entries, by stored edges, so the last of them
// points at an existing changed entry and each is a stored ancestor of it.
// Every entry of a cycle is changed or such an ancestor, and the narrowed
// search, from the changed entries over just those, finds it. An entry the
// merge creates has nothing stored pointing at it: when every changed entry
// is new, the search visits only them. Requires egraphMu.
func (c *Cache) searchMergeGraphLocked(changed []*sharedResult, children func(*sharedResult) []*sharedResult, narrow bool) (mergeGraphSearch, error) {
	var (
		isChanged map[*sharedResult]bool
		reach     map[*sharedResult]bool
		search    mergeGraphSearch
	)
	if narrow {
		isChanged = make(map[*sharedResult]bool, len(changed))
		for _, res := range changed {
			isChanged[res] = true
		}
		reach = c.mergeReachLocked(changed)
		search.reach = len(reach)
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[*sharedResult]int{}
	var visit func(*sharedResult) *sharedResult
	visit = func(res *sharedResult) *sharedResult {
		switch state[res] {
		case visiting:
			return res
		case done:
			return nil
		}
		state[res] = visiting
		if narrow && c.testMergeGraphVisited != nil {
			c.testMergeGraphVisited(res.id)
		}
		for _, child := range children(res) {
			if child == nil || narrow && !isChanged[child] && !reach[child] {
				continue
			}
			if cycle := visit(child); cycle != nil {
				return cycle
			}
		}
		state[res] = done
		return nil
	}
	for _, res := range changed {
		if cycle := visit(res); cycle != nil {
			search.visits = len(state)
			return search, fmt.Errorf("merge values: the merge would close a dependency cycle through entry %d", cycle.id)
		}
	}
	search.visits = len(state)
	return search, nil
}

// mergeReachLocked returns the stored ancestors of the changed entries that
// already exist: every entry with a stored path to one, walked up through
// each entry's dependents (depParents) and through the entries whose offers
// it owns (offerParents, to the entry holding the owner's offer). An entry
// the merge creates isn't registered yet, and nothing stored points at it.
// Requires egraphMu.
func (c *Cache) mergeReachLocked(changed []*sharedResult) map[*sharedResult]bool {
	reach := map[*sharedResult]bool{}
	var queue []*sharedResult
	for _, res := range changed {
		if res.id != 0 && c.resultsByID[res.id] == res {
			queue = append(queue, res)
		}
	}
	add := func(res *sharedResult) {
		if res != nil && !reach[res] {
			reach[res] = true
			queue = append(queue, res)
		}
	}
	for len(queue) > 0 {
		res := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if res.depParents != nil {
			for id := range res.depParents {
				add(c.resultsByID[id])
			}
		}
		for id := range res.offerParents {
			add(c.offerHolderLocked(c.offerOwners[id]))
		}
	}
	return reach
}

// offerHolderLocked returns the entry whose offer owner holds, or nil for an
// owner that holds none: one prepared and not attached, or released.
// Requires egraphMu.
func (c *Cache) offerHolderLocked(owner *offerOwner) *sharedResult {
	if owner == nil || owner.slots == 0 {
		return nil
	}
	holder := c.resultsByID[owner.origin]
	if holder == nil {
		return nil
	}
	key, err := partAddressKey(owner.address)
	if err != nil {
		return nil
	}
	if offer := holder.partOfferLocked(key); offer == nil || offer.owner != owner {
		return nil
	}
	return holder
}

// MergeGraphJudgement is one merge's cycle check decided both ways: by the
// narrowed search that decides it, and by the whole search it replaced, with
// what each visited and how long it took. NarrowReach is how many stored
// ancestors the narrowed search walked up to first.
type MergeGraphJudgement struct {
	From                                   CacheID
	Narrow, Whole                          error
	NarrowVisits, NarrowReach, WholeVisits int
	NarrowTime, WholeTime                  time.Duration
}

// Disagrees reports that the two searches decided the merge differently.
func (j MergeGraphJudgement) Disagrees() bool {
	return (j.Narrow == nil) != (j.Whole == nil)
}

var mergeGraphJudge atomic.Pointer[func(context.Context, MergeGraphJudgement)]

// JudgeMergeGraphs has every merge's cycle check from now on also decided by
// the whole search the narrowed one replaced, and gives judge each merge's
// two decisions; nil stops it. Only tests and judging builds call it: the
// narrowed search alone decides a merge either way.
func JudgeMergeGraphs(judge func(context.Context, MergeGraphJudgement)) {
	if judge == nil {
		mergeGraphJudge.Store(nil)
		return
	}
	mergeGraphJudge.Store(&judge)
}

// installedInDependencyOrder returns the rows that install a record,
// dependencies before dependents over their final dependencies.
func (commit *valueMergeCommit) installedInDependencyOrder() []*mergeRow {
	var order []*mergeRow
	visited := map[*mergeRow]bool{}
	var visit func(*mergeRow)
	visit = func(row *mergeRow) {
		if visited[row] {
			return
		}
		visited[row] = true
		for _, dep := range row.deps {
			if depRow := commit.installed[commit.byNumber[dep]]; depRow != nil {
				visit(depRow)
			}
		}
		order = append(order, row)
	}
	for _, row := range commit.rows {
		if commit.installed[row.res] == row {
			visit(row)
		}
	}
	return order
}

// mergeEntryLocked returns the entry a merge leaves under number: one it
// creates, or one the cache has. Requires egraphMu.
func (c *Cache) mergeEntryLocked(commit *valueMergeCommit, number sharedResultID) *sharedResult {
	if res := commit.byNumber[number]; res != nil {
		return res
	}
	return c.resultsByID[number]
}

// commitValueMergeLocked changes the cache as the merge decided, and reads
// the reply. Nothing it does can fail short of an invariant violation: an
// engine entry that meets one is taken back as a failed replacement is, and a
// Cloud entry goes back to no value, keeping its holdings. It returns the
// work that runs after the lock is released. Requires egraphMu for writing.
func (c *Cache) commitValueMergeLocked(ctx context.Context, commit *valueMergeCommit) (MergeReply, []mergeReplyParts, func(context.Context) error) { //nolint:gocyclo // one commit, step by step, under one hold of the graph lock
	c.initEgraphLocked()
	if c.offerOwners == nil {
		c.offerOwners = map[offerOwnerID]*offerOwner{}
	}
	var (
		queue    collectionQueue
		errs     error
		replaced []mergeReplacement
		change   RemoteChange
		created  collectionQueue
	)
	// The merge's origin commits now, before any entry names it. The Cloud
	// keeps none.
	origin := commit.prep.origin
	if origin != nil {
		origin.committedAt = time.Now().UnixNano()
	}

	// The values.
	for _, row := range commit.rows {
		if c.blobBacked && row.action.installs() {
			row.run, row.runRan = c.mergeRunLocked(commit.prep, row)
			row.parts = commit.prep.partFacts[row.ordinal]
		}
		switch row.action {
		case mergeKeep:
			continue
		case mergeRetire:
			c.retireResultLocked(row.target)
			fallthrough
		case mergeCreate:
			c.installMergedRecordLocked(row.res, row, origin)
			c.putResultLocked(row.res)
			row.res.onRelease = c.resultSnapshotLeaseCleanup(row.res)
			c.indexRecipeLocked(row.plan.recipe, row.res)
			created = append(created, row.res)
		case mergeStore:
			c.installMergedRecordLocked(row.res, row, origin)
			if row.res.onRelease == nil {
				row.res.onRelease = c.resultSnapshotLeaseCleanup(row.res)
			}
		case mergeReplace:
			fresh := &sharedResult{}
			c.installMergedRecordLocked(fresh, row, origin)
			oldFrame := row.res.entryFrameLocked()
			old, more, err := c.replaceResultValueInPlaceLocked(ctx, row.res, fresh, row.value.ExpiresAtUnix)
			queue = append(queue, more...)
			errs = errors.Join(errs, err)
			// The entry took the fresh record's frame: the content digests
			// spans taught it before go on that frame too, and those of the
			// replaced frame when it has none (a restored entry).
			if len(row.res.taught) == 0 && !row.res.taughtFrom.ok() {
				row.res.taughtFrom = oldFrame
			}
			row.res.taughtGiven = 0
			row.res.taughtFrame++
			c.queueTaughtContentLocked(row.res)
			row.res.expiresAtUnix = row.value.ExpiresAtUnix
			// The old value's release runs after the lock; until it has,
			// readers wait on the entry's attachment.
			row.res.attachDepsMu.Lock()
			row.res.attachDepsWaitCh = make(chan struct{})
			row.res.attachDepsErr = nil
			// A merge has no span of its own to target.
			row.res.attachHolder = newAttachHolder(wcprof.CurrentOpID(ctx), nil)
			row.res.attachDepsMu.Unlock()
			replaced = append(replaced, mergeReplacement{res: row.res, old: old})
		}
	}
	for _, row := range commit.rows {
		if !row.action.installs() {
			continue
		}
		res := row.res
		if res.deps == nil {
			res.deps = make(map[sharedResultID]struct{}, len(row.deps))
		}
		for _, depID := range row.deps {
			dep := c.resultsByID[depID]
			if dep == nil {
				errs = errors.Join(errs, fmt.Errorf("merge values: missing dependency %d of %d", depID, res.id))
				continue
			}
			res.deps[depID] = struct{}{}
			c.rememberDependencyEdgeLocked(res, dep)
			c.incrementIncomingOwnershipLocked(ctx, dep)
		}
		res.dependencyOwnershipRevision++
		if err := c.bindMergedFrameLocked(res, row.action == mergeReplace || row.action == mergeStore); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	// Requirements are recomputed one level deep, so dependencies first: in
	// the final graph's order, which a recipe's best record can change from
	// the bundle's. checkMergeGraphLocked refused any cycle.
	for _, row := range commit.installedInDependencyOrder() {
		if _, err := c.recomputeRequiredSessionResourcesLocked(row.res); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	// Identity, and the sender's holding.
	c.applyMergedIdentitiesLocked(ctx, commit, &change)

	// Retention: an engine's retention edge, or the Cloud's stored root,
	// but for a root the caller left unretained (Unretained).
	for i, root := range commit.prep.bundle.Roots {
		if commit.roots[i].Expired || commit.prep.unretained[root.Ordinal] {
			continue
		}
		row := commit.byOrdinal[uint64(root.Ordinal)]
		c.upsertPersistedEdgeLocked(ctx, row.res, root.ExpiresAtUnix, false)
	}

	// Parts: an engine admits the incoming offers by what each target kept.
	released, err := c.applyMergedOffersLocked(ctx, commit)
	queue = append(queue, released...)
	errs = errors.Join(errs, err)
	for _, r := range replaced {
		more, err := c.releaseReplacedDependenciesLocked(ctx, r.old, nil)
		queue = append(queue, more...)
		errs = errors.Join(errs, err)
	}
	if errs != nil {
		// Only an invariant violation gets here.
		for _, row := range commit.rows {
			if row.action.installs() {
				queue = c.failMergedEntryLocked(ctx, row.res, queue)
			}
		}
	}
	// A restore's commit holds every entry it lands on, before anything
	// unowned is collected: a root it leaves unretained lives through the
	// hold alone.
	var held []*sharedResult
	if commit.hold && errs == nil {
		seen := map[*sharedResult]bool{}
		for _, row := range commit.rows {
			if row.res == nil || seen[row.res] || row.res.noValueLocked() {
				continue
			}
			seen[row.res] = true
			c.incrementIncomingOwnershipLocked(ctx, row.res)
			held = append(held, row.res)
		}
	}
	// An entry the merge created that nothing owns, such as a dependency
	// only a kept record's bundle needed, is collected now; the reply leaves
	// it out. Roots are retained, and an installed record owns its
	// dependencies.
	releases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), created)
	errs = errors.Join(errs, collectErr)
	more, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
	releases = append(releases, more...)
	errs = errors.Join(errs, collectErr)

	reply, parts := c.mergeReplyLocked(commit)
	c.takeChangesLocked(&change)
	reply.Change = change
	if commit.hold {
		reply.Release = c.releaseHeld(held)
	}
	attached := commit.attached
	finish := func(ctx context.Context) error {
		cleanupCtx := context.WithoutCancel(ctx)
		// Merged offers are prefetched as offered ones are, a restore's
		// from the backlog. An offer the commit's failure path retired since
		// is skipped when its job runs.
		for _, a := range attached {
			if commit.restore {
				c.partOfferRestored(cleanupCtx, a.res, a.key, a.offer)
			} else {
				c.partOfferAccepted(cleanupCtx, a.res, a.key, a.offer)
			}
		}
		err := errors.Join(errs, runOnReleaseFuncs(cleanupCtx, releases))
		for _, r := range replaced {
			err = errors.Join(err, c.finishMergedReplacement(cleanupCtx, r))
		}
		return err
	}
	return reply, parts, finish
}

// applyMergedIdentitiesLocked gives every row's entry the record's identity
// and the sender's holding: on an engine its Cloud or image holding, on the
// Cloud the sending engine's holding, adding to change. A held row teaches
// its entry nothing but, on an engine, the sender's holding. Requires
// egraphMu for writing.
func (c *Cache) applyMergedIdentitiesLocked(ctx context.Context, commit *valueMergeCommit, change *RemoteChange) {
	var cloudState *remoteCacheState
	if c.blobBacked {
		cloudState = c.remoteCacheLocked(commit.from)
	}
	for _, row := range commit.rows {
		if commit.prep.held[row.ordinal] {
			// A held row teaches its entry nothing: it is the entry's own
			// value. On the Cloud, it teaches the sender's holding of it
			// when the Cloud has none yet (mergeHeldSenderHoldingLocked).
			if !c.blobBacked {
				row.res.noteSenderCopyLocked(commit.from, row.value.SenderNumber, true, row.value.ExpiresAtUnix)
			} else {
				c.mergeHeldSenderHoldingLocked(ctx, cloudState, commit, row, change)
			}
			continue
		}
		if c.unchangedCloudRowLocked(commit, row) {
			// The record and its expiry are those the entry merged, and
			// nothing changed the entry since: its identity and the Cloud's
			// copy are what the record would teach it. Its calls are taught
			// below all the same: two sets' files carry the calls of their
			// own checks' runs, so the same record can come with others.
			row.unchanged = true
			continue
		}
		frame := row.plan.row.loadResultCall()
		if row.action.installs() {
			frame = row.res.loadResultCall()
		}
		// A kept record whose sender's holding is here already teaches its
		// entry only what the holding didn't: counted, for the reply.
		var mark identityMark
		if c.blobBacked && row.action == mergeKeep {
			if _, h := c.holdingLocked(HolderKey{Cache: commit.from, Number: row.value.SenderNumber}); h != nil {
				row.keptWithHolding = true
				mark = c.identityMarkLocked(row.res)
			}
		}
		c.applyPreparedResultIdentityLocked(ctx, row.res, frame, row.plan.recipe, row.plan.self, row.plan.inputs, row.plan.provenance, row.plan.recipe)
		if row.keptWithHolding {
			row.keptTaught = c.identityMarkLocked(row.res) != mark
		}
		if !c.blobBacked {
			// The sender's entry stores the record: the Cloud's, with the
			// record's expiry, or the engine image's.
			row.res.noteSenderCopyLocked(commit.from, row.value.SenderNumber, true, row.value.ExpiresAtUnix)
			c.noteCloudMergeLocked(commit, row)
			continue
		}
		c.mergeSenderHoldingLocked(ctx, cloudState, commit, row, change)
	}
	for _, merged := range commit.prep.calls {
		row := commit.byOrdinal[merged.value]
		if row == nil || row.res == nil {
			continue
		}
		// The row's expiry is the earliest of its calls', the Cloud's
		// value's own included. Once it has passed, the call has expired:
		// as a local engine runs such a call again rather than hit, the
		// engine isn't taught it, whatever value it holds.
		if expires := row.value.ExpiresAtUnix; expires != 0 && expires <= commit.now {
			continue
		}
		// As a local cache does when a call returns an existing entry, the
		// entry takes the earliest expiry of its calls, though the entry
		// was here before.
		// Each input goes in the class of the entry its row lands on, an
		// entry of the input's class on the Cloud, before the call's term
		// is derived over its inputs' classes. An input whose row landed
		// nowhere, or has expired, places nothing: the call isn't taught.
		places, ok := c.mergedCallPlacesLocked(commit, merged)
		if !ok {
			continue
		}
		row.res.expiresAtUnix = mergeSharedResultExpiryUnix(row.res.expiresAtUnix, row.value.ExpiresAtUnix)
		c.markEntryClassLocked(row.res.id, StampMergeExpiry)
		for i, place := range places {
			if place != nil {
				c.joinDigestToResultClassLocked(ctx, merged.id.inputs[i], place)
			}
		}
		c.teachCallIdentityLocked(ctx, row.res, merged.id)
	}
}

// mergedCallPlacesLocked returns, by input of merged, the entry its row
// lands on, nil for a witness: a held row's, the recipient's current entry
// of the row's recipe, if it still holds it; a carried row's, the entry the
// commit gives it. It reports false when an input's row landed nowhere: the
// call alone goes untaught. Requires egraphMu.
func (c *Cache) mergedCallPlacesLocked(commit *valueMergeCommit, merged mergedCall) ([]*sharedResult, bool) {
	places := make([]*sharedResult, len(merged.inputs))
	for i, ordinal := range merged.inputs {
		if ordinal == 0 {
			continue
		}
		if commit.prep.held[ordinal] || commit.prep.callOnly[ordinal] {
			row := commit.prep.index[ordinal]
			place := c.currentEntryForRecipeLocked(row.Held)
			if c.heldRowStaleLocked(place, row.ExpiresAtUnix, commit.now) || c.resultExpiredAtLocked(place, commit.now) {
				return nil, false
			}
			places[i] = place
			continue
		}
		row := commit.byOrdinal[ordinal]
		if row == nil || row.res == nil || row.expired || row.keptExpired {
			return nil, false
		}
		places[i] = row.res
	}
	return places, true
}

// joinDigestToResultClassLocked puts dig in res's output class: dig is a
// digest of the class on the sender, which holds it there from an engine's
// word. Requires egraphMu for writing.
func (c *Cache) joinDigestToResultClassLocked(ctx context.Context, dig digest.Digest, res *sharedResult) {
	ids := []eqClassID{c.ensureEqClassForDigestLocked(ctx, dig.String())}
	for root := range c.outputEqClassesForResultLocked(res.id) {
		ids = append(ids, root)
	}
	c.mergeEqClassesLocked(ctx, ids...)
}

// unchangedCloudRowLocked reports whether row, a record of the Cloud's an
// engine keeps on an entry it has, would teach the entry nothing again: the
// entry's Cloud holding is the row's Cloud entry, storing a record at the
// row's expiry, and the engine last merged it from the same start of the
// same Cloud cache at the change stamp the row carries (cloudMergeNote),
// with the entry's own transfer
// revision unchanged since, so that neither side changed what the record
// teaches; and neither the row nor the value it lands on has expired. A
// pruned entry isn't kept, a replaced or re-run one has moved its revision,
// and a Cloud change has moved the stamp: each takes the commit's whole path.
// Requires egraphMu.
func (c *Cache) unchangedCloudRowLocked(commit *valueMergeCommit, row *mergeRow) bool {
	if c.blobBacked || c.testNoUnchangedRows || commit.from != cloudCacheID || row.action != mergeKeep || row.expired || row.keptExpired {
		return false
	}
	key, h := row.res.cloudHoldingLocked()
	if h == nil || h.cloudMerge == nil || key.Number != row.value.SenderNumber || h.unstored || h.expiresAtUnix != row.value.ExpiresAtUnix {
		return false
	}
	note := h.cloudMerge
	return commit.prep.bundle.SenderCache != "" && note.cache == commit.prep.bundle.SenderCache && note.stamp == row.value.SenderStamp && note.revision == row.res.transferRevision
}

// noteCloudMergeLocked records, on an engine, what row merged of its Cloud
// entry onto the entry's Cloud holding (cloudMergeNote), once its identity
// and the Cloud's copy are taught. Requires egraphMu for writing.
func (c *Cache) noteCloudMergeLocked(commit *valueMergeCommit, row *mergeRow) {
	if commit.from != cloudCacheID {
		return
	}
	key, h := row.res.cloudHoldingLocked()
	if h == nil || key.Number != row.value.SenderNumber {
		return
	}
	if commit.prep.bundle.SenderCache == "" {
		// A Cloud cache with no identity: its stamps compare with none.
		h.cloudMerge = nil
		return
	}
	h.cloudMerge = &cloudMergeNote{cache: commit.prep.bundle.SenderCache, stamp: row.value.SenderStamp, revision: row.res.transferRevision}
}

// identityMark is what teaching an entry its identity can change, read
// before and after: the cache's unions of classes and terms made, and the
// entry's terms, digests and its classes' extra digests. Teaching that
// leaves it equal taught the entry nothing.
type identityMark struct {
	unions          uint64
	nextTerm        egraphTermID
	terms, postings int
	extras          int
}

// identityMarkLocked reads res's identity mark. Requires egraphMu.
func (c *Cache) identityMarkLocked(res *sharedResult) identityMark {
	mark := identityMark{unions: c.egraphUnions, nextTerm: c.nextEgraphTermID, terms: len(c.resultTerms[res.id]), postings: len(c.resultIndexedDigests[res.id])}
	for id := range c.resultOutputEqClasses[res.id] {
		mark.extras += len(c.eqClassExtraDigests[c.eqClassRootLocked(id)])
	}
	return mark
}

// teachCallIdentityLocked teaches res a call that returned it, as
// indexWaitResultInEgraphLocked does when the call completes: the call's
// recipe digest joins res's class, and the call's term, over its inputs'
// classes, outputs that class. The call's inputs are known by digest only.
// Requires egraphMu for writing.
func (c *Cache) teachCallIdentityLocked(ctx context.Context, res *sharedResult, id callIdentity) {
	provenance := make([]egraphInputProvenanceKind, len(id.inputs))
	for i := range provenance {
		provenance[i] = egraphInputProvenanceKindDigest
	}
	frame := &ResultCall{Kind: ResultCallKindField, Field: id.field, ExtraDigests: id.extras}
	c.applyPreparedResultIdentityLocked(ctx, res, frame, id.recipe, id.self, id.inputs, provenance, id.recipe)
}

// mergeReplacement is a replacement in place that a merge made, for the work
// after the lock: releasing the old value.
type mergeReplacement struct {
	res *sharedResult
	old replacedValue
}

// installMergedRecordLocked gives res the row's relocated record as its
// value: stored and not decoded, from the merge of origin, nil on the Cloud.
// res is a new entry, an entry with no value, or the fresh value of a
// replacement. Requires egraphMu for writing.
func (c *Cache) installMergedRecordLocked(res *sharedResult, row *mergeRow, origin *mergeOrigin) {
	rec := row.rec
	// On the Cloud, the stored record names the row's run, which goes with
	// it, and whether that run ran its step as far as the Cloud knows yet.
	res.run, res.runRan = nil, runNotRan
	// A record whose run isn't known names none (mergeRunLocked).
	if c.blobBacked && row.run != (RunName{}) {
		run := row.run
		res.run, res.runRan = &run, row.runRan
	}
	// The record's own parts and offers decide its value's sources now.
	res.unavailableParts = nil
	res.merged = origin
	c.setStoredRecordBytesLocked(res, row.recordBytes)
	res.expiresAtUnix = row.value.ExpiresAtUnix
	// A capture reads these with the value (loadPayloadState).
	res.payloadMu.Lock()
	res.imported = true
	res.sessionResourceHandle = rec.Envelope.SessionResourceHandle
	res.boundSession = rec.Envelope.BoundSession
	res.payloadMu.Unlock()
	res.description = row.plan.row.description
	res.recordType = row.plan.row.recordType
	res.storeResultCall(rec.Call)
	// The record's frame has taken none of the content digests spans taught
	// the entry before it arrived (an entry known only through holdings,
	// say): they go on it too. A prune took those taught for the value it
	// dropped (dropValueLocked).
	if res.taughtDropped {
		res.taughtDropped = false
		c.taughtContentRefilled.Add(1)
	}
	res.taughtGiven = 0
	res.taughtFrame++
	c.queueTaughtContentLocked(res)
	res.payloadMu.Lock()
	res.isObject = rec.Envelope.Kind == persistedResultKindObject
	res.hasValue = false
	res.self = nil
	res.persistedEnvelope = &rec.Envelope
	res.firstUse = row.firstUse
	// The body worker compresses it after the merge (queueStoredBodies). Its
	// parts are its preparation's, on the Cloud.
	res.stored = nil
	res.partFacts = row.parts
	res.payloadRevision++
	c.setResultPayloadBytesLocked(res, persistedEnvelopePayloadBytes(&rec.Envelope))
	if res.createdAtUnixNano == 0 {
		res.createdAtUnixNano = time.Now().UnixNano()
	}
	res.payloadMu.Unlock()
	if c.blobBacked {
		// On the Cloud, an export that stores a value uses it.
		touchSharedResultLastUsed(res, time.Now().UnixNano())
	}
	res.transferRevision++
	if c.resultsByID[res.id] == res {
		c.markEntryClassLocked(res.id, StampMergeInstall)
	}
	c.poolSettleLocked(res)
}

// mergeSenderHoldingLocked records, on the Cloud, the sending engine's holding
// of the row's entry: created if absent, with its dependencies from the
// record's, named by the sender's numbers, at the record's replacement count.
// An export reports no parts and no expiry; the sender's rows do. Requires
// egraphMu for writing.
func (c *Cache) mergeSenderHoldingLocked(ctx context.Context, state *remoteCacheState, commit *valueMergeCommit, row *mergeRow, change *RemoteChange) {
	key := HolderKey{Cache: commit.from, Number: row.value.SenderNumber}
	entry, h := c.holdingLocked(key)
	if h == nil {
		entry, h = c.newHoldingLocked(ctx, state, key, RemoteHolding{Recipe: row.plan.recipe, Field: row.res.description})
		change.Candidates = append(change.Candidates, key)
	}
	deps := make([]uint64, 0, len(row.value.DependencyIDs))
	for _, dep := range row.value.DependencyIDs {
		deps = append(deps, commit.prep.index[dep].SenderNumber)
	}
	candidates := c.applyHeldValueStateLocked(ctx, state, key, entry, h, HeldValueState{Replacements: row.value.SenderReplacements, Deps: deps}, nil, nil)
	change.Candidates = append(change.Candidates, candidates...)
}

// mergeHeldSenderHoldingLocked records, on the Cloud, the sending engine's
// holding of a held row's entry when the Cloud has none: an engine sends a
// row the Cloud has from another engine as a held row, often before its
// telemetry names the row, and the rows above it name it as a dependency.
// The holding starts at the row's replacement count with no dependencies:
// the row carries none, and the engine's telemetry adds them, as it does
// for any holding. A holding the Cloud has already is left as it is: a held
// row with no dependencies has nothing to add to it. Requires egraphMu for
// writing.
func (c *Cache) mergeHeldSenderHoldingLocked(ctx context.Context, state *remoteCacheState, commit *valueMergeCommit, row *mergeRow, change *RemoteChange) {
	key := HolderKey{Cache: commit.from, Number: row.value.SenderNumber}
	if _, h := c.holdingLocked(key); h != nil {
		return
	}
	entry, h := c.newHoldingLocked(ctx, state, key, RemoteHolding{Recipe: row.plan.recipe, Field: row.res.description})
	change.Candidates = append(change.Candidates, key)
	candidates := c.applyHeldValueStateLocked(ctx, state, key, entry, h, HeldValueState{Replacements: row.value.SenderReplacements}, nil, nil)
	change.Candidates = append(change.Candidates, candidates...)
}

// selectMergedOffersLocked decides, before anything changes, which offers
// each entry is left with, by section 5.1 point 3, against the offers and
// complete parts each entry has as the commit leaves it: a record the entry
// now stores keeps its parts; an unexpired record gives its parts to
// whichever value the entry keeps; an expired record gives an expired kept
// value only the parts it lacks, and an unexpired kept value none. A new or
// replaced entry starts with no offer, and a kept one with its own. A part
// complete there takes no offer, and an offer that means the same as the one
// already there changes nothing. Selection takes the rows in commit order, so
// a later row's admitted offer supersedes an earlier one for the same part.
// applyMergedOffersLocked then attaches the final set, in two steps.
// Requires egraphMu.
func (c *Cache) selectMergedOffersLocked(commit *valueMergeCommit) error {
	held := map[*sharedResult]map[string]PersistedPartOffer{}
	commit.finalOffers = held
	commit.newOffers = map[*sharedResult]map[string]bool{}
	heldOffers := func(res *sharedResult) map[string]PersistedPartOffer {
		if offers := held[res]; offers != nil {
			return offers
		}
		offers := map[string]PersistedPartOffer{}
		if commit.installed[res] == nil {
			for key, offer := range res.partOffersLocked() {
				offers[key] = offer.record
			}
		}
		held[res] = offers
		return offers
	}
	for _, row := range commit.rows {
		row.admitted = nil
		if len(row.offers) == 0 || !row.action.installs() && row.expired && !row.keptExpired {
			continue
		}
		complete := map[string]bool{}
		for _, key := range c.mergedCompletePartKeysLocked(commit, row.res) {
			complete[key] = true
		}
		offers := heldOffers(row.res)
		for _, offer := range row.offers {
			key, err := partAddressKey(offer.Address)
			if err != nil {
				return err
			}
			if complete[key] {
				continue
			}
			if existing, ok := offers[key]; ok && (row.expired || sameOfferMeaning(existing, offer)) {
				continue
			}
			offers[key] = offer
			row.admitted = append(row.admitted, offer)
			if commit.newOffers[row.res] == nil {
				commit.newOffers[row.res] = map[string]bool{}
			}
			commit.newOffers[row.res][key] = true
		}
	}
	return nil
}

// applyMergedOffersLocked applies the offers selectMergedOffersLocked chose,
// after the dependency edges are in place, in two steps. It retires every
// offer an entry holds that its final set replaces or drops, then attaches
// each final offer the entry doesn't already hold: one per part key, however
// many rows admitted one there. Removing an offer can't close a cycle, and
// while it attaches, every graph is part of the final one checkMergeGraphLocked
// accepted, so no attachment's own check can refuse it. What the retired
// offers' owners release is collected with the rest of the commit, at its
// end, after the final offers hold their owners. The offers it attaches go
// to commit.attached, for prefetch. Requires egraphMu for writing.
func (c *Cache) applyMergedOffersLocked(ctx context.Context, commit *valueMergeCommit) (collectionQueue, error) {
	var (
		entries []*sharedResult
		queue   collectionQueue
		errs    error
	)
	seen := map[*sharedResult]bool{}
	for _, row := range commit.rows {
		if commit.from == cloudCacheID && row.res.imageSuppliesLocked() {
			// The image supplies the entry's parts: its Cloud offers are
			// left out. A record the image ships without a part takes them.
			continue
		}
		if !seen[row.res] && len(commit.newOffers[row.res]) > 0 {
			seen[row.res] = true
			entries = append(entries, row.res)
		}
	}
	for _, res := range entries {
		final, fresh := commit.finalOffers[res], commit.newOffers[res]
		var retired []PersistedPartAddress
		for key, offer := range res.partOffersLocked() {
			if _, kept := final[key]; !kept || fresh[key] {
				retired = append(retired, offer.record.Address)
			}
		}
		for _, address := range retired {
			more, err := c.retirePartOfferLocked(ctx, res, address)
			queue = append(queue, more...)
			errs = errors.Join(errs, err)
		}
	}
	for _, res := range entries {
		final := commit.finalOffers[res]
		keys := slices.Sorted(maps.Keys(commit.newOffers[res]))
		for _, key := range keys {
			offer := final[key]
			owner, err := c.newOfferOwnerLocked(ctx, offer.Owner)
			if err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			offer.Owner = owner.record
			attached := &partOffer{record: offer, owner: owner}
			if err := c.attachPartOfferLocked(res, commit.from, offer.Address, attached); err != nil {
				more, releaseErr := c.releaseOfferOwnerLocked(ctx, owner)
				queue = append(queue, more...)
				errs = errors.Join(errs, err, releaseErr)
				continue
			}
			commit.attached = append(commit.attached, mergeAttachedOffer{res: res, key: key, offer: attached})
		}
	}
	return queue, errs
}

// mergedCompletePartKeysLocked returns the complete parts of res as the merge
// leaves it. For an entry that takes a record, they are what the record
// proves, the part probe's LocalComplete over it, as the entry's own spans
// report them, read outside the lock (valueMergePrep.completeParts). For an
// entry that keeps its value, they are completePartKeysLocked's. Requires
// egraphMu.
func (c *Cache) mergedCompletePartKeysLocked(commit *valueMergeCommit, res *sharedResult) []string {
	row := commit.installed[res]
	if row == nil {
		return completePartKeysLocked(res)
	}
	return commit.prep.completeParts[row.ordinal]
}

// memoRecordCompletePartKeys returns the part keys record proves complete: the
// part probe's LocalComplete over it, sorted, or none if the record doesn't
// describe its parts.
func memoRecordCompletePartKeys(record PersistedRecord, memos payloadMemos) []string {
	probes, err := describeMemoPartRecord(record, memos)
	if err != nil {
		return nil
	}
	var keys []string
	for _, probe := range probes {
		if !probe.LocalComplete {
			continue
		}
		if key, err := partAddressKey(probe.Descriptor.Address); err == nil {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// completePartKeysLocked returns res's complete parts as far as they are known
// under the lock: those its value's last captured record reports, while that
// capture still describes the value, and those its part gate has settled.
func completePartKeysLocked(res *sharedResult) []string {
	keys, _ := completePartKeysCoverageLocked(res)
	return keys
}

// completePartKeysCoverageLocked is completePartKeysLocked, also reporting
// whether a current capture of the value's record was among its sources:
// without one, the keys are only the settled subset.
func completePartKeysCoverageLocked(res *sharedResult) ([]string, bool) {
	keys := res.settledPartKeys()
	res.payloadMu.RLock()
	carried, unprepared := res.firstUseCompletePartsLocked()
	res.payloadMu.RUnlock()
	if unprepared {
		// A record merged unprepared proves the parts its bundle carried,
		// as describing it once prepared finds them.
		keys = append(keys, carried...)
		slices.Sort(keys)
		return slices.Compact(keys), true
	}
	cached := res.completeParts.Load()
	current := cached != nil && cached.stamp.current(res)
	if current {
		keys = append(keys, cached.keys...)
	}
	slices.Sort(keys)
	return slices.Compact(keys), current
}

// failMergedEntryLocked takes back an entry whose merged value met an
// invariant violation. An engine entry leaves the recipe index and loses its
// retention edge, as a failed replacement does, and is collected once nothing
// owns it: an engine entry with no value would read as a cached nil result.
// A Cloud entry goes back to no value and keeps its holdings. Requires
// egraphMu for writing.
func (c *Cache) failMergedEntryLocked(ctx context.Context, res *sharedResult, queue collectionQueue) collectionQueue {
	if c.blobBacked {
		c.markEntryClassLocked(res.id, StampMergeFailed)
		c.setStoredRecordBytesLocked(res, 0)
		res.storedTransfer.Store(nil)
		res.payloadMu.Lock()
		res.persistedEnvelope = nil
		res.stored = nil
		res.hasValue = false
		res.payloadRevision++
		res.payloadMu.Unlock()
		c.setResultPayloadBytesLocked(res, 0)
		c.poolSettleLocked(res)
		return queue
	}
	c.unindexRecipesLocked(res)
	if _, retained := c.persistedEdgesByResult[res.id]; retained {
		delete(c.persistedEdgesByResult, res.id)
		c.poolRootLocked(res, -1)
		queue, _ = c.decrementIncomingOwnershipLocked(ctx, res, queue)
	}
	return append(queue, res)
}

// finishMergedReplacement releases the value a merge replaced, resets the
// per-value state the stored record starts without, and then lets readers
// in. If the release fails, the entry is taken back as a failed replacement
// is.
func (c *Cache) finishMergedReplacement(ctx context.Context, r mergeReplacement) error {
	err := c.finishValueReplacement(ctx, r.res, r.old.release)
	if err != nil {
		c.lockGraphForCtx(ctx, workMerge)
		queue := c.failMergedEntryLocked(ctx, r.res, nil)
		releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
		// This write hands back no marks: the entry taken back is stamped now.
		c.stampPendingLocked()
		c.egraphMu.Unlock()
		err = errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
	}
	r.res.attachDepsMu.Lock()
	ended := r.res.attachDepsWaitCh != nil
	if ended {
		r.res.attachDepsErr = err
		close(r.res.attachDepsWaitCh)
		r.res.attachHolder = nil
	}
	r.res.attachDepsMu.Unlock()
	// The attachment kept the row busy for a capture (BusyRows).
	if ended {
		c.busyEnded(r.res.id)
	}
	return err
}

// bindMergedFrameLocked binds the references of the frame a merge installed
// on res to the entries they name. published says whether res was in the
// cache before the merge: an entry that took a record, or whose value the
// merge replaced in place. Such an entry's frame is already published, and on
// the Cloud a capture outside egraphMu, a prepared merge's warming, can be
// cloning it: the references are bound on a copy, which then replaces it.
// Requires egraphMu for writing.
func (c *Cache) bindMergedFrameLocked(res *sharedResult, published bool) error {
	frame := res.loadResultCall()
	if published && frame != nil {
		frame = frame.clone()
	}
	err := walkTransferCalls(frame, func(*ResultCall) error { return nil }, func(ref *ResultCallRef) error {
		ref.shared = c.resultsByID[sharedResultID(ref.ResultID)]
		return nil
	})
	if published && frame != nil {
		res.storeResultCall(frame)
	}
	return err
}

// mergeReplyLocked reads every target as the commit left it, with each
// value's part keys beside the reply, for finishMergeReply to finish it once
// egraphMu is let go. Requires egraphMu.
func (c *Cache) mergeReplyLocked(commit *valueMergeCommit) (MergeReply, []mergeReplyParts) {
	reply := MergeReply{Generation: c.identity.Generation, EngineTimeUnixNano: time.Now().UnixNano()}
	var parts []mergeReplyParts
	for _, row := range commit.rows {
		reply.Rows.count(row)
		res := row.res
		if c.resultsByID[res.id] != res {
			// The commit's collection removed an entry the merge created
			// that nothing owns: there is no target to report.
			continue
		}
		value := MergedValue{Ordinal: row.value.Ordinal, Number: uint64(res.id), Replacements: res.replacements, Revision: RowRevision{Replacements: res.replacements, Transfer: res.transferRevision, Dependencies: res.dependencyOwnershipRevision}, ExpiresAtUnix: res.expiresAtUnix}
		value.Stored = row.value.Held == "" && (row.action == mergeCreate || row.action == mergeStore || row.action == mergeReplace || row.action == mergeRetire)
		for dep := range res.deps {
			value.Deps = append(value.Deps, uint64(dep))
		}
		// A blob-backed cache, the Cloud's, holds no local completion or
		// offer of a part (SetStoredPart keeps its parts), and its merge's
		// caller, the remote cache service, reads none from the reply.
		if !c.blobBacked {
			read := mergeReplyParts{complete: c.mergedCompletePartKeysLocked(commit, res)}
			for key := range res.partOffersLocked() {
				read.offered = append(read.offered, key)
			}
			parts = append(parts, read)
		}
		reply.Values = append(reply.Values, value)
	}
	for _, root := range commit.roots {
		if !root.Expired {
			res := commit.byOrdinal[uint64(root.Ordinal)].res
			root.Number = uint64(res.id)
			if edge, ok := c.persistedEdgesByResult[res.id]; ok {
				root.Retained, root.RetentionExpiresAtUnix = true, edge.expiresAtUnix
			}
		}
		reply.Roots = append(reply.Roots, root)
	}
	return reply, parts
}

// mergeReplyParts are a reply value's part keys as its commit read them under
// egraphMu: the parts its entry has complete, and the parts it is offered.
type mergeReplyParts struct {
	complete, offered []string
}

// finishMergeReply finishes a reply its commit read under egraphMu
// (mergeReplyLocked), with no lock held: it sorts each value's dependencies,
// and gives it its parts from parts, read beside it: the complete ones, then
// the offered ones that aren't complete, sorted by key.
func (c *Cache) finishMergeReply(reply *MergeReply, parts []mergeReplyParts) {
	for i := range reply.Values {
		slices.Sort(reply.Values[i].Deps)
	}
	if parts == nil {
		return
	}
	if c.testMergeReplyPartsDecoding != nil {
		c.testMergeReplyPartsDecoding()
	}
	for i, read := range parts {
		value := &reply.Values[i]
		complete := make(map[string]bool, len(read.complete))
		for _, key := range read.complete {
			complete[key] = true
			value.Parts = appendPartAddress(value.Parts, key)
		}
		offered := slices.DeleteFunc(read.offered, func(key string) bool { return complete[key] })
		// A part's key is its address's encoding (partAddressKey), so keys
		// sort as their addresses do (comparePartAddresses).
		slices.Sort(offered)
		for _, key := range offered {
			value.OfferedParts = appendPartAddress(value.OfferedParts, key)
		}
	}
}

func appendPartAddress(addresses []PersistedPartAddress, key string) []PersistedPartAddress {
	var address PersistedPartAddress
	if err := json.Unmarshal([]byte(key), &address); err != nil {
		return addresses
	}
	return append(addresses, address)
}

func comparePartAddresses(a, b PersistedPartAddress) int {
	ka, _ := partAddressKey(a)
	kb, _ := partAddressKey(b)
	switch {
	case ka < kb:
		return -1
	case ka > kb:
		return 1
	}
	return 0
}

// markUnprepared marks, on an engine, each record whose value carries its
// parts (TransferredValue.Parts) unprepared, with the complete parts it
// carries (valueMergePrep.unprepared). A blob-backed cache marks none.
func (prep *valueMergePrep) markUnprepared(c *Cache) {
	if c.blobBacked {
		return
	}
	for i := range prep.bundle.Values {
		row := &prep.bundle.Values[i]
		if row.Parts == nil || row.Held != "" {
			continue
		}
		if prep.unprepared == nil {
			prep.unprepared = map[uint64]bool{}
		}
		prep.unprepared[uint64(row.Ordinal)] = true
		prep.completeParts[uint64(row.Ordinal)] = partAddressKeys(row.Parts.Complete)
	}
}

// carriedDeclaredParts returns, on an engine, the parts each row of bundle
// declares, for the rows whose values carry their parts
// (TransferredValue.Parts): the bundle's outputs are checked against those,
// without mapping them from the payloads. A row whose carried part has no
// valid address is left out, and checked by mapping its payload, which
// refuses the address as a merge always has. Nil on a blob-backed cache, or
// for a bundle whose values carry none.
func carriedDeclaredParts(c *Cache, bundle ValueBundle) map[uint64][]declaredPart {
	if c.blobBacked {
		return nil
	}
	var declared map[uint64][]declaredPart
	for _, value := range bundle.Values {
		if value.Parts == nil || value.Held != "" {
			continue
		}
		parts, ok := carriedDeclared(value.Parts.Declared)
		if !ok {
			continue
		}
		if declared == nil {
			declared = map[uint64][]declaredPart{}
		}
		declared[uint64(value.Ordinal)] = parts
	}
	return declared
}

// carriedDeclared returns the declared parts a value carries with their
// keys, and false if one has no valid address.
func carriedDeclared(carried []TransferredDeclaredPart) ([]declaredPart, bool) {
	parts := make([]declaredPart, 0, len(carried))
	for _, part := range carried {
		key, err := partAddressKey(part.Address)
		if err != nil {
			return nil, false
		}
		parts = append(parts, declaredPart{key: key, address: part.Address, state: part.State, kind: part.Kind})
	}
	return parts, true
}
