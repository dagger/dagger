package dagql

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// partPrefetchLane is one of the prefetcher's two lanes. A blob of at least
// the config's largeBlob takes the large lane; any smaller blob, the small
// one. Each lane has its own queue and workers, so small blobs never wait
// behind a large download.
type partPrefetchLane int

const (
	// partPrefetchLarge downloads a job's blobs one at a time: their bytes
	// are what it bounds.
	partPrefetchLarge partPrefetchLane = iota
	// partPrefetchSmall downloads a job's blobs at once, up to smallStreams:
	// their requests' latency, not their bytes, is what an install would
	// wait on.
	partPrefetchSmall
	partPrefetchLanes
)

func (l partPrefetchLane) String() string {
	if l == partPrefetchSmall {
		return "small"
	}
	return "large"
}

// partPrefetchConfig bounds prefetching offered parts: the size from which a
// blob takes the large lane, each lane's workers, how many of a small job's
// blobs download at once, the bytes it holds unconsumed, how long it holds
// them, and how many accepted offers wait in each lane.
type partPrefetchConfig struct {
	largeBlob    int64
	workers      int
	smallWorkers int
	smallStreams int
	budget       int64
	ttl          time.Duration
	queue        int
}

func defaultPartPrefetchConfig() partPrefetchConfig {
	return partPrefetchConfig{
		largeBlob:    8 << 20,
		workers:      2,
		smallWorkers: 2,
		smallStreams: 8,
		budget:       4 << 30,
		ttl:          30 * time.Minute,
		queue:        256,
	}
}

// lane is the lane a blob of size takes.
func (cfg partPrefetchConfig) lane(size int64) partPrefetchLane {
	if size >= cfg.largeBlob {
		return partPrefetchLarge
	}
	return partPrefetchSmall
}

// contentPrefetcher is the snapshot manager's prefetch: it downloads a blob
// into the content store under a lease of its own.
type contentPrefetcher interface {
	PrefetchContent(ctx context.Context, desc ocispecs.Descriptor, provider content.Provider, ttl time.Duration) (func(context.Context) error, bool, error)
}

// chainAheadImporter is the snapshot manager's chain import, with which
// prefetching unpacks an offered part's layers as their blobs land, and its
// snapshots' own usage, which counts what that unpacked.
type chainAheadImporter interface {
	ImportChain(ctx context.Context, chain *snapshots.ExportChain) (snapshots.ImmutableRef, error)
	SnapshotUsage(ctx context.Context, snapshotID string) (int64, error)
}

// WithPartPrefetchRoom sets the check prefetching asks before holding more
// bytes on disk: whether need more bytes leave the engine's disk the free
// space its garbage collection keeps. A cache built without it has no disk
// to guard, and its check always passes.
func WithPartPrefetchRoom(room func(need int64) bool) CacheOption {
	return func(c *Cache) {
		c.partPrefetchRoom = room
	}
}

// partPrefetcher downloads the blobs of accepted offers in the background,
// before a call demands the part, so that its install finds
// them in the content store or joins their download. It fetches content
// only: it takes no permit and changes no row. Each blob is held by a lease
// of its own until the offers naming it are gone, which an install's
// settlement retires, until its TTL passes, or until the cache closes.
//
// Once a blob of an offer's chain is held for it, the chain is imported into
// snapshots too, ahead of demand, each layer as prefetching lands its blob:
// the part's install then finds each layer imported already and unpacks
// nothing. The import is held like a blob, until the offer is gone, its TTL
// passes or the cache closes.
type partPrefetcher struct {
	cache    *Cache
	cfg      partPrefetchConfig
	snapshot contentPrefetcher
	// importer imports chains ahead of demand; nil when the snapshot
	// manager can't.
	importer chainAheadImporter
	ctx      context.Context
	cancel   context.CancelFunc
	logger   *slog.Logger
	wg       sync.WaitGroup
	// wake asks for idle holds to be released now.
	wake chan struct{}
	// testRunStart, when set, runs as a job starts, before it is validated;
	// testAfterValidate between its validation and its registration.
	testRunStart      func()
	testAfterValidate func()

	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	queues [partPrefetchLanes][]*partPrefetchJob
	// jobs are the queued and running jobs, by offer and lane.
	jobs  map[partPrefetchJobKey]*partPrefetchJob
	holds map[digest.Digest]*partPrefetchHold
	// fetching are the blobs running jobs download.
	fetching map[digest.Digest]*partPrefetchDownload
	// changed is closed, and replaced, whenever a blob is held, a download
	// ends or a job's downloads may no longer come: imports ahead waiting
	// for a blob look again.
	changed chan struct{}
	// dropped are holds taken out of holds, for the janitor to release.
	dropped []*partPrefetchHold
	// consumed are the blobs an install has pinned since, by when: a hold
	// made for one after that is released at once.
	consumed map[digest.Digest]time.Time
	// held is the bytes holds and running jobs reserve, against the budget;
	// pending is the part of it not yet written, against the disk.
	held    int64
	pending int64
	// aheads are the offers whose chains are imported, or importing, ahead
	// of demand; droppedAheads those taken out of aheads, for the janitor to
	// release.
	aheads        map[*partOffer]*partPrefetchAhead
	droppedAheads []*partPrefetchAhead
	// rows are the entries of accepted offers, which name them in logs.
	rows map[*partOffer]sharedResultID
	// aheadBytes is the unpacked size of each layer an import ahead
	// unpacked that no install has used yet.
	aheadBytes map[digest.Digest]int64
}

