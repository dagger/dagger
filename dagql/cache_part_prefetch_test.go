package dagql

import (
	"context"
	"fmt"
	stdslog "log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ctdsnapshots "github.com/containerd/containerd/v2/core/snapshots"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// prefetchFixture is an exhaustion fixture whose cache prefetches every blob
// of an accepted offer in the large lane, with no workers: a test runs each
// job itself.
type prefetchFixture struct {
	*exhaustionFixture
	chain    chainFixture
	prefetch *partPrefetcher
	offer    *partOffer
	key      string
}

func newPrefetchFixture(t *testing.T, fixed bool, cfg func(*partPrefetchConfig), extra ...chainFixture) *prefetchFixture {
	t.Helper()
	chain := newChainFixture(t, "prefetch")
	f := newExhaustionFixture(t, append([]chainFixture{chain}, extra...)...)
	config := partPrefetchConfig{largeBlob: 1, budget: 1 << 30, ttl: time.Hour, queue: 8}
	if cfg != nil {
		cfg(&config)
	}
	f.cache.partPrefetchConfig = &config
	f.attach(t, f.receiver, exhaustionOffer(chain, "", fixed))
	key, err := partAddressKey(f.address)
	require.NoError(t, err)
	f.cache.egraphMu.Lock()
	offer := f.receiver.cacheSharedResult().partOfferLocked(key)
	f.cache.egraphMu.Unlock()
	require.NotNil(t, offer)
	f.cache.partOfferAccepted(f.ctx, f.receiver.cacheSharedResult(), key, offer)
	p := f.cache.partPrefetcher.Load()
	require.NotNil(t, p)
	t.Cleanup(f.cache.closePartPrefetch)
	return &prefetchFixture{exhaustionFixture: f, chain: chain, prefetch: p, offer: offer, key: key}
}

// runJob runs the offer's queued job, as a worker does, and returns its
// logged outcome.
func (f *prefetchFixture) runJob(t *testing.T) offerLine {
	t.Helper()
	p := f.prefetch
	p.mu.Lock()
	job := p.jobs[partPrefetchJobKey{offer: f.offer}]
	p.mu.Unlock()
	require.NotNil(t, job)
	p.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.NotEmpty(t, lines)
	return lines[len(lines)-1]
}

func (f *prefetchFixture) holds() (int, int64) {
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	return len(f.prefetch.holds), f.prefetch.held
}

// An accepted offer's blobs are prefetched, and the part's install then
// reads none of them: its blobs are in the content store already.
func TestPartPrefetchThenInstall(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	line := f.runJob(t)
	require.Equal(t, "done", line.fields["outcome"])
	require.EqualValues(t, 2, line.int(t, "blobs"))
	require.Equal(t, 2, f.transport.total(), "one request per blob, ahead of demand")
	holds, held := f.holds()
	require.Equal(t, 2, holds)
	require.Positive(t, held)

	require.NoError(t, f.run())
	require.True(t, f.installed())
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	require.Zero(t, installs[0].int(t, "bytes"), "the install downloads nothing")
	require.EqualValues(t, 2, installs[0].int(t, "prefetched"))
	require.Equal(t, 2, f.transport.total(), "the install made no request")
}

// A job whose offer was retired before it ran downloads nothing.
func TestPartPrefetchOfferGone(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	f.cache.egraphMu.Lock()
	_, err := f.cache.retirePartOfferLocked(f.ctx, f.receiver.cacheSharedResult(), f.address)
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	f.prefetch.mu.Lock()
	job := f.prefetch.jobs[partPrefetchJobKey{offer: f.offer}]
	f.prefetch.mu.Unlock()
	require.NotNil(t, job)
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Len(t, lines, 1)
	require.Equal(t, "offer_gone", lines[0].fields["outcome"])
	require.Zero(t, f.transport.total())
}

// A prefetched blob is released once the offer naming it is retired, and its
// bytes go back to the budget.
func TestPartPrefetchReleasedWithOffer(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	f.cache.egraphMu.Lock()
	_, err := f.cache.retirePartOfferLocked(f.ctx, f.receiver.cacheSharedResult(), f.address)
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		holds, held := f.holds()
		return holds == 0 && held == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// Expired addresses are skipped: prefetch never asks the service to renew
// them.
func TestPartPrefetchSkipsExpired(t *testing.T) {
	f := newPrefetchFixture(t, false, nil)
	bridge := attachTestBridge(t, f.cache.PartContentSource())
	taken := renewalConsumer(t, bridge, nil)
	require.Equal(t, "skipped_expired", f.runJob(t).fields["outcome"])
	require.Zero(t, f.transport.total())
	require.Empty(t, taken())
}

// A job whose blobs would take the held bytes past the budget is skipped.
func TestPartPrefetchBudget(t *testing.T) {
	f := newPrefetchFixture(t, true, func(cfg *partPrefetchConfig) { cfg.budget = 1 })
	require.Equal(t, "skipped_budget", f.runJob(t).fields["outcome"])
	require.Zero(t, f.transport.total())
	holds, held := f.holds()
	require.Zero(t, holds)
	require.Zero(t, held)
}

// A failed prefetch leaves the offer usable: the install downloads the blob
// itself.
func TestPartPrefetchFailureDoesNotExhaust(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	url := "https://fixed.invalid/prefetch/0"
	f.transport.fault(url, contentTestFault{corrupt: true, stallAfter: -1})
	require.Equal(t, "error", f.runJob(t).fields["outcome"])
	f.transport.fault(url, contentTestFault{stallAfter: -1})
	require.NoError(t, f.run())
	require.True(t, f.installed())
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	require.Positive(t, installs[0].int(t, "bytes"), "the install downloaded what the prefetch could not")
}

// Closing the cache's prefetching releases every hold.
func TestPartPrefetchCloseReleasesHolds(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	f.cache.closePartPrefetch()
	holds, held := f.holds()
	require.Zero(t, holds)
	require.Zero(t, held)
}

// donorOffer attaches the fixture chain's offer to a second, equivalent row,
// accepts it for prefetch, and returns it.
func (f *prefetchFixture) donorOffer(t *testing.T) *partOffer {
	t.Helper()
	f.addDonor(t)
	f.attach(t, f.donor, exhaustionOffer(f.chain, "", true))
	f.cache.egraphMu.Lock()
	offer := f.donor.cacheSharedResult().partOfferLocked(f.key)
	f.cache.egraphMu.Unlock()
	require.NotNil(t, offer)
	f.cache.partOfferAccepted(f.ctx, f.donor.cacheSharedResult(), f.key, offer)
	return offer
}

// replace replaces the receiver's offer with record, as an offer with new
// addresses or a new chain does, accepts it for prefetch, and returns it.
func (f *prefetchFixture) replace(t *testing.T, record PersistedPartOffer) *partOffer {
	t.Helper()
	row := f.receiver.cacheSharedResult()
	f.cache.egraphMu.Lock()
	owner, err := f.cache.newOfferOwnerLocked(f.ctx, record.Owner)
	var callbacks []OnReleaseFunc
	next := &partOffer{record: record, owner: owner}
	if err == nil {
		var queue []*sharedResult
		queue, err = f.cache.testReplacePartOfferLocked(f.ctx, row, f.address, next)
		if err == nil {
			callbacks, err = f.cache.collectUnownedResultsLocked(f.ctx, queue)
		}
	}
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	require.NoError(t, runOnReleaseFuncs(f.ctx, callbacks))
	f.cache.partOfferAccepted(f.ctx, row, f.key, next)
	return next
}

func (f *prefetchFixture) job(t *testing.T, offer *partOffer) *partPrefetchJob {
	t.Helper()
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	job := f.prefetch.jobs[partPrefetchJobKey{offer: offer}]
	require.NotNil(t, job)
	return job
}

// holdOffers returns the offers each held blob is for.
func (f *prefetchFixture) holdOffers() map[digest.Digest][]*partOffer {
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	out := map[digest.Digest][]*partOffer{}
	for d, h := range f.prefetch.holds {
		for offer := range h.offers {
			out[d] = append(out[d], offer)
		}
	}
	return out
}

// The disk check counts the unwritten bytes every running job reserved, not
// only this job's: two jobs can't both pass on the same free space. The
// import ahead, which starts with the first blob while the last one still
// downloads, counts them too, that last blob's included.
func TestPartPrefetchDiskRoomCountsPending(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	var (
		mu    sync.Mutex
		asked []int64
	)
	f.cache.partPrefetchRoom = func(need int64) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, need)
		return true
	}
	hold := make(chan struct{})
	f.transport.fault("https://fixed.invalid/prefetch/1", contentTestFault{stallAfter: -1, hold: hold})
	const other = 1000
	f.prefetch.mu.Lock()
	f.prefetch.pending = other
	f.prefetch.mu.Unlock()
	job := f.job(t, f.offer)
	done := make(chan struct{})
	go func() {
		f.prefetch.run(context.Background(), job)
		close(done)
	}()
	var size int64
	for _, layer := range f.chain.chain.Layers {
		size += layer.Descriptor.Size
	}
	last := f.chain.chain.Layers[1].Descriptor.Size
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(asked) == 2
	}, 10*time.Second, 10*time.Millisecond, "the import ahead never checked for room")
	mu.Lock()
	require.Equal(t, []int64{other + size, other + last + size}, asked, "the job's check, then the import ahead's while the last blob downloads")
	mu.Unlock()
	close(hold)
	within(t, done)
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	require.EqualValues(t, other, f.prefetch.pending, "the job's bytes are written; the other job's remain")
}

