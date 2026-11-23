package dagql

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An engine cache's state exists while it has a holding: a merged reply
// whose records' Cloud entries are gone lands nothing, and leaves no state
// behind for some other engine's collection to sweep.
func TestAMergedReplyThatLandsNothingLeavesNoEngineCacheState(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	sent := ValueBundle{Values: []TransferredValue{{Ordinal: 1, SenderNumber: 999}}}
	change, err := cloud.ApplyMergedReply(ctx, "cache-c", sent, MergeReply{Values: []MergedValue{{Ordinal: 1, Number: 1}}})
	require.NoError(t, err)
	require.Empty(t, change.Holdings)
	cloud.egraphMu.RLock()
	_, has := cloud.remoteCaches["cache-c"]
	cloud.egraphMu.RUnlock()
	require.False(t, has, "cache-c holds nothing")
	usage, err := cloud.BlobCacheUsage()
	require.NoError(t, err)
	require.Zero(t, usage.Holdings)
}

// The usage's count of holdings is every engine cache's, as holdings come
// and go.
func TestBlobCacheUsageCountsEveryEngineCachesHoldings(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	holdings := func() int {
		t.Helper()
		usage, err := cloud.BlobCacheUsage()
		require.NoError(t, err)
		return usage.Holdings
	}
	for _, key := range []HolderKey{{"cache-a", 1}, {"cache-a", 2}, {"cache-b", 1}} {
		_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf(fmt.Sprintf("usage-%d", key.Number)))
		require.NoError(t, err)
	}
	require.Equal(t, 3, holdings())
	_, _, err := cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-a").Candidates, nil)
	require.NoError(t, err)
	require.Equal(t, 1, holdings())
}

// Many engine caches write their holdings at once, on recipes they share,
// while readers walk one engine cache's graph and closures, describe whole
// entries across engine caches, and build bundles for one: run under the
// race detector, every read of an engine cache's state or holdings is under
// a lock that excludes that engine's writes. Each write and read keeps to
// the cache's own rules: the test checks that every engine's holdings end
// collected, and the usage count with them.
func TestEngineCachesWriteAndAreReadAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	const (
		engines = 8
		rows    = 200
		shared  = 40
	)
	recipe := func(e, i int) string {
		if i < shared {
			return fmt.Sprintf("locks-shared-%d", i)
		}
		return fmt.Sprintf("locks-%d-%d", e, i)
	}
	cacheOf := func(e int) CacheID { return CacheID(fmt.Sprintf("cache-%02d", e)) }

	stop := make(chan struct{})
	var readers sync.WaitGroup
	read := func(f func(int)) {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				f(n)
			}
		}()
	}
	read(func(n int) {
		cache := cacheOf(n % engines)
		cloud.HolderGraph(cache, []uint64{1, 2, 3, uint64(rows)})
		cloud.HolderClosure(HolderKey{Cache: cache, Number: uint64(rows)})
	})
	read(func(n int) {
		key := HolderKey{Cache: cacheOf(n % engines), Number: uint64(n%rows + 1)}
		cloud.RemoteEntryInfo(key)
		cloud.RemoteHoldingInfos([]HolderKey{key, {Cache: cacheOf((n + 1) % engines), Number: key.Number}})
		cloud.HoldingFacts(key)
		cloud.HoldingRuns([]HolderKey{key})
		if number, ok := cloud.HolderEntry(key); ok {
			cloud.EntryInfo(number)
			cloud.EntriesHeld([]uint64{number}, 4)
			cloud.CacheHoldings(key.Cache, []uint64{number})
			_, _ = cloud.StoredBundle(ctx, []uint64{number}, time.Now().Unix(), &HeldBy{Cache: key.Cache})
		}
		_, _ = cloud.BlobCacheUsage()
	})

	var writers sync.WaitGroup
	for e := range engines {
		writers.Add(1)
		go func() {
			defer writers.Done()
			cache := cacheOf(e)
			session := fmt.Sprintf("s-%d", e)
			for i := range rows {
				key := HolderKey{Cache: cache, Number: uint64(i + 1)}
				var deps []uint64
				if i > 0 {
					deps = []uint64{uint64(i)}
				}
				desc := holdingOf(recipe(e, i), deps...)
				if i%3 == 0 {
					desc.ContentDigest = testDigest(fmt.Sprintf("locks-content-%d", i))
				}
				_, err := cloud.AttachRemoteHolding(ctx, key, desc)
				assert.NoError(t, err)
				_, err = cloud.AddRemoteHold(ctx, key, session)
				assert.NoError(t, err)
				if i%5 == 0 {
					_, _, err = cloud.UpdateRemoteHolding(ctx, key, RemoteHoldingUpdate{Parts: []PersistedPartAddress{{Part: "fs"}}})
					assert.NoError(t, err)
				}
			}
			ended := cloud.ReleaseRemoteSession(ctx, cache, session)
			_, _, err := cloud.CollectRemoteHoldings(ctx, ended.Candidates, nil)
			assert.NoError(t, err)
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	usage, err := cloud.BlobCacheUsage()
	require.NoError(t, err)
	require.Zero(t, usage.Holdings, "every engine's holdings ended collected")
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Empty(t, cloud.remoteCaches, "and their states with them")
}