// partPrefetchAhead is an offer's chain imported ahead of demand: ref holds
// its snapshots once the import is done; cancel stops the import.
type partPrefetchAhead struct {
	ref     snapshots.ImmutableRef
	cancel  context.CancelFunc
	expires time.Time
	// layers are its chain's blobs.
	layers []digest.Digest
	// gone is set once it is taken out of aheads: its import, if still
	// running, then releases what it imported.
	gone bool
}

// partPrefetchJobKey names a job: an offer has one in each lane its blobs
// take.
type partPrefetchJobKey struct {
	offer *partOffer
	lane  partPrefetchLane
}

// partPrefetchJob is one accepted offer's blobs of one lane to prefetch.
type partPrefetchJob struct {
	row      sharedResultID
	key      string
	offer    *partOffer
	lane     partPrefetchLane
	record   PersistedPartOffer
	accepted time.Time
	// gone is set once the offer is retired; cancel stops a running job;
	// digests are the blobs a running job downloads, set once it registers
	// them.
	gone    bool
	cancel  context.CancelFunc
	digests []digest.Digest
}

// partPrefetchDownload is one blob a running job downloads: the job, and
// the offers that want the blob, which its hold will be for.
type partPrefetchDownload struct {
	job    *partPrefetchJob
	offers map[*partOffer]struct{}
}

// partPrefetchHold is one prefetched blob, held for the offers naming it.
type partPrefetchHold struct {
	bytes   int64
	release func(context.Context) error
	offers  map[*partOffer]struct{}
	expires time.Time
}

// jobKey is the job's current key. Requires p.mu: a replacement passes the
// job to its new offer.
func (job *partPrefetchJob) jobKey() partPrefetchJobKey {
	return partPrefetchJobKey{offer: job.offer, lane: job.lane}
}

// partPrefetch returns the cache's prefetcher, starting it on first use, or
// nil when the cache is closing or its snapshot manager can't prefetch.
func (c *Cache) partPrefetch(ctx context.Context) *partPrefetcher {
	if p := c.partPrefetcher.Load(); p != nil {
		return p
	}
	snapshot, ok := c.snapshotManager.(contentPrefetcher)
	if !ok {
		return nil
	}
	importer, _ := c.snapshotManager.(chainAheadImporter)
	c.partPrefetchMu.Lock()
	defer c.partPrefetchMu.Unlock()
	if p := c.partPrefetcher.Load(); p != nil {
		return p
	}
	if c.closing.Load() {
		return nil
	}
	cfg := defaultPartPrefetchConfig()
	if c.partPrefetchConfig != nil {
		cfg = *c.partPrefetchConfig
	}
	base, cancel := context.WithCancel(context.Background())
	p := &partPrefetcher{cache: c, cfg: cfg, snapshot: snapshot, importer: importer, ctx: base, cancel: cancel, logger: slog.FromContext(ctx), wake: make(chan struct{}, 1), jobs: map[partPrefetchJobKey]*partPrefetchJob{}, holds: map[digest.Digest]*partPrefetchHold{}, fetching: map[digest.Digest]*partPrefetchDownload{}, changed: make(chan struct{}), consumed: map[digest.Digest]time.Time{}, aheads: map[*partOffer]*partPrefetchAhead{}, rows: map[*partOffer]sharedResultID{}, aheadBytes: map[digest.Digest]int64{}}
	p.cond = sync.NewCond(&p.mu)
	p.wg.Add(cfg.workers + cfg.smallWorkers + 1)
	for range cfg.workers {
		go p.work(partPrefetchLarge)
	}
	for range cfg.smallWorkers {
		go p.work(partPrefetchSmall)
	}
	go p.expire()
	c.partPrefetcher.Store(p)
	return p
}

