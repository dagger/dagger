package dagql

import (
	"context"
	"fmt"
	stdslog "log/slog"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
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
// only this job's: two jobs can't both pass on the same free space.
func TestPartPrefetchDiskRoomCountsPending(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	var asked []int64
	f.cache.partPrefetchRoom = func(need int64) bool {
		asked = append(asked, need)
		return true
	}
	const other = 1000
	f.prefetch.mu.Lock()
	f.prefetch.pending = other
	f.prefetch.mu.Unlock()
	require.Equal(t, "done", f.runJob(t).fields["outcome"])
	var size int64
	for _, layer := range f.chain.chain.Layers {
		size += layer.Descriptor.Size
	}
	require.Equal(t, []int64{other + size}, asked)
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

	require.Zero(t, f.transport.count(suffix), "a blob only the retired offer wanted is requested")
	require.Equal(t, map[digest.Digest][]*partOffer{f.chain.digest(0): {second}}, f.holdOffers())
	f.prefetch.mu.Lock()
	defer f.prefetch.mu.Unlock()
	require.Equal(t, f.chain.chain.Layers[0].Descriptor.Size, f.prefetch.held, "only the shared blob's bytes stay held")
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