// blockingHandler blocks every log record until released.
type blockingHandler struct {
	release <-chan struct{}
	entered chan<- struct{}
}

func (h blockingHandler) Enabled(context.Context, stdslog.Level) bool { return true }
func (h blockingHandler) Handle(context.Context, stdslog.Record) error {
	h.entered <- struct{}{}
	<-h.release
	return nil
}
func (h blockingHandler) WithAttrs([]stdslog.Attr) stdslog.Handler { return h }
func (h blockingHandler) WithGroup(string) stdslog.Handler         { return h }

// A full queue drops its oldest job and logs it without holding the
// prefetcher's mutex, so a slow log writer can't stall a retirement, which
// takes that mutex under egraphMu.
func TestPartPrefetchQueueDropDoesNotBlockRetirement(t *testing.T) {
	f := newPrefetchFixture(t, true, func(cfg *partPrefetchConfig) { cfg.queue = 1 })
	release, entered := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	f.prefetch.logger = slog.New(blockingHandler{release: release, entered: entered})
	second := &partOffer{record: f.offer.record}
	go f.cache.partOfferAccepted(f.ctx, f.receiver.cacheSharedResult(), f.key, second)
	within(t, entered)
	retired := make(chan struct{})
	go func() {
		f.cache.partOfferGone(f.offer)
		close(retired)
	}()
	within(t, retired)
}