// closePartPrefetch stops prefetching and releases every hold.
func (c *Cache) closePartPrefetch() {
	c.partPrefetchMu.Lock()
	p := c.partPrefetcher.Load()
	c.partPrefetchMu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
	p.mu.Lock()
	holds := append(slices.Collect(maps.Values(p.holds)), p.dropped...)
	p.holds, p.dropped = map[digest.Digest]*partPrefetchHold{}, nil
	p.held, p.pending = 0, 0
	aheads := append(slices.Collect(maps.Values(p.aheads)), p.droppedAheads...)
	p.aheads, p.droppedAheads = map[*partOffer]*partPrefetchAhead{}, nil
	p.mu.Unlock()
	for _, h := range holds {
		_ = h.release(context.Background())
	}
	for _, a := range aheads {
		a.release(context.Background())
	}
}

// release lets go of an import ahead's snapshots, if it finished.
func (a *partPrefetchAhead) release(ctx context.Context) {
	if a.ref != nil {
		_ = a.ref.Release(ctx)
		a.ref = nil
	}
}

// partOfferAccepted queues an offer the cache just attached for prefetch: a
// job in each lane its blobs take. It never blocks.
func (c *Cache) partOfferAccepted(ctx context.Context, row *sharedResult, key string, offer *partOffer) {
	p := c.partPrefetch(ctx)
	if p == nil || offer == nil {
		return
	}
	var wanted [partPrefetchLanes]bool
	for _, layer := range offer.record.Chain.Layers {
		wanted[p.cfg.lane(layer.Descriptor.Size)] = true
	}
	var jobs []*partPrefetchJob
	now := time.Now()
	for lane := range partPrefetchLanes {
		if !wanted[lane] {
			continue
		}
		// Each job has its own copy: a replacement refreshes a queued job's.
		record, err := clonePartOffer(offer.record)
		if err != nil {
			return
		}
		jobs = append(jobs, &partPrefetchJob{row: row.id, key: key, offer: offer, lane: lane, record: record, accepted: now})
	}
	var dropped []*partPrefetchJob
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.rows[offer] = row.id
	for _, job := range jobs {
		if p.jobs[job.jobKey()] != nil {
			continue
		}
		if queue := p.queues[job.lane]; len(queue) >= p.cfg.queue {
			dropped = append(dropped, queue[0])
			p.queues[job.lane] = queue[1:]
			delete(p.jobs, queue[0].jobKey())
			p.changedLocked()
		}
		p.queues[job.lane] = append(p.queues[job.lane], job)
		p.jobs[job.jobKey()] = job
	}
	p.cond.Broadcast()
	p.mu.Unlock()
	// Logged outside the mutex, which retirement takes under egraphMu: a
	// slow log writer must not stall the cache.
	for _, job := range dropped {
		p.log(job, "skipped_queue", 0, 0, 0, nil)
	}
}

// partOfferGone notes that the cache retired offer: the blobs held or
// downloading only for it are released, and its job stops unless another
// offer wants one of its downloads. Requires egraphMu; never blocks.
func (c *Cache) partOfferGone(offer *partOffer) {
	p := c.partPrefetcher.Load()
	if p == nil || offer == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.goneLocked(offer, nil)
}

