package dagql

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
	"github.com/stretchr/testify/require"
)

var testCallerReads = wcprof.NewLockHolder("test.caller")

// observeGraphReadHolds sets c's read hold observer for the test, and
// returns the kinds of the holds it has been told of so far, in order.
func observeGraphReadHolds(t *testing.T, c *Cache) func() []wcprof.LockHolder {
	var (
		mu   sync.Mutex
		seen []wcprof.LockHolder
	)
	stop := c.ObserveGraphReadHolds(func(kind wcprof.LockHolder, _, _ time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, kind)
	})
	t.Cleanup(stop)
	return func() []wcprof.LockHolder {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		return out
	}
}

// Every read of the graph lock is a hold its observer sees, of the kind its
// caller's context names, or else of the cache's own kind of work: a
// caller's bundle, pricing walk, closure listing and run of remote reads are
// its own, and a read with no context, such as an entry's info, is the
// cache's.
func TestGraphReadHoldsAreOfTheirCallersKind(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	for i := range 3 {
		_, err := c.AttachRemoteHolding(ctx, HolderKey{"cache-a", uint64(i + 1)}, holdingOf(fmt.Sprintf("read-holds-%d", i)))
		require.NoError(t, err)
	}
	var numbers []uint64
	for i := range 3 {
		numbers = append(numbers, c.EquivalentEntries(testDigest(fmt.Sprintf("read-holds-%d", i)).String())...)
	}
	require.Len(t, numbers, 3)
	seen := observeGraphReadHolds(t, c)
	callers := wcprof.WithLockHolder(ctx, testCallerReads)
	now := time.Now().Unix()
	onlyCallers := func(what string) {
		t.Helper()
		got := seen()
		require.NotEmpty(t, got, what)
		require.Equal(t, []wcprof.LockHolder{testCallerReads}, slices.Compact(got), what)
	}

	_, err := c.StoredBundle(callers, numbers, now, nil)
	require.NoError(t, err)
	onlyCallers("bundle")
	_, err = c.StoredSizes(callers, numbers, now, nil)
	require.NoError(t, err)
	onlyCallers("pricing walk")
	_, err = c.StoredClosure(callers, numbers, now, nil)
	require.NoError(t, err)
	onlyCallers("closure listing")
	r := c.HoldRemoteReads(callers)
	r.EntriesHeld(numbers, 0)
	r.Release()
	onlyCallers("remote reads")

	_, ok := c.EntryInfo(numbers[0])
	require.True(t, ok)
	got := seen()
	require.Equal(t, []wcprof.LockHolder{graphHolders[workRemote]}, got)
	require.Equal(t, "remote", got[0].String())
}

// A call's lookup reads the graph lock as a lookup.
func TestGraphReadHoldsOfALookup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	call := cacheTestIntCall("read-holds-lookup")
	_, err = c.GetOrInitCall(ctx, "seed", noopTypeResolver{}, &CallRequest{ResultCall: call}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(call, 1), nil
	})
	require.NoError(t, err)
	seen := observeGraphReadHolds(t, c)
	_, err = c.GetOrInitCall(ctx, "hit", noopTypeResolver{}, &CallRequest{ResultCall: cacheTestIntCall("read-holds-lookup")}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("hit executed the call")
	})
	require.NoError(t, err)
	require.Contains(t, seen(), graphHolders[workLookup])
	require.NoError(t, c.ReleaseSession(ctx, "hit"))
	require.NoError(t, c.ReleaseSession(ctx, "seed"))
}
