package dagql

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/stretchr/testify/require"
)

// newSizedChainFixture builds a real two-layer chain whose files each hold
// size bytes, so its blobs are at least that large.
func newSizedChainFixture(t *testing.T, tag string, size int) chainFixture {
	t.Helper()
	ctx := context.Background()
	store := testutil.NewStore(t)
	lower, _ := store.Build(t, nil, "lower.txt", strings.Repeat("l", size))
	upper, _ := store.Build(t, lower, "upper.txt", strings.Repeat("u", size))
	export := func(ref snapshots.ImmutableRef) *snapshots.ExportChain {
		chain, err := ref.ExportChain(ctx, config.RefConfig{Compression: compression.New(compression.Uncompressed)})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, chain.Release(context.Background())) })
		return chain
	}
	fixture := chainFixture{tag: tag, chain: export(upper), lower: export(lower)}
	require.Len(t, fixture.chain.Layers, 2)
	for _, layer := range fixture.chain.Layers {
		data, err := content.ReadBlob(ctx, fixture.chain.Provider, layer.Descriptor)
		require.NoError(t, err)
		fixture.blobs = append(fixture.blobs, data)
	}
	return fixture
}

// An offer a merge attaches is prefetched as an offered one is: once the
// merge commits, its blobs are downloaded and held before any call demands
// the part.
func TestPartPrefetchMergedOffer(t *testing.T) {
	chain := newChainFixture(t, "merged")
	f := newExhaustionFixture(t, chain)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, workers: 1, budget: 1 << 30, ttl: time.Hour, queue: 8}
	t.Cleanup(f.cache.closePartPrefetch)
	bundle, _ := mergeTestSource(t, "merged-root", "pending")
	require.Len(t, bundle.Values, 1)
	bundle.Values[0].Record.Envelope.PendingOffers = []PersistedPartOffer{exhaustionOffer(chain, "", true)}
	reply, err := f.cache.MergeValues(f.ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Eventually(t, func() bool {
		return f.transport.total() == len(chain.blobs)
	}, 10*time.Second, 10*time.Millisecond, "the merged offer's blobs are prefetched")
	require.Eventually(t, func() bool {
		p := f.cache.partPrefetcher.Load()
		if p == nil {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.holds) == len(chain.blobs)
	}, 10*time.Second, 10*time.Millisecond, "the prefetched blobs are held for the merged offer")
}