// A replacement with the same meaning and fresh addresses gives a queued job
// the fresh addresses, so it prefetches rather than skipping as expired.
func TestPartPrefetchReplacementRefreshesAddresses(t *testing.T) {
	f := newPrefetchFixture(t, false, nil)
	fresh := f.replace(t, exhaustionOffer(f.chain, "", true))
	job := f.job(t, fresh)
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Len(t, lines, 1)
	require.Equal(t, "done", lines[0].fields["outcome"])
	require.Equal(t, 2, f.transport.total())
}

// A replacement naming another chain retires the old offer's holds, and the
// replacement's own job prefetches the new chain.
func TestPartPrefetchReplacementChangedChain(t *testing.T) {
	other := newChainFixture(t, "prefetch-other")
	f := newPrefetchFixture(t, true, nil, other)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	next := f.replace(t, exhaustionOffer(other, "", true))
	f.prefetch.run(context.Background(), f.job(t, next))
	require.Eventually(t, func() bool {
		held := f.holdOffers()
		if len(held) != len(other.chain.Layers) {
			return false
		}
		for _, layer := range other.chain.Layers {
			offers := held[layer.Descriptor.Digest]
			if len(offers) != 1 || offers[0] != next {
				return false
			}
		}
		return true
	}, 10*time.Second, 10*time.Millisecond, "only the new chain's blobs are held, for the replacement")
}

