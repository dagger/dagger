package dagql

import (
	"context"
	"sync"
	"sync/atomic"
)

// A load of a stored value (ensurePersistedHitValueLoaded) decodes the whole
// graph its payload names, depth first: a module's first load on an engine
// decodes about 1,400 entries. Each entry's decode needs a few things of the
// cache's graph: the entry each reference names, whether it has a value and
// part offers, its dependencies, for its snapshot leases, and, once decoded,
// the cache's count of its payload bytes. Read entry by entry, they would
// take the graph lock about four times an entry, each taking waiting out any
// writer queued then: on a busy engine the load would spend most of its time
// queued, not decoding.
//
// So the load that leads the first decode reads all of it at once, under one
// hold of the graph lock, before it decodes (readGraphForDecode): from the
// entry it loads through the dependencies of every entry still holding a
// payload to decode, which are all the entries a payload can name. The
// decode reads that copy (graphReads), and the payload sizes it decodes go in
// together under one more hold once the load ends (recordDecodedSizes). An
// entry the copy doesn't hold is read under the lock.
//
// Every value the copy holds was read at one moment, as a decode that met no
// writer would have read it entry by entry. A change after the copy reaches
// the load as a change just after an entry's own read would: an offer
// that arrives moves the entry to part acquisition at its next demand, a
// dependency added is read by the entry's next lease sync, a payload replaced
// in place is decoded as it stands when its entry's decode starts, and its
// size isn't recorded over the replacement's (the revision check). The
// entries the copy names stay in the cache while the load runs: the loaded
// entry, held by its loader, owns its dependencies.

// graphReads is what one load of a stored value read of the cache's graph
// before decoding it (readGraphForDecode), by entry number, and the payload
// sizes its decodes are to record (recordDecodedSizes). It is the load's own:
// the load's context carries it, and nothing in the cache keeps it.
type graphReads struct {
	rows map[sharedResultID]*graphRead

	mu sync.Mutex
	// sizes are the decoded payloads' sizes to record, and recorded is set
	// once they are: a size that comes after records itself.
	sizes    []decodedSize
	recorded bool
}

// graphRead is one entry as a load read it: the entry, whether it has no
// value of this engine (noValueLocked) or offers a part (hasPartOffersLocked),
// and, when it held a payload to decode, the entries it depends on, then
// still in the cache.
type graphRead struct {
	res     *sharedResult
	noValue bool
	offered bool
	// decodes is set when the entry held a payload to decode: deps are its
	// dependencies then. depsChanged is set once the load's own decode of
	// the entry added dependencies (retainDecodedDependencyResults): its
	// lease sync then reads them under the lock.
	decodes     bool
	deps        []*sharedResult
	depsChanged atomic.Bool
}

// decodedSize is the size of a payload a load decoded, and the revision of
// the entry's payload it decoded into.
type decodedSize struct {
	res      *sharedResult
	bytes    int64
	revision uint64
}

type graphReadsKey struct{}

func withGraphReads(ctx context.Context, reads *graphReads) context.Context {
	return context.WithValue(ctx, graphReadsKey{}, reads)
}

// graphReadsOf is the graph a load under ctx read, or nil.
func graphReadsOf(ctx context.Context) *graphReads {
	reads, _ := ctx.Value(graphReadsKey{}).(*graphReads)
	return reads
}

// row is the read of entry id, or nil when the load read none.
func (r *graphReads) row(id sharedResultID) *graphRead {
	if r == nil {
		return nil
	}
	return r.rows[id]
}

// depsOf returns the dependencies of res as the load read them, if it read
// them: res held a payload to decode then.
func (r *graphReads) depsOf(res *sharedResult) ([]*sharedResult, bool) {
	row := r.row(res.id)
	if row == nil || row.res != res || !row.decodes || row.depsChanged.Load() {
		return nil, false
	}
	return row.deps, true
}

// changedDeps notes that res's dependencies changed after the load read
// them.
func (r *graphReads) changedDeps(res *sharedResult) {
	if row := r.row(res.id); row != nil {
		row.depsChanged.Store(true)
	}
}

// addSize keeps a decoded payload's size for the load to record, and reports
// whether it did: false once the load has recorded its sizes.
func (r *graphReads) addSize(res *sharedResult, bytes int64, revision uint64) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recorded {
		return false
	}
	r.sizes = append(r.sizes, decodedSize{res: res, bytes: bytes, revision: revision})
	return true
}

// readGraphForDecode reads, under one hold of the graph lock, every entry a
// decode of root's payload can reach: root, and the dependencies of each
// entry that holds a payload to decode, through the entries that do. An
// entry already decoded is read, for a reference to it, but not walked: its
// decode names nothing.
func (c *Cache) readGraphForDecode(ctx context.Context, root *sharedResult) *graphReads {
	reads := &graphReads{rows: map[sharedResultID]*graphRead{}}
	c.readLockGraphGathered(ctx)
	defer c.egraphMu.RUnlock()
	read := func(res *sharedResult) *graphRead {
		row := &graphRead{res: res, noValue: res.noValueLocked(), offered: res.hasPartOffersLocked()}
		reads.rows[res.id] = row
		return row
	}
	queue := []*graphRead{read(root)}
	for len(queue) > 0 {
		row := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if !row.res.holdsPayloadToDecode() {
			continue
		}
		row.decodes = true
		row.deps = c.dependencyResultsLocked(row.res)
		for _, dep := range row.deps {
			if _, seen := reads.rows[dep.id]; !seen {
				queue = append(queue, read(dep))
			}
		}
	}
	return reads
}

// holdsPayloadToDecode reports whether res holds a stored payload its value
// is still to be decoded from.
func (res *sharedResult) holdsPayloadToDecode() bool {
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	return !res.hasValue && res.persistedEnvelope != nil
}

// recordDecodedSizes records the sizes of the payloads a load decoded, under
// one hold of the graph lock, each unless its entry's payload changed after
// its decode (updateDecodedPayloadBytes). A size kept after this records
// itself.
func (c *Cache) recordDecodedSizes(ctx context.Context, reads *graphReads) {
	reads.mu.Lock()
	sizes := reads.sizes
	reads.sizes, reads.recorded = nil, true
	reads.mu.Unlock()
	if len(sizes) == 0 {
		return
	}
	c.readLockGraphGathered(ctx)
	defer c.egraphMu.RUnlock()
	for _, size := range sizes {
		size.res.payloadMu.RLock()
		current := size.res.payloadRevision == size.revision
		size.res.payloadMu.RUnlock()
		if current {
			c.setResultPayloadBytesLocked(size.res, size.bytes)
		}
	}
}
