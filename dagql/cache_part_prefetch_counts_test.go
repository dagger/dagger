package dagql

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The bytes prefetching downloads are counted when it fetches them, held
// until an install uses them, and then counted as used. Like the other
// prefetch tests, these don't run in parallel: an exhaustion fixture's
// operation hook is registered by entry number, which another cache's
// fixture can share.
func TestPartPrefetchCountsFetchedThenUsed(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	line := f.runJob(t)
	require.Equal(t, "done", line.fields["outcome"])
	size := chainBytes(f.chain)

	counts := f.cache.PartPrefetchCounts()
	require.Equal(t, int64(1), counts.Jobs["done"])
	require.Equal(t, size, counts.Fetched)
	require.Equal(t, size, counts.Held)
	require.Zero(t, counts.Used)
	require.Zero(t, counts.Unread)

	require.NoError(t, f.run())
	require.True(t, f.installed())
	counts = f.cache.PartPrefetchCounts()
	require.Equal(t, size, counts.Fetched)
	require.Equal(t, size, counts.Used, "the install used every fetched byte")
	require.Zero(t, counts.Held)
	require.Zero(t, counts.Unread)
}

// The bytes of a blob no offer wants any more are released unread.
func TestPartPrefetchCountsReleasedUnread(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	f.runJob(t)
	size := chainBytes(f.chain)
	f.cache.egraphMu.Lock()
	f.cache.partOfferGone(f.offer)
	f.cache.egraphMu.Unlock()
	f.prefetch.releaseIdle(time.Now())

	counts := f.cache.PartPrefetchCounts()
	require.Equal(t, size, counts.Fetched)
	require.Equal(t, size, counts.Unread)
	require.Zero(t, counts.Used)
	require.Zero(t, counts.Held)
}

// The bytes still held when prefetching closes are released unread.
func TestPartPrefetchCountsReleasedAtClose(t *testing.T) {
	f := newPrefetchFixture(t, true, nil)
	f.runJob(t)
	size := chainBytes(f.chain)
	f.cache.closePartPrefetch()

	counts := f.cache.PartPrefetchCounts()
	require.Equal(t, size, counts.Fetched)
	require.Equal(t, size, counts.Unread)
	require.Zero(t, counts.Used)
	require.Zero(t, counts.Held)
}

// A job a full lane drops is counted by its outcome, and fetches nothing.
func TestPartPrefetchCountsDroppedJobs(t *testing.T) {
	held := newChainFixture(t, "held")
	var later []chainFixture
	for i := range 3 {
		later = append(later, newChainFixture(t, fmt.Sprintf("later%d", i)))
	}
	f := newExhaustionFixture(t, append([]chainFixture{held}, later...)...)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, workers: 1, smallWorkers: 1, budget: 1 << 30, ttl: time.Hour, queue: 1}
	t.Cleanup(f.cache.closePartPrefetch)
	hold := make(chan struct{})
	defer close(hold)
	for i := range held.blobs {
		f.transport.fault(fmt.Sprintf("https://fixed.invalid/held/%d", i), contentTestFault{stallAfter: -1, hold: hold})
	}
	f.acceptOffer(t, f.receiver, held)
	require.Eventually(t, func() bool {
		return f.transport.count("https://fixed.invalid/held/0") > 0
	}, 10*time.Second, 10*time.Millisecond, "the held download takes the lane's only worker")
	for i, chain := range later {
		f.acceptOffer(t, persistedListTestResult(t, f.ctx, f.cache, f.srv, fmt.Sprintf("later-row-%d", i), &transferTestValue{Text: "pending"}), chain)
	}
	counts := f.cache.PartPrefetchCounts()
	require.Equal(t, int64(2), counts.Jobs["skipped_queue"], "a lane of one keeps the last of three")
	require.Zero(t, counts.Fetched)
}

// The bytes fetched and used are split by where their offer sits in the
// order offers were accepted, in tenths: with ten offers, each its own tenth.
func TestPartPrefetchCountsByOrderOfAcceptance(t *testing.T) {
	var chains []chainFixture
	for i := range 10 {
		chains = append(chains, newChainFixture(t, fmt.Sprintf("order%d", i)))
	}
	f := newExhaustionFixture(t, chains...)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, budget: 1 << 30, ttl: time.Hour, queue: 16}
	t.Cleanup(f.cache.closePartPrefetch)
	key, err := partAddressKey(f.address)
	require.NoError(t, err)
	var offers []*partOffer
	for i, chain := range chains {
		row := f.receiver
		if i > 0 {
			row = persistedListTestResult(t, f.ctx, f.cache, f.srv, fmt.Sprintf("order-row-%d", i), &transferTestValue{Text: "pending"})
		}
		f.acceptOffer(t, row, chain)
		f.cache.egraphMu.Lock()
		offers = append(offers, row.cacheSharedResult().partOfferLocked(key))
		f.cache.egraphMu.Unlock()
	}
	p := f.cache.partPrefetcher.Load()
	require.NotNil(t, p)
	for _, i := range []int{0, 4, 9} {
		p.mu.Lock()
		job := p.jobs[partPrefetchJobKey{offer: offers[i]}]
		p.mu.Unlock()
		require.NotNil(t, job)
		p.run(f.ctx, job)
	}
	// An install of the first offer's part uses its bytes.
	f.cache.partPrefetchConsumed(f.ctx, chains[0].chain.Layers)

	counts := f.cache.PartPrefetchCounts()
	require.Equal(t, int64(10), counts.Offers)
	for i, tenth := range counts.ByTenth {
		switch i {
		case 0:
			require.Equal(t, PartPrefetchTenth{Fetched: chainBytes(chains[0]), Used: chainBytes(chains[0])}, tenth, "tenth %d", i+1)
		case 4, 9:
			require.Equal(t, PartPrefetchTenth{Fetched: chainBytes(chains[i])}, tenth, "tenth %d", i+1)
		default:
			require.Zero(t, tenth, "tenth %d", i+1)
		}
	}
}

// chainBytes is the size of chain's blobs.
func chainBytes(chain chainFixture) int64 {
	var n int64
	for _, blob := range chain.blobs {
		n += int64(len(blob))
	}
	return n
}
