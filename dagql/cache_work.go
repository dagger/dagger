package dagql

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
)

// cacheWork is a kind of work the cache does, for its diagnostics: the path
// a first use of a merged record came by (FirstUseStats.Paths), and the kind
// of writer holding the graph lock when a lookup waits for it
// (LookupLockWaits). Work under a context carries its kind (withCacheWork);
// work that carries none is workOther.
type cacheWork uint8

const (
	workOther cacheWork = iota
	// workLookup is a call's lookup of its result: its sections under the
	// graph lock, and the decode of a hit.
	workLookup
	// workPublication is a call's publication of its result.
	workPublication
	// workRestore is a merge of a part of a set file an engine restores.
	workRestore
	// workMerge is any other merge of the Cloud's records.
	workMerge
	// workExport is an export's capture of the records it sends.
	workExport
	// workOffer is the placing of the Cloud's offered parts on entries.
	workOffer
	// workPart is the fetching, installing and settling of an entry's
	// parts: a call's demand for one, and the prefetcher's, and a capture's
	// hold of the rows whose parts it reads.
	workPart
	// workTeach is a call's teaching of what its result is the same as: its
	// content's digest, or another call's.
	workTeach
	// workDependency is a call's explicit dependencies and retention.
	workDependency
	// workEvaluation is a lazy evaluation's own bookkeeping, such as the
	// ownership its attempt takes.
	workEvaluation
	// workUpkeep is the engine's housekeeping: a session's release, a
	// prune, the usage and size measurements, and a save.
	workUpkeep
	// workHold is the service's holds on entries, and their release.
	workHold
	// workRemote is the remote cache service's writes of engines' holdings
	// and of the store's parts, and its collections of them, that take the
	// graph lock themselves: those outside a hold the service took
	// (HoldRemoteWrites).
	workRemote
	// workBody is the body worker's installing of the stored bodies of
	// entries a merge or a restore landed, and of the content digests
	// taught to them.
	workBody
	numCacheWork
)

var cacheWorkNames = [numCacheWork]string{
	workOther:       "other",
	workLookup:      "lookup",
	workPublication: "publication",
	workRestore:     "restore",
	workMerge:       "merge",
	workExport:      "export",
	workOffer:       "offer",
	workPart:        "part",
	workTeach:       "teach",
	workDependency:  "dependency",
	workEvaluation:  "evaluation",
	workUpkeep:      "upkeep",
	workHold:        "hold",
	workRemote:      "remote",
	workBody:        "body",
}

func (w cacheWork) String() string { return cacheWorkNames[w] }

// graphHolders are the kinds of work, as the graph lock's writers name
// themselves (wcprof.WriteLockAs), by cacheWork. Their names are the
// cacheWork's.
var graphHolders = func() (holders [numCacheWork]wcprof.LockHolder) {
	for w := workOther + 1; w < numCacheWork; w++ {
		holders[w] = wcprof.NewLockHolder(w.String())
	}
	return holders
}()

// graphHolderWork is the cacheWork a graph-lock holder names, with
// wcprof.LockHolderReaders kept apart; a holder of another package's kind
// would read as workOther.
func graphHolderWork(h wcprof.LockHolder) (cacheWork, bool) {
	if h == wcprof.LockHolderReaders {
		return 0, false
	}
	for w, holder := range graphHolders {
		if holder == h {
			return cacheWork(w), true
		}
	}
	return workOther, true
}

// ObserveGraphHolds calls f with each hold of the graph lock for writing
// that the cache takes itself, as it ends, until the returned stop is
// called: the kind of work it held the lock for, whose name is its
// cacheWork's ("merge", "remote" and so on, "other" for none), how long it
// waited to take the lock, and how long it held it. A hold through
// HoldRemoteWrites is its holder's to time, and isn't seen. f runs in the
// releasing goroutine while the lock is still held, so it must not touch
// the cache, and should cost little: every waiter waits for it too.
func (c *Cache) ObserveGraphHolds(f func(work wcprof.LockHolder, waited, held time.Duration)) (stop func()) {
	return c.egraphMu.ObserveHolds(f)
}