// goneLocked drops offer's interest in every blob, except those keep names,
// which next inherits instead, and stops its job unless another offer still
// wants one of its downloads.
func (p *partPrefetcher) goneLocked(offer *partOffer, next *partOffer, keep ...digest.Digest) {
	kept := func(d digest.Digest) bool {
		return next != nil && slices.Contains(keep, d)
	}
	idle := false
	for d, h := range p.holds {
		if _, ok := h.offers[offer]; !ok {
			continue
		}
		delete(h.offers, offer)
		if kept(d) {
			h.offers[next] = struct{}{}
		}
		idle = idle || len(h.offers) == 0
	}
	// The jobs whose downloads may have lost their last interest.
	affected := map[*partPrefetchJob]struct{}{}
	for d, download := range p.fetching {
		if _, ok := download.offers[offer]; !ok {
			continue
		}
		delete(download.offers, offer)
		if kept(d) {
			download.offers[next] = struct{}{}
		}
		affected[download.job] = struct{}{}
	}
	for lane := range partPrefetchLanes {
		if job := p.jobs[partPrefetchJobKey{offer: offer, lane: lane}]; job != nil {
			job.gone = true
			affected[job] = struct{}{}
		}
	}
	for job := range affected {
		p.cancelUnwantedLocked(job)
	}
	p.changedLocked()
	delete(p.rows, offer)
	if a := p.aheads[offer]; a != nil {
		p.dropAheadLocked(offer, a)
		idle = true
	}
	if idle {
		p.wakeLocked()
	}
}

// dropAheadLocked takes offer's import ahead out of aheads, for the janitor
// to release.
func (p *partPrefetcher) dropAheadLocked(offer *partOffer, a *partPrefetchAhead) {
	delete(p.aheads, offer)
	a.gone = true
	a.cancel()
	p.droppedAheads = append(p.droppedAheads, a)
	p.forgetAheadLocked(a)
}

// forgetAheadLocked stops counting the layers of a's chain, a out of aheads,
// that no other import ahead holds, whichever import unpacked them: an
// install of them later unpacks them again.
func (p *partPrefetcher) forgetAheadLocked(a *partPrefetchAhead) {
	for _, d := range a.layers {
		held := false
		for _, other := range p.aheads {
			held = held || slices.Contains(other.layers, d)
		}
		if !held {
			delete(p.aheadBytes, d)
		}
	}
}

// cancelUnwantedLocked stops a running job none of whose downloads any
// offer still wants. The coordinator keeps a download an install joined.
func (p *partPrefetcher) cancelUnwantedLocked(job *partPrefetchJob) {
	if job.cancel == nil {
		return
	}
	for _, d := range job.digests {
		if download := p.fetching[d]; download != nil && download.job == job && len(download.offers) > 0 {
			return
		}
	}
	job.cancel()
}

// changedLocked lets the imports ahead waiting for a blob look again.
func (p *partPrefetcher) changedLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *partPrefetcher) wakeLocked() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// partOfferReplaced notes that next replaced old. With the same meaning,
// only the addresses changed: old's job, its interests and its holds pass to
// next, and a queued job takes next's addresses. Otherwise old is gone, and
// next inherits only its interest in the blobs both chains name; next's own
// acceptance queues its job. Requires egraphMu; never blocks.
func (c *Cache) partOfferReplaced(old, next *partOffer) {
	p := c.partPrefetcher.Load()
	if p == nil || old == nil || next == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !sameOfferMeaning(old.record, next.record) {
		var shared []digest.Digest
		for _, layer := range next.record.Chain.Layers {
			shared = append(shared, layer.Descriptor.Digest)
		}
		p.goneLocked(old, next, shared...)
		return
	}
	for lane := range partPrefetchLanes {
		oldKey, nextKey := partPrefetchJobKey{offer: old, lane: lane}, partPrefetchJobKey{offer: next, lane: lane}
		if job := p.jobs[oldKey]; job != nil && p.jobs[nextKey] == nil {
			delete(p.jobs, oldKey)
			job.offer = next
			p.jobs[nextKey] = job
			if job.cancel == nil {
				// Queued: run it with the replacement's fresh addresses.
				if record, err := clonePartOffer(next.record); err == nil {
					job.record = record
				}
			}
		}
	}
	for _, h := range p.holds {
		if _, ok := h.offers[old]; ok {
			delete(h.offers, old)
			h.offers[next] = struct{}{}
		}
	}
	if row, ok := p.rows[old]; ok {
		delete(p.rows, old)
		p.rows[next] = row
	}
	if a := p.aheads[old]; a != nil && p.aheads[next] == nil {
		delete(p.aheads, old)
		p.aheads[next] = a
	}
	for _, download := range p.fetching {
		if _, ok := download.offers[old]; ok {
			delete(download.offers, old)
			download.offers[next] = struct{}{}
		}
	}
}