// A blob another offer's job is downloading is held for both offers once it
// lands, and the first offer's retirement mid-download leaves it to the
// second.
func TestPartPrefetchSharedDownload(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	hold := make(chan struct{})
	for i := range f.chain.blobs {
		f.transport.fault(fmt.Sprintf("https://fixed.invalid/prefetch/%d", i), contentTestFault{stallAfter: -1, hold: hold})
	}
	firstJob := f.job(t, f.offer)
	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		f.prefetch.mu.Lock()
		firstJob.cancel = cancel
		f.prefetch.mu.Unlock()
		f.prefetch.run(ctx, firstJob)
		close(done)
	}()
	require.Eventually(t, func() bool {
		f.prefetch.mu.Lock()
		defer f.prefetch.mu.Unlock()
		return len(f.prefetch.fetching) > 0
	}, 10*time.Second, 10*time.Millisecond)
	second := f.donorOffer(t)
	f.prefetch.run(context.Background(), f.job(t, second))
	f.cache.egraphMu.Lock()
	_, err := f.cache.retirePartOfferLocked(f.ctx, f.receiver.cacheSharedResult(), f.address)
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	close(hold)
	within(t, done)
	held := f.holdOffers()
	require.Len(t, held, len(f.chain.blobs))
	for _, offers := range held {
		require.Equal(t, []*partOffer{second}, offers)
	}
}