type cacheWorkKey struct{}

// withCacheWork returns ctx carrying work as its kind, unless ctx already
// carries one: work done for a pathway that another pathway started is the
// first pathway's.
func withCacheWork(ctx context.Context, work cacheWork) context.Context {
	if _, ok := ctx.Value(cacheWorkKey{}).(cacheWork); ok {
		return ctx
	}
	return context.WithValue(ctx, cacheWorkKey{}, work)
}

// cacheWorkOf is the kind of work ctx carries, or otherwise when it carries
// none.
func cacheWorkOf(ctx context.Context, otherwise cacheWork) cacheWork {
	if work, ok := ctx.Value(cacheWorkKey{}).(cacheWork); ok {
		return work
	}
	return otherwise
}

// lockGraphFor takes the graph lock for writing as work, so that a lookup
// it keeps waiting can tell what held it, and adds its own wait to the waits
// ctx gathers (withGraphLockWaits).
func (c *Cache) lockGraphFor(ctx context.Context, work cacheWork) {
	gatherGraphLockWait(ctx, wcprof.WriteLockAs(ctx, &c.egraphMu, egraphLockIdent, graphHolders[work]))
}

// readLockGraphGathered takes the graph lock for reading, adding a wait for
// it to the waits ctx gathers (withGraphLockWaits).
func (c *Cache) readLockGraphGathered(ctx context.Context) {
	gatherGraphLockWait(ctx, wcprof.ReadLockWaited(ctx, &c.egraphMu, egraphLockIdent))
}

// lockGraphForCtx takes the graph lock for writing as the work ctx
// carries, or otherwise when it carries none: for code that runs for
// several kinds of work, such as the parts machinery, which a sharing
// pass's slots run too.
func (c *Cache) lockGraphForCtx(ctx context.Context, otherwise cacheWork) {
	c.lockGraphFor(ctx, cacheWorkOf(ctx, otherwise))
}

// lookupReadLockGraph takes the graph lock for reading for a call's lookup,
// and counts a wait for it (LookupLockWaits), adding it to the waits ctx
// gathers too (withGraphLockWaits).
func (c *Cache) lookupReadLockGraph(ctx context.Context) {
	lw := wcprof.ReadLockWaited(ctx, &c.egraphMu, egraphLockIdent)
	c.countLookupWait(lw)
	gatherGraphLockWait(ctx, lw)
}

// lookupLockGraph takes the graph lock for writing for a call's lookup, and
// counts a wait for it (LookupLockWaits), adding it to the waits ctx gathers
// too (withGraphLockWaits).
func (c *Cache) lookupLockGraph(ctx context.Context) {
	lw := wcprof.WriteLockAs(ctx, &c.egraphMu, egraphLockIdent, graphHolders[workLookup])
	c.countLookupWait(lw)
	gatherGraphLockWait(ctx, lw)
}

// countLookupWait counts a lookup's wait for the graph lock, if it waited,
// by whether a set was landing as the wait ended: a lookup that took the
// lock at once reads and writes nothing more.
func (c *Cache) countLookupWait(lw wcprof.LockWait) {
	if lw.Waited <= 0 {
		return
	}
	c.lookupWaits.add(c.landing.Load() > 0, lw)
}

// Landing marks a set of the Cloud's records landing on the cache, from its
// arrival to its last commit, until the returned func is called: a lookup's
// wait for the graph lock that ends while any set lands is counted apart
// (LookupLockWaits.Landing).
func (c *Cache) Landing() (landed func()) {
	c.landing.Add(1)
	var once atomic.Bool
	return func() {
		if once.CompareAndSwap(false, true) {
			c.landing.Add(-1)
		}
	}
}

// lockWaitCounter counts waits for a lock: how many, their summed time, and
// the longest, in nanoseconds.
type lockWaitCounter struct {
	waits, total, longest atomic.Int64
}