// partPrefetchConsumed notes that an install pinned layers' blobs: their
// holds are released, whichever offers they were for, since the installed
// snapshot owns the content now.
func (c *Cache) partPrefetchConsumed(layers []snapshots.ExportLayer) {
	p := c.partPrefetcher.Load()
	if p == nil {
		return
	}
	p.mu.Lock()
	now := time.Now()
	idle := false
	var used int64
	usedLayers := 0
	for _, layer := range layers {
		d := layer.Descriptor.Digest
		if b, ok := p.aheadBytes[d]; ok {
			delete(p.aheadBytes, d)
			used += b
			usedLayers++
		}
		if h := p.holds[d]; h != nil {
			// Out of holds at once, so no job can take it up again; the
			// janitor releases its lease outside the mutex.
			delete(p.holds, d)
			p.held -= h.bytes
			p.dropped = append(p.dropped, h)
			idle = true
		} else if _, ok := p.fetching[d]; ok {
			p.consumed[d] = now
		}
	}
	if idle {
		p.wakeLocked()
	}
	p.mu.Unlock()
	if usedLayers > 0 {
		p.logger.Info("remote cache part ahead used", "layers", usedLayers, "unpacked", used)
	}
}

// partPrefetchHeld counts the layers whose blob prefetching holds or is
// fetching.
func (c *Cache) partPrefetchHeld(layers []snapshots.ExportLayer) int {
	p := c.partPrefetcher.Load()
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, layer := range layers {
		d := layer.Descriptor.Digest
		_, fetching := p.fetching[d]
		if p.holds[d] != nil || fetching {
			n++
		}
	}
	return n
}

// work runs lane's jobs, one at a time, until the prefetcher closes.
func (p *partPrefetcher) work(lane partPrefetchLane) {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for !p.closed && len(p.queues[lane]) == 0 {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return
		}
		job := p.queues[lane][0]
		p.queues[lane] = p.queues[lane][1:]
		if job.gone {
			delete(p.jobs, job.jobKey())
			p.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(p.ctx)
		job.cancel = cancel
		p.mu.Unlock()
		p.run(ctx, job)
		cancel()
		p.mu.Lock()
		if key := job.jobKey(); p.jobs[key] == job {
			delete(p.jobs, key)
		}
		p.changedLocked()
		p.mu.Unlock()
	}
}

