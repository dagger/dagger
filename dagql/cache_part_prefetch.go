package dagql

import (
	"context"
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
type partPrefetcher struct {
	cache    *Cache
	cfg      partPrefetchConfig
	snapshot contentPrefetcher
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
	// dropped are holds taken out of holds, for the janitor to release.
	dropped []*partPrefetchHold
	// consumed are the blobs an install has pinned since, by when: a hold
	// made for one after that is released at once.
	consumed map[digest.Digest]time.Time
	// held is the bytes holds and running jobs reserve, against the budget;
	// pending is the part of it not yet written, against the disk.
	held    int64
	pending int64
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
	// digests are the blobs a running job downloads.
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
	p := &partPrefetcher{cache: c, cfg: cfg, snapshot: snapshot, ctx: base, cancel: cancel, logger: slog.FromContext(ctx), wake: make(chan struct{}, 1), jobs: map[partPrefetchJobKey]*partPrefetchJob{}, holds: map[digest.Digest]*partPrefetchHold{}, fetching: map[digest.Digest]*partPrefetchDownload{}, consumed: map[digest.Digest]time.Time{}}
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
	p.mu.Unlock()
	for _, h := range holds {
		_ = h.release(context.Background())
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
	for _, job := range jobs {
		if p.jobs[job.jobKey()] != nil {
			continue
		}
		if queue := p.queues[job.lane]; len(queue) >= p.cfg.queue {
			dropped = append(dropped, queue[0])
			p.queues[job.lane] = queue[1:]
			delete(p.jobs, queue[0].jobKey())
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
	if idle {
		p.wakeLocked()
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
	defer p.mu.Unlock()
	now := time.Now()
	idle := false
	for _, layer := range layers {
		d := layer.Descriptor.Digest
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
	p.mu.Unlock()
	if !c.partPrefetchRoom(pending) {
		p.mu.Lock()
		p.held -= need
		p.pending -= need
		for _, desc := range wanted {
			delete(p.fetching, desc.Digest)
		}
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
// smallStreams at once. After a failure, no blob starts.
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
			if !failed {
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
	_, consumed := p.consumed[desc.Digest]
	delete(p.consumed, desc.Digest)
	if !fetched || len(offers) == 0 || consumed || p.closed {
		p.held -= desc.Size
		return release
	}
	p.holds[desc.Digest] = &partPrefetchHold{bytes: desc.Size, release: release, offers: offers, expires: job.accepted.Add(p.cfg.ttl)}
	return nil
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
	p.mu.Unlock()
	for _, h := range idle {
		_ = h.release(context.WithoutCancel(p.ctx))
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