func (w *lockWaitCounter) add(d time.Duration) {
	w.waits.Add(1)
	w.total.Add(int64(d))
	for {
		longest := w.longest.Load()
		if int64(d) <= longest || w.longest.CompareAndSwap(longest, int64(d)) {
			return
		}
	}
}

func (w *lockWaitCounter) load() LockWaitStats {
	return LockWaitStats{Waits: w.waits.Load(), Total: time.Duration(w.total.Load()), Longest: time.Duration(w.longest.Load())}
}

// lookupLockWaits counts the lookups' waits for the graph lock, while a set
// lands and otherwise, by the kind of writer that let each in
// (wcprof.LockWait). Only a wait touches it, so a lookup that takes the lock
// at once writes nothing.
type lookupLockWaits struct {
	byHolder [2][wcprof.LockHolders]lockWaitCounter
}

func (l *lookupLockWaits) add(landing bool, lw wcprof.LockWait) {
	i := 0
	if landing {
		i = 1
	}
	l.byHolder[i][lw.Holder].add(lw.Waited)
}

// LockWaitStats are waits for a lock: how many, their summed time, and the
// longest.
type LockWaitStats struct {
	Waits          int64
	Total, Longest time.Duration
}

// LookupLockWaits are the calls' lookups' waits for the cache's graph lock,
// over the cache's life, by the kind of writer whose release let each in:
// lookup, publication, restore (a set file's part's merge), merge (another
// merge of the Cloud's records), export, offer, part (fetching, installing
// or settling parts), teach (a call's teaching of what its result is the
// same as), dependency (a call's explicit dependencies), evaluation (a lazy
// evaluation's bookkeeping),
// upkeep (a session's release, a prune, a measurement or a save), hold (the
// service's holds on entries), other (a writer that names no kind), or
// readers (no writer: the lookup waited for readers to leave). Landing counts the waits that ended while a set of
// the Cloud's records was landing (Landing), Other the rest. A kind with no
// wait is left out.
type LookupLockWaits struct {
	Landing, Other map[string]LockWaitStats
}

// LookupLockWaits returns the lookups' waits for the graph lock so far.
func (c *Cache) LookupLockWaits() LookupLockWaits {
	read := func(counters *[wcprof.LockHolders]lockWaitCounter) map[string]LockWaitStats {
		var out map[string]LockWaitStats
		for h := range counters {
			stats := counters[h].load()
			if stats.Waits == 0 {
				continue
			}
			if out == nil {
				out = map[string]LockWaitStats{}
			}
			out[graphHolderName(wcprof.LockHolder(h))] = stats
		}
		return out
	}
	return LookupLockWaits{Other: read(&c.lookupWaits.byHolder[0]), Landing: read(&c.lookupWaits.byHolder[1])}
}

// graphHolderName names a graph-lock holder as LookupLockWaits does.
func graphHolderName(h wcprof.LockHolder) string {
	work, writer := graphHolderWork(h)
	if !writer {
		return wcprof.LockHolderReaders.String()
	}
	return work.String()
}

// graphLockWaits gathers one piece of work's waits for the graph lock, such
// as a merge's preparation's, by the kind of writer that let each in
// (wcprof.LockWait). Work gathers them in its context (withGraphLockWaits),
// so a wait is gathered wherever the work takes the lock: in its own
// sections, and in the captures and holds it calls.
type graphLockWaits struct {
	byKind [wcprof.LockHolders]atomic.Int64
}

func (w *graphLockWaits) add(lw wcprof.LockWait) {
	if lw.Waited > 0 {
		w.byKind[lw.Holder].Add(int64(lw.Waited))
	}
}

// addAll gathers more's waits too.
func (w *graphLockWaits) addAll(more *graphLockWaits) {
	for h := range more.byKind {
		if waited := more.byKind[h].Load(); waited != 0 {
			w.byKind[h].Add(waited)
		}
	}
}

func (w *graphLockWaits) total() time.Duration {
	var total time.Duration
	for h := range w.byKind {
		total += time.Duration(w.byKind[h].Load())
	}
	return total
}