// run prefetches the blobs of one job's lane that no hold has yet.
func (p *partPrefetcher) run(ctx context.Context, job *partPrefetchJob) {
	start := time.Now()
	c := p.cache
	if p.testRunStart != nil {
		p.testRunStart()
	}
	// The job's offer is read under egraphMu, which a replacement holds to
	// pass the job on, so the row and the job agree.
	c.egraphMu.RLock()
	p.mu.Lock()
	offer := job.offer
	p.mu.Unlock()
	row := c.resultsByID[job.row]
	current := row != nil && row.partOfferLocked(job.key) == offer
	installed := false
	if current {
		if gate := row.partGate.gate.Load(); gate != nil {
			gate.mu.Lock()
			installed = gate.outputs[job.key].phase != PartPending
			gate.mu.Unlock()
		}
	}
	c.egraphMu.RUnlock()
	switch {
	case !current:
		p.log(job, "offer_gone", 0, 0, time.Since(start), nil)
		return
	case installed:
		p.log(job, "installed", 0, 0, time.Since(start), nil)
		return
	}

	if p.testAfterValidate != nil {
		p.testAfterValidate()
	}
	now := time.Now()
	var wanted []ocispecs.Descriptor
	var need int64
	expired := false
	p.mu.Lock()
	// A retirement since the validation registers nothing: interests are
	// registered under the mutex goneLocked takes, after this check.
	if job.gone {
		p.mu.Unlock()
		p.log(job, "offer_gone", 0, 0, time.Since(start), nil)
		return
	}
	offer = job.offer
	for _, layer := range job.record.Chain.Layers {
		desc := layer.Descriptor
		if p.cfg.lane(desc.Size) != job.lane {
			continue
		}
		address, ok := job.record.Chain.Addresses[desc.Digest]
		if blobAddressUsability(address, ok, now) != blobAddressUsable {
			expired = true
			continue
		}
		if h := p.holds[desc.Digest]; h != nil {
			h.offers[offer] = struct{}{}
			continue
		}
		if download := p.fetching[desc.Digest]; download != nil {
			// Another job downloads it: its hold will be for this offer too.
			download.offers[offer] = struct{}{}
			continue
		}
		wanted = append(wanted, desc)
		need += desc.Size
	}
	switch {
	case len(wanted) == 0:
		p.checkAheadLocked(offer)
		p.mu.Unlock()
		if expired {
			p.log(job, "skipped_expired", 0, 0, time.Since(start), nil)
		}
		return
	case p.held+need > p.cfg.budget:
		p.mu.Unlock()
		p.log(job, "skipped_budget", 0, 0, time.Since(start), nil)
		return
	}
	p.held += need
	p.pending += need
	// The disk check counts every running job's unwritten bytes, not only
	// this one's: they all fill the same disk.
	pending := p.pending
	for _, desc := range wanted {
		p.fetching[desc.Digest] = &partPrefetchDownload{job: job, offers: map[*partOffer]struct{}{offer: {}}}
		job.digests = append(job.digests, desc.Digest)
	}
	// A blob it skipped, its address expired, is no longer coming.
	p.changedLocked()
	p.mu.Unlock()
	if !c.partPrefetchRoom(pending) {
		p.mu.Lock()
		p.held -= need
		p.pending -= need
		for _, desc := range wanted {
			delete(p.fetching, desc.Digest)
		}
		p.changedLocked()
		p.mu.Unlock()
		p.log(job, "skipped_budget", 0, 0, time.Since(start), nil)
		return
	}

	blobs, bytes, failure := p.fetch(ctx, job, wanted)
	outcome := "done"
	if failure != nil {
		outcome = "error"
		p.mu.Lock()
		if job.gone || p.closed {
			outcome, failure = "offer_gone", nil
		}
		p.mu.Unlock()
	}
	p.log(job, outcome, blobs, bytes, time.Since(start), failure)
}

// fetch downloads job's wanted blobs, which run registered, and settles
// each. The large lane downloads one blob at a time; the small lane up to
// smallStreams at once. After a failure, no blob starts, nor does one that
// no offer wants any more.
func (p *partPrefetcher) fetch(ctx context.Context, job *partPrefetchJob, wanted []ocispecs.Descriptor) (blobs int, bytes int64, failure error) {
	provider := p.cache.PartContentSource().Provider(ctx, job.record, nil)
	streams := 1
	if job.lane == partPrefetchSmall && p.cfg.smallStreams > 1 {
		streams = p.cfg.smallStreams
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	slots := make(chan struct{}, streams)
	for _, desc := range wanted {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			mu.Lock()
			failed := failure != nil
			mu.Unlock()
			var release func(context.Context) error
			fetched := false
			var err error
			if !failed && p.wanted(job, desc.Digest) {
				release, fetched, err = p.snapshot.PrefetchContent(ctx, desc, provider, p.cfg.ttl)
			}
			mu.Lock()
			if err != nil && failure == nil {
				failure = err
			}
			if fetched {
				blobs++
				bytes += desc.Size
			}
			mu.Unlock()
			if release = p.settle(job, desc, release, fetched); release != nil {
				_ = release(context.WithoutCancel(ctx))
			}
		})
	}
	wg.Wait()
	return blobs, bytes, failure
}

// wanted reports whether an offer still wants job's download of d.
func (p *partPrefetcher) wanted(job *partPrefetchJob, d digest.Digest) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	download := p.fetching[d]
	return download != nil && download.job == job && len(download.offers) > 0
}

