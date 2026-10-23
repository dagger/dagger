package dagql

import (
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
	f.cache.partPrefetchConfig = &partPrefetchConfig{minBlob: 1, workers: 1, budget: 1 << 30, ttl: time.Hour, queue: 8}
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
	f.cache.partPrefetchConfig = &partPrefetchConfig{minBlob: largeBlob, workers: 1, budget: 1 << 30, ttl: time.Hour, queue: 8}
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