// byHolder is the waits by the name of the kind of writer, as
// LookupLockWaits names them, or nil when there were none.
func (w *graphLockWaits) byHolder() map[string]time.Duration {
	var out map[string]time.Duration
	for h := range w.byKind {
		waited := time.Duration(w.byKind[h].Load())
		if waited == 0 {
			continue
		}
		if out == nil {
			out = map[string]time.Duration{}
		}
		out[graphHolderName(wcprof.LockHolder(h))] += waited
	}
	return out
}

type graphLockWaitsKey struct{}

// withGraphLockWaits returns ctx gathering the graph-lock waits of the work
// under it into w.
func withGraphLockWaits(ctx context.Context, w *graphLockWaits) context.Context {
	return context.WithValue(ctx, graphLockWaitsKey{}, w)
}

// gatherGraphLockWait adds a wait for the graph lock to the waits ctx
// gathers, if it waited and ctx gathers any: only a wait looks into ctx.
func gatherGraphLockWait(ctx context.Context, lw wcprof.LockWait) {
	if lw.Waited <= 0 {
		return
	}
	if w, ok := ctx.Value(graphLockWaitsKey{}).(*graphLockWaits); ok {
		w.add(lw)
	}
}

// hitLoad gathers, for a hit's span (CacheDecision), what the hit's own work
// after its lookup waited for and what its load decoded: its waits for the
// graph lock, by the kind of writer that let each in; its waits for another
// caller's decode of an entry it loads, and for an entry's dependency
// attachment; and the entries its load decoded, its own value and the
// results its payload references that weren't decoded yet. Only a wait and a
// decode touch it.
type hitLoad struct {
	graph          graphLockWaits
	decode, attach atomic.Int64
	decoded        atomic.Int64
}

type hitLoadKey struct{}

// withHitLoad returns ctx gathering the hit's own work under it into l, its
// graph-lock waits included.
func withHitLoad(ctx context.Context, l *hitLoad) context.Context {
	return context.WithValue(withGraphLockWaits(ctx, &l.graph), hitLoadKey{}, l)
}

// hitLoadOf is the hit load ctx gathers into, or nil.
func hitLoadOf(ctx context.Context) *hitLoad {
	l, _ := ctx.Value(hitLoadKey{}).(*hitLoad)
	return l
}

// waits is what the hit's own work waited for, by kind: "graph.<writer>" for
// the graph lock, with the writer named as LookupLockWaits names it,
// "decode" for another caller's decode and "attach" for a dependency
// attachment. Nil when it waited for nothing.
func (l *hitLoad) waits() map[string]time.Duration {
	var out map[string]time.Duration
	add := func(kind string, d time.Duration) {
		if d <= 0 {
			return
		}
		if out == nil {
			out = map[string]time.Duration{}
		}
		out[kind] += d
	}
	for writer, d := range l.graph.byHolder() {
		add("graph."+writer, d)
	}
	add("decode", time.Duration(l.decode.Load()))
	add("attach", time.Duration(l.attach.Load()))
	return out
}

// hitLoadWait is a kind of wait a hit's load gathers apart from the graph
// lock's (hitLoad).
type hitLoadWait uint8

const (
	// hitLoadDecode is a wait for another caller's decode of an entry the
	// hit loads.
	hitLoadDecode hitLoadWait = iota
	// hitLoadAttach is a wait for an entry's dependency attachment.
	hitLoadAttach
)

// awaitHitLoad waits for ready to close, or for ctx to end, and adds the wait
// to the hit load ctx gathers into (hitLoad), as kind. A channel already
// closed is no wait, and a wait nothing gathers isn't timed.
func awaitHitLoad(ctx context.Context, ready <-chan struct{}, kind hitLoadWait) error {
	select {
	case <-ready:
		return nil
	default:
	}
	load := hitLoadOf(ctx)
	var began time.Time
	if load != nil {
		began = time.Now()
	}
	select {
	case <-ready:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if load != nil {
		waited := int64(time.Since(began))
		switch kind {
		case hitLoadDecode:
			load.decode.Add(waited)
		case hitLoadAttach:
			load.attach.Add(waited)
		}
	}
	return nil
}
