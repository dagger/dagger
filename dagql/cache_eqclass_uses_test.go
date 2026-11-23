package dagql

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// eqClassCheckHolding is the i'th entry of a cache shaped as the Cloud's:
// its term takes the entries before it and at half its number as inputs;
// one in three has a content digest, which one in thirty shares with the
// entry before it, joining their classes; and one in ten was requested
// under another digest. Its classes leave fewer dead slots than live
// classes, so a compaction check frees nothing.
func eqClassCheckHolding(i int) RemoteHolding {
	h := holdingOf("check-entry-" + strconv.Itoa(i))
	if i > 0 {
		h.Term.Inputs = []digest.Digest{testDigest("check-entry-" + strconv.Itoa(i-1)), testDigest("check-entry-" + strconv.Itoa(i/2))}
	}
	switch {
	case i%30 == 0 && i > 0:
		h.ContentDigest = testDigest("check-content-" + strconv.Itoa(i-1))
	case i%3 == 0:
		h.ContentDigest = testDigest("check-content-" + strconv.Itoa(i))
	}
	if i%10 == 0 {
		h.Request = testDigest("check-request-" + strconv.Itoa(i))
	}
	return h
}

// A collection's check of the classes' compaction that frees nothing
// changes no state engines share, and names none: over entries whose terms
// take each other as inputs, some joined by content and some requested
// under another digest, with dead slots fewer than the live classes, a
// check made after the slots grew leaves the e-graph, the entries and the
// free slots as they were, and its collection's change names no kind of
// shared state.
func TestEqClassCheckThatFreesNothingChangesNothing(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	advance := testEqClassClock(cloud)
	const entries = 300
	for i := range entries {
		_, err := cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", uint64(i + 1)}, eqClassCheckHolding(i))
		require.NoError(t, err)
	}
	_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{"cache-a", entries + 1}, eqClassCheckHolding(entries))
	require.NoError(t, err)
	advance(eqClassCheckInterval)

	cloud.egraphMu.Lock()
	slots, live := cloud.eqClassSlotsLocked(), testLiveEqClassesLocked(cloud)
	cloud.egraphMu.Unlock()
	require.Greater(t, slots, live, "the cache has dead slots")
	require.Less(t, slots, 2*live, "fewer than the live classes: the check frees nothing")

	checks, before := testEqClassChecks(cloud), sharedStateOf(cloud)
	_, change, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
	require.NoError(t, err)
	require.Equal(t, checks+1, testEqClassChecks(cloud), "the collection checks the classes' compaction")
	require.Equal(t, SharedWrites(0), change.Shared, "a check that frees nothing changes no shared state: %v", change.Shared)
	require.Equal(t, before, sharedStateOf(cloud), "the check left the e-graph as it was")
}

// BenchmarkEqClassCheckThatFreesNothing measures the Cloud's check of the
// classes' compaction on caches of 43,000 entries and of 430,000, about a
// Cloud cache's serving several pull requests at once. Each entry is held
// by three engine caches of 165, so 1.29 million holdings at the larger
// size. The
// entries are eqClassCheckHolding's, whose classes leave fewer dead slots
// than live classes. Each operation adds an entry, which grows the slots,
// moves the checks' clock past their interval and times a collection of
// nothing, which holds the cache's lock for the check alone. It reports the
// checks' mean and longest time, and the compactions, which should be none.
// It fails when the checks' mean is a millisecond or more: a check that
// frees nothing has no reason to walk the cache, and a walk of the smaller
// cache takes tens of milliseconds.
func BenchmarkEqClassCheckThatFreesNothing(b *testing.B) {
	for _, entries := range []int{43_000, 430_000} {
		b.Run("entries="+strconv.Itoa(entries), func(b *testing.B) {
			ctx := context.Background()
			cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			attach := func(i int) {
				for k := range 3 {
					cache := CacheID(fmt.Sprint("cache-", (i+55*k)%165))
					_, err := cloud.AttachRemoteHolding(ctx, HolderKey{cache, uint64(i + 1)}, eqClassCheckHolding(i))
					require.NoError(b, err)
				}
			}
			for i := range entries {
				attach(i)
			}
			advance := testEqClassClock(cloud)
			_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
			require.NoError(b, err)
			cloud.egraphMu.RLock()
			checks, slots := cloud.eqClassChecks, cloud.eqClassSlotsLocked()
			terms, holdings := len(cloud.egraphTerms), cloud.remoteHoldings.Load()
			cloud.egraphMu.RUnlock()
			b.Logf("%d entries, %d terms, %d holdings, %d class slots", entries, terms, holdings, slots)

			var total, longest time.Duration
			compactions, n := 0, 0
			for b.Loop() {
				attach(entries + n)
				n++
				advance(eqClassCheckInterval)
				cloud.egraphMu.RLock()
				before := cloud.eqClassSlotsLocked()
				cloud.egraphMu.RUnlock()
				start := time.Now()
				_, _, err := cloud.CollectRemoteHoldings(ctx, nil, nil)
				took := time.Since(start)
				require.NoError(b, err)
				total += took
				if took > longest {
					longest = took
				}
				cloud.egraphMu.RLock()
				if cloud.eqClassSlotsLocked() < before {
					compactions++
				}
				cloud.egraphMu.RUnlock()
			}
			cloud.egraphMu.RLock()
			checks = cloud.eqClassChecks - checks
			cloud.egraphMu.RUnlock()
			require.Equal(b, n, checks, "each collection checks")
			mean := total / time.Duration(n)
			b.Logf("%d checks: mean %v, longest %v; %d compactions", n, mean, longest, compactions)
			b.ReportMetric(float64(mean.Nanoseconds())/1e6, "check-ms")
			b.ReportMetric(float64(longest.Nanoseconds())/1e6, "longest-check-ms")
			b.ReportMetric(float64(compactions), "compactions")
			require.Zero(b, compactions, "the checks free nothing")
			require.Less(b, mean, time.Millisecond, "a check that frees nothing walked the cache: %v a check", mean)
		})
	}
}