// A blob below the large-blob size is prefetched in a lane of its own: a
// large download holding the large lane's only worker doesn't hold it up.
func TestPartPrefetchSmallBlobOwnLane(t *testing.T) {
	const largeBlob = 32 << 10
	large := newSizedChainFixture(t, "large", 64<<10)
	small := newChainFixture(t, "small")
	for _, blob := range large.blobs {
		require.GreaterOrEqual(t, len(blob), largeBlob)
	}
	for _, blob := range small.blobs {
		require.Less(t, len(blob), largeBlob)
	}
	f := newExhaustionFixture(t, large, small)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: largeBlob, workers: 1, smallWorkers: 1, smallStreams: 8, budget: 1 << 30, ttl: time.Hour, queue: 8}
	t.Cleanup(f.cache.closePartPrefetch)
	hold := make(chan struct{})
	defer close(hold)
	for i := range large.blobs {
		f.transport.fault(fmt.Sprintf("https://fixed.invalid/large/%d", i), contentTestFault{stallAfter: -1, hold: hold})
	}
	key, err := partAddressKey(f.address)
	require.NoError(t, err)
	accept := func(row AnyResult, chain chainFixture) {
		t.Helper()
		f.attach(t, row, exhaustionOffer(chain, "", true))
		f.cache.egraphMu.Lock()
		offer := row.cacheSharedResult().partOfferLocked(key)
		f.cache.egraphMu.Unlock()
		require.NotNil(t, offer)
		f.cache.partOfferAccepted(f.ctx, row.cacheSharedResult(), key, offer)
	}
	accept(f.receiver, large)
	require.Eventually(t, func() bool {
		return f.transport.count("https://fixed.invalid/large/0") > 0
	}, 10*time.Second, 10*time.Millisecond, "the large download starts and holds the large lane")
	other := persistedListTestResult(t, f.ctx, f.cache, f.srv, "small-row", &transferTestValue{Text: "pending"})
	accept(other, small)
	require.Eventually(t, func() bool {
		for i := range small.blobs {
			if f.transport.count(fmt.Sprintf("https://fixed.invalid/small/%d", i)) == 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 10*time.Millisecond, "the small blobs are prefetched while the large download is held")
}

// acceptOffer attaches chain's offer to row and queues it for prefetch, as a
// merge's offer is accepted.
func (f *exhaustionFixture) acceptOffer(t *testing.T, row AnyResult, chain chainFixture) {
	t.Helper()
	key, err := partAddressKey(f.address)
	require.NoError(t, err)
	f.attach(t, row, exhaustionOffer(chain, "", true))
	f.cache.egraphMu.Lock()
	offer := row.cacheSharedResult().partOfferLocked(key)
	f.cache.egraphMu.Unlock()
	require.NotNil(t, offer)
	f.cache.partOfferAccepted(f.ctx, row.cacheSharedResult(), key, offer)
}

// An offer whose chain is at least aheadOffer is never dropped to make room
// for later offers: with its lane's queue full and its worker held, the
// large offer's blobs are still prefetched once later offers have arrived.
func TestPartPrefetchAheadQueueKeepsALargeOffer(t *testing.T) {
	big := newSizedChainFixture(t, "big", 64<<10)
	held := newChainFixture(t, "held")
	later := []chainFixture{newChainFixture(t, "later0"), newChainFixture(t, "later1"), newChainFixture(t, "later2")}
	f := newExhaustionFixture(t, append([]chainFixture{big, held}, later...)...)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, workers: 1, smallWorkers: 1, smallStreams: 8, budget: 1 << 30, ttl: time.Hour, queue: 1, aheadOffer: 64 << 10}
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
	f.acceptOffer(t, persistedListTestResult(t, f.ctx, f.cache, f.srv, "big-row", &transferTestValue{Text: "pending"}), big)
	for i, chain := range later {
		f.acceptOffer(t, persistedListTestResult(t, f.ctx, f.cache, f.srv, fmt.Sprintf("later-row-%d", i), &transferTestValue{Text: "pending"}), chain)
	}
	require.Eventually(t, func() bool {
		for i := range big.blobs {
			if f.transport.count(fmt.Sprintf("https://fixed.invalid/big/%d", i)) == 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 10*time.Millisecond, "the large offer's blobs are prefetched")
}

// The ahead queue runs its largest offer first: with its worker held on one
// offer, a larger offer queued after a smaller one is fetched before it.
func TestPartPrefetchAheadQueueRunsTheLargestFirst(t *testing.T) {
	first := newSizedChainFixture(t, "first", 48<<10)
	mid := newSizedChainFixture(t, "mid", 64<<10)
	big := newSizedChainFixture(t, "big", 128<<10)
	f := newExhaustionFixture(t, first, mid, big)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, workers: 1, smallWorkers: 1, smallStreams: 8, budget: 1 << 30, ttl: time.Hour, queue: 8, aheadOffer: 64 << 10}
	t.Cleanup(f.cache.closePartPrefetch)
	hold := make(chan struct{})
	for i := range first.blobs {
		f.transport.fault(fmt.Sprintf("https://fixed.invalid/first/%d", i), contentTestFault{stallAfter: -1, hold: hold})
	}
	f.acceptOffer(t, f.receiver, first)
	require.Eventually(t, func() bool {
		return f.transport.count("https://fixed.invalid/first/0") > 0
	}, 10*time.Second, 10*time.Millisecond, "the first offer takes the ahead worker")
	f.acceptOffer(t, persistedListTestResult(t, f.ctx, f.cache, f.srv, "mid-row", &transferTestValue{Text: "pending"}), mid)
	f.acceptOffer(t, persistedListTestResult(t, f.ctx, f.cache, f.srv, "big-row", &transferTestValue{Text: "pending"}), big)
	require.Zero(t, f.transport.count("https://fixed.invalid/big/0"), "the ahead worker is held")
	close(hold)
	require.Eventually(t, func() bool {
		return f.transport.count("https://fixed.invalid/mid/0") > 0 && f.transport.count("https://fixed.invalid/big/0") > 0
	}, 10*time.Second, 10*time.Millisecond, "both later offers are fetched")
	require.Less(t, f.transport.first("https://fixed.invalid/big/0"), f.transport.first("https://fixed.invalid/mid/0"), "the larger offer goes first")
}

// A large offer whose blobs are already in the content store is still
// applied ahead: its blobs are held, though nothing is downloaded, and its
// chain is imported.
func TestPartPrefetchAheadQueueAppliesStoredBlobs(t *testing.T) {
	big := newSizedChainFixture(t, "big", 64<<10)
	f := newExhaustionFixture(t, big)
	f.cache.partPrefetchConfig = &partPrefetchConfig{largeBlob: 1, workers: 1, smallWorkers: 1, smallStreams: 8, budget: 1 << 30, ttl: time.Hour, queue: 8, aheadOffer: 64 << 10}
	t.Cleanup(f.cache.closePartPrefetch)
	for i, layer := range big.chain.Layers {
		require.NoError(t, content.WriteBlob(f.ctx, f.store.Content, fmt.Sprintf("stored-%d", i), bytes.NewReader(big.blobs[i]), layer.Descriptor))
	}
	f.acceptOffer(t, f.receiver, big)
	require.Eventually(t, func() bool {
		p := f.cache.partPrefetcher.Load()
		if p == nil {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, a := range p.aheads {
			if a.ref != nil {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond, "the stored chain is applied ahead")
	require.Zero(t, f.transport.total(), "nothing is downloaded")
}