// An install that pins prefetched blobs releases their holds, and their
// bytes return to the budget, though another offer still names them.
func TestPartPrefetchConsumedReleasesHolds(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	second := f.donorOffer(t)
	f.prefetch.run(context.Background(), f.job(t, second))
	for _, offers := range f.holdOffers() {
		require.Len(t, offers, 2)
	}
	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.Eventually(t, func() bool {
		holds, held := f.holds()
		return holds == 0 && held == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// Once every offer that wanted a download is retired, the job downloading
// it stops, though its own offer was retired first and another offer had
// joined meanwhile.
func TestPartPrefetchSharedDownloadAllRetired(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	hold := make(chan struct{})
	defer close(hold)
	for i := range f.chain.blobs {
		f.transport.fault(fmt.Sprintf("https://fixed.invalid/prefetch/%d", i), contentTestFault{stallAfter: -1, hold: hold})
	}
	firstJob := f.job(t, f.offer)
	ctx, cancel := context.WithCancel(context.Background())
	f.prefetch.mu.Lock()
	firstJob.cancel = cancel
	f.prefetch.mu.Unlock()
	done := make(chan struct{})
	go func() {
		f.prefetch.run(ctx, firstJob)
		close(done)
	}()
	require.Eventually(t, func() bool {
		f.prefetch.mu.Lock()
		defer f.prefetch.mu.Unlock()
		return len(f.prefetch.fetching) > 0
	}, 10*time.Second, 10*time.Millisecond)
	second := f.donorOffer(t)
	f.prefetch.run(context.Background(), f.job(t, second))
	for _, row := range []AnyResult{f.receiver, f.donor} {
		f.cache.egraphMu.Lock()
		_, err := f.cache.retirePartOfferLocked(f.ctx, row.cacheSharedResult(), f.address)
		f.cache.egraphMu.Unlock()
		require.NoError(t, err)
	}
	within(t, done)
	holds, held := f.holds()
	require.Zero(t, holds)
	require.Zero(t, held)
}

// A job whose offer is retired while it downloads a blob that another
// offer, whose chain is a prefix of its own, also wants keeps that download
// for the other offer, and stops there: the blobs only the retired offer
// wanted are never requested, and their bytes return to the budget.
func TestPartPrefetchRetiredJobSkipsUnwantedSuffix(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	shared := "https://fixed.invalid/prefetch/0"
	suffix := "https://fixed.invalid/prefetch/1"
	hold := make(chan struct{})
	f.transport.fault(shared, contentTestFault{stallAfter: -1, hold: hold})
	firstJob := f.job(t, f.offer)
	ctx, cancel := context.WithCancel(context.Background())
	f.prefetch.mu.Lock()
	firstJob.cancel = cancel
	f.prefetch.mu.Unlock()
	done := make(chan struct{})
	go func() {
		f.prefetch.run(ctx, firstJob)
		close(done)
	}()
	require.Eventually(t, func() bool {
		return f.transport.count(shared) > 0
	}, 10*time.Second, 10*time.Millisecond)

	prefix := chainFixture{tag: f.chain.tag, chain: f.chain.lower, blobs: f.chain.blobs[:1]}
	require.Equal(t, f.chain.digest(0), prefix.digest(0))
	f.addDonor(t)
	f.attach(t, f.donor, exhaustionOffer(prefix, "", true))
	f.cache.egraphMu.Lock()
	second := f.donor.cacheSharedResult().partOfferLocked(f.key)
	f.cache.egraphMu.Unlock()
	require.NotNil(t, second)
	f.cache.partOfferAccepted(f.ctx, f.donor.cacheSharedResult(), f.key, second)
	f.prefetch.run(context.Background(), f.job(t, second))

	f.cache.egraphMu.Lock()
	_, err := f.cache.retirePartOfferLocked(f.ctx, f.receiver.cacheSharedResult(), f.address)
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	close(hold)
	within(t, done)

	// The suffix's download may start before the retirement, as the shared
	// blob's last request does: it is requested once at most, and not held
	// past the retirement.
	require.LessOrEqual(t, f.transport.count(suffix), 1, "a blob only the retired offer wanted is requested again")
	require.Eventually(t, func() bool {
		f.prefetch.mu.Lock()
		defer f.prefetch.mu.Unlock()
		return f.prefetch.held == f.chain.chain.Layers[0].Descriptor.Size
	}, 10*time.Second, 10*time.Millisecond, "only the shared blob's bytes stay held")
	require.Equal(t, map[digest.Digest][]*partOffer{f.chain.digest(0): {second}}, f.holdOffers())
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	require.Zero(t, f.prefetch.pending)
	require.Empty(t, f.prefetch.fetching)
}

// A hold an install consumed is gone at once: a job that runs after the
// consumption, before the janitor, doesn't take it up again.
func TestPartPrefetchConsumedHoldNotRetaken(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	second := f.donorOffer(t)
	require.NoError(t, f.run())
	require.True(t, f.installed())
	f.prefetch.run(context.Background(), f.job(t, second))
	require.Eventually(t, func() bool {
		holds, held := f.holds()
		return holds == 0 && held == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// A replacement with the same meaning that lands as the worker starts its
// job leaves the job prefetching for the replacement.
func TestPartPrefetchReplacementAsJobStarts(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	job := f.job(t, f.offer)
	var next *partOffer
	f.prefetch.testRunStart = func() {
		next = f.replace(t, exhaustionOffer(f.chain, "", true))
	}
	f.prefetch.run(context.Background(), job)
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Len(t, lines, 1)
	require.Equal(t, "done", lines[0].fields["outcome"])
	for _, offers := range f.holdOffers() {
		require.Equal(t, []*partOffer{next}, offers)
	}
}

// An offer retired between its job's validation and its registration
// registers no interest, so the remaining offer's retirement releases the
// blob it holds.
func TestPartPrefetchRetiredBeforeRegistration(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	second := f.donorOffer(t)
	f.prefetch.run(context.Background(), f.job(t, second))
	for _, offers := range f.holdOffers() {
		require.Equal(t, []*partOffer{second}, offers)
	}
	retire := func(row AnyResult) {
		f.cache.egraphMu.Lock()
		_, err := f.cache.retirePartOfferLocked(f.ctx, row.cacheSharedResult(), f.address)
		f.cache.egraphMu.Unlock()
		require.NoError(t, err)
	}
	f.prefetch.testAfterValidate = func() { retire(f.receiver) }
	f.prefetch.run(context.Background(), f.job(t, f.offer))
	lines := f.logs.lines(t, "remote cache part prefetched")
	require.Equal(t, "offer_gone", lines[len(lines)-1].fields["outcome"])
	for _, offers := range f.holdOffers() {
		require.Equal(t, []*partOffer{second}, offers, "the retired offer registered no interest")
	}
	retire(f.donor)
	require.Eventually(t, func() bool {
		holds, held := f.holds()
		return holds == 0 && held == 0
	}, 10*time.Second, 10*time.Millisecond)
}

// aheadLine waits for the offer's import ahead to be logged, and returns it.
func (f *prefetchFixture) aheadLine(t *testing.T) offerLine {
	t.Helper()
	var line offerLine
	require.Eventually(t, func() bool {
		lines := f.logs.lines(t, "remote cache part applied ahead")
		if len(lines) == 0 {
			return false
		}
		line = lines[len(lines)-1]
		return true
	}, 10*time.Second, 10*time.Millisecond, "the offer's chain was never imported ahead")
	return line
}

// snapshotCount is how many snapshots the cache's store holds after a
// collection.
func (f *prefetchFixture) snapshotCount(t *testing.T) int {
	t.Helper()
	f.store.GC(t)
	n := 0
	require.NoError(t, f.store.Snapshots.Walk(context.Background(), func(context.Context, ctdsnapshots.Info) error {
		n++
		return nil
	}))
	return n
}

// Once an offer's blobs are prefetched, its chain is imported ahead of
// demand: the part's install then unpacks nothing, and its layers count as
// used.
func TestPartPrefetchAppliesAhead(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	ahead := f.aheadLine(t)
	require.Equal(t, "done", ahead.fields["outcome"])
	require.EqualValues(t, 2, ahead.int(t, "layers"))
	require.EqualValues(t, 2, ahead.int(t, "unpacked_layers"))
	require.Positive(t, ahead.int(t, "unpacked"))
	require.EqualValues(t, 2, f.store.Applies.Load(), "both layers unpacked ahead")

	require.NoError(t, f.run())
	require.True(t, f.installed())
	installs := f.logs.lines(t, "remote cache part installed")
	require.Len(t, installs, 1)
	require.Zero(t, installs[0].int(t, "bytes"), "the install downloads nothing")
	require.EqualValues(t, 2, f.store.Applies.Load(), "the install unpacks nothing")
	used := f.logs.lines(t, "remote cache part ahead used")
	require.Len(t, used, 1)
	require.EqualValues(t, 2, used[0].int(t, "layers"))
	require.Equal(t, ahead.int(t, "unpacked"), used[0].int(t, "unpacked"))
}

// An import ahead starts with the offer's first prefetched blob and applies
// each layer as its own blob lands: the first layer is unpacked while the
// last blob still downloads. It requests no blob itself, and the install
// then unpacks nothing.
func TestPartPrefetchAheadAppliesLayersAsTheyLand(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	hold := make(chan struct{})
	f.transport.fault("https://fixed.invalid/prefetch/1", contentTestFault{stallAfter: -1, hold: hold})
	applied := make(chan struct{})
	var once sync.Once
	f.store.BeforeApply = func(_ context.Context, desc ocispecs.Descriptor) error {
		if desc.Digest == f.chain.digest(0) {
			once.Do(func() { close(applied) })
		}
		return nil
	}
	job := f.job(t, f.offer)
	done := make(chan struct{})
	go func() {
		f.prefetch.run(context.Background(), job)
		close(done)
	}()
	select {
	case <-applied:
	case <-time.After(10 * time.Second):
		t.Fatal("the first layer was not unpacked before the last blob landed")
	}
	require.Empty(t, f.logs.lines(t, "remote cache part applied ahead"), "the import ahead waits for the last blob")
	close(hold)
	within(t, done)
	ahead := f.aheadLine(t)
	require.Equal(t, "done", ahead.fields["outcome"])
	require.EqualValues(t, 2, ahead.int(t, "unpacked_layers"))
	require.Equal(t, 2, f.transport.total(), "one request per blob, the prefetch's")

	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.EqualValues(t, 2, f.store.Applies.Load(), "the install unpacks nothing")
	require.Equal(t, 2, f.transport.total(), "the install made no request")
}

// An import ahead never requests a blob itself: when prefetching one fails,
// it gives up after the layers before it, and the install downloads that
// blob.
func TestPartPrefetchAheadLeavesAFailedBlobToTheInstall(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	url := "https://fixed.invalid/prefetch/1"
	f.transport.fault(url, contentTestFault{corrupt: true, stallAfter: -1})
	require.Equal(t, "error", f.runJob(t).fields["outcome"])
	ahead := f.aheadLine(t)
	require.Equal(t, "not_prefetched", ahead.fields["outcome"])
	require.EqualValues(t, 1, ahead.int(t, "unpacked_layers"), "the first layer was unpacked as its blob landed")
	require.Equal(t, 1, f.transport.count(url), "the prefetch's request only")

	f.transport.fault(url, contentTestFault{stallAfter: -1})
	require.NoError(t, f.run())
	require.True(t, f.installed())
	require.Equal(t, 2, f.transport.count(url), "the install downloaded what the prefetch could not")
}

// The large lane starts a job's next blob once the blob before it has started
// its last request: a late response at a blob's end doesn't hold the next
// blob's download back.
func TestPartPrefetchNextBlobStartsAtTheTail(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	hold := make(chan struct{})
	first, next := "https://fixed.invalid/prefetch/0", "https://fixed.invalid/prefetch/1"
	f.transport.fault(first, contentTestFault{stallAfter: -1, hold: hold})
	job := f.job(t, f.offer)
	done := make(chan struct{})
	go func() {
		f.prefetch.run(context.Background(), job)
		close(done)
	}()
	require.Eventually(t, func() bool {
		return f.transport.count(first) == 1 && f.transport.count(next) == 1
	}, 10*time.Second, 10*time.Millisecond, "the next blob is requested while the first one's response is late")
	close(hold)
	within(t, done)
	require.Equal(t, "done", f.logs.lines(t, "remote cache part prefetched")[0].fields["outcome"])
	holds, _ := f.holds()
	require.Equal(t, 2, holds, "both blobs are held")
}

// An import ahead is released with its offer: once retired, the snapshots it
// unpacked are collected.
func TestPartPrefetchAheadReleasedWithOffer(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
	require.Positive(t, f.snapshotCount(t), "the import ahead holds its snapshots")
	f.cache.egraphMu.Lock()
	_, err := f.cache.retirePartOfferLocked(f.ctx, f.receiver.cacheSharedResult(), f.address)
	f.cache.egraphMu.Unlock()
	require.NoError(t, err)
	require.Eventually(t, func() bool { return f.snapshotCount(t) == 0 }, 10*time.Second, 10*time.Millisecond)
}

// Closing prefetching releases its imports ahead.
func TestPartPrefetchAheadReleasedAtClose(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	require.Equal(t, "done", f.aheadLine(t).fields["outcome"])
	f.cache.closePartPrefetch()
	require.Zero(t, f.snapshotCount(t))
}

// aheadLines returns the logged imports ahead, waiting until there are n.
func (f *prefetchFixture) aheadLines(t *testing.T, n int) []offerLine {
	t.Helper()
	var lines []offerLine
	require.Eventually(t, func() bool {
		lines = f.logs.lines(t, "remote cache part applied ahead")
		return len(lines) >= n
	}, 10*time.Second, 10*time.Millisecond, "fewer than %d imports ahead", n)
	return lines
}

// An import ahead that a replacement of its offer took over, and that then
// found no room, leaves nothing behind: the replacement's next job imports
// ahead once there is room.
func TestPartPrefetchAheadAfterSkippedReplacement(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	entered, resume := make(chan struct{}), make(chan bool)
	var calls atomic.Int64
	f.cache.partPrefetchRoom = func(int64) bool {
		if calls.Add(1) != 2 {
			return true
		}
		// The job's check passed; the first import ahead's waits.
		close(entered)
		return <-resume
	}
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the import ahead never checked for room")
	}
	fresh := f.replace(t, exhaustionOffer(f.chain, "", true))
	resume <- false
	require.Equal(t, "skipped_budget", f.aheadLines(t, 1)[0].fields["outcome"])
	f.prefetch.run(context.Background(), f.job(t, fresh))
	require.Equal(t, "done", f.aheadLines(t, 2)[1].fields["outcome"], "the replacement imports ahead")
}

// An install that arrives while its chain imports ahead reuses what the
// import ahead unpacked, and what that unpacked is counted both as unpacked
// ahead and as used. Which of them applies the layers after the first one the
// import ahead holds up is up to the scheduler, and the install may retire
// the offer before the import ahead logs: each layer is still applied once.
func TestPartPrefetchAheadCountsDemandMidImport(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.store.BeforeApply = func(ctx context.Context, _ ocispecs.Descriptor) error {
		first := false
		once.Do(func() {
			first = true
			close(entered)
		})
		if !first {
			return nil
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the import ahead never applied a layer")
	}
	installed := make(chan error, 1)
	go func() { installed <- f.run() }()
	require.Never(t, func() bool { return len(installed) > 0 }, 200*time.Millisecond, 10*time.Millisecond, "the install waits for the layer the import ahead applies")
	close(release)
	require.NoError(t, <-installed)
	require.True(t, f.installed())
	ahead := f.aheadLines(t, 1)[0]
	require.Contains(t, []string{"done", "offer_gone"}, ahead.fields["outcome"])
	require.EqualValues(t, 2, f.store.Applies.Load(), "each layer is applied once")
	unpacked := ahead.int(t, "unpacked_layers")
	require.Positive(t, unpacked, "the import ahead applied the layer it held up")
	var used []offerLine
	require.Eventually(t, func() bool {
		used = f.logs.lines(t, "remote cache part ahead used")
		return len(used) > 0
	}, 10*time.Second, 10*time.Millisecond)
	require.Equal(t, unpacked, used[0].int(t, "layers"))
	require.Equal(t, ahead.int(t, "unpacked"), used[0].int(t, "unpacked"))
}

// What an import ahead unpacked stops being counted once no import ahead
// holds it any more, even when the last one to go reused it rather than
// unpacking it: an install of it after that unpacks it again.
func TestPartPrefetchAheadForgottenWithLastHolder(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	require.EqualValues(t, 2, f.aheadLines(t, 1)[0].int(t, "unpacked_layers"))
	donor := f.donorOffer(t)
	f.prefetch.run(context.Background(), f.job(t, donor))
	reused := f.aheadLines(t, 2)[1]
	require.Equal(t, "done", reused.fields["outcome"])
	require.Zero(t, reused.int(t, "unpacked_layers"), "the donor's import ahead reuses the snapshots")
	for _, row := range []AnyResult{f.receiver, f.donor} {
		f.cache.egraphMu.Lock()
		_, err := f.cache.retirePartOfferLocked(f.ctx, row.cacheSharedResult(), f.address)
		f.cache.egraphMu.Unlock()
		require.NoError(t, err)
	}
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	require.Empty(t, f.prefetch.aheadBytes, "no import ahead holds those layers")
}