// settle ends desc's download for job: a fetched blob becomes a hold for the
// offers that still want it, unless none does, it was consumed meanwhile or
// prefetching closed. It returns the release to run, outside the mutex, for
// a blob that isn't held.
func (p *partPrefetcher) settle(job *partPrefetchJob, desc ocispecs.Descriptor, release func(context.Context) error, fetched bool) func(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending -= desc.Size
	var offers map[*partOffer]struct{}
	if download := p.fetching[desc.Digest]; download != nil {
		offers = download.offers
	}
	delete(p.fetching, desc.Digest)
	p.changedLocked()
	// The settled download may have been the last one an offer wanted.
	p.cancelUnwantedLocked(job)
	_, consumed := p.consumed[desc.Digest]
	delete(p.consumed, desc.Digest)
	if !fetched || len(offers) == 0 || consumed || p.closed {
		p.held -= desc.Size
		return release
	}
	p.holds[desc.Digest] = &partPrefetchHold{bytes: desc.Size, release: release, offers: offers, expires: job.accepted.Add(p.cfg.ttl)}
	for offer := range offers {
		p.checkAheadLocked(offer)
	}
	return nil
}

// checkAheadLocked starts importing offer's chain ahead of demand once one
// of its blobs is held for it, unless it is imported or importing already.
// Requires p.mu.
func (p *partPrefetcher) checkAheadLocked(offer *partOffer) {
	if p.importer == nil || p.closed || p.aheads[offer] != nil {
		return
	}
	layers := offer.record.Chain.Layers
	expires := time.Time{}
	for _, layer := range layers {
		h := p.holds[layer.Descriptor.Digest]
		if h == nil {
			continue
		}
		if _, ok := h.offers[offer]; !ok {
			continue
		}
		if expires.IsZero() || h.expires.Before(expires) {
			expires = h.expires
		}
	}
	if expires.IsZero() {
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	a := &partPrefetchAhead{cancel: cancel, expires: expires}
	for _, layer := range layers {
		a.layers = append(a.layers, layer.Descriptor.Digest)
	}
	p.aheads[offer] = a
	p.wg.Add(1)
	go p.importAhead(ctx, offer, p.rows[offer], a)
}

// importAhead imports offer's chain, which unpacks each layer the engine
// doesn't have yet as soon as its blob is held, and keeps the import until a
// is dropped. It downloads nothing itself: a blob whose download isn't coming
// for the offer leaves the chain to its install. The engine's disk must have
// room for at least the chain's compressed bytes, beyond the bytes running
// jobs have yet to write.
func (p *partPrefetcher) importAhead(ctx context.Context, offer *partOffer, row sharedResultID, a *partPrefetchAhead) {
	defer p.wg.Done()
	start := time.Now()
	record := offer.record
	var size int64
	for _, layer := range record.Chain.Layers {
		size += layer.Descriptor.Size
	}
	var (
		ref     snapshots.ImmutableRef
		err     error
		timing  snapshots.ImportTiming
		outcome = "done"
		// unpacked and applied count the layers this import applied
		// itself, each recorded before another import can reuse it.
		mu       sync.Mutex
		unpacked int64
		applied  int
	)
	chain := &snapshots.ExportChain{
		Layers: record.Chain.Layers,
		Await: func(ctx context.Context, desc ocispecs.Descriptor) error {
			return p.awaitBlob(ctx, a, desc)
		},
		Timing: &timing,
		Applied: func(ctx context.Context, desc ocispecs.Descriptor, snapshotID string) {
			size, err := p.importer.SnapshotUsage(ctx, snapshotID)
			if err != nil {
				return
			}
			p.mu.Lock()
			if !a.gone {
				p.aheadBytes[desc.Digest] = size
			}
			p.mu.Unlock()
			mu.Lock()
			unpacked += size
			applied++
			mu.Unlock()
		},
	}
	p.mu.Lock()
	need := p.pending + size
	p.mu.Unlock()
	if !p.cache.partPrefetchRoom(need) {
		outcome = "skipped_budget"
	} else {
		if ref, err = p.importer.ImportChain(ctx, chain); errors.Is(err, errPartPrefetchNotComing) {
			outcome = "not_prefetched"
		} else if err != nil {
			outcome = "error"
		}
	}
	p.mu.Lock()
	keep := ref != nil && !a.gone && !p.closed
	if keep {
		a.ref = ref
	} else if !a.gone {
		// Under whichever offer holds it now: a replacement with the same
		// meaning takes it over while it imports.
		for holder, current := range p.aheads {
			if current == a {
				delete(p.aheads, holder)
			}
		}
		p.forgetAheadLocked(a)
	}
	if a.gone && outcome != "skipped_budget" {
		outcome, err = "offer_gone", nil
	}
	p.mu.Unlock()
	if ref != nil && !keep {
		_ = ref.Release(context.WithoutCancel(ctx))
	}
	mu.Lock()
	defer mu.Unlock()
	args := []any{
		"entry", uint64(row),
		"output", record.Address.OutputPath.String(),
		"part", string(record.Address.Part),
		"outcome", outcome,
		"layers", len(record.Chain.Layers),
		"unpacked_layers", applied,
		"unpacked", unpacked,
		"apply", timing.Apply,
		"duration", time.Since(start),
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	p.logger.Info("remote cache part applied ahead", args...)
}

// errPartPrefetchNotComing stops an import ahead that needs a blob no
// download is coming for.
var errPartPrefetchNotComing = errors.New("no prefetch of the blob is coming")

// awaitBlob waits until desc's blob is held, which puts it in the content
// store, for a's import. It gives up once no download of it is coming for
// a's offer: none runs for the offer, and the offer's job in desc's lane is
// neither queued nor yet to register its downloads.
func (p *partPrefetcher) awaitBlob(ctx context.Context, a *partPrefetchAhead, desc ocispecs.Descriptor) error {
	for {
		p.mu.Lock()
		if p.holds[desc.Digest] != nil {
			p.mu.Unlock()
			return nil
		}
		coming := false
		// Under whichever offer holds it now, as a replacement may have
		// taken it over.
		for offer, current := range p.aheads {
			if current == a {
				coming = p.comingLocked(offer, desc)
			}
		}
		changed := p.changed
		p.mu.Unlock()
		if !coming {
			return fmt.Errorf("%w: %s", errPartPrefetchNotComing, desc.Digest)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// comingLocked reports whether a download of desc's blob is coming for
// offer. Requires p.mu.
func (p *partPrefetcher) comingLocked(offer *partOffer, desc ocispecs.Descriptor) bool {
	if download := p.fetching[desc.Digest]; download != nil {
		if _, ok := download.offers[offer]; ok {
			return true
		}
	}
	job := p.jobs[partPrefetchJobKey{offer: offer, lane: p.cfg.lane(desc.Size)}]
	return job != nil && !job.gone && job.digests == nil
}

// expire releases the holds whose offers are all gone or whose TTL passed.
func (p *partPrefetcher) expire() {
	defer p.wg.Done()
	interval := p.cfg.ttl / 4
	if interval < time.Second {
		interval = time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-tick.C:
		case <-p.wake:
		}
		p.releaseIdle(time.Now())
	}
}

// releaseIdle releases the holds no live offer names, or whose TTL passed.
func (p *partPrefetcher) releaseIdle(now time.Time) {
	p.mu.Lock()
	idle := p.dropped
	p.dropped = nil
	for d, h := range p.holds {
		if len(h.offers) == 0 || !now.Before(h.expires) {
			idle = append(idle, h)
			delete(p.holds, d)
			p.held -= h.bytes
		}
	}
	for d, at := range p.consumed {
		if now.Sub(at) > p.cfg.ttl {
			delete(p.consumed, d)
		}
	}
	for offer, a := range p.aheads {
		if !now.Before(a.expires) {
			p.dropAheadLocked(offer, a)
		}
	}
	aheads := p.droppedAheads
	p.droppedAheads = nil
	p.mu.Unlock()
	for _, h := range idle {
		_ = h.release(context.WithoutCancel(p.ctx))
	}
	for _, a := range aheads {
		a.release(context.WithoutCancel(p.ctx))
	}
}

func (p *partPrefetcher) log(job *partPrefetchJob, outcome string, blobs int, bytes int64, duration time.Duration, err error) {
	args := []any{
		"entry", uint64(job.row),
		"output", job.record.Address.OutputPath.String(),
		"part", string(job.record.Address.Part),
		"lane", job.lane.String(),
		"outcome", outcome,
		"blobs", blobs,
		"bytes", bytes,
		"duration", duration,
		"lead", time.Since(job.accepted),
	}
	if err != nil {
		args = append(args, "error", err.Error())
	}
	p.logger.Info("remote cache part prefetched", args...)
}
