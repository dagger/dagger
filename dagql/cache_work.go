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
	// workSharing is a snapshot-sharing pass, and the slots it runs.
	workSharing
	// workExport is an export's capture of the records it sends.
	workExport
	// workOffer is the placing of the Cloud's offered parts on entries.
	workOffer
	// workPart is the fetching, installing and settling of an entry's
	// parts: a call's demand for one, and the prefetcher's.
	workPart
	numCacheWork
)

var cacheWorkNames = [numCacheWork]string{
	workOther:       "other",
	workLookup:      "lookup",
	workPublication: "publication",
	workRestore:     "restore",
	workMerge:       "merge",
	workSharing:     "sharing",
	workExport:      "export",
	workOffer:       "offer",
	workPart:        "part",
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

type cacheWorkKey struct{}

// withCacheWork returns ctx carrying work as its kind, unless ctx already
// carries one: work done for a pathway that another pathway started, such
// as a part a sharing pass's slot fetches, is the first pathway's.
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
// it keeps waiting can tell what held it, and returns its own wait.
func (c *Cache) lockGraphFor(ctx context.Context, work cacheWork) wcprof.LockWait {
	return wcprof.WriteLockAs(ctx, &c.egraphMu, egraphLockIdent, graphHolders[work])
}

// lockGraphForCtx takes the graph lock for writing as the work ctx
// carries, or otherwise when it carries none: for code that runs for
// several kinds of work, such as the parts machinery, which a sharing
// pass's slots run too.
func (c *Cache) lockGraphForCtx(ctx context.Context, otherwise cacheWork) wcprof.LockWait {
	return c.lockGraphFor(ctx, cacheWorkOf(ctx, otherwise))
}

// lookupReadLockGraph takes the graph lock for reading for a call's lookup,
// and counts a wait for it (LookupLockWaits).
func (c *Cache) lookupReadLockGraph(ctx context.Context) {
	c.lookupWaits.add(c.landing.Load() > 0, wcprof.ReadLockWaited(ctx, &c.egraphMu, egraphLockIdent))
}

// lookupLockGraph takes the graph lock for writing for a call's lookup, and
// counts a wait for it (LookupLockWaits).
func (c *Cache) lookupLockGraph(ctx context.Context) {
	c.lookupWaits.add(c.landing.Load() > 0, wcprof.WriteLockAs(ctx, &c.egraphMu, egraphLockIdent, graphHolders[workLookup]))
}

// Landing marks a set of the Cloud's records landing on the cache, from its
// arrival to its last commit, until the returned func is called: a lookup's
// wait for the graph lock begun while any set lands is counted apart
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
	if lw.Waited <= 0 {
		return
	}
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
// merge of the Cloud's records), sharing (a snapshot-sharing pass), export,
// offer, part (fetching, installing or settling parts), other (a writer
// that names no kind), or readers (no writer: the lookup waited for
// readers to leave). Landing counts the waits begun while a set of the
// Cloud's records was landing (Landing), Other the rest. A kind with no
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

// graphLockWaits are one piece of work's waits for the graph lock, by the
// kind of writer that let each in (wcprof.LockWait); the work's own, so
// counted without atomics.
type graphLockWaits [wcprof.LockHolders]time.Duration

func (w *graphLockWaits) add(lw wcprof.LockWait) {
	if lw.Waited > 0 {
		w[lw.Holder] += lw.Waited
	}
}

func (w *graphLockWaits) addAll(more graphLockWaits) {
	for h, waited := range more {
		w[h] += waited
	}
}

func (w *graphLockWaits) total() time.Duration {
	var total time.Duration
	for _, waited := range w {
		total += waited
	}
	return total
}

// byHolder is the waits by the name of the kind of writer, as
// LookupLockWaits names them, or nil when there were none.
func (w *graphLockWaits) byHolder() map[string]time.Duration {
	var out map[string]time.Duration
	for h, waited := range w {
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
