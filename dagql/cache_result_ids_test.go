package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A merge's preparation reserves its records' numbers without the graph
// lock (takeResultIDs): it finishes while a reader holds the lock, as a set
// file's walk or a save's snapshot does. Taking the lock for writing to
// reserve them waited for every reader in progress, and every new reader
// waited behind it. On the Cloud and on an engine alike.
func TestPrepareValueMergeReservesItsNumbersBesideAReader(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"cloud", "engine"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			bundle := splitTestSource(t)
			from := CacheID("cache-a")
			var ctx context.Context
			var c *Cache
			if target == "cloud" {
				ctx, c = storedPartTestCache(t, WithBlobStore())
			} else {
				from = cloudCacheID
				ctx, c, _ = transferTestCache(t)
			}
			type prepared struct {
				p   *PreparedValueMerge
				err error
			}
			done := make(chan prepared, 1)
			c.egraphMu.RLock()
			go func() {
				p, err := c.PrepareValueMerge(ctx, from, bundle)
				done <- prepared{p, err}
			}()
			var got prepared
			var waited bool
			select {
			case got = <-done:
			case <-time.After(10 * time.Second):
				waited = true
			}
			c.egraphMu.RUnlock()
			if waited {
				got = <-done
				if got.p != nil {
					got.p.Release()
				}
				t.Fatal("the preparation waited for the reader to let go of the graph lock")
			}
			require.NoError(t, got.err)
			reply, err := c.CommitValueMerge(ctx, got.p)
			require.NoError(t, err)
			require.True(t, reply.Committed)
		})
	}
}

// Entry numbers stay one an entry when merges reserve theirs beside the
// Cloud's own new entries: merges of distinct records prepared and committed
// side by side with holdings that make entries of their own leave every
// record and every holding on an entry of its own.
func TestResultIDsStayUniqueBesideMergesReservations(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	const n = 16
	bundles := make([]ValueBundle, n)
	for i := range bundles {
		bundles[i], _ = mergeTestSource(t, fmt.Sprint("merged root ", i), fmt.Sprint("sent ", i))
	}
	replies := make([]MergeReply, n)
	errs := make([]error, 2*n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			replies[i], errs[i] = cloud.MergeValues(ctx, CacheID(fmt.Sprint("cache-", i)), bundles[i])
		})
		wg.Go(func() {
			_, errs[n+i] = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-holdings", Number: uint64(i + 1)}, RemoteHolding{Recipe: digest.FromString(fmt.Sprint("holding ", i)), Field: "holding"})
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	owner := map[uint64]string{}
	claim := func(number uint64, by string) {
		t.Helper()
		require.NotZero(t, number, "%s has a number", by)
		if other, ok := owner[number]; ok {
			require.Equal(t, other, by, "entry %d is one record's", number)
		}
		owner[number] = by
		require.NotNil(t, cloud.resultsByID[sharedResultID(number)], "%s's entry %d is in the cache", by, number)
	}
	for i, reply := range replies {
		require.True(t, reply.Committed)
		for _, value := range reply.Values {
			claim(value.Number, fmt.Sprintf("merge %d's record %d", i, value.Ordinal))
		}
	}
	for i := range n {
		entry, h := cloud.holdingLocked(HolderKey{Cache: "cache-holdings", Number: uint64(i + 1)})
		require.NotNil(t, h)
		claim(uint64(entry.id), fmt.Sprint("holding ", i))
	}
	for id, res := range cloud.resultsByID {
		require.Equal(t, id, res.id, "entry %d is filed under its own number", id)
	}
}

// A cache saved after a merge reserved its records' numbers and committed
// them, and reopened, numbers its new entries above every entry it had: a
// save's snapshot reads the counter beside the entries it saves, under the
// graph lock for reading, while a merge reserves its numbers without it,
// and every entry is installed under the lock for writing.
func TestAReopenedCacheNumbersAboveAMergesReservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, cache, srv := persistedListTestCache(t, path)
	persistedListTestResult(t, ctx, cache, srv, "before", Int(1))
	p, err := cache.PrepareValueMerge(ctx, cloudCacheID, splitTestSource(t))
	require.NoError(t, err)
	reply, err := cache.CommitValueMerge(ctx, p)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	var highest sharedResultID
	cache.egraphMu.RLock()
	for id := range cache.resultsByID {
		if id > highest {
			highest = id
		}
	}
	cache.egraphMu.RUnlock()
	require.NoError(t, cache.ReleaseSession(ctx, "test-session"))
	require.NoError(t, cache.Close(ctx))

	ctx, cache, srv = persistedListTestCache(t, path)
	after := persistedListTestResult(t, ctx, cache, srv, "after", Int(2))
	id, err := cache.PersistedResultID(after)
	require.NoError(t, err)
	require.Greater(t, id, uint64(highest), "a new entry's number is above every number the cache gave out before")
}
